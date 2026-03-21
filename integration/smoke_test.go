package integration

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/runtime"

	"go.uber.org/zap"
)

func TestRuntimeSmoke(t *testing.T) {
	d := runtime.NewSSDriver(zap.NewNop())
	port := freePort(t)
	cfg := runtime.PortConfig{
		Port:     port,
		UserID:   1,
		Method:   "aes-256-gcm",
		Password: "smoke-pass",
	}
	if err := d.Start(context.Background(), cfg); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer d.Close(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", intToString(port)), 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port not reachable in time: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := d.Stop(context.Background(), port); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	_, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", intToString(port)), 200*time.Millisecond)
	if err == nil {
		t.Fatalf("expected stopped port to reject connection")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port failed: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func intToString(v int) string {
	return fmt.Sprintf("%d", v)
}
