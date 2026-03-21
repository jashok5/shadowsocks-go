package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"
)

func TestClientRetrySuccessAfterFailures(t *testing.T) {
	var n int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&n, 1)
		if cur < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":1,"data":"pong"}`))
	}))
	defer ts.Close()

	cfg := config.APIConfig{
		URL:             ts.URL,
		Token:           "t",
		Timeout:         time.Second,
		RetryMax:        3,
		RetryBackoff:    10 * time.Millisecond,
		RetryMaxBackoff: 50 * time.Millisecond,
	}
	c := NewClient(&http.Client{Timeout: time.Second}, cfg)

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping should succeed after retries: %v", err)
	}
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestClientNoRetryOnBadRequest(t *testing.T) {
	var n int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer ts.Close()

	cfg := config.APIConfig{
		URL:             ts.URL,
		Token:           "t",
		Timeout:         time.Second,
		RetryMax:        3,
		RetryBackoff:    10 * time.Millisecond,
		RetryMaxBackoff: 50 * time.Millisecond,
	}
	c := NewClient(&http.Client{Timeout: time.Second}, cfg)

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatalf("ping should fail")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("expected no retry on bad request, got attempts=%d", got)
	}
}
