package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/model"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ss/core"
	"github.com/jashok5/shadowsocks-go/internal/protocol/ss/socks"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	udpBufSize                  = 64 * 1024
	handshakeReadTimeout        = 10 * time.Second
	idleReadTimeout             = 30 * time.Second
	maxTCPConnPerPort           = 1024
	maxUDPPendingPerPort        = 256
	maxUDPSessionPerPort        = 2048
	maxSSUDPResolveCacheEntries = 4096
	udpSessionIdleTTL           = 30 * time.Second
	udpResolveIdleTTL           = 30 * time.Second
	udpSessionSweepEvery        = 10 * time.Second
)

type udpSession struct {
	mu            sync.Mutex
	conn          *net.UDPConn
	clientAddr    net.Addr
	srcAddr       []byte
	readerRunning bool
}

type ssPortRuntime struct {
	cfg PortConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	tcpListener net.Listener
	udpConn     net.PacketConn
	tcpSem      chan struct{}
	udpSem      chan struct{}
	connTrack   *connTracker
	limitUp     *rate.Limiter
	limitDown   *rate.Limiter
	firstDetect bool

	upload    int64
	download  int64
	activeTCP int64
	tcpDrop   int64
	udpDrop   int64

	onlineMu sync.RWMutex
	onlineIP map[string]struct{}

	detectMu sync.RWMutex
	detect   map[int]struct{}

	udpSessionMu    sync.Mutex
	udpSessionCache *sessionCache
	udpResolveMu    sync.Mutex
	udpResolveCache *sessionCache
}

type SSDriver struct {
	log *zap.Logger

	mu           sync.RWMutex
	ports        map[int]*ssPortRuntime
	authGuard    *authFailGuard
	handshakeS   chan struct{}
	handshakeCap int
	perIPCap     int
	maxUDPSess   int
	maxResolve   int
}

type PortRuntimeStat struct {
	Port            int
	ActiveTCP       int64
	TCPDrop         int64
	UDPDrop         int64
	UDPSessionCache SessionCacheSnapshot
	UDPResolveCache SessionCacheSnapshot
}

func NewSSDriver(log *zap.Logger) *SSDriver {
	return NewSSDriverWithTuning(log, DriverTuning{})
}

func NewSSDriverWithTuning(log *zap.Logger, tuning DriverTuning) *SSDriver {
	d := &SSDriver{
		log:          log,
		ports:        make(map[int]*ssPortRuntime),
		authGuard:    newAuthFailGuard(log, "ss"),
		handshakeCap: resolveHandshakeLimit(tuning.HandshakeMaxConcurrent),
		perIPCap:     resolvePerIPHandshakeLimit(tuning.PerIPHandshakeMax),
		maxUDPSess:   tuning.maxUDPSessionPerPortOr(maxUDPSessionPerPort),
		maxResolve:   tuning.maxUDPResolveCacheEntriesOr(maxSSUDPResolveCacheEntries),
	}
	if d.authGuard != nil {
		d.authGuard.inflightMax = d.perIPCap
	}
	d.ensureHandshakeSemaphore()
	return d
}

func (d *SSDriver) ensureHandshakeSemaphore() {
	if d.handshakeS != nil && cap(d.handshakeS) == d.handshakeCap {
		return
	}
	d.handshakeS = make(chan struct{}, d.handshakeCap)
}

func (d *SSDriver) Start(ctx context.Context, cfg PortConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureHandshakeSemaphore()
	if _, ok := d.ports[cfg.Port]; ok {
		return fmt.Errorf("port %d already started", cfg.Port)
	}
	rt, err := d.startPortLocked(ctx, cfg)
	if err != nil {
		return err
	}
	d.ports[cfg.Port] = rt
	d.log.Info("ss driver start", zap.Int("port", cfg.Port), zap.Int("user_id", cfg.UserID), zap.String("method", cfg.Method), zap.String("protocol", cfg.Protocol), zap.String("obfs", cfg.Obfs))
	return nil
}

func (d *SSDriver) Reload(ctx context.Context, cfg PortConfig) error {
	d.mu.RLock()
	rt, ok := d.ports[cfg.Port]
	d.mu.RUnlock()
	if ok && canHotReloadSS(rt.cfg, cfg) {
		d.mu.Lock()
		rt.cfg = cfg
		rt.limitUp = newRateLimiter(cfg.NodeSpeedLimit)
		rt.limitDown = newRateLimiter(cfg.NodeSpeedLimit)
		d.mu.Unlock()
		d.log.Info("ss driver hot reload", zap.Int("port", cfg.Port), zap.Int("user_id", cfg.UserID))
		return nil
	}
	if err := d.Stop(ctx, cfg.Port); err != nil {
		return err
	}
	return d.Start(ctx, cfg)
}

func (d *SSDriver) Stop(ctx context.Context, port int) error {
	var cancel context.CancelFunc
	ctx, cancel = normalizeStopContext(ctx, 0)
	defer cancel()
	d.mu.Lock()
	rt, ok := d.ports[port]
	if ok {
		delete(d.ports, port)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	rt.cancel()
	if rt.tcpListener != nil {
		_ = rt.tcpListener.Close()
	}
	if rt.udpConn != nil {
		_ = rt.udpConn.Close()
	}
	rt.closeActiveConn()
	rt.closeUDPSessions()
	if err := waitGroupWithContext(ctx, &rt.wg); err != nil {
		d.log.Warn("ss driver stop timeout", zap.Int("port", port), zap.Error(err))
		return err
	}
	d.log.Info("ss driver stop", zap.Int("port", port))
	return nil
}

func (d *SSDriver) Snapshot(_ context.Context) (DriverSnapshot, error) {
	if d.authGuard != nil {
		d.authGuard.SweepNow()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := DriverSnapshot{
		Transfer:     make(map[int]model.PortTransfer, len(d.ports)),
		UserTransfer: map[int]model.PortTransfer{},
		OnlineIP:     make(map[int][]string, len(d.ports)),
		UserOnlineIP: map[int][]string{},
		Detect:       make(map[int][]int, len(d.ports)),
		UserDetect:   map[int][]int{},
		WrongIP:      []string{},
	}

	for port, rt := range d.ports {
		up := atomic.LoadInt64(&rt.upload)
		down := atomic.LoadInt64(&rt.download)
		out.Transfer[port] = model.PortTransfer{Upload: up, Download: down}

		rt.onlineMu.Lock()
		ips := make([]string, 0, len(rt.onlineIP))
		for ip := range rt.onlineIP {
			ips = append(ips, ip)
		}
		clear(rt.onlineIP)
		rt.onlineMu.Unlock()
		sort.Strings(ips)
		out.OnlineIP[port] = ips

		rt.detectMu.Lock()
		ruleIDs := make([]int, 0, len(rt.detect))
		for id := range rt.detect {
			ruleIDs = append(ruleIDs, id)
		}
		clear(rt.detect)
		rt.firstDetect = false
		rt.detectMu.Unlock()
		sort.Ints(ruleIDs)
		out.Detect[port] = ruleIDs
	}
	return out, nil
}

func (d *SSDriver) Close(ctx context.Context) error {
	d.mu.RLock()
	ports := make([]int, 0, len(d.ports))
	for p := range d.ports {
		ports = append(ports, p)
	}
	d.mu.RUnlock()
	for _, p := range ports {
		if err := d.Stop(ctx, p); err != nil {
			return err
		}
	}
	d.log.Info("ss driver close", zap.Int("ports", len(ports)))
	return nil
}

func (d *SSDriver) AddTraffic(port int, upload, download int64) {
	d.mu.RLock()
	rt, ok := d.ports[port]
	d.mu.RUnlock()
	if !ok {
		return
	}
	atomic.AddInt64(&rt.upload, upload)
	atomic.AddInt64(&rt.download, download)
}

func (d *SSDriver) AddOnlineIP(port int, ip string) {
	d.mu.RLock()
	rt, ok := d.ports[port]
	d.mu.RUnlock()
	if !ok || ip == "" {
		return
	}
	rt.onlineMu.Lock()
	rt.onlineIP[ip] = struct{}{}
	rt.onlineMu.Unlock()
}

func (d *SSDriver) AddDetectRule(port int, ruleID int) {
	d.mu.RLock()
	rt, ok := d.ports[port]
	d.mu.RUnlock()
	if !ok || ruleID <= 0 {
		return
	}
	rt.detectMu.Lock()
	rt.detect[ruleID] = struct{}{}
	rt.detectMu.Unlock()
}

func (d *SSDriver) startPortLocked(ctx context.Context, cfg PortConfig) (*ssPortRuntime, error) {
	method := normalizeCipherName(cfg.Method)
	ciph, err := core.PickCipher(method, nil, cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("pick cipher for port %d method=%s normalized=%s: %w", cfg.Port, cfg.Method, method, err)
	}

	rctx, cancel := context.WithCancel(ctx)
	rt := &ssPortRuntime{
		cfg:         cfg,
		ctx:         rctx,
		cancel:      cancel,
		onlineIP:    make(map[string]struct{}),
		detect:      make(map[int]struct{}),
		tcpSem:      make(chan struct{}, maxTCPConnPerPort),
		udpSem:      make(chan struct{}, maxUDPPendingPerPort),
		connTrack:   newConnTracker(),
		limitUp:     newRateLimiter(cfg.NodeSpeedLimit),
		limitDown:   newRateLimiter(cfg.NodeSpeedLimit),
		firstDetect: false,
	}
	rt.udpSessionCache = newSessionCache(d.maxUDPSess, udpSessionIdleTTL, func(_ string, value any) {
		sess, ok := value.(*udpSession)
		if !ok || sess == nil {
			return
		}
		sess.mu.Lock()
		conn := sess.conn
		sess.conn = nil
		sess.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	})
	rt.udpResolveCache = newSessionCache(d.maxResolve, udpResolveIdleTTL, nil)

	tcpLn, err := core.Listen("tcp", fmt.Sprintf(":%d", cfg.Port), ciph)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("listen shadowsocks tcp %d: %w", cfg.Port, err)
	}
	rt.tcpListener = tcpLn

	udpPC, err := core.ListenPacket("udp", fmt.Sprintf(":%d", cfg.Port), ciph)
	if err != nil {
		_ = tcpLn.Close()
		cancel()
		return nil, fmt.Errorf("listen shadowsocks udp %d: %w", cfg.Port, err)
	}
	rt.udpConn = udpPC

	startBackgroundTasks(&rt.wg,
		func() { d.serveTCP(rt) },
		func() { d.serveUDP(rt) },
	)
	startPeriodicBackground(&rt.wg, rt.ctx, udpSessionSweepEvery, func(now time.Time) {
		rt.sweepUDPSessions(now)
		rt.sweepUDPResolveCache(now)
	}, nil)

	return rt, nil
}

func (d *SSDriver) serveTCP(rt *ssPortRuntime) {
	port := rt.cfg.Port
	for {
		conn, err := rt.tcpListener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || rt.ctx.Err() != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		clientIP := addrIP(conn.RemoteAddr())
		if d.isAuthBlocked(clientIP) {
			d.logAuthSampled(clientIP, "ss tcp blocked by auth-fail threshold", zap.Int("port", port))
			_ = conn.Close()
			continue
		}
		if !authTryAcquireHandshake(d.authGuard, clientIP) {
			d.logAuthSampled(clientIP, "ss tcp drop by per-ip handshake limit", zap.Int("port", port))
			_ = conn.Close()
			continue
		}
		select {
		case <-rt.ctx.Done():
			authReleaseHandshake(d.authGuard, clientIP)
			_ = conn.Close()
			return
		case d.handshakeS <- struct{}{}:
		default:
			authReleaseHandshake(d.authGuard, clientIP)
			d.logAuthSampled(clientIP, "ss tcp drop by handshake concurrency limit", zap.Int("port", port))
			_ = conn.Close()
			continue
		}
		rt.wg.Add(1)
		go func(c net.Conn, clientIP string) {
			defer func() { <-d.handshakeS }()
			defer authReleaseHandshake(d.authGuard, clientIP)
			rt.addActiveConn(c)
			defer rt.removeActiveConn(c)
			if !acquireToken(rt.ctx, rt.tcpSem) {
				atomic.AddInt64(&rt.tcpDrop, 1)
				_ = c.Close()
				return
			}
			defer releaseToken(rt.tcpSem)
			atomic.AddInt64(&rt.activeTCP, 1)
			defer atomic.AddInt64(&rt.activeTCP, -1)
			defer rt.wg.Done()
			defer c.Close()
			if addr, ok := c.RemoteAddr().(*net.TCPAddr); ok {
				d.AddOnlineIP(port, addr.IP.String())
			}

			_ = c.SetReadDeadline(time.Now().Add(handshakeReadTimeout))
			target, err := socks.ReadAddr(c)
			_ = c.SetReadDeadline(time.Time{})
			if err != nil {
				clientIP := addrIP(c.RemoteAddr())
				d.recordAuthFail(clientIP, "ss tcp read target failed")
				d.log.Debug("read target failed", zap.Int("port", port), zap.Error(err))
				return
			}

			host, portText, splitErr := net.SplitHostPort(target.String())
			if splitErr != nil {
				return
			}
			p, convErr := strconv.Atoi(portText)
			if convErr != nil {
				return
			}
			if blockedIP(host, rt.cfg.ForbiddenIP) || blockedPort(p, rt.cfg.ForbiddenPort) {
				d.log.Info("tcp target blocked", zap.Int("listen_port", rt.cfg.Port), zap.String("target", target.String()))
				return
			}

			remote, err := dialTCPWithConfig(rt.ctx, rt.cfg, target.String())
			if err != nil {
				d.log.Warn("dial target failed", zap.Int("port", port), zap.String("target", target.String()), zap.Error(err))
				return
			}
			defer remote.Close()

			rt.detectMu.Lock()
			if !rt.firstDetect {
				rt.firstDetect = true
				rt.detectMu.Unlock()
				d.applyDetect(port, []byte(target.String()))
			} else {
				rt.detectMu.Unlock()
			}
			relayCounted(rt.ctx, rt.limitUp, rt.limitDown, port, c, remote, d.AddTraffic)
		}(conn, clientIP)
	}
}

func (d *SSDriver) isAuthBlocked(ip string) bool {
	return authShouldBlock(d.authGuard, ip)
}

func (d *SSDriver) recordAuthFail(ip string, reason string) {
	authRecordFail(d.authGuard, ip, reason)
}

func (d *SSDriver) logAuthSampled(ip string, msg string, fields ...zap.Field) {
	authLogSampled(d.authGuard, ip, msg, fields...)
}

func (d *SSDriver) serveUDP(rt *ssPortRuntime) {
	port := rt.cfg.Port
	buf := acquireUDPPacketBuf(udpBufSize)
	defer releaseUDPPacketBuf(buf)
	for {
		_ = rt.udpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, raddr, err := rt.udpConn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || rt.ctx.Err() != nil {
				return
			}
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				continue
			}
			continue
		}

		if !acquireToken(rt.ctx, rt.udpSem) {
			atomic.AddInt64(&rt.udpDrop, 1)
			continue
		}
		pkt := acquireUDPPacketBuf(n)
		copy(pkt, buf[:n])
		raddrCopy := raddr
		rt.wg.Go(func() {
			defer releaseToken(rt.udpSem)
			defer releaseUDPPacketBuf(pkt)
			d.processUDPPacket(rt, port, raddrCopy, pkt)
		})
	}
}

func (d *SSDriver) processUDPPacket(rt *ssPortRuntime, port int, raddr net.Addr, payload []byte) {
	target := socks.SplitAddr(payload)
	if target == nil {
		return
	}
	targetAddr, err := d.resolveSSUDPAddrWithCache(rt, target.String())
	if err != nil {
		return
	}
	if blockedIP(targetAddr.IP.String(), rt.cfg.ForbiddenIP) || blockedPort(targetAddr.Port, rt.cfg.ForbiddenPort) {
		d.log.Info("udp target blocked", zap.Int("listen_port", rt.cfg.Port), zap.String("target", targetAddr.String()))
		return
	}
	body := payload[len(target):]
	rt.detectMu.Lock()
	if !rt.firstDetect {
		rt.firstDetect = true
		rt.detectMu.Unlock()
		d.applyDetect(port, body)
	} else {
		rt.detectMu.Unlock()
	}

	if udpAddr, ok := raddr.(*net.UDPAddr); ok {
		d.AddOnlineIP(port, udpAddr.IP.String())
	}

	sess, err := d.getOrCreateUDPSession(rt, raddr, targetAddr)
	if err != nil {
		return
	}
	d.startUDPSessionReader(rt, port, sess)
	sess.mu.Lock()
	conn := sess.conn
	sess.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	if _, err = conn.Write(body); err != nil {
		return
	}
	_ = waitLimiter(rt.ctx, rt.limitUp, len(body))
	d.AddTraffic(port, 0, int64(len(body)))
}

func relayCounted(ctx context.Context, upLimit, downLimit *rate.Limiter, port int, left, right net.Conn, addTraffic func(int, int64, int64)) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := acquireRelayBuf()
		defer releaseRelayBuf(buf)
		for {
			_ = left.SetReadDeadline(time.Now().Add(idleReadTimeout))
			n, err := left.Read(buf)
			if n > 0 {
				_ = waitLimiter(ctx, upLimit, n)
				if _, werr := right.Write(buf[:n]); werr == nil {
					addTraffic(port, 0, int64(n))
				}
			}
			if err != nil {
				if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
					select {
					case <-ctx.Done():
						return
					default:
					}
					continue
				}
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := acquireRelayBuf()
		defer releaseRelayBuf(buf)
		for {
			_ = right.SetReadDeadline(time.Now().Add(idleReadTimeout))
			n, err := right.Read(buf)
			if n > 0 {
				_ = waitLimiter(ctx, downLimit, n)
				if _, werr := left.Write(buf[:n]); werr == nil {
					addTraffic(port, int64(n), 0)
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
					select {
					case <-ctx.Done():
						return
					default:
					}
					continue
				}
				return
			}
		}
	}()
	wg.Wait()
}

func (d *SSDriver) udpSessionKey(raddr net.Addr, target *net.UDPAddr) string {
	remote := ""
	if raddr != nil {
		remote = raddr.String()
	}
	return remote + "|" + target.String()
}

func (d *SSDriver) getOrCreateUDPSession(rt *ssPortRuntime, raddr net.Addr, target *net.UDPAddr) (*udpSession, error) {
	key := d.udpSessionKey(raddr, target)
	rt.udpSessionMu.Lock()
	defer rt.udpSessionMu.Unlock()
	created, err := rt.udpSessionCache.GetOrCreate(key, func() (any, error) {
		network := "udp"
		if rt.cfg.DNSPreferIPv4 {
			network = "udp4"
		}
		conn, derr := net.DialUDP(network, nil, target)
		if derr != nil {
			return nil, derr
		}
		srcAddr := socks.ParseAddr(target.String())
		if srcAddr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("invalid udp source addr")
		}
		return &udpSession{conn: conn, clientAddr: raddr, srcAddr: append([]byte(nil), srcAddr...)}, nil
	})
	if err != nil {
		return nil, err
	}
	sess, ok := created.(*udpSession)
	if !ok || sess == nil {
		return nil, fmt.Errorf("invalid udp session cache item type")
	}
	return sess, nil
}

func (d *SSDriver) startUDPSessionReader(rt *ssPortRuntime, port int, sess *udpSession) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	if sess.readerRunning || sess.conn == nil {
		sess.mu.Unlock()
		return
	}
	sess.readerRunning = true
	sess.mu.Unlock()

	rt.wg.Go(func() {
		defer func() {
			sess.mu.Lock()
			sess.readerRunning = false
			sess.mu.Unlock()
		}()
		buf := acquireUDPPacketBuf(udpBufSize)
		defer releaseUDPPacketBuf(buf)
		for {
			sess.mu.Lock()
			conn := sess.conn
			clientAddr := sess.clientAddr
			srcAddr := sess.srcAddr
			sess.mu.Unlock()
			if conn == nil || clientAddr == nil || len(srcAddr) == 0 {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
			nr, rerr := conn.Read(buf)
			if nr > 0 {
				resp := acquireUDPPacketBuf(len(srcAddr) + nr)
				copy(resp, srcAddr)
				copy(resp[len(srcAddr):], buf[:nr])
				_, _ = rt.udpConn.WriteTo(resp[:len(srcAddr)+nr], clientAddr)
				releaseUDPPacketBuf(resp)
				_ = waitLimiter(rt.ctx, rt.limitDown, nr)
				d.AddTraffic(port, int64(nr), 0)
			}
			if rerr != nil {
				if ne, ok := errors.AsType[net.Error](rerr); ok && ne.Timeout() {
					return
				}
				return
			}
		}
	})
}

func (rt *ssPortRuntime) sweepUDPSessions(now time.Time) {
	rt.udpSessionMu.Lock()
	defer rt.udpSessionMu.Unlock()
	if rt.udpSessionCache == nil {
		return
	}
	rt.udpSessionCache.SweepExpired(now)
}

func (rt *ssPortRuntime) addActiveConn(conn net.Conn) {
	trackerAddConn(rt.connTrack, conn)
}

func (rt *ssPortRuntime) removeActiveConn(conn net.Conn) {
	trackerRemoveConn(rt.connTrack, conn)
}

func (rt *ssPortRuntime) closeActiveConn() {
	trackerCloseAll(rt.connTrack)
}

func (rt *ssPortRuntime) closeUDPSessions() {
	rt.udpSessionMu.Lock()
	if rt.udpSessionCache != nil {
		rt.udpSessionCache.CloseAll()
	}
	rt.udpSessionMu.Unlock()
	closeSessionCache(&rt.udpResolveMu, rt.udpResolveCache)
}

func (rt *ssPortRuntime) sweepUDPResolveCache(now time.Time) {
	sweepSessionCache(&rt.udpResolveMu, rt.udpResolveCache, now)
}

func (d *SSDriver) resolveSSUDPAddrWithCache(rt *ssPortRuntime, target string) (*net.UDPAddr, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		return resolveUDPAddrWithConfig(rt.ctx, rt.cfg, target)
	}

	return resolveUDPAddrFromCache(&rt.udpResolveMu, rt.udpResolveCache, target, func(v string) (*net.UDPAddr, error) {
		return resolveUDPAddrWithConfig(rt.ctx, rt.cfg, v)
	}, "invalid ss udp resolve cache item type")
}

func (d *SSDriver) applyDetect(port int, payload []byte) {
	d.mu.RLock()
	rt, ok := d.ports[port]
	d.mu.RUnlock()
	if !ok || len(payload) == 0 {
		return
	}
	walkMatchedRulesBytes(payload, rt.cfg.Detect, func(ruleID int) bool {
		d.AddDetectRule(port, ruleID)
		return true
	})
}

func canHotReloadSS(oldCfg, newCfg PortConfig) bool {
	if oldCfg.Port != newCfg.Port || oldCfg.UserID != newCfg.UserID {
		return false
	}
	if oldCfg.Password != newCfg.Password || oldCfg.Method != newCfg.Method {
		return false
	}
	if oldCfg.Protocol != newCfg.Protocol || oldCfg.Obfs != newCfg.Obfs {
		return false
	}
	if oldCfg.IsMultiUser != newCfg.IsMultiUser {
		return false
	}
	if oldCfg.ProtocolParam != newCfg.ProtocolParam || oldCfg.ObfsParam != newCfg.ObfsParam {
		return false
	}
	return true
}

func acquireToken(ctx context.Context, sem chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseToken(sem chan struct{}) {
	select {
	case <-sem:
	default:
	}
}

func (d *SSDriver) Stats() []PortRuntimeStat {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]PortRuntimeStat, 0, len(d.ports))
	for port, rt := range d.ports {
		rt.udpSessionMu.Lock()
		sessSnap := SessionCacheSnapshot{}
		if rt.udpSessionCache != nil {
			sessSnap = rt.udpSessionCache.Snapshot()
		}
		rt.udpSessionMu.Unlock()

		rt.udpResolveMu.Lock()
		resolveSnap := SessionCacheSnapshot{}
		if rt.udpResolveCache != nil {
			resolveSnap = rt.udpResolveCache.Snapshot()
		}
		rt.udpResolveMu.Unlock()

		out = append(out, PortRuntimeStat{
			Port:            port,
			ActiveTCP:       atomic.LoadInt64(&rt.activeTCP),
			TCPDrop:         atomic.LoadInt64(&rt.tcpDrop),
			UDPDrop:         atomic.LoadInt64(&rt.udpDrop),
			UDPSessionCache: sessSnap,
			UDPResolveCache: resolveSnap,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
