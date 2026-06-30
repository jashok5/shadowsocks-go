package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/model"

	"go.uber.org/zap"
)

func TestResolveSpeedLimitRules(t *testing.T) {
	tests := []struct {
		name string
		node float64
		user float64
		want float64
	}{
		{name: "node only", node: 0, user: 50, want: 50},
		{name: "user only", node: 80, user: 0, want: 80},
		{name: "both nonzero", node: 80, user: 50, want: 50},
		{name: "both zero", node: 0, user: 0, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSpeedLimit(tc.node, tc.user); got != tc.want {
				t.Fatalf("resolveSpeedLimit(%v, %v) = %v, want %v", tc.node, tc.user, got, tc.want)
			}
		})
	}
}

func TestMemoryManagerMUOnlyAndPortOffset(t *testing.T) {
	drv := NewMockDriver()
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 4)

	in := SyncInput{
		NodeInfo: model.NodeInfo{MUOnly: 1, PortOffset: 1000},
		MUHost:   MUHostRule{Enabled: true, Regex: "%5m%id.%suffix", Suffix: "example.com"},
		Users: []model.User{
			{ID: 1, Port: 2000, IsMultiUser: 0, Passwd: "p1", Method: "aes-256-gcm", Obfs: "tls1.2_ticket_auth", Protocol: "auth_aes128_md5"},
			{ID: 2, Port: 3000, IsMultiUser: 1, Passwd: "pmu", Method: "aes-256-gcm", Obfs: "tls1.2_ticket_auth", Protocol: "auth_aes128_md5"},
		},
		Rules: nil,
	}
	if err := mgr.Sync(context.Background(), in); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	snap, err := mgr.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	if len(snap.PortUser) != 1 {
		t.Fatalf("expected 1 active port, got %d", len(snap.PortUser))
	}
	if uid, ok := snap.PortUser[4000]; !ok || uid != 2 {
		t.Fatalf("expected user 2 at effective port 4000, got %#v", snap.PortUser)
	}

	starts, _, _ := drv.Stats()
	if starts != 1 {
		t.Fatalf("expected 1 start call, got %d", starts)
	}

	cfg := drv.configs[4000]
	if len(cfg.MUHosts) != 1 || cfg.MUHosts[0] != "f32de1.example.com" {
		t.Fatalf("unexpected MU hosts: %#v", cfg.MUHosts)
	}
}

func TestMemoryManagerReloadOnRuleChange(t *testing.T) {
	drv := NewMockDriver()
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 4)

	base := SyncInput{
		NodeInfo: model.NodeInfo{},
		Users:    []model.User{{ID: 1, Port: 8388, IsMultiUser: 0, Passwd: "p1", Method: "aes-256-gcm"}},
		Rules:    []model.DetectRule{{ID: 10, Regex: "abc", Type: 1}},
	}
	if err := mgr.Sync(context.Background(), base); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}

	changed := base
	changed.Rules = []model.DetectRule{{ID: 10, Regex: "xyz", Type: 1}}
	if err := mgr.Sync(context.Background(), changed); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}

	starts, reloads, _ := drv.Stats()
	if starts != 1 {
		t.Fatalf("expected starts=1, got %d", starts)
	}
	if reloads != 1 {
		t.Fatalf("expected reloads=1, got %d", reloads)
	}
}

func TestMemoryManagerStopRemovedPorts(t *testing.T) {
	drv := NewMockDriver()
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 4)

	first := SyncInput{
		NodeInfo: model.NodeInfo{},
		Users: []model.User{
			{ID: 1, Port: 1001, IsMultiUser: 0},
			{ID: 2, Port: 1002, IsMultiUser: 0},
		},
	}
	if err := mgr.Sync(context.Background(), first); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}

	second := SyncInput{
		NodeInfo: model.NodeInfo{},
		Users:    []model.User{{ID: 1, Port: 1001, IsMultiUser: 0}},
	}
	if err := mgr.Sync(context.Background(), second); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}

	snap, err := mgr.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snap.PortUser) != 1 {
		t.Fatalf("expected 1 active port after remove, got %d", len(snap.PortUser))
	}
	if _, ok := snap.PortUser[1002]; ok {
		t.Fatalf("port 1002 should be removed")
	}

	starts, _, stops := drv.Stats()
	if starts != 2 {
		t.Fatalf("expected starts=2, got %d", starts)
	}
	if stops != 1 {
		t.Fatalf("expected stops=1, got %d", stops)
	}
}

func TestMemoryManagerUnsupportedCipherFail(t *testing.T) {
	drv := NewMockDriver()
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 2)
	err := mgr.Sync(context.Background(), SyncInput{
		NodeInfo: model.NodeInfo{},
		Users: []model.User{
			{ID: 1, Port: 1001, Method: "rc4-md5", Passwd: "p"},
		},
		Runtime: Options{OnUnsupportedCipher: "fail"},
	})
	if err == nil {
		t.Fatalf("expected error on unsupported cipher with fail mode")
	}
}

func TestMemoryManagerWithATPAwareDriver(t *testing.T) {
	installATPTestTLSHooks(t)

	drv := NewATPDriver(zap.NewNop())
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	in := SyncInput{
		NodeInfo: model.NodeInfo{
			Sort:   16,
			Server: "127.0.0.1;port=19443|transport=tls|host=localhost",
		},
		Users: []model.User{{ID: 1, UUID: "u1", Passwd: "p1", NodeConnector: 1}},
		Rules: []model.DetectRule{{ID: 1, Regex: "example", Type: 1}},
		ATP: ATPConfig{
			NodeInfo:              model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=19443|transport=tls|host=localhost"},
			Users:                 []model.User{{ID: 1, UUID: "u1", Passwd: "p1", NodeConnector: 1}},
			Rules:                 []model.DetectRule{{ID: 1, Regex: "example", Type: 1}},
			HandshakeTimeout:      10 * time.Second,
			IdleTimeout:           30 * time.Second,
			ResumeTicketTTL:       2 * time.Minute,
			MaxConnsPerUser:       1,
			MaxOpenStreamsPerUser: 16,
			EnableAuditBlock:      true,
			AuditBlockDuration:    10 * time.Minute,
		},
	}

	if err := mgr.Sync(ctx, in); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	snap, err := mgr.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snap.PortUser) != 0 {
		t.Fatalf("expected empty port mapping for atp driver")
	}

	if err := mgr.Stop(ctx); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}
