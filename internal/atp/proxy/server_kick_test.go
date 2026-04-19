package proxy

import (
	"context"
	"testing"
)

func TestKickUsers(t *testing.T) {
	s := &Server{userSessions: make(map[int32]map[uint64]sessionControl)}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	s.registerSession(1001, 1, cancel1, nil)
	s.registerSession(1001, 2, cancel2, nil)

	kicked := s.KickUsers([]int32{1001})
	if kicked != 2 {
		t.Fatalf("expected 2 kicked sessions, got %d", kicked)
	}

	select {
	case <-ctx1.Done():
	default:
		t.Fatal("session 1 was not canceled")
	}
	select {
	case <-ctx2.Done():
	default:
		t.Fatal("session 2 was not canceled")
	}
}
