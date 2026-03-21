package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"

	"go.uber.org/zap"
)

func TestFailureWaitBackoff(t *testing.T) {
	s := &Service{
		cfg: config.Config{
			Sync: config.SyncConfig{
				UpdateInterval:  10 * time.Millisecond,
				FailureBaseWait: 100 * time.Millisecond,
				FailureMaxWait:  800 * time.Millisecond,
			},
		},
		log: zap.NewNop(),
	}

	if got := s.failureWait(); got != 100*time.Millisecond {
		t.Fatalf("attempt1 wait=%v", got)
	}
	if got := s.failureWait(); got != 200*time.Millisecond {
		t.Fatalf("attempt2 wait=%v", got)
	}
	if got := s.failureWait(); got != 400*time.Millisecond {
		t.Fatalf("attempt3 wait=%v", got)
	}
	if got := s.failureWait(); got != 800*time.Millisecond {
		t.Fatalf("attempt4 wait=%v", got)
	}
	if got := s.failureWait(); got != 800*time.Millisecond {
		t.Fatalf("attempt5 wait should cap max, got=%v", got)
	}
}

func TestSleepContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepContext(ctx, time.Second) {
		t.Fatalf("sleepContext should return false when context canceled")
	}
}
