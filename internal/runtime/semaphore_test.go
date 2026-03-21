package runtime

import (
	"context"
	"testing"
)

func TestAcquireReleaseToken(t *testing.T) {
	sem := make(chan struct{}, 1)
	if !acquireToken(context.Background(), sem) {
		t.Fatalf("first acquire should succeed")
	}
	if acquireToken(context.Background(), sem) {
		t.Fatalf("second acquire should fail when full")
	}
	releaseToken(sem)
	if !acquireToken(context.Background(), sem) {
		t.Fatalf("acquire should succeed after release")
	}
}

func TestAcquireTokenCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := make(chan struct{}, 1)
	if acquireToken(ctx, sem) {
		t.Fatalf("acquire should fail on canceled context")
	}
}
