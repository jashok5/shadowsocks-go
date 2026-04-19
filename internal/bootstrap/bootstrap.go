package bootstrap

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	goRuntime "runtime"
	"strings"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/panel"
	"github.com/jashok5/shadowsocks-go/internal/runtime"
	"github.com/jashok5/shadowsocks-go/internal/transfer"
	"github.com/jashok5/shadowsocks-go/internal/updater"
	"go.uber.org/zap"
)

func StartDebugServer(cfg config.Config, log *zap.Logger) {
	if !cfg.Debug.PPROFEnabled {
		return
	}
	addr := strings.TrimSpace(cfg.Debug.PPROFListen)
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	go func() {
		srv := &http.Server{Addr: addr}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("pprof server stopped", logger.Err(err), zap.String("listen", addr))
		}
	}()
	log.Info("pprof server started", zap.String("listen", addr))
}

func StartPanelServer(cfg config.Config, log *zap.Logger, rt runtime.Manager, assets fs.FS, driver string, appVersion string, startedAt time.Time) *http.Server {
	if !cfg.Panel.Enabled {
		return nil
	}
	addr := strings.TrimSpace(cfg.Panel.Listen)
	if addr == "" {
		addr = "0.0.0.0:18080"
	}
	h := panel.NewServer(cfg.Panel, log, rt, assets, driver, appVersion, startedAt, cfg.Sync.UpdateInterval).Handler()
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("panel server stopped", logger.Err(err), zap.String("listen", addr))
		}
	}()
	log.Info("panel server started", zap.String("listen", addr))
	return srv
}

func BuildHTTPClient(cfg config.APIConfig) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: cfg.TransportDialTimeout, KeepAlive: cfg.TransportKeepAlive}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.TransportMaxIdleConns,
		MaxIdleConnsPerHost:   cfg.TransportMaxIdlePerHost,
		IdleConnTimeout:       cfg.TransportIdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TransportTLSHandshake,
		ExpectContinueTimeout: cfg.TransportExpectContinue,
	}
	return &http.Client{Timeout: cfg.Timeout, Transport: transport}
}

func ResolveReconcileWorkers(configured int) int {
	if configured > 0 {
		return configured
	}
	v := max(goRuntime.GOMAXPROCS(0)*2, 4)
	if v > 64 {
		v = 64
	}
	return v
}

func BuildUpdater(cfg config.Config, log *zap.Logger, configPath string) (*updater.Updater, error) {
	if !cfg.Update.Enabled {
		return nil, nil
	}
	return updater.New(cfg.Update, log, configPath)
}

func BuildRuntime(cfg config.Config, resolvedDriver string, log *zap.Logger) (runtime.Manager, error) {
	drv, err := runtime.NewDriver(resolvedDriver, log, runtime.DriverTuning{
		MaxUDPSessionPerPort:      cfg.RT.MaxUDPSessionPerPort,
		MaxUDPResolveCacheEntries: cfg.RT.MaxUDPResolveCacheEntries,
		HandshakeMaxConcurrent:    cfg.RT.HandshakeMaxConcurrent,
		PerIPHandshakeMax:         cfg.RT.PerIPHandshakeMax,
		UDPAssocErrorDeltaWarn:    cfg.RT.UDPAssocErrorDeltaWarn,
	})
	if err != nil {
		return nil, err
	}
	return runtime.NewMemoryManagerWithDriver(log, drv, ResolveReconcileWorkers(cfg.RT.ReconcileWorkers)), nil
}

func BuildAPIClient(cfg config.Config, log *zap.Logger) *api.Client {
	httpClient := BuildHTTPClient(cfg.API)
	apiClient := api.NewClient(httpClient, cfg.API)
	apiClient.SetNodeID(cfg.Node.ID)
	apiClient.UpdateRetryPolicy(cfg.API.RetryMax, cfg.API.RetryBackoff, cfg.API.RetryMaxBackoff)
	apiClient.SetLogger(log)
	return apiClient
}

func BuildService(cfg config.Config, configPath string, log *zap.Logger, apiClient *api.Client, rt runtime.Manager, up *updater.Updater, version string) *transfer.Service {
	return transfer.NewService(cfg, configPath, log, apiClient, rt, up, version)
}
