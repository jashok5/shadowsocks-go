package runtime

import (
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	defaultHandshakeMaxConcurrent = 512

	defaultAuthFailWindow    = 30 * time.Second
	defaultAuthFailThreshold = 20
	defaultAuthBlockDuration = 5 * time.Minute
	defaultAuthLogInterval   = 10 * time.Second
	defaultAuthStateTTL      = 15 * time.Minute
)

type authFailState struct {
	WindowStart time.Time
	Count       int
	BlockedTill time.Time
	LastLog     time.Time
}

type authFailGuard struct {
	mu     sync.Mutex
	states map[string]*authFailState

	window    time.Duration
	threshold int
	blockFor  time.Duration
	logEvery  time.Duration
	stateTTL  time.Duration

	log    *zap.Logger
	driver string
}

func newAuthFailGuard(log *zap.Logger, driver string) *authFailGuard {
	if log == nil {
		log = zap.NewNop()
	}
	return &authFailGuard{
		states:    make(map[string]*authFailState),
		window:    defaultAuthFailWindow,
		threshold: defaultAuthFailThreshold,
		blockFor:  defaultAuthBlockDuration,
		logEvery:  defaultAuthLogInterval,
		stateTTL:  defaultAuthStateTTL,
		log:       log,
		driver:    strings.TrimSpace(driver),
	}
}

func (g *authFailGuard) ShouldBlock(ip string, now time.Time) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.states[ip]
	if !ok || st == nil {
		return false
	}
	return now.Before(st.BlockedTill)
}

func (g *authFailGuard) ShouldBlockNow(ip string) bool {
	return g.ShouldBlock(ip, time.Now())
}

func (g *authFailGuard) RecordFail(ip string, now time.Time, reason string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	var (
		blocked bool
		count   int
	)
	g.mu.Lock()
	st, ok := g.states[ip]
	if !ok || st == nil {
		st = &authFailState{WindowStart: now}
		g.states[ip] = st
	}
	if now.Sub(st.WindowStart) > g.window {
		st.WindowStart = now
		st.Count = 0
	}
	st.Count++
	if st.Count >= g.threshold {
		st.BlockedTill = now.Add(g.blockFor)
		st.Count = 0
		st.WindowStart = now
		blocked = true
	}
	count = st.Count
	g.mu.Unlock()

	if blocked {
		g.LogSampled(ip, now, "auth-fail ip temporarily blocked", zap.Duration("block_for", g.blockFor), zap.String("reason", reason))
		return
	}
	g.LogSampled(ip, now, "auth-fail", zap.Int("window_count", count), zap.String("reason", reason))
}

func (g *authFailGuard) RecordFailNow(ip string, reason string) {
	g.RecordFail(ip, time.Now(), reason)
}

func (g *authFailGuard) LogSampled(ip string, now time.Time, msg string, fields ...zap.Field) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	allow := false
	g.mu.Lock()
	st, ok := g.states[ip]
	if !ok || st == nil {
		st = &authFailState{WindowStart: now}
		g.states[ip] = st
	}
	if now.Sub(st.LastLog) >= g.logEvery {
		st.LastLog = now
		allow = true
	}
	g.mu.Unlock()
	if !allow {
		return
	}
	base := []zap.Field{zap.String("client_ip", ip)}
	if g.driver != "" {
		base = append(base, zap.String("driver", g.driver))
	}
	base = append(base, fields...)
	g.log.Warn(msg, base...)
}

func (g *authFailGuard) LogSampledNow(ip string, msg string, fields ...zap.Field) {
	g.LogSampled(ip, time.Now(), msg, fields...)
}

func (g *authFailGuard) Sweep(now time.Time) {
	cut := now.Add(-g.stateTTL)
	g.mu.Lock()
	defer g.mu.Unlock()
	for ip, st := range g.states {
		if st == nil {
			delete(g.states, ip)
			continue
		}
		if now.Before(st.BlockedTill) {
			continue
		}
		if st.LastLog.After(cut) || st.WindowStart.After(cut) {
			continue
		}
		delete(g.states, ip)
	}
}

func (g *authFailGuard) SweepNow() {
	g.Sweep(time.Now())
}
