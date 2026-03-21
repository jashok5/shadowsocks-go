package transfer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"github.com/jashok5/shadowsocks-go/internal/runtime"
	"github.com/jashok5/shadowsocks-go/internal/updater"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	cfg         config.Config
	configPath  string
	log         *zap.Logger
	api         *api.Client
	runtime     runtime.Manager
	updater     *updater.Updater
	version     string
	nextUpdate  time.Time
	lastTraffic map[int]model.PortTransfer
	failCount   int64
}

var ErrRestartRequired = errors.New("restart required after update")

func NewService(cfg config.Config, configPath string, log *zap.Logger, client *api.Client, rt runtime.Manager, up *updater.Updater, currentVersion string) *Service {
	client.SetNodeID(cfg.Node.ID)
	client.UpdateRetryPolicy(cfg.API.RetryMax, cfg.API.RetryBackoff, cfg.API.RetryMaxBackoff)
	return &Service{
		cfg:         cfg,
		configPath:  configPath,
		log:         log,
		api:         client,
		runtime:     rt,
		updater:     up,
		version:     strings.TrimSpace(currentVersion),
		lastTraffic: make(map[int]model.PortTransfer),
	}
}

func (s *Service) Run(ctx context.Context) error {
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
			s.log.Warn("enter failure backoff", zap.Duration("wait", wait))
			if !sleepContext(ctx, wait) {
				return ctx.Err()
			}
		} else {
			atomic.StoreInt64(&s.failCount, 0)
		}

		if err := s.checkUpdateIfNeeded(ctx); err != nil {
			return err
		}
	}
}

func (s *Service) Stop(ctx context.Context) error {
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
	s.log.Debug("sync phase done", zap.String("phase", "pull_remote"), zap.Duration("cost", time.Since(phase)))

	phase = time.Now()
	if err := s.runtime.Sync(ctx, runtime.SyncInput{NodeInfo: nodeInfo, Users: users, Rules: rules}); err != nil {
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
	s.log.Debug("sync phase done", zap.String("phase", "push_user_data"), zap.Duration("cost", time.Since(phase)))

	phase = time.Now()
	if err := s.api.PostNodeInfo(ctx, s.uptimeText(), s.loadText()); err != nil {
		s.log.Warn("push node info failed", logger.Err(err))
	}
	s.log.Debug("sync phase done", zap.String("phase", "push_node_info"), zap.Duration("cost", time.Since(phase)))

	if ds, ok := s.runtime.(*runtime.MemoryManager); ok {
		if ss, ok := ds.Driver().(*runtime.SSDriver); ok {
			stats := ss.Stats()
			if len(stats) > 0 {
				maxDropTCP := int64(0)
				maxDropUDP := int64(0)
				activeTCP := int64(0)
				for _, st := range stats {
					activeTCP += st.ActiveTCP
					if st.TCPDrop > maxDropTCP {
						maxDropTCP = st.TCPDrop
					}
					if st.UDPDrop > maxDropUDP {
						maxDropUDP = st.UDPDrop
					}
				}
				s.log.Debug("runtime pressure", zap.Int("ports", len(stats)), zap.Int64("active_tcp", activeTCP), zap.Int64("max_tcp_drop", maxDropTCP), zap.Int64("max_udp_drop", maxDropUDP))
			}
		}
	}

	s.log.Info("sync cycle complete", zap.Int("users", len(users)), zap.Int("rules", len(rules)), zap.Duration("cost", time.Since(cycleStart)))
	return nil
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
		payload := make([]model.UserTraffic, 0, len(userCurrent))
		for uid, now := range userCurrent {
			key := -uid
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
		if len(payload) > 0 {
			s.log.Debug("post user traffic", zap.String("mode", "uid"), zap.Int("count", len(payload)), zap.Any("payload", payload))
			return s.api.PostUserTraffic(ctx, payload)
		}
		s.log.Debug("post user traffic skipped", zap.String("mode", "uid"), zap.Int("count", 0))
		return nil
	}

	payload := make([]model.UserTraffic, 0, len(current))
	for port, now := range current {
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
	if len(payload) == 0 {
		s.log.Debug("post user traffic skipped", zap.String("mode", "port_map"), zap.Int("count", 0))
		return nil
	}
	s.log.Debug("post user traffic", zap.String("mode", "port_map"), zap.Int("count", len(payload)), zap.Any("payload", payload))
	return s.api.PostUserTraffic(ctx, payload)
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
