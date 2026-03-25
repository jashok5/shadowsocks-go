package network

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewShadowsocksRDecorateRequiresObfsService(t *testing.T) {
	_, err := NewShadowsocksRDecorate(nil, "", "", "", "", "", "", "", 0, false, 0, nil, nil)
	if err == nil {
		t.Fatalf("expected error when obfs service is nil")
	}
}

func TestDetectInterfaceMTUByIPCacheHit(t *testing.T) {
	oldSlow := detectInterfaceMTUByIPSlowFunc
	oldTTL := mtuCacheTTL
	defer func() {
		detectInterfaceMTUByIPSlowFunc = oldSlow
		mtuCacheTTL = oldTTL
		mtuByIPCache = syncMapReset()
	}()

	var calls atomic.Int64
	detectInterfaceMTUByIPSlowFunc = func(ip net.IP) int {
		calls.Add(1)
		return 1500
	}
	mtuCacheTTL = time.Minute
	mtuByIPCache = syncMapReset()

	ip := net.ParseIP("127.0.0.1")
	if got := detectInterfaceMTUByIP(ip); got != 1500 {
		t.Fatalf("unexpected mtu: %d", got)
	}
	if got := detectInterfaceMTUByIP(ip); got != 1500 {
		t.Fatalf("unexpected mtu on cache hit: %d", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("expected slow path called once, got %d", n)
	}
}

func TestDetectInterfaceMTUByIPCacheTTLExpire(t *testing.T) {
	oldSlow := detectInterfaceMTUByIPSlowFunc
	oldTTL := mtuCacheTTL
	defer func() {
		detectInterfaceMTUByIPSlowFunc = oldSlow
		mtuCacheTTL = oldTTL
		mtuByIPCache = syncMapReset()
	}()

	var calls atomic.Int64
	detectInterfaceMTUByIPSlowFunc = func(ip net.IP) int {
		return int(1400 + calls.Add(1))
	}
	mtuCacheTTL = 10 * time.Millisecond
	mtuByIPCache = syncMapReset()

	ip := net.ParseIP("127.0.0.1")
	first := detectInterfaceMTUByIP(ip)
	time.Sleep(20 * time.Millisecond)
	second := detectInterfaceMTUByIP(ip)
	if first == second {
		t.Fatalf("expected mtu to refresh after ttl, first=%d second=%d", first, second)
	}
	if n := calls.Load(); n < 2 {
		t.Fatalf("expected slow path called at least twice, got %d", n)
	}
}

func BenchmarkDetectInterfaceMTUByIPCacheHit(b *testing.B) {
	oldSlow := detectInterfaceMTUByIPSlowFunc
	oldTTL := mtuCacheTTL
	defer func() {
		detectInterfaceMTUByIPSlowFunc = oldSlow
		mtuCacheTTL = oldTTL
		mtuByIPCache = syncMapReset()
	}()

	detectInterfaceMTUByIPSlowFunc = func(ip net.IP) int { return 1500 }
	mtuCacheTTL = time.Hour
	mtuByIPCache = syncMapReset()
	ip := net.ParseIP("127.0.0.1")
	_ = detectInterfaceMTUByIP(ip)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = detectInterfaceMTUByIP(ip)
	}
}

func syncMapReset() sync.Map {
	return sync.Map{}
}
