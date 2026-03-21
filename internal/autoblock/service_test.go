package autoblock

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/runtime"

	"go.uber.org/zap"
)

type testAPI struct {
	nodes       []map[string]any
	blockRows   []map[string]any
	unblockRows []map[string]any
	posted      []string
	postErr     error
}

func (a *testAPI) GetNodes(context.Context) ([]map[string]any, error) { return a.nodes, nil }

func (a *testAPI) PostBlockIP(_ context.Context, ips []string) error {
	a.posted = append(a.posted, ips...)
	return a.postErr
}

func (a *testAPI) GetBlockIP(context.Context) ([]map[string]any, error) { return a.blockRows, nil }

func (a *testAPI) GetUnblockIP(context.Context) ([]map[string]any, error) { return a.unblockRows, nil }

type testRuntime struct {
	snap runtime.Snapshot
}

func (r *testRuntime) Snapshot(context.Context) (runtime.Snapshot, error) { return r.snap, nil }

type testBackend struct {
	reconciled map[netip.Addr]struct{}
	closed     bool
}

func (b *testBackend) Name() string { return "test" }

func (b *testBackend) Reconcile(_ context.Context, desired map[netip.Addr]struct{}) error {
	b.reconciled = cloneAddrSet(desired)
	return nil
}

func (b *testBackend) Close(context.Context) error {
	b.closed = true
	return nil
}

func TestSyncOnceFiltersAndReconciles(t *testing.T) {
	api := &testAPI{
		nodes: []map[string]any{{"node_ip": "10.0.0.1,10.0.0.2"}},
		blockRows: []map[string]any{
			{"ip": "1.1.1.1"},
			{"ip": "2.2.2.2"},
			{"ip": "10.0.0.1"},
		},
		unblockRows: []map[string]any{{"ip": "2.2.2.2"}},
	}
	rt := &testRuntime{snap: runtime.Snapshot{WrongIP: []string{"1.1.1.1", "10.0.0.1", "127.0.0.1", "bad-ip"}}}
	be := &testBackend{}

	s := newWithBackend(zap.NewNop(), config.AutoBlockConfig{
		Enabled:         true,
		SyncInterval:    time.Second,
		ProtectNodeIP:   true,
		StaticWhitelist: []string{"127.0.0.1"},
	}, api, rt, be)

	if err := s.syncOnce(context.Background()); err != nil {
		t.Fatalf("syncOnce failed: %v", err)
	}

	if len(api.posted) != 1 || api.posted[0] != "1.1.1.1" {
		t.Fatalf("unexpected posted ips: %+v", api.posted)
	}
	if len(be.reconciled) != 1 {
		t.Fatalf("unexpected reconciled size: %d", len(be.reconciled))
	}
	ip := netip.MustParseAddr("1.1.1.1")
	if _, ok := be.reconciled[ip]; !ok {
		t.Fatalf("expected %s in reconciled set", ip)
	}
}

func TestParseSingleIP(t *testing.T) {
	tests := []struct {
		in   string
		good bool
		want string
	}{
		{in: "1.2.3.4", good: true, want: "1.2.3.4"},
		{in: "1.2.3.4:443", good: true, want: "1.2.3.4"},
		{in: "::1", good: false},
		{in: "  bad", good: false},
	}
	for _, tt := range tests {
		ip, ok := parseSingleIP(tt.in)
		if ok != tt.good {
			t.Fatalf("parseSingleIP(%q) ok=%v, want %v", tt.in, ok, tt.good)
		}
		if tt.good && ip.String() != tt.want {
			t.Fatalf("parseSingleIP(%q)=%s, want %s", tt.in, ip.String(), tt.want)
		}
	}
}
