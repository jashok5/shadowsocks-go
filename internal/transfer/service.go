package transfer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	goRuntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/autoblock"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"github.com/jashok5/shadowsocks-go/internal/runtime"
	"github.com/jashok5/shadowsocks-go/internal/updater"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	cfg           config.Config
	configPath    string
	log           *zap.Logger
	api           *api.Client
	runtime       runtime.Manager
	updater       *updater.Updater
	version       string
	nextUpdate    time.Time
	autoBlock     *autoblock.Service
	abCancel      context.CancelFunc
	abDone        chan struct{}
	lastTraffic   map[int]model.PortTransfer
	failCount     int64
	configModTime time.Time
}

var ErrRestartRequired = errors.New("restart required after update")

func NewService(cfg config.Config, configPath string, log *zap.Logger, client *api.Client, rt runtime.Manager, up *updater.Updater, currentVersion string) *Service {
	client.SetNodeID(cfg.Node.ID)
	client.UpdateRetryPolicy(cfg.API.RetryMax, cfg.API.RetryBackoff, cfg.API.RetryMaxBackoff)
	var ab *autoblock.Service
	if cfg.Security.AutoBlock.Enabled {
		ab = autoblock.New(log, cfg.Security.AutoBlock, client, rt)
	}
	return &Service{
		cfg:           cfg,
		configPath:    configPath,
		log:           log,
		api:           client,
		runtime:       rt,
		updater:       up,
		version:       strings.TrimSpace(currentVersion),
		autoBlock:     ab,
		lastTraffic:   make(map[int]model.PortTransfer),
		configModTime: readConfigModTime(configPath),
	}
}

func (s *Service) Run(ctx context.Context) error {
	if s.autoBlock != nil && s.abDone == nil {
		abCtx, cancel := context.WithCancel(ctx)
		s.abCancel = cancel
		s.abDone = make(chan struct{})
		go func() {
			defer close(s.abDone)
			if err := s.autoBlock.Run(abCtx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Warn("auto_block stopped", logger.Err(err))
			}
		}()
	}

	if err := s.syncOnce(ctx); err != nil {
		s.log.Warn("first sync failed", logger.Err(err))
	}

	for {
		if !sleepContext(ctx, s.cfg.Sync.UpdateInterval) {
			return ctx.Err()
		}
		s.reloadConfigIfNeeded()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := s.syncOnce(ctx); err != nil {
			s.log.Error("sync failed", logger.Err(err))
			wait := s.failureWait()
			s.log.Warn("sync failed, continue with fixed update interval", zap.Duration("suggested_backoff", wait), zap.Duration("next_sync_in", s.cfg.Sync.UpdateInterval))
		} else {
			atomic.StoreInt64(&s.failCount, 0)
		}

		if err := s.checkUpdateIfNeeded(ctx); err != nil {
			return err
		}
	}
}

func (s *Service) Stop(ctx context.Context) error {
	if s.abCancel != nil {
		s.abCancel()
		s.abCancel = nil
	}
	if s.abDone != nil {
		select {
		case <-s.abDone:
			s.abDone = nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.runtime.Stop(ctx)
}

func (s *Service) syncOnce(ctx context.Context) error {
	cycleStart := time.Now()

	phase := time.Now()
	if err := s.api.Ping(ctx); err != nil {
		return err
	}
	s.log.Debug("sync phase done", zap.String("phase", "ping"), zap.Duration("cost", time.Since(phase)))

	var (
		nodeInfo model.NodeInfo
		users    []model.User
		rules    []model.DetectRule
	)

	phase = time.Now()
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		nodeInfo, err = s.api.GetNodeInfo(gctx)
		return err
	})
	g.Go(func() error {
		var err error
		users, err = s.api.GetUsers(gctx)
		return err
	})
	g.Go(func() error {
		var err error
		rules, err = s.api.GetDetectRules(gctx)
		return err
	})
	if err := g.Wait(); err != nil {
		return err
	}
	usersInput := users
	users, filteredBySwitch, switchErr := applySwitchRule(usersInput, s.cfg.RT.SwitchRule)
	if switchErr != nil {
		s.log.Warn("switchrule evaluate failed, fallback to unfiltered users", logger.Err(switchErr))
		users = usersInput
		filteredBySwitch = 0
	}
	if filteredBySwitch > 0 {
		s.log.Info("switchrule filtered users", zap.Int("before", len(usersInput)), zap.Int("after", len(users)), zap.Int("filtered", filteredBySwitch), zap.String("mode", s.cfg.RT.SwitchRule.Mode))
	}
	if s.cfg.Node.GetPortOffsetByNodeName {
		nodes, nodesErr := s.api.GetNodes(ctx)
		if nodesErr != nil {
			s.log.Warn("pull nodes for port offset by node name failed", logger.Err(nodesErr))
		} else if offset, nodeName, ok := resolvePortOffsetByNodeName(nodes, s.cfg.Node.ID); ok {
			if offset != nodeInfo.PortOffset {
				s.log.Info("port offset resolved from node name", zap.Int("node_id", s.cfg.Node.ID), zap.String("node_name", nodeName), zap.Int("old", nodeInfo.PortOffset), zap.Int("new", offset))
			}
			nodeInfo.PortOffset = offset
		}
	}
	s.log.Debug("sync phase done", zap.String("phase", "pull_remote"), zap.Duration("cost", time.Since(phase)))

	phase = time.Now()
	in := runtime.SyncInput{
		NodeInfo: nodeInfo,
		Users:    users,
		Rules:    rules,
		ATP: runtime.ATPConfig{
			NodeInfo: nodeInfo,
			Users:    users,
			Rules:    rules,

			HandshakeTimeout: s.cfg.RT.ATP.HandshakeTimeout,
			IdleTimeout:      s.cfg.RT.ATP.IdleTimeout,
			ResumeTicketTTL:  s.cfg.RT.ATP.ResumeTicketTTL,
			RestartDebounce:  s.cfg.RT.ATP.RestartDebounce,
			CertReadyTimeout: s.cfg.RT.ATP.CertReadyTimeout,
			CertRetryGap:     s.cfg.RT.ATP.CertRetryGap,

			MaxConnsPerUser:       s.cfg.RT.ATP.MaxConnsPerUser,
			MaxOpenStreamsPerUser: s.cfg.RT.ATP.MaxOpenStreamsPerUser,
			EnableAuditBlock:      s.cfg.RT.ATP.EnableAuditBlock,
			AuditBlockDuration:    s.cfg.RT.ATP.AuditBlockDuration,

			PullNodeInfoInterval:  s.cfg.RT.ATP.PullNodeInfoInterval,
			PullUsersInterval:     s.cfg.RT.ATP.PullUsersInterval,
			PullDetectInterval:    s.cfg.RT.ATP.PullDetectInterval,
			ReportTrafficInterval: s.cfg.RT.ATP.ReportTrafficInterval,
			ReportAliveInterval:   s.cfg.RT.ATP.ReportAliveInterval,
			ReportDetectInterval:  s.cfg.RT.ATP.ReportDetectInterval,
			ReportNodeInterval:    s.cfg.RT.ATP.ReportNodeInterval,
		},
		MUHost: runtime.MUHostRule{
			Enabled: s.cfg.Node.EnableMUHostRule,
			Regex:   s.cfg.Node.MURegex,
			Suffix:  s.cfg.Node.MUSuffix,
		},
		SwitchRule: runtime.UserSwitchRule{
			Enabled: s.cfg.RT.SwitchRule.Enabled,
			Mode:    s.cfg.RT.SwitchRule.Mode,
			Expr:    s.cfg.RT.SwitchRule.Expr,
		},
		Runtime: runtime.Options{
			OnUnsupportedCipher: s.cfg.RT.OnUnsupportedCipher,
			DialTimeout:         s.cfg.RT.DialTimeout,
			DNSResolver:         s.cfg.RT.DNSResolver,
			DNSPreferIPv4:       s.cfg.RT.DNSPreferIPv4,
		},
	}
	if err := s.runtime.Sync(ctx, in); err != nil {
		return err
	}
	s.log.Debug("sync phase done", zap.String("phase", "runtime_sync"), zap.Duration("cost", time.Since(phase)))

	phase = time.Now()
	snap, err := s.runtime.Snapshot(ctx)
	if err != nil {
		return err
	}
	s.log.Debug("sync phase done", zap.String("phase", "runtime_snapshot"), zap.Duration("cost", time.Since(phase)))

	phase = time.Now()
	if err := s.pushTraffic(ctx, snap.Transfer, snap.UserTransfer, snap.PortUser); err != nil {
		s.log.Warn("push users traffic failed", logger.Err(err))
	}
	if err := s.api.PostNodeInfo(ctx, s.uptimeText(), s.loadText()); err != nil {
		s.log.Warn("push node info failed", logger.Err(err))
	}
	aliveData := snap.OnlineIP
	if len(snap.UserOnlineIP) > 0 {
		aliveData = snap.UserOnlineIP
	}
	if err := s.pushAliveIP(ctx, aliveData); err != nil {
		s.log.Warn("push alive ip failed", logger.Err(err))
	}
	detectData := snap.Detect
	if len(snap.UserDetect) > 0 {
		detectData = snap.UserDetect
	}
	if err := s.pushDetectLog(ctx, detectData); err != nil {
		s.log.Warn("push detect log failed", logger.Err(err))
	}
	s.log.Debug("sync phase done", zap.String("phase", "push_remote"), zap.Duration("cost", time.Since(phase)))

	if ds, ok := s.runtime.(*runtime.MemoryManager); ok {
		if ss, ok := ds.Driver().(*runtime.SSDriver); ok {
			stats := ss.Stats()
			if len(stats) > 0 {
				maxDropTCP := int64(0)
				maxDropUDP := int64(0)
				activeTCP := int64(0)
				totalSessionSize := 0
				totalResolveSize := 0
				totalSessionHit := int64(0)
				totalSessionMiss := int64(0)
				totalResolveHit := int64(0)
				totalResolveMiss := int64(0)
				for _, st := range stats {
					activeTCP += st.ActiveTCP
					if st.TCPDrop > maxDropTCP {
						maxDropTCP = st.TCPDrop
					}
					if st.UDPDrop > maxDropUDP {
						maxDropUDP = st.UDPDrop
					}
					totalSessionSize += st.UDPSessionCache.Size
					totalResolveSize += st.UDPResolveCache.Size
					totalSessionHit += st.UDPSessionCache.Hits
					totalSessionMiss += st.UDPSessionCache.Misses
					totalResolveHit += st.UDPResolveCache.Hits
					totalResolveMiss += st.UDPResolveCache.Misses
				}
				s.log.Debug("runtime pressure", zap.Int("ports", len(stats)), zap.Int64("active_tcp", activeTCP), zap.Int64("max_tcp_drop", maxDropTCP), zap.Int64("max_udp_drop", maxDropUDP), zap.Int("udp_session_cache_size", totalSessionSize), zap.Int("udp_resolve_cache_size", totalResolveSize), zap.Int64("udp_session_hit", totalSessionHit), zap.Int64("udp_session_miss", totalSessionMiss), zap.Int64("udp_resolve_hit", totalResolveHit), zap.Int64("udp_resolve_miss", totalResolveMiss))
			}
		} else if ssr, ok := ds.Driver().(*runtime.SSRDriver); ok {
			stats := ssr.CacheStats()
			if len(stats) > 0 {
				totalResolveSize := 0
				totalResolveHit := int64(0)
				totalResolveMiss := int64(0)
				for _, st := range stats {
					totalResolveSize += st.UDPResolveCache.Size
					totalResolveHit += st.UDPResolveCache.Hits
					totalResolveMiss += st.UDPResolveCache.Misses
				}
				s.log.Debug("runtime pressure", zap.Int("ports", len(stats)), zap.Int("udp_resolve_cache_size", totalResolveSize), zap.Int64("udp_resolve_hit", totalResolveHit), zap.Int64("udp_resolve_miss", totalResolveMiss))
			}
		}
	}
	s.logRuntimeMemStats()

	s.log.Info("sync cycle complete", zap.Int("users", len(users)), zap.Int("rules", len(rules)), zap.Duration("cost", time.Since(cycleStart)))
	return nil
}

func (s *Service) logRuntimeMemStats() {
	var ms goRuntime.MemStats
	goRuntime.ReadMemStats(&ms)
	s.log.Debug("runtime memstats",
		zap.Uint64("heap_alloc", ms.HeapAlloc),
		zap.Uint64("heap_inuse", ms.HeapInuse),
		zap.Uint64("heap_objects", ms.HeapObjects),
		zap.Uint32("num_gc", ms.NumGC),
		zap.Uint64("gc_pause_total_ns", ms.PauseTotalNs),
		zap.Int("goroutines", goRuntime.NumGoroutine()),
	)
}

var nodeNameOffsetPattern = regexp.MustCompile(`#(\d+)`)

func resolvePortOffsetByNodeName(nodes []map[string]any, nodeID int) (int, string, bool) {
	for _, n := range nodes {
		id, ok := parseNodeID(n)
		if !ok || id != nodeID {
			continue
		}
		name := parseNodeName(n)
		if name == "" {
			return 0, "", false
		}
		m := nodeNameOffsetPattern.FindStringSubmatch(name)
		if len(m) < 2 {
			return 0, name, false
		}
		offset, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, name, false
		}
		return offset, name, true
	}
	return 0, "", false
}

func parseNodeID(node map[string]any) (int, bool) {
	for _, key := range []string{"id", "node_id"} {
		raw, ok := node[key]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmt.Sprint(raw))
		if s == "" {
			continue
		}
		id, err := strconv.Atoi(s)
		if err == nil {
			return id, true
		}
	}
	return 0, false
}

func parseNodeName(node map[string]any) string {
	for _, key := range []string{"name", "node_name"} {
		raw, ok := node[key]
		if !ok {
			continue
		}
		v := strings.TrimSpace(fmt.Sprint(raw))
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Service) failureWait() time.Duration {
	n := atomic.AddInt64(&s.failCount, 1)
	base := s.cfg.Sync.FailureBaseWait
	maxWait := s.cfg.Sync.FailureMaxWait
	mult := math.Pow(2, float64(n-1))
	wait := time.Duration(float64(base) * mult)
	if wait > maxWait {
		return maxWait
	}
	return wait
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Service) reloadConfigIfNeeded() {
	if strings.TrimSpace(s.configPath) == "" {
		return
	}
	mod := readConfigModTime(s.configPath)
	if !mod.IsZero() && !s.configModTime.IsZero() && !mod.After(s.configModTime) {
		return
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		s.log.Warn("reload config failed, keep old config", logger.Err(err))
		return
	}

	if cfg.Node.ID != s.cfg.Node.ID {
		s.api.SetNodeID(cfg.Node.ID)
		s.log.Info("config reloaded: node id changed", zap.Int("old", s.cfg.Node.ID), zap.Int("new", cfg.Node.ID))
	}
	if cfg.Sync.UpdateInterval != s.cfg.Sync.UpdateInterval {
		s.log.Info("config reloaded: update interval changed", zap.Duration("old", s.cfg.Sync.UpdateInterval), zap.Duration("new", cfg.Sync.UpdateInterval))
	}

	s.api.UpdateRetryPolicy(cfg.API.RetryMax, cfg.API.RetryBackoff, cfg.API.RetryMaxBackoff)
	s.cfg = cfg
	if !mod.IsZero() {
		s.configModTime = mod
	}
}

func readConfigModTime(path string) time.Time {
	if strings.TrimSpace(path) == "" {
		return time.Time{}
	}
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func (s *Service) checkUpdateIfNeeded(ctx context.Context) error {
	if !s.cfg.Update.Enabled || s.updater == nil {
		return nil
	}
	if s.version == "" {
		return nil
	}
	now := time.Now()
	if !s.nextUpdate.IsZero() && now.Before(s.nextUpdate) {
		return nil
	}
	s.nextUpdate = now.Add(s.cfg.Update.CheckInterval)

	res, err := s.updater.CheckAndUpdate(ctx, s.version)
	if err != nil {
		s.log.Warn("check update failed", logger.Err(err))
		return nil
	}
	if res.LatestVersion == "" {
		return nil
	}
	if res.Updated {
		s.log.Info("update applied, restart required", zap.String("current", s.version), zap.String("latest", res.LatestVersion), zap.String("file", res.DownloadedFile))
		return ErrRestartRequired
	}
	s.log.Info("new version downloaded", zap.String("current", s.version), zap.String("latest", res.LatestVersion), zap.String("file", res.DownloadedFile))
	return nil
}

func (s *Service) pushTraffic(ctx context.Context, current map[int]model.PortTransfer, userCurrent map[int]model.PortTransfer, portUser map[int]int) error {
	if len(userCurrent) > 0 {
		activeKeys := make(map[int]struct{}, len(userCurrent))
		payload := make([]model.UserTraffic, 0, len(userCurrent))
		for uid, now := range userCurrent {
			key := -uid
			activeKeys[key] = struct{}{}
			last := s.lastTraffic[key]
			du := now.Upload - last.Upload
			dd := now.Download - last.Download
			if du < 0 || dd < 0 {
				du = now.Upload
				dd = now.Download
			}
			if du == 0 && dd == 0 {
				continue
			}
			s.lastTraffic[key] = model.PortTransfer{Upload: now.Upload, Download: now.Download}
			payload = append(payload, model.UserTraffic{U: du, D: dd, UserID: uid})
		}
		s.pruneLastTraffic(activeKeys)
		if len(payload) > 0 {
			s.log.Debug("post user traffic", zap.String("mode", "uid"), zap.Int("count", len(payload)), zap.Any("payload", payload))
			return s.api.PostUserTraffic(ctx, payload)
		}
		s.log.Debug("post user traffic skipped", zap.String("mode", "uid"), zap.Int("count", 0))
		return nil
	}

	activeKeys := make(map[int]struct{}, len(current))
	payload := make([]model.UserTraffic, 0, len(current))
	for port, now := range current {
		activeKeys[port] = struct{}{}
		uid, ok := portUser[port]
		if !ok {
			continue
		}
		last := s.lastTraffic[port]
		du := now.Upload - last.Upload
		dd := now.Download - last.Download
		if du < 0 || dd < 0 {
			du = now.Upload
			dd = now.Download
		}
		if du == 0 && dd == 0 {
			continue
		}
		s.lastTraffic[port] = model.PortTransfer{Upload: now.Upload, Download: now.Download}
		payload = append(payload, model.UserTraffic{U: du, D: dd, UserID: uid})
	}
	s.pruneLastTraffic(activeKeys)
	if len(payload) == 0 {
		s.log.Debug("post user traffic skipped", zap.String("mode", "port_map"), zap.Int("count", 0))
		return nil
	}
	s.log.Debug("post user traffic", zap.String("mode", "port_map"), zap.Int("count", len(payload)), zap.Any("payload", payload))
	return s.api.PostUserTraffic(ctx, payload)
}

func (s *Service) pruneLastTraffic(active map[int]struct{}) {
	if len(s.lastTraffic) == 0 {
		return
	}
	for key := range s.lastTraffic {
		if _, ok := active[key]; ok {
			continue
		}
		delete(s.lastTraffic, key)
	}
}

func (s *Service) uptimeText() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return strconv.FormatInt(time.Now().Unix(), 10)
	}
	parts := strings.Fields(string(b))
	if len(parts) == 0 {
		return strconv.FormatInt(time.Now().Unix(), 10)
	}
	return parts[0]
}

func (s *Service) loadText() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0.00 0.00 0.00"
	}
	parts := strings.Fields(string(b))
	if len(parts) < 3 {
		return "0.00 0.00 0.00"
	}
	return fmt.Sprintf("%s %s %s", parts[0], parts[1], parts[2])
}

func (s *Service) pushAliveIP(ctx context.Context, alive map[int][]string) error {
	payload := make([]model.AliveIP, 0)
	for userID, ips := range alive {
		for _, ip := range ips {
			payload = append(payload, model.AliveIP{IP: ip, UserID: userID})
		}
	}
	if len(payload) == 0 {
		return nil
	}
	return s.api.PostAliveIP(ctx, payload)
}

func (s *Service) pushDetectLog(ctx context.Context, detect map[int][]int) error {
	payload := make([]model.DetectLog, 0)
	for userID, ruleIDs := range detect {
		for _, ruleID := range ruleIDs {
			payload = append(payload, model.DetectLog{ListID: ruleID, UserID: userID})
		}
	}
	if len(payload) == 0 {
		return nil
	}
	return s.api.PostDetectLog(ctx, payload)
}
