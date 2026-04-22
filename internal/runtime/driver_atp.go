package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	atpAudit "github.com/jashok5/shadowsocks-go/internal/atp/audit"
	atpConcurrency "github.com/jashok5/shadowsocks-go/internal/atp/concurrency"
	atpInit "github.com/jashok5/shadowsocks-go/internal/atp/initialize"
	atpLimiter "github.com/jashok5/shadowsocks-go/internal/atp/limiter"
	atpPanel "github.com/jashok5/shadowsocks-go/internal/atp/panel"
	atpProxy "github.com/jashok5/shadowsocks-go/internal/atp/proxy"
	atpReporter "github.com/jashok5/shadowsocks-go/internal/atp/reporter"
	atpState "github.com/jashok5/shadowsocks-go/internal/atp/state"
	atpTransport "github.com/jashok5/shadowsocks-go/internal/atp/transport"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"go.uber.org/zap"
)

var (
	atpBuildTLSConfig                = atpInit.BuildTLSConfig
	atpEnsureCertificateReadyWithTry = atpInit.EnsureCertificateReadyWithRetry
	atpReadCertificateCacheState     = atpInit.ReadCertificateCacheState
	atpStartACMEChallengeListener    = atpInit.StartACMEChallengeListener
	atpStartCertificateMaintenance   = atpInit.StartCertificateMaintenance
)

type ATPDriver struct {
	log *zap.Logger

	mu            sync.Mutex
	store         *atpState.Store
	usage         *atpReporter.Collector
	limiter       *atpLimiter.Manager
	connMgr       *atpConcurrency.Manager
	audit         *atpAudit.Engine
	proxy         *atpProxy.Server
	proxyCtx      context.Context
	proxyCancel   context.CancelFunc
	proxyDone     chan error
	certCancel    context.CancelFunc
	listen        string
	port          int
	transport     string
	tlsSNI        string
	tlsCfg        *tls.Config
	usersByID     map[int32]struct{}
	onlineSeen    map[int32]map[string]time.Time
	onlineTTL     time.Duration
	ruleCount     int
	lastApplyAt   time.Time
	lastLogAt     time.Time
	lastLogUsers  int
	lastLogRules  int
	lastSnapshot  DriverSnapshot
	lastProxyErr  error
	proxyGen      int
	lastRestartAt time.Time
	minRestartGap time.Duration
	certNotAfter  time.Time
}

type ATPRuntimeStat struct {
	Listen           string `json:"listen"`
	Port             int    `json:"port"`
	Transport        string `json:"transport"`
	SNI              string `json:"sni"`
	Users            int    `json:"users"`
	Rules            int    `json:"rules"`
	ProxyActive      bool   `json:"proxy_active"`
	ProxyGen         int    `json:"proxy_generation"`
	CertNotAfter     int64  `json:"cert_not_after_unix"`
	CertRemainingSec int64  `json:"cert_remaining_sec"`
	LastError        string `json:"last_error,omitempty"`
	LastApply        int64  `json:"last_apply_unix"`
}

func NewATPDriver(log *zap.Logger) *ATPDriver {
	if log == nil {
		log = zap.NewNop()
	}
	return &ATPDriver{
		log:        log,
		store:      atpState.NewStore(),
		usage:      atpReporter.NewCollector(),
		connMgr:    atpConcurrency.NewManager(),
		audit:      atpAudit.NewEngine(),
		usersByID:  make(map[int32]struct{}),
		onlineSeen: make(map[int32]map[string]time.Time),
		onlineTTL:  2 * time.Minute,
		lastSnapshot: DriverSnapshot{
			Transfer:     map[int]model.PortTransfer{},
			UserTransfer: map[int]model.PortTransfer{},
			OnlineIP:     map[int][]string{},
			UserOnlineIP: map[int][]string{},
			Detect:       map[int][]int{},
			UserDetect:   map[int][]int{},
			WrongIP:      nil,
		},
		minRestartGap: 3 * time.Second,
	}
}

func (d *ATPDriver) ApplyATP(ctx context.Context, cfg ATPConfig) error {
	if cfg.NodeInfo.Sort != 16 {
		return fmt.Errorf("atp driver requires node sort=16, got=%d", cfg.NodeInfo.Sort)
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.consumeProxyErrLocked(); err != nil {
		d.log.Warn("atp proxy exited, forcing recovery restart",
			zap.Error(err),
			zap.String("listen", d.listen),
			zap.Int("port", d.port),
			zap.String("transport", d.transport),
			zap.String("sni", d.tlsSNI),
		)
		d.resetProxyStateLocked()
	}

	prevUsers := len(d.usersByID)
	prevRules := d.ruleCount
	prevListen := d.listen
	prevPort := d.port
	prevTransport := d.transport
	prevSNI := d.tlsSNI

	d.minRestartGap = cfg.RestartDebounce
	if d.minRestartGap < 0 {
		d.minRestartGap = 0
	}
	if d.limiter == nil {
		d.limiter = atpLimiter.NewManager()
	}
	d.applyNodeAndUsersLocked(cfg)
	d.applyRulesLocked(cfg.Rules)
	d.updateStoreLocked(cfg)
	d.lastApplyAt = time.Now()
	if cfg.ReportAliveInterval > 0 {
		ttl := cfg.ReportAliveInterval * 3
		if ttl < time.Minute {
			ttl = time.Minute
		}
		d.onlineTTL = ttl
	}

	panelNode := toATPPanelNode(cfg.NodeInfo)
	resolvedPort, resolvedTransport := atpInit.ParsePortAndTransportFromNode(panelNode)
	listen := "0.0.0.0"
	if resolvedPort <= 0 {
		resolvedPort = 443
	}
	if resolvedTransport == "" {
		resolvedTransport = "tls"
	}
	sni := strings.TrimSpace(atpInit.ExtractSNIFromNode(panelNode))
	if sni == "" {
		sni = strings.TrimSpace(strings.Split(cfg.NodeInfo.Server, ";")[0])
	}

	reloadProxy := d.proxy == nil || d.listen != listen || d.port != resolvedPort || !strings.EqualFold(d.transport, resolvedTransport) || d.tlsCfg == nil || d.tlsSNI != sni
	if !reloadProxy {
		d.logHealthLocked(prevUsers, prevRules, prevListen, prevPort, prevTransport, prevSNI)
		return nil
	}
	if d.proxy != nil && d.minRestartGap > 0 && !d.lastRestartAt.IsZero() && time.Since(d.lastRestartAt) < d.minRestartGap {
		d.log.Warn("atp proxy restart debounced",
			zap.Duration("min_restart_gap", d.minRestartGap),
			zap.Time("last_restart_at", d.lastRestartAt),
			zap.String("current_listen", d.listen),
			zap.Int("current_port", d.port),
			zap.String("current_transport", d.transport),
			zap.String("target_listen", listen),
			zap.Int("target_port", resolvedPort),
			zap.String("target_transport", resolvedTransport),
		)
		d.logHealthLocked(prevUsers, prevRules, prevListen, prevPort, prevTransport, prevSNI)
		return nil
	}

	tlsCfg, err := atpBuildTLSConfig(&atpInit.Config{}, panelNode)
	if err != nil {
		return err
	}
	cacheState, err := atpReadCertificateCacheState(ctx, sni)
	if err != nil {
		return err
	}
	certReadyTimeout := cfg.CertReadyTimeout
	if certReadyTimeout <= 0 {
		certReadyTimeout = 2 * time.Minute
	}
	certRetryGap := cfg.CertRetryGap
	if certRetryGap <= 0 {
		certRetryGap = 3 * time.Second
	}
	needChallenge := !cacheState.Exists || cacheState.Expired
	if needChallenge {
		reason := "missing"
		if cacheState.Expired {
			reason = "expired"
		}
		d.log.Info("atp certificate challenge listener start", zap.String("listen", listen), zap.Int("port", resolvedPort), zap.String("sni", sni), zap.String("reason", reason))
		challengeCtx, cancelChallenge := context.WithCancel(context.Background())
		challengeErrCh := make(chan error, 1)
		go func() {
			challengeErrCh <- atpStartACMEChallengeListener(challengeCtx, d.log, listen, resolvedPort, tlsCfg)
			close(challengeErrCh)
		}()
		time.Sleep(200 * time.Millisecond)
		err = atpEnsureCertificateReadyWithTry(ctx, d.log, tlsCfg, sni, certReadyTimeout, certRetryGap)
		cancelChallenge()
		select {
		case challengeErr, ok := <-challengeErrCh:
			if ok && challengeErr != nil && !errors.Is(challengeErr, context.Canceled) {
				d.log.Warn("atp certificate challenge listener stopped with error", zap.Error(challengeErr), zap.String("sni", sni))
			}
		case <-time.After(2 * time.Second):
			d.log.Warn("atp certificate challenge listener stop timeout", zap.String("sni", sni))
		}
		if err != nil {
			d.log.Warn("atp certificate prepare failed, keeping current proxy", zap.Error(err), zap.String("sni", sni))
			d.logHealthLocked(prevUsers, prevRules, prevListen, prevPort, prevTransport, prevSNI)
			return err
		}
		if notAfter, cacheErr := atpReadCertificateCacheState(ctx, sni); cacheErr == nil && notAfter.Exists {
			d.certNotAfter = notAfter.NotAfter
		}
	} else {
		d.certNotAfter = cacheState.NotAfter
		d.log.Info("atp certificate cache valid", zap.String("sni", sni), zap.Time("not_after", cacheState.NotAfter), zap.Duration("remaining", time.Until(cacheState.NotAfter)))
	}
	if notAfter, certErr := readCertNotAfter(tlsCfg, sni); certErr == nil {
		d.certNotAfter = notAfter
	}

	if err = d.restartProxyLocked(listen, resolvedPort, resolvedTransport, sni, tlsCfg, cfg); err != nil {
		return err
	}

	d.log.Info("atp runtime updated",
		zap.String("listen", listen),
		zap.Int("port", resolvedPort),
		zap.String("transport", resolvedTransport),
		zap.String("sni", sni),
		zap.Int("users", len(cfg.Users)),
		zap.Int("rules", len(cfg.Rules)),
	)
	d.logHealthLocked(prevUsers, prevRules, prevListen, prevPort, prevTransport, prevSNI)
	return nil
}

func (d *ATPDriver) Stats() ATPRuntimeStat {
	d.mu.Lock()
	defer d.mu.Unlock()
	proxyActive := d.proxy != nil
	if d.proxyDone != nil {
		select {
		case err, ok := <-d.proxyDone:
			d.proxyDone = nil
			if ok && err != nil && !errors.Is(err, context.Canceled) {
				d.lastProxyErr = err
			}
			d.proxy = nil
			d.proxyCtx = nil
			d.proxyCancel = nil
			proxyActive = false
		default:
		}
	}
	st := ATPRuntimeStat{
		Listen:      d.listen,
		Port:        d.port,
		Transport:   d.transport,
		SNI:         d.tlsSNI,
		Users:       len(d.usersByID),
		Rules:       d.ruleCount,
		ProxyActive: proxyActive,
		ProxyGen:    d.proxyGen,
	}
	if !d.lastApplyAt.IsZero() {
		st.LastApply = d.lastApplyAt.Unix()
	}
	if d.lastProxyErr != nil {
		st.LastError = d.lastProxyErr.Error()
	}
	if !d.certNotAfter.IsZero() {
		st.CertNotAfter = d.certNotAfter.Unix()
		remain := time.Until(d.certNotAfter)
		if remain > 0 {
			st.CertRemainingSec = int64(remain.Seconds())
		}
	}
	return st
}

func (d *ATPDriver) Start(context.Context, PortConfig) error { return nil }

func (d *ATPDriver) Reload(context.Context, PortConfig) error { return nil }

func (d *ATPDriver) Stop(context.Context, int) error { return nil }

func (d *ATPDriver) Snapshot(context.Context) (DriverSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.consumeProxyErrLocked(); err != nil {
		return DriverSnapshot{}, err
	}

	traffic := d.usage.FlushTraffic()
	userTransfer := make(map[int]model.PortTransfer, len(traffic))
	for uid, t := range traffic {
		userTransfer[int(uid)] = model.PortTransfer{Upload: t.U, Download: t.D}
	}

	alive := d.usage.FlushAliveIP()
	now := time.Now()
	for uid, ips := range alive {
		if _, ok := d.onlineSeen[uid]; !ok {
			d.onlineSeen[uid] = make(map[string]time.Time)
		}
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			d.onlineSeen[uid][ip] = now
		}
	}

	cutoff := now.Add(-d.onlineTTL)
	userOnlineIP := make(map[int][]string, len(d.onlineSeen))
	for uid, seen := range d.onlineSeen {
		arr := make([]string, 0, len(seen))
		for ip, ts := range seen {
			if ts.Before(cutoff) {
				delete(seen, ip)
				continue
			}
			arr = append(arr, ip)
		}
		if len(seen) == 0 {
			delete(d.onlineSeen, uid)
			continue
		}
		sort.Strings(arr)
		userOnlineIP[int(uid)] = arr
	}

	logs := d.usage.FlushDetectLog()
	userDetect := make(map[int][]int)
	for _, item := range logs {
		uid := int(item.UserID)
		userDetect[uid] = append(userDetect[uid], int(item.ListID))
	}
	for uid := range userDetect {
		sort.Ints(userDetect[uid])
	}

	d.lastSnapshot = DriverSnapshot{
		Transfer:     map[int]model.PortTransfer{},
		UserTransfer: userTransfer,
		OnlineIP:     map[int][]string{},
		UserOnlineIP: userOnlineIP,
		Detect:       map[int][]int{},
		UserDetect:   userDetect,
		WrongIP:      nil,
	}
	return d.lastSnapshot, nil
}

func (d *ATPDriver) Close(ctx context.Context) error {
	d.mu.Lock()
	err := d.stopProxyLocked(ctx)
	d.mu.Unlock()
	return err
}

func (d *ATPDriver) applyNodeAndUsersLocked(cfg ATPConfig) {
	nodeMbps := cfg.NodeInfo.NodeSpeedLimit
	if nodeMbps < 0 {
		nodeMbps = 0
	}
	d.limiter.SetNodeLimit(nodeMbps)

	live := make(map[int32]struct{}, len(cfg.Users))
	removed := make([]int32, 0)
	for _, user := range cfg.Users {
		uid := int32(user.ID)
		live[uid] = struct{}{}
		mbps := user.NodeSpeed
		if mbps < 0 {
			mbps = 0
		}
		d.limiter.SetUserLimit(uid, mbps)
		connLimit := user.NodeConnector
		if connLimit <= 0 {
			connLimit = cfg.MaxConnsPerUser
		}
		d.connMgr.SetLimit(uid, connLimit)
	}
	for uid := range d.usersByID {
		if _, ok := live[uid]; ok {
			continue
		}
		removed = append(removed, uid)
		d.limiter.RemoveUser(uid)
		d.connMgr.SetLimit(uid, 0)
		delete(d.onlineSeen, uid)
	}
	d.usersByID = live
	if d.proxy != nil && len(removed) > 0 {
		kicked := d.proxy.KickUsers(removed)
		if kicked > 0 {
			d.log.Info("atp sessions evicted for removed users", zap.Int("users", len(removed)), zap.Int("sessions", kicked))
		}
	}
}

func (d *ATPDriver) applyRulesLocked(rules []model.DetectRule) {
	items := make([]atpAudit.Rule, 0, len(rules))
	for _, rule := range rules {
		items = append(items, atpAudit.Rule{
			ID:    int32(rule.ID),
			Regex: strings.TrimSpace(rule.Regex),
			Type:  rule.Type,
		})
	}
	d.audit.Update(items)
	d.ruleCount = len(items)
}

func (d *ATPDriver) updateStoreLocked(cfg ATPConfig) {
	usersByID := make(map[int32]atpState.UserPolicy, len(cfg.Users))
	usersByToken := make(map[string][]atpState.UserPolicy, len(cfg.Users))
	usersByPwd := make(map[string][]atpState.UserPolicy, len(cfg.Users))
	for _, user := range cfg.Users {
		uid := int32(user.ID)
		token := strings.TrimSpace(user.UUID)
		if token == "" {
			token = strings.TrimSpace(user.Passwd)
		}
		pwd := strings.TrimSpace(user.Passwd)
		if uid <= 0 || token == "" {
			continue
		}
		policy := atpState.UserPolicy{
			UserID:         uid,
			Token:          token,
			Password:       pwd,
			NodeSpeedLimit: user.NodeSpeed,
			NodeConnector:  user.NodeConnector,
		}
		usersByID[uid] = policy
		usersByToken[token] = append(usersByToken[token], policy)
		if pwd != "" {
			usersByPwd[pwd] = append(usersByPwd[pwd], policy)
		}
	}
	d.store.Update(atpState.Snapshot{
		Node: atpState.NodeInfo{
			NodeGroup:      cfg.NodeInfo.NodeGroup,
			NodeClass:      cfg.NodeInfo.NodeClass,
			NodeSpeedlimit: cfg.NodeInfo.NodeSpeedLimit,
			TrafficRate:    cfg.NodeInfo.TrafficRate,
			Sort:           cfg.NodeInfo.Sort,
			Server:         cfg.NodeInfo.Server,
		},
		UsersByID:    usersByID,
		UsersByToken: usersByToken,
		UsersByPwd:   usersByPwd,
		UpdatedAt:    time.Now(),
	})
}

func (d *ATPDriver) restartProxyLocked(listen string, port int, transport, sni string, tlsCfg *tls.Config, cfg ATPConfig) error {
	if err := d.stopProxyLocked(context.Background()); err != nil {
		return err
	}
	atpTransport.SetLogger(d.log)
	proxy, err := atpProxy.New(atpProxy.Config{
		Listen:           listen,
		Port:             port,
		Transport:        transport,
		HandshakeTimeout: cfg.HandshakeTimeout,
		IdleTimeout:      cfg.IdleTimeout,
		MaxOpenStreams:   cfg.MaxOpenStreamsPerUser,
		TLSConfig:        tlsCfg,
		Reporter:         d.usage,
		Limiter:          d.limiter,
		ConnManager:      d.connMgr,
		AuditEngine:      d.audit,
		EnableAuditBlock: cfg.EnableAuditBlock,
		ResumeTicketTTL:  cfg.ResumeTicketTTL,
	}, d.log, d.store)
	if err != nil {
		return err
	}
	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- proxy.Start(proxyCtx)
		close(proxyDone)
	}()

	if d.certCancel != nil {
		d.certCancel()
	}
	certCtx, certCancel := context.WithCancel(context.Background())
	atpStartCertificateMaintenance(certCtx, d.log, tlsCfg, sni)

	d.proxy = proxy
	d.proxyCtx = proxyCtx
	d.proxyCancel = proxyCancel
	d.proxyDone = proxyDone
	d.certCancel = certCancel
	d.listen = listen
	d.port = port
	d.transport = transport
	d.tlsSNI = sni
	d.tlsCfg = tlsCfg
	d.lastProxyErr = nil
	d.proxyGen++
	d.lastRestartAt = time.Now()
	return nil
}

func (d *ATPDriver) stopProxyLocked(ctx context.Context) error {
	var outErr error
	if d.proxyCancel != nil {
		d.proxyCancel()
		d.proxyCancel = nil
	}
	if d.certCancel != nil {
		d.certCancel()
		d.certCancel = nil
	}
	if d.proxy != nil {
		if err := d.proxy.Close(); err != nil {
			outErr = err
		}
	}
	if d.proxyDone != nil {
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		select {
		case err, ok := <-d.proxyDone:
			if ok && err != nil && !errors.Is(err, context.Canceled) && outErr == nil {
				outErr = err
			}
		case <-waitCtx.Done():
			if outErr == nil {
				outErr = waitCtx.Err()
			}
		}
	}
	d.proxy = nil
	d.proxyDone = nil
	d.proxyCtx = nil
	return outErr
}

func (d *ATPDriver) consumeProxyErrLocked() error {
	if d.proxyDone == nil {
		return nil
	}
	select {
	case err, ok := <-d.proxyDone:
		if !ok {
			d.proxyDone = nil
			d.proxy = nil
			d.proxyCtx = nil
			d.proxyCancel = nil
			if d.lastProxyErr != nil {
				return d.lastProxyErr
			}
			return nil
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			d.lastProxyErr = err
			d.proxyDone = nil
			d.proxy = nil
			d.proxyCtx = nil
			d.proxyCancel = nil
			return err
		}
	default:
	}
	if d.lastProxyErr != nil {
		return d.lastProxyErr
	}
	return nil
}

func (d *ATPDriver) resetProxyStateLocked() {
	if d.proxyCancel != nil {
		d.proxyCancel()
	}
	if d.certCancel != nil {
		d.certCancel()
		d.certCancel = nil
	}
	if d.proxy != nil {
		_ = d.proxy.Close()
	}
	d.proxy = nil
	d.proxyDone = nil
	d.proxyCtx = nil
	d.proxyCancel = nil
	d.lastProxyErr = nil
}

func toATPPanelNode(n model.NodeInfo) atpPanel.NodeInfo {
	return atpPanel.NodeInfo{
		NodeGroup:      n.NodeGroup,
		NodeClass:      n.NodeClass,
		NodeSpeedlimit: n.NodeSpeedLimit,
		TrafficRate:    n.TrafficRate,
		Sort:           n.Sort,
		Server:         n.Server,
	}
}

func (d *ATPDriver) logHealthLocked(prevUsers, prevRules int, prevListen string, prevPort int, prevTransport, prevSNI string) {
	now := time.Now()
	users := len(d.usersByID)
	rules := d.ruleCount
	changed := users != prevUsers || rules != prevRules || d.listen != prevListen || d.port != prevPort || !strings.EqualFold(d.transport, prevTransport) || d.tlsSNI != prevSNI
	if !changed && !d.lastLogAt.IsZero() && now.Sub(d.lastLogAt) < 2*time.Minute {
		return
	}
	d.lastLogAt = now
	d.lastLogUsers = users
	d.lastLogRules = rules
	fields := []zap.Field{
		zap.Int("users", users),
		zap.Int("rules", rules),
		zap.String("listen", d.listen),
		zap.Int("port", d.port),
		zap.String("transport", d.transport),
		zap.String("sni", d.tlsSNI),
		zap.Bool("proxy_active", d.proxy != nil),
		zap.Int("proxy_generation", d.proxyGen),
	}
	if !d.certNotAfter.IsZero() {
		fields = append(fields, zap.Time("cert_not_after", d.certNotAfter), zap.Duration("cert_remaining", time.Until(d.certNotAfter)))
	}
	if d.lastProxyErr != nil {
		fields = append(fields, zap.Error(d.lastProxyErr))
	}
	if changed {
		d.log.Info("atp runtime health changed", fields...)
		return
	}
	d.log.Debug("atp runtime health", fields...)
}

func readCertNotAfter(tlsCfg *tls.Config, sni string) (time.Time, error) {
	if tlsCfg == nil {
		return time.Time{}, fmt.Errorf("tls config is nil")
	}
	if strings.TrimSpace(sni) == "" {
		return time.Time{}, fmt.Errorf("sni is empty")
	}
	if tlsCfg.GetCertificate == nil {
		return time.Time{}, fmt.Errorf("tls get certificate callback is nil")
	}
	chi := &tls.ClientHelloInfo{ServerName: sni, SupportedProtos: []string{"h2", "http/1.1"}}
	cert, err := tlsCfg.GetCertificate(chi)
	if err != nil {
		return time.Time{}, err
	}
	if cert == nil || len(cert.Certificate) == 0 {
		return time.Time{}, fmt.Errorf("empty tls certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}
