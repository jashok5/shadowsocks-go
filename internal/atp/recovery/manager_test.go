package recovery

import (
	"errors"
	"testing"
	"time"
)

func TestIssueConsumeSuccess(t *testing.T) {
	m := NewManagerWithSecret(Config{TTL: time.Minute}, []byte("01234567890123456789012345678901"))
	ticket, err := m.Issue(42, "client-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	sid, err := m.Consume(ticket, "client-a")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if sid != 42 {
		t.Fatalf("sid=%d want=42", sid)
	}
}

func TestReplayRejected(t *testing.T) {
	m := NewManagerWithSecret(Config{TTL: time.Minute}, []byte("01234567890123456789012345678901"))
	ticket, err := m.Issue(77, "client-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := m.Consume(ticket, "client-a"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := m.Consume(ticket, "client-a"); !errors.Is(err, ErrReplayTicket) {
		t.Fatalf("expected replay error, got %v", err)
	}
}

func TestBindingMismatch(t *testing.T) {
	m := NewManagerWithSecret(Config{TTL: time.Minute}, []byte("01234567890123456789012345678901"))
	ticket, err := m.Issue(99, "client-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := m.Consume(ticket, "client-b"); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("expected binding mismatch, got %v", err)
	}
}
