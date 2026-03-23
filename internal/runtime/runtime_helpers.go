package runtime

import (
	"context"
	"net"
	stdRuntime "runtime"
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
	v := 128 * stdRuntime.GOMAXPROCS(0)
	if v < 256 {
		v = 256
	}
	if v > 2048 {
		v = 2048
	}
	return v
}
