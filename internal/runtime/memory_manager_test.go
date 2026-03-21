package runtime

import (
	"context"
	"testing"

	"github.com/jashok5/shadowsocks-go/internal/model"

	"go.uber.org/zap"
)

func TestMemoryManagerMUOnlyAndPortOffset(t *testing.T) {
	drv := NewMockDriver()
	mgr := NewMemoryManagerWithDriver(zap.NewNop(), drv, 4)

	in := SyncInput{
		NodeInfo: model.NodeInfo{MUOnly: 1, PortOffset: 1000},
		Users: []model.User{
			{ID: 1, Port: 2000, IsMultiUser: 0},
			{ID: 2, Port: 3000, IsMultiUser: 1},
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
