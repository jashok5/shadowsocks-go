package transfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"github.com/jashok5/shadowsocks-go/internal/runtime"

	"go.uber.org/zap"
)

func TestSyncOnceWithMockAPI(t *testing.T) {
	var (
		mu            sync.Mutex
		trafficPosted []model.UserTraffic
		nodeInfoPost  int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-token" {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		uri := strings.TrimPrefix(r.URL.Path, "/mod_mu/")
		switch {
		case r.Method == http.MethodGet && uri == "func/ping":
			writeJSON(w, map[string]any{"ret": 1, "data": "pong"})
		case r.Method == http.MethodGet && uri == "nodes/5/info":
			writeJSON(w, map[string]any{"ret": 1, "data": map[string]any{"node_speedlimit": 0.0, "traffic_rate": 1.0, "mu_only": 1, "port_offset": 0}})
		case r.Method == http.MethodGet && uri == "nodes":
			writeJSON(w, map[string]any{"ret": 1, "data": []map[string]any{{"id": 5, "name": "HK #1200", "node_ip": "10.0.0.1"}}})
		case r.Method == http.MethodGet && uri == "users":
			writeJSON(w, map[string]any{"ret": 1, "data": []map[string]any{{"id": 1001, "port": 9001, "passwd": "p1", "method": "aes-256-gcm", "protocol": "origin", "obfs": "plain", "is_multi_user": 1}}})
		case r.Method == http.MethodGet && uri == "func/detect_rules":
			writeJSON(w, map[string]any{"ret": 1, "data": []any{}})
		case r.Method == http.MethodPost && uri == "users/traffic":
			var body struct {
				Data []model.UserTraffic `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			trafficPosted = append(trafficPosted, body.Data...)
			mu.Unlock()
			writeJSON(w, map[string]any{"ret": 1, "data": map[string]any{}})
		case r.Method == http.MethodPost && uri == "nodes/5/info":
			mu.Lock()
			nodeInfoPost++
			mu.Unlock()
			writeJSON(w, map[string]any{"ret": 1, "data": map[string]any{}})
		default:
			http.Error(w, "not found: "+uri, http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		Node: config.NodeConfig{ID: 5, GetPortOffsetByNodeName: true},
		API: config.APIConfig{
			URL:             server.URL,
			Token:           "test-token",
			Timeout:         time.Second,
			RetryMax:        0,
			RetryBackoff:    10 * time.Millisecond,
			RetryMaxBackoff: 10 * time.Millisecond,
		},
		Sync: config.SyncConfig{
			UpdateInterval:  time.Second,
			FailureBaseWait: time.Millisecond,
			FailureMaxWait:  5 * time.Millisecond,
		},
		RT: config.RuntimeConfig{ReconcileWorkers: 1},
	}

	client := api.NewClient(&http.Client{Timeout: time.Second}, cfg.API)
	log := zap.NewNop()
	drv := runtime.NewMockDriver()
	drv.InjectUserTransfer(1001, model.PortTransfer{Upload: 123, Download: 456})
	rt := runtime.NewMemoryManagerWithDriver(log, drv, 1)
	svc := NewService(cfg, "", log, client, rt, nil, "v0.0.0")

	if err := svc.syncOnce(context.Background()); err != nil {
		t.Fatalf("syncOnce failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if nodeInfoPost == 0 {
		t.Fatalf("expected nodes/{id}/info post called")
	}
	if len(trafficPosted) != 1 {
		t.Fatalf("expected 1 traffic record, got %d", len(trafficPosted))
	}
	if trafficPosted[0].UserID != 1001 || trafficPosted[0].U != 123 || trafficPosted[0].D != 456 {
		t.Fatalf("unexpected traffic payload: %+v", trafficPosted[0])
	}

	if !drv.HasPort(10201) {
		t.Fatalf("expected effective port with name offset 10201")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
