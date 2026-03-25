package runtime

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBuildUDPAssocKeyModes(t *testing.T) {
	if got := buildUDPAssocKey(udpAssocKeyClientOnly, testAddr("1.2.3.4:1000"), "8.8.8.8:53", 11); got != "1.2.3.4:1000" {
		t.Fatalf("client_only key mismatch: %q", got)
	}
	if got := buildUDPAssocKey(udpAssocKeyClientTarget, testAddr("1.2.3.4:1000"), "8.8.8.8:53", 11); got != "1.2.3.4:1000|8.8.8.8:53" {
		t.Fatalf("client_target key mismatch: %q", got)
	}
	if got := buildUDPAssocKey(udpAssocKeyClientUID, testAddr("1.2.3.4:1000"), "8.8.8.8:53", 11); got != "1.2.3.4:1000|11" {
		t.Fatalf("client_uid key mismatch: %q", got)
	}
}

func TestUDPAssocStoreEvictAndSnapshot(t *testing.T) {
	evicted := 0
	store := newUDPAssocStore(1, 20*time.Millisecond, func(_ string, _ any) {
		evicted++
	})
	if _, err := store.GetOrCreate("a", func() (any, error) { return 1, nil }); err != nil {
		t.Fatalf("create a failed: %v", err)
	}
	if _, err := store.GetOrCreate("b", func() (any, error) { return 2, nil }); err != nil {
		t.Fatalf("create b failed: %v", err)
	}
	if evicted == 0 {
		t.Fatalf("expected eviction when exceeding max entries")
	}
	s := store.Snapshot()
	if s.Size != 1 {
		t.Fatalf("unexpected size after max-entry eviction: %d", s.Size)
	}

	time.Sleep(30 * time.Millisecond)
	store.Sweep(time.Now())
	s = store.Snapshot()
	if s.Size != 0 {
		t.Fatalf("unexpected size after ttl sweep: %d", s.Size)
	}
}

func TestSSRCacheStatsIncludeUDPAssoc(t *testing.T) {
	d := NewSSRDriverWithTuning(zap.NewNop(), DriverTuning{})
	inst := &ssrInstance{
		resolvedAddrTTL: newSessionCache(8, time.Minute, nil),
		udpAssoc:        newUDPAssocStore(8, time.Minute, nil),
		udpRunnerStats:  &udpAssocRunnerMetrics{},
	}
	if _, err := inst.resolvedAddrTTL.GetOrCreate("r", func() (any, error) { return "ok", nil }); err != nil {
		t.Fatalf("resolve cache seed failed: %v", err)
	}
	if _, err := inst.udpAssoc.GetOrCreate("u", func() (any, error) { return "ok", nil }); err != nil {
		t.Fatalf("assoc cache seed failed: %v", err)
	}

	d.mu.Lock()
	d.servers[12345] = inst
	d.portUserOnline[12345] = map[int]map[string]struct{}{7: {"1.1.1.1": {}, "2.2.2.2": {}}}
	d.mu.Unlock()

	stats := d.CacheStats()
	if len(stats) != 1 {
		t.Fatalf("unexpected stats length: %d", len(stats))
	}
	if stats[0].Port != 12345 {
		t.Fatalf("unexpected port in stats: %d", stats[0].Port)
	}
	if stats[0].UDPAssocCache.Size != 1 {
		t.Fatalf("unexpected udp assoc cache size: %d", stats[0].UDPAssocCache.Size)
	}
	if stats[0].UDPResolveCache.Size != 1 {
		t.Fatalf("unexpected udp resolve cache size: %d", stats[0].UDPResolveCache.Size)
	}
	if stats[0].UDPAssocRunner.ActiveReaders != 0 {
		t.Fatalf("unexpected active readers in snapshot: %+v", stats[0].UDPAssocRunner)
	}
	if stats[0].UserOnlineCount[7] != 2 {
		t.Fatalf("unexpected per-user online count: %+v", stats[0].UserOnlineCount)
	}
}

func TestUDPAssocRunnerMetricsSnapshot(t *testing.T) {
	m := &udpAssocRunnerMetrics{}
	m.activeReaders.Store(2)
	m.packets.Store(11)
	m.errors.Store(3)
	s := m.Snapshot()
	if s.ActiveReaders != 2 || s.Packets != 11 || s.Errors != 3 {
		t.Fatalf("unexpected metrics snapshot: %+v", s)
	}
	a1 := m.AlertSnapshot(1)
	if !a1.Warn || a1.ErrorDelta != 3 {
		t.Fatalf("unexpected alert snapshot #1: %+v", a1)
	}
	a2 := m.AlertSnapshot(1)
	if a2.Warn || a2.ErrorDelta != 0 {
		t.Fatalf("unexpected alert snapshot #2: %+v", a2)
	}
}

func TestClassifyUDPAssocError(t *testing.T) {
	if got := classifyUDPAssocError(context.Background(), nil); got != udpAssocErrNone {
		t.Fatalf("unexpected kind for nil error: %v", got)
	}
	if got := classifyUDPAssocError(context.Background(), context.Canceled); got != udpAssocErrCanceled {
		t.Fatalf("unexpected kind for canceled: %v", got)
	}
	timeoutErr := &net.DNSError{IsTimeout: true}
	if got := classifyUDPAssocError(context.Background(), timeoutErr); got != udpAssocErrTimeout {
		t.Fatalf("unexpected kind for timeout: %v", got)
	}
	if got := classifyUDPAssocError(context.Background(), net.ErrClosed); got != udpAssocErrClosed {
		t.Fatalf("unexpected kind for closed: %v", got)
	}
	if got := classifyUDPAssocError(context.Background(), errors.New("random io error")); got != udpAssocErrIO {
		t.Fatalf("unexpected kind for io error: %v", got)
	}
}

func TestUDPAssocErrorKindString(t *testing.T) {
	if udpAssocErrTimeout.String() != "timeout" {
		t.Fatalf("unexpected timeout string: %s", udpAssocErrTimeout.String())
	}
	if udpAssocErrorKind(999).String() != "unknown" {
		t.Fatalf("unexpected fallback string: %s", udpAssocErrorKind(999).String())
	}
}

func TestSSUDPResponseWriterValidation(t *testing.T) {
	w := newSSUDPResponseWriter(nil, nil, nil)
	if err := w.WriteResponse([]byte("x"), nil); err == nil {
		t.Fatalf("expected validation error for invalid ss writer")
	}
}

func TestSSRUDPResponseWriterValidation(t *testing.T) {
	w := newSSRUDPResponseWriter(nil, nil, nil)
	if err := w.WriteResponse([]byte("x"), testAddr("127.0.0.1:53")); err == nil {
		t.Fatalf("expected validation error for invalid ssr writer")
	}
}

type testAddr string

func (a testAddr) Network() string { return "udp" }

func (a testAddr) String() string { return string(a) }
