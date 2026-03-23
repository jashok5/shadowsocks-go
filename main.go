package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	goRuntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/api"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/runtime"
	"github.com/jashok5/shadowsocks-go/internal/transfer"
	"github.com/jashok5/shadowsocks-go/internal/updater"
)

func main() {
	configPath, logLevelOverride, logFormatOverride, showVersion := parseFlags()
	if showVersion {
		fmt.Println(versionString())
		return
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(logLevelOverride) != "" {
		cfg.Log.Level = strings.TrimSpace(logLevelOverride)
	}
	if strings.TrimSpace(logFormatOverride) != "" {
		cfg.Log.Format = strings.TrimSpace(logFormatOverride)
	}

	log, err := logger.New(cfg.Log)
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	httpClient := buildHTTPClient(cfg.API)
	apiClient := api.NewClient(httpClient, cfg.API)
	apiClient.SetLogger(log)

	var up *updater.Updater
	if cfg.Update.Enabled {
		up, err = updater.New(cfg.Update, log, configPath)
		if err != nil {
			log.Fatal("init updater failed", logger.Err(err))
		}
	}

	drv, err := runtime.NewDriver(cfg.RT.Driver, log, runtime.DriverTuning{
		MaxUDPSessionPerPort:      cfg.RT.MaxUDPSessionPerPort,
		MaxUDPResolveCacheEntries: cfg.RT.MaxUDPResolveCacheEntries,
		HandshakeMaxConcurrent:    cfg.RT.HandshakeMaxConcurrent,
	})
	if err != nil {
		log.Fatal("init runtime driver failed", logger.Err(err))
	}
	rt := runtime.NewMemoryManagerWithDriver(log, drv, resolveReconcileWorkers(cfg.RT.ReconcileWorkers))
	svc := transfer.NewService(cfg, configPath, log, apiClient, rt, up, version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("node service started", config.Fields(cfg)...)
	if err = svc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, transfer.ErrRestartRequired) {
		log.Fatal("node service stopped with error", logger.Err(err))
	}
	if errors.Is(err, transfer.ErrRestartRequired) {
		log.Info("node service fast exit for restart after auto update")
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = svc.Stop(shutdownCtx); err != nil {
		log.Error("service stop failed", logger.Err(err))
		os.Exit(1)
	}
	log.Info("node service stopped")
}

func buildHTTPClient(cfg config.APIConfig) *http.Client {
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

func resolveReconcileWorkers(configured int) int {
	if configured > 0 {
		return configured
	}
	v := goRuntime.GOMAXPROCS(0) * 2
	if v < 4 {
		v = 4
	}
	if v > 64 {
		v = 64
	}
	return v
}

func parseFlags() (configPath string, logLevel string, logFormat string, showVersion bool) {
	configPathFlag := flag.String("config", "configs/config.example.yaml", "配置文件路径")
	logLevelFlag := flag.String("log-level", "", "覆盖日志级别: debug/info/warn/error")
	logFormatFlag := flag.String("log-format", "", "覆盖日志格式: console/json")
	versionFlag := flag.Bool("version", false, "输出版本信息并退出")
	flag.Parse()
	return *configPathFlag, *logLevelFlag, *logFormatFlag, *versionFlag
}
