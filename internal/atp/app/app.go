package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/atp/audit"
	"github.com/jashok5/shadowsocks-go/internal/atp/concurrency"
	"github.com/jashok5/shadowsocks-go/internal/atp/initialize"
	"github.com/jashok5/shadowsocks-go/internal/atp/limiter"
	"github.com/jashok5/shadowsocks-go/internal/atp/panel"
	"github.com/jashok5/shadowsocks-go/internal/atp/proxy"
	"github.com/jashok5/shadowsocks-go/internal/atp/reporter"
	"github.com/jashok5/shadowsocks-go/internal/atp/state"
	"github.com/jashok5/shadowsocks-go/internal/atp/transport"

	"go.uber.org/zap"
)

type App struct {
	cfg     *initialize.Config
	logger  *zap.Logger
	tlsCfg  *tls.Config
	tlsSNI  string
	store   *state.Store
	panel   *panel.Client
	proxy   *proxy.Server
	usage   *reporter.Collector
	limiter *limiter.Manager
	connMgr *concurrency.Manager
	audit   *audit.Engine
	retryQ  *reporter.RetryQueue
	startAt time.Time
}

func New(cfg *initialize.Config, logger *zap.Logger) (*App, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	store := state.NewStore()
	usage := reporter.NewCollector()
	rl := limiter.NewManager(cfg.Policy.DefaultUserMbps)
	cm := concurrency.NewManager()
	auditEngine := audit.NewEngine()
	retryPath := filepath.Join("data", "traffic-retry.json")
	retryQueue, err := reporter.NewRetryQueue(256, retryPath)
	if err != nil {
		return nil, fmt.Errorf("init retry queue: %w", err)
	}
	pc := initialize.BuildPanelClient(cfg)

	tlsInitCtx, cancelTLSInit := context.WithTimeout(context.Background(), cfg.Panel.Timeout)
	nodeForTLS, err := initialize.FetchNodeInfoForTLS(tlsInitCtx, cfg, pc)
	cancelTLSInit()
	if err != nil {
		return nil, fmt.Errorf("prepare tls node info failed: %w", err)
	}
	if err = initialize.ApplyServerEndpointFromNode(cfg, nodeForTLS); err != nil {
		return nil, fmt.Errorf("apply server endpoint from node failed: %w", err)
	}
	logger.Info("resolved startup endpoint from panel node",
		zap.Int32("node_id", cfg.Server.NodeID),
		zap.String("listen", cfg.Server.Listen),
		zap.Int("port", cfg.Server.Port),
		zap.String("transport", cfg.Server.Transport),
		zap.String("sni", initialize.ExtractSNIFromNode(nodeForTLS)),
	)
	transport.SetLogger(logger)
	sni := initialize.ExtractSNIFromNode(nodeForTLS)

	tlsCfg, err := initialize.BuildTLSConfig(cfg, nodeForTLS)
	if err != nil {
		return nil, err
	}

	px, err := proxy.New(proxy.Config{
		Listen:           cfg.Server.Listen,
		Port:             cfg.Server.Port,
		Transport:        cfg.Server.Transport,
		HandshakeTimeout: cfg.Server.HandshakeTimeout,
		IdleTimeout:      cfg.Server.IdleTimeout,
		ResumeTicketTTL:  cfg.Server.ResumeTicketTTL,
		MaxOpenStreams:   cfg.Policy.MaxOpenStreamsPerUser,
		TLSConfig:        tlsCfg,
		Reporter:         usage,
		Limiter:          rl,
		ConnManager:      cm,
		AuditEngine:      auditEngine,
		EnableAuditBlock: cfg.Policy.EnableAuditBlock,
	}, logger, store)
	if err != nil {
		return nil, err
	}

	return &App{cfg: cfg, logger: logger, tlsCfg: tlsCfg, tlsSNI: sni, store: store, panel: pc, proxy: px, usage: usage, limiter: rl, connMgr: cm, audit: auditEngine, retryQ: retryQueue, startAt: time.Now()}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.logger.Sync()

	if err := a.initialSync(ctx); err != nil {
		return err
	}
	errCh := make(chan error, 2)
	if err := initialize.EnsureCertificateReadyWithRetry(ctx, a.logger, a.tlsCfg, a.tlsSNI, 2*time.Minute, 3*time.Second); err != nil {
		panic(fmt.Sprintf("certificate bootstrap failed: %v", err))
	}
	initialize.StartCertificateMaintenance(ctx, a.logger, a.tlsCfg, a.tlsSNI)
	go func() {
		if err := a.proxy.Start(ctx); err != nil {
			errCh <- err
		}
	}()

	go a.refreshNodeLoop(ctx)
	go a.refreshUsersLoop(ctx)
	go a.refreshDetectRulesLoop(ctx)
	go a.reportNodeStatusLoop(ctx)
	go a.reportTrafficLoop(ctx)
	go a.reportAliveIPLoop(ctx)
	go a.reportDetectLogLoop(ctx)

	select {
	case <-ctx.Done():
		_ = a.proxy.Close()
		return nil
	case err := <-errCh:
		_ = a.proxy.Close()
		return err
	}
}

func (a *App) initialSync(ctx context.Context) error {
	node, err := a.panel.GetNodeInfo(ctx, a.cfg.Server.NodeID)
	if err != nil {
		return err
	}
	if node.Sort != 16 {
		return fmt.Errorf("node sort=%d is not ATP(16)", node.Sort)
	}
	users, err := a.panel.GetUsers(ctx, a.cfg.Server.NodeID)
	if err != nil {
		return err
	}
	a.store.Update(buildSnapshot(node, users))
	a.applyNodeLimit(node)
	a.applyUserPolicies(users)
	a.logger.Info("initial snapshot loaded", zap.Int("users", len(users)), zap.Int("sort", node.Sort))
	rules, err := a.panel.GetDetectRules(ctx)
	if err != nil {
		a.logger.Warn("initial detect rules fetch failed", zap.Error(err))
	} else {
		stats := a.audit.Update(convertRules(rules))
		a.logger.Info("initial detect rules loaded", zap.Int("total", stats.Total), zap.Int("active", stats.Active), zap.Int("regex_errors", stats.RegexErrors))
	}
	return nil
}

func (a *App) refreshNodeLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.PullInterval.NodeInfo)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			node, err := a.panel.GetNodeInfo(ctx, a.cfg.Server.NodeID)
			if err != nil {
				a.logger.Warn("refresh node failed", zap.Error(err))
				continue
			}
			if node.Sort != 16 {
				a.logger.Error("node sort changed to non-atp", zap.Int("sort", node.Sort))
				continue
			}
			a.applyNodeLimit(node)
			snap := a.store.Load()
			snap.Node = state.NodeInfo(node)
			snap.UpdatedAt = time.Now()
			a.store.Update(snap)
		}
	}
}

func (a *App) refreshUsersLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.PullInterval.Users)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			users, err := a.panel.GetUsers(ctx, a.cfg.Server.NodeID)
			if err != nil {
				a.logger.Warn("refresh users failed", zap.Error(err))
				continue
			}
			a.applyUserPolicies(users)
			snap := a.store.Load()
			snap.UsersByID, snap.UsersByToken, snap.UsersByPwd = buildUserIndexes(users)
			snap.UpdatedAt = time.Now()
			a.store.Update(snap)
		}
	}
}

func (a *App) refreshDetectRulesLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.PullInterval.DetectRules)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rules, err := a.panel.GetDetectRules(ctx)
			if err != nil {
				a.logger.Warn("refresh detect rules failed", zap.Error(err))
				continue
			}
			stats := a.audit.Update(convertRules(rules))
			a.logger.Info("detect rules refreshed", zap.Int("total", stats.Total), zap.Int("active", stats.Active), zap.Int("regex_errors", stats.RegexErrors))
		}
	}
}

func (a *App) reportNodeStatusLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.ReportEvery.NodeStatus)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uptime := strconv.FormatInt(int64(time.Since(a.startAt).Seconds()), 10)
			err := a.panel.PostNodeStatus(ctx, a.cfg.Server.NodeID, panel.NodeStatusRequest{Load: "0 0 0", Uptime: uptime})
			if err != nil {
				a.logger.Warn("report node status failed", zap.Error(err))
			}
		}
	}
}

func buildSnapshot(node panel.NodeInfo, users []panel.UserInfo) state.Snapshot {
	byID, byToken, byPwd := buildUserIndexes(users)
	return state.Snapshot{
		Node:         state.NodeInfo(node),
		UsersByID:    byID,
		UsersByToken: byToken,
		UsersByPwd:   byPwd,
		UpdatedAt:    time.Now(),
	}
}

func buildUserIndexes(users []panel.UserInfo) (map[int32]state.UserPolicy, map[string][]state.UserPolicy, map[string][]state.UserPolicy) {
	byID := make(map[int32]state.UserPolicy, len(users))
	byToken := make(map[string][]state.UserPolicy, len(users))
	byPwd := make(map[string][]state.UserPolicy, len(users))
	for _, user := range users {
		token := strings.TrimSpace(user.UUID)
		if token == "" {
			token = strings.TrimSpace(user.Passwd)
		}
		pwd := strings.TrimSpace(user.Passwd)
		if token == "" {
			continue
		}
		policy := state.UserPolicy{
			UserID:         user.ID,
			Token:          token,
			Password:       pwd,
			NodeSpeedLimit: user.NodeSpeedlimit,
			NodeConnector:  user.NodeConnector,
		}
		byID[user.ID] = policy
		byToken[token] = append(byToken[token], policy)
		if pwd != "" {
			byPwd[pwd] = append(byPwd[pwd], policy)
		}
	}
	return byID, byToken, byPwd
}

func (a *App) reportTrafficLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.ReportEvery.Traffic)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if b, ok := a.retryQ.DequeueDue(time.Now()); ok {
				rows := make([]panel.TrafficRecord, 0, len(b.Rows))
				for _, row := range b.Rows {
					rows = append(rows, panel.TrafficRecord{UserID: row.UserID, U: row.U, D: row.D})
				}
				if err := a.panel.PostTraffic(ctx, a.cfg.Server.NodeID, panel.TrafficRequest{Data: rows}); err != nil {
					a.retryQ.RequeueFailed(b, err.Error())
					a.logger.Warn("retry traffic failed", zap.Error(err), zap.Int("rows", len(rows)))
					continue
				}
			}

			traffic := a.usage.FlushTraffic()
			if len(traffic) == 0 {
				continue
			}
			records := make([]panel.TrafficRecord, 0, len(traffic))
			retryRows := make([]reporter.TrafficRow, 0, len(traffic))
			for userID, t := range traffic {
				u := safeInt64ToInt(t.U)
				d := safeInt64ToInt(t.D)
				records = append(records, panel.TrafficRecord{UserID: userID, U: u, D: d})
				retryRows = append(retryRows, reporter.TrafficRow{UserID: userID, U: u, D: d})
			}
			if err := a.panel.PostTraffic(ctx, a.cfg.Server.NodeID, panel.TrafficRequest{Data: records}); err != nil {
				a.retryQ.EnqueueRows(retryRows)
				a.logger.Warn("report traffic failed", zap.Error(err), zap.Int("users", len(records)))
			}
		}
	}
}

func (a *App) reportAliveIPLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.ReportEvery.AliveIP)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			alive := a.usage.FlushAliveIP()
			if len(alive) == 0 {
				continue
			}
			records := make([]panel.AliveIPRecord, 0)
			for userID, ips := range alive {
				for _, ip := range ips {
					records = append(records, panel.AliveIPRecord{IP: ip, UserID: userID})
				}
			}
			if len(records) == 0 {
				continue
			}
			if err := a.panel.PostAliveIP(ctx, a.cfg.Server.NodeID, panel.AliveIPRequest{Data: records}); err != nil {
				a.logger.Warn("report aliveip failed", zap.Error(err), zap.Int("records", len(records)))
			}
		}
	}
}

func (a *App) reportDetectLogLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Panel.ReportEvery.DetectLog)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logs := a.usage.FlushDetectLog()
			if len(logs) == 0 {
				continue
			}
			records := make([]panel.DetectLogRecord, 0, len(logs))
			for _, item := range logs {
				records = append(records, panel.DetectLogRecord{UserID: item.UserID, ListID: item.ListID})
			}
			if err := a.panel.PostDetectLog(ctx, a.cfg.Server.NodeID, panel.DetectLogRequest{Data: records}); err != nil {
				a.logger.Warn("report detectlog failed", zap.Error(err), zap.Int("records", len(records)))
			}
		}
	}
}

func safeInt64ToInt(v int64) int {
	if v > int64(math.MaxInt) {
		return math.MaxInt
	}
	if v < int64(math.MinInt) {
		return math.MinInt
	}
	return int(v)
}

func (a *App) applyNodeLimit(node panel.NodeInfo) {
	mbps := node.NodeSpeedlimit
	if mbps <= 0 {
		mbps = a.cfg.Policy.DefaultNodeMbps
	}
	a.limiter.SetNodeLimit(mbps)
}

func (a *App) applyUserPolicies(users []panel.UserInfo) {
	live := make(map[int32]struct{}, len(users))
	for _, user := range users {
		live[user.ID] = struct{}{}
		mbps := user.NodeSpeedlimit
		if mbps <= 0 {
			mbps = a.cfg.Policy.DefaultUserMbps
		}
		a.limiter.SetUserLimit(user.ID, mbps)
		limit := user.NodeConnector
		if limit <= 0 {
			limit = a.cfg.Policy.MaxConnsPerUser
		}
		a.connMgr.SetLimit(user.ID, limit)
	}
	for userID := range a.store.Load().UsersByID {
		if _, ok := live[userID]; !ok {
			a.limiter.RemoveUser(userID)
			a.connMgr.SetLimit(userID, 0)
		}
	}
}

func convertRules(in []panel.DetectRule) []audit.Rule {
	out := make([]audit.Rule, 0, len(in))
	for _, item := range in {
		out = append(out, audit.Rule{ID: item.ID, Name: item.Name, Text: item.Text, Regex: item.Regex, Type: item.Type})
	}
	return out
}
