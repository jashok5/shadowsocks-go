package runtime

import (
	"context"
	"net"
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestSSDriverLifecycle(t *testing.T) {
	d := NewSSDriver(zap.NewNop())
	port := freePort(t)
	cfg := PortConfig{Port: port, UserID: 1001, Method: "aes-256-gcm", Password: "pass-1", Protocol: "origin", Obfs: "plain"}

	if err := d.Start(context.Background(), cfg); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := d.Start(context.Background(), cfg); err == nil {
		t.Fatalf("duplicate start should fail")
	}

	reload := cfg
	reload.Method = "chacha20-ietf-poly1305"
	if err := d.Reload(context.Background(), reload); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	d.AddTraffic(port, 100, 200)
	d.AddOnlineIP(port, "1.1.1.1")
	d.AddDetectRule(port, 10)

	snap, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if tr, ok := snap.Transfer[port]; !ok || tr.Upload != 100 || tr.Download != 200 {
		t.Fatalf("unexpected transfer snapshot: %#v", snap.Transfer)
	}
	if len(snap.OnlineIP[port]) != 1 {
		t.Fatalf("unexpected online ip snapshot: %#v", snap.OnlineIP)
	}
	if snap.OnlineIP[port][0] != "1.1.1.1" {
		t.Fatalf("unexpected online ip value: %#v", snap.OnlineIP[port])
	}
	if len(snap.Detect[port]) != 1 {
		t.Fatalf("unexpected detect snapshot: %#v", snap.Detect)
	}
	if snap.Detect[port][0] != 10 {
		t.Fatalf("unexpected detect value: %#v", snap.Detect[port])
	}

	if err := d.Stop(context.Background(), port); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestSSDriverConcurrentTraffic(t *testing.T) {
	d := NewSSDriver(zap.NewNop())
	port := freePort(t)
	if err := d.Start(context.Background(), PortConfig{Port: port, UserID: 42, Method: "aes-128-gcm", Password: "pass-2"}); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	const workers = 16
	const loops = 1000
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range loops {
				d.AddTraffic(port, 1, 2)
			}
		})
	}
	wg.Wait()

	snap, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	tr := snap.Transfer[port]
	if tr.Upload != workers*loops || tr.Download != workers*loops*2 {
		t.Fatalf("unexpected concurrent transfer: %+v", tr)
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
