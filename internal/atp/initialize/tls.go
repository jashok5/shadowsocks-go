package initialize

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/publicsuffix"

	"go.uber.org/zap"

	"github.com/jashok5/shadowsocks-go/internal/atp/panel"
)

const (
	defaultACMECacheDir   = "data/acme"
	defaultACMEEmailFile  = "data/acme/account_email"
	defaultCertCheckEvery = 12 * time.Hour
	defaultRenewBefore    = 30 * 24 * time.Hour
)

func BuildTLSConfig(cfg *Config, node panel.NodeInfo) (*tls.Config, error) {
	sni := ExtractSNIFromNode(node)
	if sni == "" {
		return nil, fmt.Errorf("unable to derive tls sni from node server")
	}
	email, err := ensureACMEAccountEmail(defaultACMEEmailFile)
	if err != nil {
		return nil, err
	}
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      email,
		HostPolicy: autocert.HostWhitelist(sni),
		Cache:      autocert.DirCache(defaultACMECacheDir),
	}
	tlsCfg := manager.TLSConfig()
	tlsCfg.MinVersion = tls.VersionTLS13
	_, transport := ParsePortAndTransportFromNode(node)
	preferredALPN := preferredALPNByTransport(transport)
	tlsCfg.NextProtos = prioritizeALPN(tlsCfg.NextProtos, preferredALPN)
	return tlsCfg, nil
}

func BuildPanelClient(cfg *Config) *panel.Client {
	return panel.New(cfg.Panel.BaseURL, cfg.Panel.MuKey, cfg.Panel.Timeout)
}

func FetchNodeInfoForTLS(ctx context.Context, cfg *Config, pc *panel.Client) (panel.NodeInfo, error) {
	if pc == nil {
		return panel.NodeInfo{}, fmt.Errorf("panel client is nil")
	}
	node, err := pc.GetNodeInfo(ctx, cfg.Server.NodeID)
	if err != nil {
		return panel.NodeInfo{}, err
	}
	if node.Sort != 16 {
		return panel.NodeInfo{}, fmt.Errorf("node sort=%d is not ATP(16)", node.Sort)
	}
	return node, nil
}

func ApplyServerEndpointFromNode(cfg *Config, node panel.NodeInfo) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	port, transport := ParsePortAndTransportFromNode(node)
	if port <= 0 {
		return fmt.Errorf("unable to derive port from node server")
	}
	if transport == "" {
		transport = "tls"
	}
	if transport != "tls" && transport != "quic" {
		return fmt.Errorf("unsupported transport from node: %s", transport)
	}
	cfg.Server.Port = port
	cfg.Server.Transport = transport
	if strings.TrimSpace(cfg.Server.Listen) == "" {
		cfg.Server.Listen = "0.0.0.0"
	}
	return nil
}

func ExtractSNIFromNode(node panel.NodeInfo) string {
	raw := strings.TrimSpace(node.Server)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ";")
	host := strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		params := parseNodeParams(parts[1])
		if paramHost := strings.TrimSpace(params["host"]); paramHost != "" {
			return paramHost
		}
	}
	return host
}

func ParsePortAndTransportFromNode(node panel.NodeInfo) (int, string) {
	raw := strings.TrimSpace(node.Server)
	if raw == "" {
		return 0, ""
	}
	parts := strings.Split(raw, ";")
	hostPort := strings.TrimSpace(parts[0])
	params := map[string]string{}
	if len(parts) > 1 {
		params = parseNodeParams(parts[1])
	}

	port := 0
	if p := strings.TrimSpace(params["port"]); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			port = parsed
		}
	}
	if port == 0 {
		if _, p, err := net.SplitHostPort(hostPort); err == nil {
			if parsed, convErr := strconv.Atoi(p); convErr == nil {
				port = parsed
			}
		}
	}
	if port == 0 {
		nodePort := extractPortFromNodeAddress(node.Server)
		if nodePort > 0 {
			port = nodePort
		}
	}

	transport := strings.ToLower(strings.TrimSpace(params["transport"]))
	if transport == "tcp" || transport == "udp" {
		transport = "tls"
	}

	return port, transport
}

func extractPortFromNodeAddress(raw string) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	if strings.Contains(trimmed, ";") {
		trimmed = strings.Split(trimmed, ";")[0]
	}
	if strings.Contains(trimmed, "://") {
		if parsedURL, err := url.Parse(trimmed); err == nil {
			if p := parsedURL.Port(); p != "" {
				if parsed, convErr := strconv.Atoi(p); convErr == nil {
					return parsed
				}
			}
		}
	}
	if _, p, err := net.SplitHostPort(trimmed); err == nil {
		if parsed, convErr := strconv.Atoi(p); convErr == nil {
			return parsed
		}
	}
	return 0
}

func ensureACMEAccountEmail(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultACMEEmailFile
	}
	if data, err := os.ReadFile(path); err == nil {
		existing := strings.TrimSpace(string(data))
		if existing == "" {
			return "", nil
		}
		if isValidACMEEmail(existing) {
			return existing, nil
		}
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read acme email failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create acme email dir failed: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := os.WriteFile(path, []byte("\n"), 0o600); writeErr != nil {
			return "", fmt.Errorf("init acme email file failed: %w", writeErr)
		}
	}
	return "", nil
}

func isValidACMEEmail(email string) bool {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || addr == nil {
		return false
	}
	parts := strings.Split(addr.Address, "@")
	if len(parts) != 2 {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(parts[1])
	if !icann || suffix == "" {
		return false
	}
	return true
}

func parseNodeParams(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, "|") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

type CertificateCacheState struct {
	Exists   bool
	Expired  bool
	NotAfter time.Time
}

func ReadCertificateCacheState(ctx context.Context, serverName string) (CertificateCacheState, error) {
	serverName = strings.TrimSpace(strings.ToLower(strings.TrimSuffix(serverName, ".")))
	if serverName == "" {
		return CertificateCacheState{}, fmt.Errorf("server name is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := autocert.DirCache(defaultACMECacheDir).Get(ctx, serverName)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, autocert.ErrCacheMiss) {
			return CertificateCacheState{}, nil
		}
		return CertificateCacheState{}, fmt.Errorf("read certificate cache failed: %w", err)
	}
	pair, err := tls.X509KeyPair(data, data)
	if err != nil {
		return CertificateCacheState{}, fmt.Errorf("parse certificate cache failed: %w", err)
	}
	notAfter, err := certNotAfter(&pair)
	if err != nil {
		return CertificateCacheState{}, fmt.Errorf("parse cached certificate not_after failed: %w", err)
	}
	return CertificateCacheState{
		Exists:   true,
		Expired:  !notAfter.After(time.Now()),
		NotAfter: notAfter,
	}, nil
}

func StartACMEChallengeListener(ctx context.Context, logger *zap.Logger, listen string, port int, baseTLS *tls.Config) error {
	if baseTLS == nil {
		return fmt.Errorf("acme tcp listener tls config is nil")
	}
	addr := net.JoinHostPort(listen, strconv.Itoa(port))
	rawLn, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("start acme tcp listener at %s: %w", addr, err)
	}
	tlsLn := tls.NewListener(rawLn, baseTLS.Clone())
	logger.Info("acme/camouflage tcp listener started", zap.String("addr", addr))
	defer tlsLn.Close()

	body := `{"code":403,"message":"\u672a\u6388\u6743\u8bbf\u95ee"}`
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			alpn := "unknown"
			if r.TLS != nil {
				if p := strings.TrimSpace(r.TLS.NegotiatedProtocol); p != "" {
					alpn = strings.ToLower(p)
				}
			}
			logger.Info("camouflage request served",
				zap.String("remote", r.RemoteAddr),
				zap.String("host", r.Host),
				zap.String("method", r.Method),
				zap.String("uri", r.URL.RequestURI()),
				zap.String("proto", r.Proto),
				zap.String("alpn", alpn),
			)
			w.Header().Set("Server", "nginx")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(body))
		}),
		ConnState: func(c net.Conn, state http.ConnState) {
			if state != http.StateActive {
				return
			}
			tlsConn, ok := c.(*tls.Conn)
			if !ok {
				return
			}
			st := tlsConn.ConnectionState()
			alpn := strings.TrimSpace(strings.ToLower(st.NegotiatedProtocol))
			if alpn == "" {
				alpn = "unknown"
			}
			logger.Info("camouflage tls accepted",
				zap.String("remote", c.RemoteAddr().String()),
				zap.String("local", c.LocalAddr().String()),
				zap.String("alpn", alpn),
			)
		},
	}

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(tlsLn); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("acme tcp listener serve failed: %w", err)
	}
	return nil
}

func StartCertificateMaintenance(ctx context.Context, logger *zap.Logger, tlsCfg *tls.Config, serverName string) {
	if tlsCfg == nil || strings.TrimSpace(serverName) == "" {
		return
	}
	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = EnsureCertificateReady(checkCtx, logger, tlsCfg, serverName)
		cancel()

		ticker := time.NewTicker(defaultCertCheckEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				checkCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
				_ = EnsureCertificateReady(checkCtx, logger, tlsCfg, serverName)
				cancel()
			}
		}
	}()
}

func EnsureCertificateReady(ctx context.Context, logger *zap.Logger, tlsCfg *tls.Config, serverName string) error {
	if tlsCfg == nil {
		return fmt.Errorf("tls config is nil")
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return fmt.Errorf("server name is empty")
	}
	if tlsCfg.GetCertificate == nil {
		return fmt.Errorf("tls get certificate callback is nil")
	}

	chi := &tls.ClientHelloInfo{ServerName: serverName, SupportedProtos: []string{"h2", "http/1.1"}}
	cert, err := tlsCfg.GetCertificate(chi)
	if err != nil {
		if logger != nil {
			logger.Warn("ensure certificate failed", zap.String("server_name", serverName), zap.Error(err))
		}
		return err
	}

	notAfter, parseErr := certNotAfter(cert)
	if parseErr != nil {
		if logger != nil {
			logger.Warn("parse certificate failed", zap.String("server_name", serverName), zap.Error(parseErr))
		}
		return parseErr
	}

	remaining := time.Until(notAfter)
	if logger != nil {
		logger.Info("certificate check ok",
			zap.String("server_name", serverName),
			zap.Time("not_after", notAfter),
			zap.Duration("remaining", remaining),
		)
	}
	if remaining <= 0 {
		return fmt.Errorf("certificate expired at %s", notAfter.Format(time.RFC3339))
	}
	if remaining <= defaultRenewBefore {
		if _, err = tlsCfg.GetCertificate(chi); err != nil {
			if logger != nil {
				logger.Warn("certificate renewal trigger failed", zap.String("server_name", serverName), zap.Error(err))
			}
			return err
		}
	}
	return nil
}

func EnsureCertificateReadyWithRetry(ctx context.Context, logger *zap.Logger, tlsCfg *tls.Config, serverName string, timeout time.Duration, interval time.Duration) error {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		err := EnsureCertificateReady(deadlineCtx, logger, tlsCfg, serverName)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return lastErr
			}
			return deadlineCtx.Err()
		case <-time.After(interval):
		}
	}
}

func certNotAfter(cert *tls.Certificate) (time.Time, error) {
	if cert == nil || len(cert.Certificate) == 0 {
		return time.Time{}, fmt.Errorf("empty tls certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

func preferredALPNByTransport(transport string) []string {
	if strings.EqualFold(strings.TrimSpace(transport), "quic") {
		return []string{"atp", "h3"}
	}
	return []string{"atp", "h2", "http/1.1"}
}

func prioritizeALPN(existing []string, preferred []string) []string {
	ordered := make([]string, 0, len(preferred)+len(existing))
	seen := make(map[string]struct{}, len(preferred)+len(existing))
	for _, proto := range preferred {
		p := strings.TrimSpace(proto)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, p)
	}
	for _, proto := range existing {
		p := strings.TrimSpace(proto)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, p)
	}
	return ordered
}
