package session

import "testing"

func TestNewSessionIDNonZero(t *testing.T) {
	id, err := NewSessionID()
	if err != nil {
		t.Fatalf("new session id: %v", err)
	}
	if id == 0 {
		t.Fatalf("session id should not be zero")
	}
}

func TestSequencerNext(t *testing.T) {
	s := NewSequencer(10)
	if got := s.Next(); got != 11 {
		t.Fatalf("next= %d, want=11", got)
	}
	if got := s.Next(); got != 12 {
		t.Fatalf("next= %d, want=12", got)
	}
}
