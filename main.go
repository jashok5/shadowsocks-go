package main

import (
	"context"
	"errors"
	"fmt"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/bootstrap"
	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/logger"
	"github.com/jashok5/shadowsocks-go/internal/transfer"
	"go.uber.org/zap"
)

func main() {
	startedAt := time.Now()
	flags := bootstrap.ParseFlags()
	if flags.ShowVersionOnly {
		fmt.Println(versionString())
		return
	}

	cfg, err := config.Load(flags.ConfigPath)
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(flags.LogLevel) != "" {
		cfg.Log.Level = strings.TrimSpace(flags.LogLevel)
	}
	if strings.TrimSpace(flags.LogFormat) != "" {
		cfg.Log.Format = strings.TrimSpace(flags.LogFormat)
	}

	log, err := logger.New(cfg.Log)
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	bootstrap.StartDebugServer(cfg, log)
	apiClient := bootstrap.BuildAPIClient(cfg, log)

	detectCtx, detectCancel := context.WithTimeout(context.Background(), cfg.API.Timeout)
	resolvedDriver, nodeInfo, err := bootstrap.ResolveRuntimeDriver(detectCtx, cfg, apiClient, log)
	detectCancel()
	if err != nil {
		log.Fatal("resolve runtime driver failed", logger.Err(err))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	up, err := bootstrap.BuildUpdater(cfg, log, flags.ConfigPath)
	if err != nil {
		log.Fatal("init updater failed", logger.Err(err))
	}
	rt, err := bootstrap.BuildRuntime(cfg, resolvedDriver, log)
	if err != nil {
		log.Fatal("init runtime driver failed", logger.Err(err))
	}
	svc := bootstrap.BuildService(cfg, flags.ConfigPath, log, apiClient, rt, up, version)
	panelServer := bootstrap.StartPanelServer(cfg, log, rt, resolvePanelAssets(cfg.Panel.Mode), resolvedDriver, version, startedAt)
	defer func() {
		if panelServer == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := panelServer.Shutdown(shutdownCtx); err != nil {
			log.Warn("panel server shutdown failed", logger.Err(err))
		}
	}()

	log.Info("node service started",
		append(config.Fields(cfg),
			zap.String("resolved_runtime_driver", resolvedDriver),
			zap.Int("node_sort", nodeInfo.Sort),
		)...,
	)
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
