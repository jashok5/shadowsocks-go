package runtime

import (
	"context"
	"net"
	stdRuntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

func normalizeStopContext(ctx context.Context, defaultTimeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if defaultTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			return context.WithTimeout(ctx, defaultTimeout)
		}
	}
	return ctx, func() {}
}

func waitGroupWithContext(ctx context.Context, wg *sync.WaitGroup) error {
	if wg == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func authShouldBlock(g *authFailGuard, ip string) bool {
	if g == nil {
		return false
	}
	return g.ShouldBlockNow(ip)
}

func authRecordFail(g *authFailGuard, ip string, reason string) {
	if g == nil {
		return
	}
	g.RecordFailNow(ip, reason)
}

func authLogSampled(g *authFailGuard, ip string, msg string, fields ...zap.Field) {
	if g == nil {
		return
	}
	g.LogSampledNow(ip, msg, fields...)
}

func authTryAcquireHandshake(g *authFailGuard, ip string) bool {
	if g == nil {
		return true
	}
	return g.TryAcquireHandshake(ip)
}

func authReleaseHandshake(g *authFailGuard, ip string) {
	if g == nil {
		return
	}
	g.ReleaseHandshake(ip)
}

func authHandshakeKey(ip string, port int) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	return ip + "|" + strconv.Itoa(port)
}

func trackerAddConn(t *connTracker, conn net.Conn) {
	if t != nil {
		t.Add(conn)
	}
}

func trackerRemoveConn(t *connTracker, conn net.Conn) {
	if t != nil {
		t.Remove(conn)
	}
}

func trackerCloseAll(t *connTracker) {
	if t != nil {
		t.CloseAll()
	}
}

func resolveHandshakeLimit(configured int) int {
	if configured > 0 {
		return configured
	}
	v := max(128*stdRuntime.GOMAXPROCS(0), 256)
	if v > 2048 {
		v = 2048
	}
	return v
}

func resolvePerIPHandshakeLimit(configured int) int {
	if configured > 0 {
		return configured
	}
	return 0
}
