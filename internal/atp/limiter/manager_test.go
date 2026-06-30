package limiter

import (
	"context"
	"testing"
	"time"
)

func TestManagerEffectiveLimitRules(t *testing.T) {
	m := NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	m.SetNodeLimit(0)
	m.SetUserLimit(1, 50)
	if err := m.WaitN(ctx, 1, 1024); err != nil {
		t.Fatalf("expected user-only limit to pass, got %v", err)
	}
	if got := m.userApplied[1]; got != 50 {
		t.Fatalf("expected applied user limit 50, got %v", got)
	}

	m.SetNodeLimit(80)
	m.SetUserLimit(2, 0)
	if err := m.WaitN(ctx, 2, 1024); err != nil {
		t.Fatalf("expected node-only limit to pass, got %v", err)
	}
	if got := m.userApplied[2]; got != 80 {
		t.Fatalf("expected applied node limit 80, got %v", got)
	}

	m.SetNodeLimit(30)
	m.SetUserLimit(3, 50)
	if err := m.WaitN(ctx, 3, 1024); err != nil {
		t.Fatalf("expected min(user,node) limit to pass, got %v", err)
	}
	if got := m.userApplied[3]; got != 30 {
		t.Fatalf("expected applied min limit 30, got %v", got)
	}

	m.SetNodeLimit(0)
	m.SetUserLimit(4, 0)
	if err := m.WaitN(ctx, 4, 1024); err != nil {
		t.Fatalf("expected unlimited case to pass, got %v", err)
	}
	if _, ok := m.userLimiter[4]; ok {
		t.Fatalf("expected no limiter for unlimited case")
	}
}

func TestManagerKeepsPerUserAppliedLimits(t *testing.T) {
	m := NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	m.SetNodeLimit(100)
	m.SetUserLimit(11, 10)
	m.SetUserLimit(12, 20)

	if err := m.WaitN(ctx, 11, 512); err != nil {
		t.Fatalf("user 11 wait failed: %v", err)
	}
	if err := m.WaitN(ctx, 12, 512); err != nil {
		t.Fatalf("user 12 wait failed: %v", err)
	}
	if got := m.userApplied[11]; got != 10 {
		t.Fatalf("expected user 11 applied 10, got %v", got)
	}
	if got := m.userApplied[12]; got != 20 {
		t.Fatalf("expected user 12 applied 20, got %v", got)
	}
	if m.userLimiter[11] == nil || m.userLimiter[12] == nil {
		t.Fatalf("expected per-user limiters to be created")
	}
	if m.userLimiter[11] == m.userLimiter[12] {
		t.Fatalf("expected separate limiters per user")
	}
}
