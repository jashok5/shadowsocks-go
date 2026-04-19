package runtime

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	atpInit "github.com/jashok5/shadowsocks-go/internal/atp/initialize"
	atpPanel "github.com/jashok5/shadowsocks-go/internal/atp/panel"
	"github.com/jashok5/shadowsocks-go/internal/model"
	"go.uber.org/zap"
)

func installATPTestTLSHooks(t *testing.T) {
	t.Helper()
	origBuildTLS := atpBuildTLSConfig
	origEnsure := atpEnsureCertificateReadyWithTry
	origReadState := atpReadCertificateCacheState
	origStartChallenge := atpStartACMEChallengeListener
	origStartMaint := atpStartCertificateMaintenance
	t.Cleanup(func() {
		atpBuildTLSConfig = origBuildTLS
		atpEnsureCertificateReadyWithTry = origEnsure
		atpReadCertificateCacheState = origReadState
		atpStartACMEChallengeListener = origStartChallenge
		atpStartCertificateMaintenance = origStartMaint
	})
	atpBuildTLSConfig = func(_ *atpInit.Config, _ atpPanel.NodeInfo) (*tls.Config, error) {
		return &tls.Config{
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{"atp", "h2", "http/1.1"},
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return nil, context.Canceled
			},
		}, nil
	}
	atpEnsureCertificateReadyWithTry = func(context.Context, *zap.Logger, *tls.Config, string, time.Duration, time.Duration) error {
		return nil
	}
	atpReadCertificateCacheState = func(context.Context, string) (atpInit.CertificateCacheState, error) {
		return atpInit.CertificateCacheState{}, nil
	}
	atpStartACMEChallengeListener = func(context.Context, *zap.Logger, string, int, *tls.Config) error {
		return nil
	}
	atpStartCertificateMaintenance = func(context.Context, *zap.Logger, *tls.Config, string) {}
}

func TestATPDriverSyncAndSnapshot(t *testing.T) {
	installATPTestTLSHooks(t)

	drv := NewATPDriver(zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := ATPConfig{
		NodeInfo: model.NodeInfo{
			Sort:           16,
			Server:         "127.0.0.1;port=18443|transport=tls|host=localhost",
			NodeSpeedLimit: 100,
		},
		Users: []model.User{
			{ID: 1001, UUID: "u1001", Passwd: "p1001", NodeSpeed: 10, NodeConnector: 2},
		},
		Rules: []model.DetectRule{{ID: 7, Regex: "example", Type: 1}},

		HandshakeTimeout:      10 * time.Second,
		IdleTimeout:           30 * time.Second,
		ResumeTicketTTL:       2 * time.Minute,
		MaxConnsPerUser:       2,
		MaxOpenStreamsPerUser: 32,
		EnableAuditBlock:      true,
		AuditBlockDuration:    10 * time.Minute,
	}

	if err := drv.ApplyATP(ctx, cfg); err != nil {
		t.Fatalf("apply atp failed: %v", err)
	}

	st := drv.Stats()
	if st.Port != 18443 {
		t.Fatalf("unexpected atp port: %d", st.Port)
	}
	if !st.ProxyActive {
		t.Fatalf("expected proxy active")
	}
	if st.Users != 1 {
		t.Fatalf("expected 1 user, got %d", st.Users)
	}
	if st.Rules != 1 {
		t.Fatalf("expected 1 rule, got %d", st.Rules)
	}

	snap, err := drv.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snap.UserTransfer) != 0 {
		t.Fatalf("expected empty user transfer on cold snapshot, got %d", len(snap.UserTransfer))
	}

	if err := drv.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	st = drv.Stats()
	if st.ProxyActive {
		t.Fatalf("expected proxy inactive after close")
	}
}

func TestATPDriverRejectNonATPNode(t *testing.T) {
	drv := NewATPDriver(zap.NewNop())
	err := drv.ApplyATP(context.Background(), ATPConfig{NodeInfo: model.NodeInfo{Sort: 0}})
	if err == nil {
		t.Fatalf("expected error for non-atp node")
	}
}

func TestATPDriverHotReloadBehavior(t *testing.T) {
	installATPTestTLSHooks(t)

	drv := NewATPDriver(zap.NewNop())
	drv.minRestartGap = 0
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	baseCfg := ATPConfig{
		NodeInfo: model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=20443|transport=tls|host=localhost", NodeSpeedLimit: 50},
		Users:    []model.User{{ID: 2001, UUID: "u2001", Passwd: "p2001", NodeSpeed: 12, NodeConnector: 2}},
		Rules:    []model.DetectRule{{ID: 11, Regex: "foo", Type: 1}},

		HandshakeTimeout:      10 * time.Second,
		IdleTimeout:           30 * time.Second,
		ResumeTicketTTL:       2 * time.Minute,
		MaxConnsPerUser:       2,
		MaxOpenStreamsPerUser: 32,
		EnableAuditBlock:      true,
		AuditBlockDuration:    10 * time.Minute,
	}

	if err := drv.ApplyATP(ctx, baseCfg); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	first := drv.Stats()
	if first.ProxyGen != 1 {
		t.Fatalf("expected proxy generation=1, got %d", first.ProxyGen)
	}

	policyOnlyCfg := baseCfg
	policyOnlyCfg.Users = []model.User{{ID: 2001, UUID: "u2001", Passwd: "p2001", NodeSpeed: 20, NodeConnector: 3}}
	policyOnlyCfg.Rules = []model.DetectRule{{ID: 22, Regex: "bar", Type: 1}}
	if err := drv.ApplyATP(ctx, policyOnlyCfg); err != nil {
		t.Fatalf("policy apply failed: %v", err)
	}
	second := drv.Stats()
	if second.ProxyGen != first.ProxyGen {
		t.Fatalf("expected proxy generation unchanged on policy update, got %d -> %d", first.ProxyGen, second.ProxyGen)
	}
	if second.Rules != 1 || second.Users != 1 {
		t.Fatalf("expected users=1 rules=1 after policy update, got users=%d rules=%d", second.Users, second.Rules)
	}

	endpointCfg := policyOnlyCfg
	endpointCfg.NodeInfo = model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=20444|transport=tls|host=localhost", NodeSpeedLimit: 50}
	if err := drv.ApplyATP(ctx, endpointCfg); err != nil {
		t.Fatalf("endpoint apply failed: %v", err)
	}
	third := drv.Stats()
	if third.ProxyGen != second.ProxyGen+1 {
		t.Fatalf("expected proxy generation increment on endpoint update, got %d -> %d", second.ProxyGen, third.ProxyGen)
	}
	if third.Port != 20444 {
		t.Fatalf("expected updated atp port=20444, got %d", third.Port)
	}

	if err := drv.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestATPDriverRestartDebounce(t *testing.T) {
	installATPTestTLSHooks(t)

	drv := NewATPDriver(zap.NewNop())
	drv.minRestartGap = 2 * time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	firstCfg := ATPConfig{
		NodeInfo: model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=21443|transport=tls|host=localhost", NodeSpeedLimit: 50},
		Users:    []model.User{{ID: 3001, UUID: "u3001", Passwd: "p3001", NodeSpeed: 12, NodeConnector: 2}},
		Rules:    []model.DetectRule{{ID: 31, Regex: "foo", Type: 1}},

		HandshakeTimeout:      10 * time.Second,
		IdleTimeout:           30 * time.Second,
		ResumeTicketTTL:       2 * time.Minute,
		RestartDebounce:       2 * time.Hour,
		MaxConnsPerUser:       2,
		MaxOpenStreamsPerUser: 32,
		EnableAuditBlock:      true,
		AuditBlockDuration:    10 * time.Minute,
	}
	if err := drv.ApplyATP(ctx, firstCfg); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	st1 := drv.Stats()
	if st1.ProxyGen != 1 {
		t.Fatalf("expected generation=1, got %d", st1.ProxyGen)
	}

	secondCfg := firstCfg
	secondCfg.NodeInfo = model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=21444|transport=tls|host=localhost", NodeSpeedLimit: 50}
	if err := drv.ApplyATP(ctx, secondCfg); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	st2 := drv.Stats()
	if st2.ProxyGen != st1.ProxyGen {
		t.Fatalf("expected generation unchanged due to debounce, got %d -> %d", st1.ProxyGen, st2.ProxyGen)
	}
	if st2.Port != 21443 {
		t.Fatalf("expected port unchanged due to debounce, got %d", st2.Port)
	}

	if err := drv.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestATPDriverKeepsOldProxyOnCertFailure(t *testing.T) {
	installATPTestTLSHooks(t)

	drv := NewATPDriver(zap.NewNop())
	drv.minRestartGap = 0
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	baseCfg := ATPConfig{
		NodeInfo: model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=22443|transport=tls|host=localhost", NodeSpeedLimit: 50},
		Users:    []model.User{{ID: 4001, UUID: "u4001", Passwd: "p4001", NodeSpeed: 10, NodeConnector: 2}},
		Rules:    []model.DetectRule{{ID: 41, Regex: "foo", Type: 1}},

		HandshakeTimeout:      10 * time.Second,
		IdleTimeout:           30 * time.Second,
		ResumeTicketTTL:       2 * time.Minute,
		MaxConnsPerUser:       2,
		MaxOpenStreamsPerUser: 32,
		EnableAuditBlock:      true,
		AuditBlockDuration:    10 * time.Minute,
	}
	if err := drv.ApplyATP(ctx, baseCfg); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	st1 := drv.Stats()

	origEnsure := atpEnsureCertificateReadyWithTry
	atpEnsureCertificateReadyWithTry = func(context.Context, *zap.Logger, *tls.Config, string, time.Duration, time.Duration) error {
		return context.DeadlineExceeded
	}
	t.Cleanup(func() {
		atpEnsureCertificateReadyWithTry = origEnsure
	})

	failedCfg := baseCfg
	failedCfg.NodeInfo = model.NodeInfo{Sort: 16, Server: "127.0.0.1;port=22444|transport=tls|host=localhost", NodeSpeedLimit: 50}
	err := drv.ApplyATP(ctx, failedCfg)
	if err == nil {
		t.Fatalf("expected cert failure error")
	}
	st2 := drv.Stats()
	if st2.ProxyGen != st1.ProxyGen {
		t.Fatalf("expected generation unchanged on cert failure, got %d -> %d", st1.ProxyGen, st2.ProxyGen)
	}
	if st2.Port != st1.Port {
		t.Fatalf("expected old proxy endpoint preserved on cert failure, got %d -> %d", st1.Port, st2.Port)
	}

	if err := drv.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
