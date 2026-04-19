package atperr

import (
	"errors"
	"testing"
)

func TestIsCode(t *testing.T) {
	err := &Error{Code: CodeAuthFailed, Reason: "auth_failed"}
	if !IsCode(err, CodeAuthFailed) {
		t.Fatalf("expected IsCode true")
	}
	if IsCode(err, CodeInternal) {
		t.Fatalf("expected IsCode false")
	}
}

func TestIsCodeWrapped(t *testing.T) {
	err := errors.New("plain")
	if IsCode(err, CodeAuthFailed) {
		t.Fatalf("expected false for non atp error")
	}
}
