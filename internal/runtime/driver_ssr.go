package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/model"

	vnetCommon "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common"
	vnetNetwork "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/network"
	vnetObfs "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/common/obfs"
	vnetCore "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/core"
	vnetServer "github.com/jashok5/shadowsocks-go/internal/protocol/ssr/proxy/server"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/binaryx"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ssr/utils/socksproxy"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	ssrHandshakeTimeout          = 10 * time.Second
	ssrRelayIOTimeout            = 120 * time.Second
	ssrStopTimeout               = 15 * time.Second
	maxSSRUDPResolveCacheEntries = 4096
	ssrUDPResolveTTL             = 30 * time.Second
)

type SSRDriver struct {
	log *zap.Logger

	mu      sync.RWMutex
	servers map[int]*ssrInstance

	trafficMu    sync.RWMutex
	transfer     map[int]model.PortTransfer
	userTransfer map[int]model.PortTransfer
	onlineIP     map[int]map[string]struct{}
	userOnlineIP map[int]map[string]struct{}
	wrongIP      map[string]time.Time
	userDetect   map[int]map[int]struct{}
	authGuard    *authFailGuard
	maxResolve   int
	handshakeCap int
	perIPCap     int
}

type ssrInstance struct {
	port      int
	proxy     *vnetServer.ShadowsocksRProxy
	cfg       PortConfig
	usersPack map[string]string
	detect    map[int]struct{}
	tcp       net.Listener
	udp       net.PacketConn
	limitUp   map[int]*rate.Limiter
	limitDown map[int]*rate.Limiter
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	connTrack *connTracker
	handshake chan struct{}

	resolveMu       sync.Mutex
	resolvedAddrTTL *sessionCache
}

type SSRPortCacheStat struct {
	Port            int
	UDPResolveCache SessionCacheSnapshot
}

func NewSSRDriverWithTuning(log *zap.Logger, tuning DriverTuning) *SSRDriver {
	d := &SSRDriver{
		log:          log,
		servers:      make(map[int]*ssrInstance),
		transfer:     make(map[int]model.PortTransfer),
		userTransfer: make(map[int]model.PortTransfer),
		onlineIP:     make(map[int]map[string]struct{}),
		userOnlineIP: make(map[int]map[string]struct{}),
		wrongIP:      make(map[string]time.Time),
		userDetect:   make(map[int]map[int]struct{}),
		authGuard:    newAuthFailGuard(log, "ssr"),
		maxResolve:   tuning.maxUDPResolveCacheEntriesOr(maxSSRUDPResolveCacheEntries),
		handshakeCap: resolveHandshakeLimit(tuning.HandshakeMaxConcurrent),
		perIPCap:     resolvePerIPHandshakeLimit(tuning.PerIPHandshakeMax),
	}
	if d.authGuard != nil {
		d.authGuard.inflightMax = d.perIPCap
	}
	return d
}

func (d *SSRDriver) Start(_ context.Context, cfg PortConfig) error {
	if strings.TrimSpace(cfg.Method) == "" {
		cfg.Method = "chacha20-ietf"
	}
	if strings.TrimSpace(cfg.Protocol) == "" {
		cfg.Protocol = "origin"
	}
	if strings.TrimSpace(cfg.Obfs) == "" {
		cfg.Obfs = "plain"
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.servers[cfg.Port]; ok {
		return fmt.Errorf("ssr port %d already started", cfg.Port)
	}

	usersPack := convertUsers(cfg.Users)
	proxy := &vnetServer.ShadowsocksRProxy{
		Host:          "0.0.0.0",
		Port:          cfg.Port,
		Method:        cfg.Method,
		Password:      cfg.Password,
		Protocol:      cfg.Protocol,
		ProtocolParam: cfg.ProtocolParam,
		Obfs:          cfg.Obfs,
		ObfsParam:     buildMultiUserObfsParam(cfg),
		Single:        boolToSingle(cfg.IsMultiUser),
		Users:         usersPack,
		ShadowsocksRArgs: &vnetServer.ShadowsocksRArgs{
			TCPSwitch: "false",
			UDPSwitch: "false",
		},
		TrafficReport: trafficReporter(func(uid int, up, down int64) {
			d.addTraffic(cfg.Port, uid, up, down)
		}),
		OnlineReport: onlineReporter(func(uid int, ip string) {
			d.addOnlineIP(cfg.Port, uid, ip)
		}),
		HostFirewall: newHostFirewall(cfg.ForbiddenIP, cfg.ForbiddenPort, cfg.Detect, func(ruleID int) {
			d.markDetect(cfg.Port, ruleID)
		}),
	}

	instCtx, cancel := context.WithCancel(context.Background())
	inst := &ssrInstance{port: cfg.Port, proxy: proxy, cfg: cfg, usersPack: usersPack, detect: make(map[int]struct{}), limitUp: make(map[int]*rate.Limiter), limitDown: make(map[int]*rate.Limiter), ctx: instCtx, cancel: cancel, connTrack: newConnTracker(), handshake: make(chan struct{}, d.handshakeCap)}
	inst.resolvedAddrTTL = newSessionCache(d.maxResolve, ssrUDPResolveTTL, nil)
	for uid, speed := range cfg.UserSpeed {
		inst.limitUp[uid] = newRateLimiter(speed)
		inst.limitDown[uid] = newRateLimiter(speed)
	}
	if len(inst.limitUp) == 0 {
		inst.limitUp[cfg.UserID] = newRateLimiter(cfg.NodeSpeedLimit)
		inst.limitDown[cfg.UserID] = newRateLimiter(cfg.NodeSpeedLimit)
	}
	d.servers[cfg.Port] = inst
	vnetCore.GetApp().SetObfsProtocolService(vnetObfs.NewObfsAuthChainData(cfg.Protocol))
	_ = vnetCore.GetApp().Init()
	if err := d.startTCPListener(inst); err != nil {
		delete(d.servers, cfg.Port)
		cancel()
		return fmt.Errorf("start ssr tcp port %d failed: %w", cfg.Port, err)
	}
	if err := d.startUDPListener(inst); err != nil {
		_ = inst.tcp.Close()
		delete(d.servers, cfg.Port)
		cancel()
		return fmt.Errorf("start ssr udp port %d failed: %w", cfg.Port, err)
	}
	startPeriodicBackground(&inst.wg, inst.ctx, 30*time.Second, func(now time.Time) {
		d.cleanWrongIP(now)
	}, nil)
	startPeriodicBackground(&inst.wg, inst.ctx, 30*time.Second, func(now time.Time) {
		d.cleanResolvedUDPAddr(inst, now)
	}, func() {
		closeSessionCache(&inst.resolveMu, inst.resolvedAddrTTL)
	})

	d.log.Info("ssr driver start", zap.Int("port", cfg.Port), zap.String("method", cfg.Method), zap.String("protocol", cfg.Protocol), zap.String("obfs", cfg.Obfs))
	return nil
}

func (d *SSRDriver) Reload(ctx context.Context, cfg PortConfig) error {
	d.mu.RLock()
	inst, ok := d.servers[cfg.Port]
	d.mu.RUnlock()
	if ok && canHotReloadSSR(inst.cfg, cfg) {
		usersPack := convertUsers(cfg.Users)
		inst.proxy.Reload(usersPack)
		d.mu.Lock()
		inst.cfg = cfg
		inst.usersPack = usersPack
		d.mu.Unlock()
		d.log.Info("ssr driver hot reload users", zap.Int("port", cfg.Port), zap.Int("users", len(cfg.Users)))
		return nil
	}
	if err := d.Stop(ctx, cfg.Port); err != nil {
		return err
	}
	return d.Start(ctx, cfg)
}

func (d *SSRDriver) Stop(ctx context.Context, port int) error {
	var cancel context.CancelFunc
	ctx, cancel = normalizeStopContext(ctx, ssrStopTimeout)
	defer cancel()
	d.mu.Lock()
	inst, ok := d.servers[port]
	if ok {
		delete(d.servers, port)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	inst.closeActiveTCP()
	if inst.proxy != nil && inst.proxy.Listener != nil {
		_ = inst.proxy.Listener.Close()
	}
	inst.cancel()
	if inst.tcp != nil {
		_ = inst.tcp.Close()
	}
	if inst.udp != nil {
		_ = inst.udp.Close()
	}
	if err := waitGroupWithContext(ctx, &inst.wg); err != nil {
		d.log.Warn("ssr driver stop timeout", zap.Int("port", port), zap.Error(err))
		return err
	}
	d.log.Info("ssr driver stop", zap.Int("port", port))
	return nil
}

func (inst *ssrInstance) addActiveTCP(conn net.Conn) {
	trackerAddConn(inst.connTrack, conn)
}

func (inst *ssrInstance) removeActiveTCP(conn net.Conn) {
	trackerRemoveConn(inst.connTrack, conn)
}

func (inst *ssrInstance) closeActiveTCP() {
	trackerCloseAll(inst.connTrack)
}

func (d *SSRDriver) Snapshot(_ context.Context) (DriverSnapshot, error) {
	d.mu.Lock()
	detect := make(map[int][]int, len(d.servers))
	for port, inst := range d.servers {
		ids := make([]int, 0, len(inst.detect))
		for id := range inst.detect {
			ids = append(ids, id)
		}
		detect[port] = ids
		clear(inst.detect)
	}
	d.mu.Unlock()

	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()

	transfer := make(map[int]model.PortTransfer, len(d.transfer))
	maps.Copy(transfer, d.transfer)
	userTransfer := make(map[int]model.PortTransfer, len(d.userTransfer))
	maps.Copy(userTransfer, d.userTransfer)
	online := make(map[int][]string, len(d.onlineIP))
	for p, m := range d.onlineIP {
		arr := make([]string, 0, len(m))
		for ip := range m {
			arr = append(arr, ip)
		}
		online[p] = arr
	}
	userOnline := make(map[int][]string, len(d.userOnlineIP))
	for uid, m := range d.userOnlineIP {
		arr := make([]string, 0, len(m))
		for ip := range m {
			arr = append(arr, ip)
		}
		userOnline[uid] = arr
	}
	userDetect := make(map[int][]int, len(d.userDetect))
	for uid, rules := range d.userDetect {
		ids := make([]int, 0, len(rules))
		for id := range rules {
			ids = append(ids, id)
		}
		userDetect[uid] = ids
	}
	wrongIP := make([]string, 0, len(d.wrongIP))
	for ip := range d.wrongIP {
		wrongIP = append(wrongIP, ip)
	}
	sort.Strings(wrongIP)

	for k := range d.onlineIP {
		delete(d.onlineIP, k)
	}
	for k := range d.userOnlineIP {
		delete(d.userOnlineIP, k)
	}
	for k := range d.userDetect {
		delete(d.userDetect, k)
	}

	return DriverSnapshot{Transfer: transfer, UserTransfer: userTransfer, OnlineIP: online, UserOnlineIP: userOnline, Detect: detect, UserDetect: userDetect, WrongIP: wrongIP}, nil
}

func (d *SSRDriver) Close(ctx context.Context) error {
	d.mu.RLock()
	ports := make([]int, 0, len(d.servers))
	for p := range d.servers {
		ports = append(ports, p)
	}
	d.mu.RUnlock()
	for _, p := range ports {
		if err := d.Stop(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (d *SSRDriver) addTraffic(port int, uid int, up int64, down int64) {
	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()
	v := d.transfer[port]
	v.Upload += up
	v.Download += down
	d.transfer[port] = v
	if uid > 0 {
		u := d.userTransfer[uid]
		u.Upload += up
		u.Download += down
		d.userTransfer[uid] = u
	}
}

func (d *SSRDriver) markUserDetect(userID int, ruleID int) {
	if userID <= 0 || ruleID <= 0 {
		return
	}
	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()
	if _, ok := d.userDetect[userID]; !ok {
		d.userDetect[userID] = make(map[int]struct{})
	}
	d.userDetect[userID][ruleID] = struct{}{}
}

func (d *SSRDriver) applySSRDetect(userID int, payload []byte, buckets DetectBuckets) {
	walkMatchedRulesBytes(payload, buckets, func(ruleID int) bool {
		d.markUserDetect(userID, ruleID)
		return true
	})
}

func (d *SSRDriver) addOnlineIP(port int, uid int, ip string) {
	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()
	if _, ok := d.onlineIP[port]; !ok {
		d.onlineIP[port] = make(map[string]struct{})
	}
	d.onlineIP[port][ip] = struct{}{}
	if uid > 0 {
		if _, ok := d.userOnlineIP[uid]; !ok {
			d.userOnlineIP[uid] = make(map[string]struct{})
		}
		d.userOnlineIP[uid][ip] = struct{}{}
	}
}

func (d *SSRDriver) markWrongIP(ip string) {
	if strings.TrimSpace(ip) == "" {
		return
	}
	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()
	d.wrongIP[ip] = time.Now()
}

func (d *SSRDriver) markDetect(port int, ruleID int) {
	if ruleID <= 0 {
		return
	}
	userID := 0
	d.mu.Lock()
	inst, ok := d.servers[port]
	if ok {
		inst.detect[ruleID] = struct{}{}
		userID = inst.cfg.UserID
	}
	d.mu.Unlock()
	if userID > 0 {
		d.markUserDetect(userID, ruleID)
	}
}

type trafficReporter func(uid int, up, down int64)

func (r trafficReporter) Upload(uid int, n int64) { r(uid, n, 0) }

func (r trafficReporter) Download(uid int, n int64) { r(uid, 0, n) }

type onlineReporter func(uid int, ip string)

func (r onlineReporter) Online(uid int, ip string) { r(uid, ip) }

type hostFirewall struct {
	forbiddenIP   string
	forbiddenPort string
	detect        DetectBuckets
	onRuleHit     func(int)
}

func newHostFirewall(ip, port string, detect DetectBuckets, onRuleHit func(int)) *hostFirewall {
	return &hostFirewall{forbiddenIP: ip, forbiddenPort: port, detect: detect, onRuleHit: onRuleHit}
}

func (h *hostFirewall) JudgeHostWithReport(ipOrDomain string, uid int) bool {
	host := ipOrDomain
	port := 0
	if hp, pp, ok := strings.Cut(ipOrDomain, ":"); ok {
		host = hp
		if p, err := strconv.Atoi(pp); err == nil {
			port = p
		}
	}
	if blockedIP(host, h.forbiddenIP) {
		return false
	}
	if port > 0 && blockedPort(port, h.forbiddenPort) {
		return false
	}
	blockedByDetect := false
	walkMatchedRules(ipOrDomain, h.detect, func(ruleID int) bool {
		if h.onRuleHit != nil {
			h.onRuleHit(ruleID)
		}
		blockedByDetect = true
		return false
	})
	if blockedByDetect {
		return false
	}
	_ = uid
	return true
}

func convertUsers(users map[int]string) map[string]string {
	out := make(map[string]string, len(users))
	for uid, pass := range users {
		uidPack := string(binaryx.LEUint32ToBytes(uint32(uid)))
		out[uidPack] = pass
	}
	return out
}

func boolToSingle(isMultiUser bool) int {
	if isMultiUser {
		return 1
	}
	return 0
}

func buildMultiUserObfsParam(cfg PortConfig) string {
	base := strings.TrimSpace(cfg.ObfsParam)
	if !cfg.IsMultiUser || len(cfg.MUHosts) == 0 {
		return base
	}
	items := make([]string, 0, 1+len(cfg.MUHosts))
	seen := make(map[string]struct{}, 1+len(cfg.MUHosts))
	if base != "" {
		for part := range strings.SplitSeq(base, ",") {
			v := strings.TrimSpace(part)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			items = append(items, v)
		}
	}
	for _, host := range cfg.MUHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		items = append(items, host)
	}
	return strings.Join(items, ",")
}

func canHotReloadSSR(oldCfg, newCfg PortConfig) bool {
	if oldCfg.Port != newCfg.Port {
		return false
	}
	if oldCfg.IsMultiUser != newCfg.IsMultiUser || !newCfg.IsMultiUser {
		return false
	}
	if oldCfg.Password != newCfg.Password {
		return false
	}
	if oldCfg.Method != newCfg.Method || oldCfg.Protocol != newCfg.Protocol || oldCfg.Obfs != newCfg.Obfs {
		return false
	}
	if oldCfg.ProtocolParam != newCfg.ProtocolParam || oldCfg.ObfsParam != newCfg.ObfsParam {
		return false
	}
	if oldCfg.ForbiddenIP != newCfg.ForbiddenIP || oldCfg.ForbiddenPort != newCfg.ForbiddenPort {
		return false
	}
	if !sameStringSlice(oldCfg.MUHosts, newCfg.MUHosts) {
		return false
	}
	return true
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (d *SSRDriver) startTCPListener(inst *ssrInstance) error {
	tcpLn, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", inst.port))
	if err != nil {
		return err
	}
	inst.tcp = tcpLn
	inst.wg.Go(func() {
		d.serveSSRTCP(inst)
	})
	return nil
}

func (d *SSRDriver) serveSSRTCP(inst *ssrInstance) {
	for {
		conn, err := inst.tcp.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || inst.ctx.Err() != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		ip := addrIP(conn.RemoteAddr())
		if d.isAuthBlocked(ip) {
			d.logAuthSampled(ip, "ssr tcp blocked by auth-fail threshold", zap.Int("port", inst.port))
			_ = conn.Close()
			continue
		}
		if !authTryAcquireHandshake(d.authGuard, ip) {
			d.logAuthSampled(ip, "ssr tcp drop by per-ip handshake limit", zap.Int("port", inst.port))
			_ = conn.Close()
			continue
		}
		select {
		case <-inst.ctx.Done():
			authReleaseHandshake(d.authGuard, ip)
			_ = conn.Close()
			return
		case inst.handshake <- struct{}{}:
		default:
			authReleaseHandshake(d.authGuard, ip)
			d.logAuthSampled(ip, "ssr tcp drop by handshake concurrency limit", zap.Int("port", inst.port))
			_ = conn.Close()
			continue
		}
		inst.wg.Add(1)
		go func(c net.Conn, ip string) {
			defer inst.wg.Done()
			defer func() { <-inst.handshake }()
			defer authReleaseHandshake(d.authGuard, ip)
			inst.addActiveTCP(c)
			defer inst.removeActiveTCP(c)
			defer c.Close()

			request := vnetNetwork.NewRequestWithTCP(c)
			ssrd, err := vnetNetwork.NewShadowsocksRDecorate(request,
				inst.cfg.Obfs, inst.cfg.Method,
				inst.cfg.Password, inst.cfg.Protocol,
				inst.cfg.ObfsParam, inst.cfg.ProtocolParam,
				"0.0.0.0", inst.port,
				false,
				boolToSingle(inst.cfg.IsMultiUser),
				inst.usersPack,
			)
			if err != nil || ssrd == nil {
				clientIP := addrIP(c.RemoteAddr())
				d.markWrongIP(clientIP)
				d.recordAuthFail(clientIP, "ssr tcp decorate failed")
				d.log.Debug("ssr tcp decorate failed", zap.Int("port", inst.port), zap.Error(err))
				return
			}
			ssrd.TrafficReport = inst.proxy.TrafficReport

			_ = c.SetReadDeadline(time.Now().Add(ssrHandshakeTimeout))
			addr, err := socksproxy.ReadAddr(ssrd)
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				clientIP := addrIP(c.RemoteAddr())
				d.markWrongIP(clientIP)
				d.recordAuthFail(clientIP, "ssr tcp read target failed")
				d.log.Debug("ssr tcp read target failed", zap.Int("port", inst.port), zap.Error(err))
				return
			}
			if addr == nil {
				return
			}

			uid := ssrd.UID
			if uid == 0 {
				uid = inst.cfg.UserID
			}
			clientIP := addrIP(c.RemoteAddr())
			if inst.proxy.OnlineReport != nil && uid > 0 && clientIP != "" {
				inst.proxy.OnlineReport.Online(uid, clientIP)
			}
			if inst.proxy.HostFirewall != nil && !inst.proxy.HostFirewall.JudgeHostWithReport(addr.GetAddress(), uid) {
				return
			}
			d.applySSRDetect(uid, []byte(addr.String()), inst.cfg.Detect)
			upLimiter := inst.limitUp[uid]
			downLimiter := inst.limitDown[uid]

			remote, err := dialTCPWithConfig(inst.ctx, inst.cfg, addr.String())
			if err != nil {
				d.log.Debug("ssr tcp dial target failed", zap.String("target", addr.String()), zap.Error(err))
				return
			}
			defer remote.Close()
			relayQuietLimited(inst.ctx, ssrd, remote, upLimiter, downLimiter)
		}(conn, ip)
	}
}

func relayQuietLimited(ctx context.Context, left net.Conn, right net.Conn, upLimiter, downLimiter *rate.Limiter) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		relayOneWayLimited(ctx, left, right, upLimiter)
	}()
	go func() {
		defer wg.Done()
		relayOneWayLimited(ctx, right, left, downLimiter)
	}()
	wg.Wait()
}

func relayOneWayLimited(ctx context.Context, src net.Conn, dst net.Conn, limiter *rate.Limiter) {
	buf := acquireRelayBuf()
	defer releaseRelayBuf(buf)
	for {
		if err := src.SetReadDeadline(time.Now().Add(ssrRelayIOTimeout)); err != nil {
			return
		}
		n, err := src.Read(buf)
		if n > 0 {
			_ = waitLimiter(ctx, limiter, n)
			_ = dst.SetWriteDeadline(time.Now().Add(ssrRelayIOTimeout))
			_, _ = dst.Write(buf[:n])
		}
		if err != nil {
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					if c, ok := dst.(*net.TCPConn); ok {
						_ = c.CloseWrite()
					}
					return
				default:
					continue
				}
			}
			if c, ok := dst.(*net.TCPConn); ok {
				_ = c.CloseWrite()
			}
			return
		}
	}
}

func (d *SSRDriver) startUDPListener(inst *ssrInstance) error {
	vnetCore.GetApp().SetObfsProtocolService(vnetObfs.NewObfsAuthChainData(inst.cfg.Protocol))
	_ = vnetCore.GetApp().Init()

	udpConn, err := net.ListenPacket("udp", fmt.Sprintf("0.0.0.0:%d", inst.port))
	if err != nil {
		return err
	}
	inst.udp = udpConn
	startBackgroundTasks(&inst.wg, func() { d.serveSSRUDP(inst) })
	return nil
}

func (d *SSRDriver) serveSSRUDP(inst *ssrInstance) {
	request := vnetNetwork.NewRequestWithUDP(inst.udp)
	ssrd, err := vnetNetwork.NewShadowsocksRDecorate(request,
		inst.cfg.Obfs, inst.cfg.Method,
		inst.cfg.Password, inst.cfg.Protocol,
		inst.cfg.ObfsParam, inst.cfg.ProtocolParam,
		"0.0.0.0", inst.port,
		false,
		boolToSingle(inst.cfg.IsMultiUser),
		inst.usersPack,
	)
	if err != nil || ssrd == nil {
		d.log.Error("new ssr decorate for udp failed", zap.Int("port", inst.port), zap.Error(err))
		return
	}
	ssrd.TrafficReport = inst.proxy.TrafficReport
	udpMap := vnetServer.NewShadowsocksRUDPMap(30)

	for {
		select {
		case <-inst.ctx.Done():
			return
		default:
		}

		data, uid, addr, rerr := ssrd.ReadFrom()
		if rerr != nil {
			if errors.Is(rerr, net.ErrClosed) || strings.Contains(rerr.Error(), "use of closed network connection") {
				return
			}
			d.markWrongIP(addrIP(addr))
			continue
		}
		remoteAddr, rerr := socksproxy.SplitAddr(data)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			continue
		}
		if remoteAddr == nil {
			d.markWrongIP(addrIP(addr))
			continue
		}
		payload := data[len(remoteAddr.Raw):]
		uidInt := int(binaryx.LEBytesToUInt32(uid))
		if uidInt == 0 {
			uidInt = inst.cfg.UserID
		}
		if inst.proxy.OnlineReport != nil {
			inst.proxy.OnlineReport.Online(uidInt, addrIP(addr))
		}
		d.clearWrongIP(addrIP(addr))
		if inst.proxy.HostFirewall != nil && !inst.proxy.HostFirewall.JudgeHostWithReport(remoteAddr.GetAddress(), uidInt) {
			continue
		}
		d.applySSRDetect(uidInt, payload, inst.cfg.Detect)
		upLimiter := inst.limitUp[uidInt]

		remotePacketConn := udpMap.Get(addr.String())
		if remotePacketConn == nil {
			remotePacketConn = &vnetServer.ShadowsocksRUDPMapItem{Uid: uid}
			remotePacketConn.PacketConn, rerr = net.ListenPacket("udp", "")
			if rerr != nil {
				continue
			}
			udpMap.Add(addr, ssrd, remotePacketConn)
		}
		remoteResolve, rerr := inst.resolveUDPAddrWithCache(remoteAddr.String())
		if rerr != nil {
			d.markWrongIP(addrIP(addr))
			continue
		}
		_ = waitLimiter(inst.ctx, upLimiter, len(payload))
		if _, err = remotePacketConn.WriteTo(payload, remoteResolve); err != nil {
			d.markWrongIP(addrIP(addr))
			continue
		}
	}
}

func addrIP(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}

func (d *SSRDriver) clearWrongIP(ip string) {
	if strings.TrimSpace(ip) == "" {
		return
	}
	d.trafficMu.Lock()
	defer d.trafficMu.Unlock()
	delete(d.wrongIP, ip)
}

func (d *SSRDriver) isAuthBlocked(ip string) bool {
	return authShouldBlock(d.authGuard, ip)
}

func (d *SSRDriver) recordAuthFail(ip string, reason string) {
	authRecordFail(d.authGuard, ip, reason)
}

func (d *SSRDriver) logAuthSampled(ip string, msg string, fields ...zap.Field) {
	authLogSampled(d.authGuard, ip, msg, fields...)
}

func (d *SSRDriver) cleanWrongIP(now time.Time) {
	cut := now.Add(-60 * time.Second)
	d.trafficMu.Lock()
	for ip, ts := range d.wrongIP {
		if ts.Before(cut) {
			delete(d.wrongIP, ip)
		}
	}
	d.trafficMu.Unlock()
	if d.authGuard != nil {
		d.authGuard.SweepNow()
	}
}

func (d *SSRDriver) cleanResolvedUDPAddr(inst *ssrInstance, now time.Time) {
	sweepSessionCache(&inst.resolveMu, inst.resolvedAddrTTL, now)
}

func (inst *ssrInstance) resolveUDPAddrWithCache(target string) (*net.UDPAddr, error) {
	return resolveUDPAddrFromCache(&inst.resolveMu, inst.resolvedAddrTTL, target, func(v string) (*net.UDPAddr, error) {
		return resolveUDPAddrWithConfig(inst.ctx, inst.cfg, v)
	}, "invalid ssr udp resolve cache item type")
}

func (d *SSRDriver) CacheStats() []SSRPortCacheStat {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]SSRPortCacheStat, 0, len(d.servers))
	for port, inst := range d.servers {
		inst.resolveMu.Lock()
		snap := SessionCacheSnapshot{}
		if inst.resolvedAddrTTL != nil {
			snap = inst.resolvedAddrTTL.Snapshot()
		}
		inst.resolveMu.Unlock()
		out = append(out, SSRPortCacheStat{Port: port, UDPResolveCache: snap})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

var _ vnetCommon.TrafficReport = (trafficReporter)(nil)
var _ vnetCommon.OnlineReport = (onlineReporter)(nil)
