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
	udpBufSize           = 64 * 1024
	dialTimeout          = 8 * time.Second
	idleReadTimeout      = 30 * time.Second
	maxTCPConnPerPort    = 1024
	maxUDPPendingPerPort = 256
)

type ssPortRuntime struct {
	cfg PortConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	tcpListener net.Listener
	udpConn     net.PacketConn
	tcpSem      chan struct{}
	udpSem      chan struct{}
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
}

type SSDriver struct {
	log *zap.Logger

	mu    sync.RWMutex
	ports map[int]*ssPortRuntime
}

type PortRuntimeStat struct {
	Port      int
	ActiveTCP int64
	TCPDrop   int64
	UDPDrop   int64
}

func NewSSDriver(log *zap.Logger) *SSDriver {
	return &SSDriver{log: log, ports: make(map[int]*ssPortRuntime)}
}

func (d *SSDriver) Start(ctx context.Context, cfg PortConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
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
	if err := d.Stop(ctx, cfg.Port); err != nil {
		return err
	}
	return d.Start(ctx, cfg)
}

func (d *SSDriver) Stop(ctx context.Context, port int) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.wg.Wait()
	}()
	select {
	case <-done:
	case <-ctx.Done():
		d.log.Warn("ss driver stop timeout", zap.Int("port", port), zap.Error(ctx.Err()))
		return ctx.Err()
	}
	d.log.Info("ss driver stop", zap.Int("port", port))
	return nil
}

func (d *SSDriver) Snapshot(_ context.Context) (DriverSnapshot, error) {
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
		rt.onlineIP = make(map[string]struct{})
		rt.onlineMu.Unlock()
		sort.Strings(ips)
		out.OnlineIP[port] = ips

		rt.detectMu.Lock()
		ruleIDs := make([]int, 0, len(rt.detect))
		for id := range rt.detect {
			ruleIDs = append(ruleIDs, id)
		}
		rt.detect = make(map[int]struct{})
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
		limitUp:     newRateLimiter(cfg.NodeSpeedLimit),
		limitDown:   newRateLimiter(cfg.NodeSpeedLimit),
		firstDetect: false,
	}

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

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		d.serveTCP(rt)
	}()

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		d.serveUDP(rt)
	}()

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
		rt.wg.Add(1)
		go func(c net.Conn) {
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

			target, err := socks.ReadAddr(c)
			if err != nil {
				d.log.Warn("read target failed", zap.Int("port", port), zap.Error(err))
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
		}(conn)
	}
}

func (d *SSDriver) serveUDP(rt *ssPortRuntime) {
	port := rt.cfg.Port
	buf := make([]byte, udpBufSize)
	for {
		_ = rt.udpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, raddr, err := rt.udpConn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || rt.ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			continue
		}

		if !acquireToken(rt.ctx, rt.udpSem) {
			atomic.AddInt64(&rt.udpDrop, 1)
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		raddrCopy := raddr
		rt.wg.Add(1)
		go func() {
			defer rt.wg.Done()
			defer releaseToken(rt.udpSem)
			d.processUDPPacket(rt, port, raddrCopy, pkt)
		}()
	}
}

func (d *SSDriver) processUDPPacket(rt *ssPortRuntime, port int, raddr net.Addr, payload []byte) {
	target := socks.SplitAddr(payload)
	if target == nil {
		return
	}
	targetAddr, err := resolveUDPAddrWithConfig(rt.ctx, rt.cfg, target.String())
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

	network := "udp"
	if rt.cfg.DNSPreferIPv4 {
		network = "udp4"
	}
	conn, err := net.DialUDP(network, nil, targetAddr)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err = conn.Write(body); err != nil {
		return
	}
	_ = waitLimiter(rt.ctx, rt.limitUp, len(body))
	d.AddTraffic(port, 0, int64(len(body)))

	buf := make([]byte, udpBufSize)
	nr, err := conn.Read(buf)
	if err != nil || nr <= 0 {
		return
	}

	srcAddr := socks.ParseAddr(targetAddr.String())
	if srcAddr == nil {
		return
	}
	resp := make([]byte, len(srcAddr)+nr)
	copy(resp, srcAddr)
	copy(resp[len(srcAddr):], buf[:nr])
	if _, err = rt.udpConn.WriteTo(resp, raddr); err != nil {
		return
	}
	_ = waitLimiter(rt.ctx, rt.limitDown, nr)
	d.AddTraffic(port, int64(nr), 0)
}

func relayCounted(ctx context.Context, upLimit, downLimit *rate.Limiter, port int, left, right net.Conn, addTraffic func(int, int64, int64)) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
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
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
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
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
		}
	}()
	wg.Wait()
}

func (d *SSDriver) applyDetect(port int, payload []byte) {
	d.mu.RLock()
	rt, ok := d.ports[port]
	d.mu.RUnlock()
	if !ok || len(payload) == 0 {
		return
	}
	walkMatchedRules(string(payload), rt.cfg.Detect, func(ruleID int) bool {
		d.AddDetectRule(port, ruleID)
		return true
	})
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
		out = append(out, PortRuntimeStat{
			Port:      port,
			ActiveTCP: atomic.LoadInt64(&rt.activeTCP),
			TCPDrop:   atomic.LoadInt64(&rt.tcpDrop),
			UDPDrop:   atomic.LoadInt64(&rt.udpDrop),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
