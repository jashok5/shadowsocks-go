package autoblock

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jashok5/shadowsocks-go/internal/config"
	"github.com/jashok5/shadowsocks-go/internal/runtime"

	"go.uber.org/zap"
)

type apiClient interface {
	GetNodes(context.Context) ([]map[string]any, error)
	PostBlockIP(context.Context, []string) error
	GetBlockIP(context.Context) ([]map[string]any, error)
	GetUnblockIP(context.Context) ([]map[string]any, error)
}

type runtimeSource interface {
	Snapshot(context.Context) (runtime.Snapshot, error)
}

type Service struct {
	log  *zap.Logger
	cfg  config.AutoBlockConfig
	api  apiClient
	rt   runtimeSource
	be   Backend
	node map[netip.Addr]struct{}
}

func New(log *zap.Logger, cfg config.AutoBlockConfig, api apiClient, rt runtimeSource) *Service {
	return newWithBackend(log, cfg, api, rt, newBackend(cfg.Backend, log))
}

func newWithBackend(log *zap.Logger, cfg config.AutoBlockConfig, api apiClient, rt runtimeSource, be Backend) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = time.Minute
	}
	return &Service{
		log:  log,
		cfg:  cfg,
		api:  api,
		rt:   rt,
		be:   be,
		node: make(map[netip.Addr]struct{}),
	}
}

func (s *Service) Run(ctx context.Context) error {
	s.log.Info("auto_block started", zap.String("backend", s.be.Name()), zap.Duration("interval", s.cfg.SyncInterval))
	if err := s.syncOnce(ctx); err != nil {
		s.log.Warn("auto_block first sync failed", zap.Error(err))
	}
	t := time.NewTicker(s.cfg.SyncInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := s.be.Close(context.Background()); err != nil {
				s.log.Warn("auto_block backend close failed", zap.Error(err))
			}
			return ctx.Err()
		case <-t.C:
			if err := s.syncOnce(ctx); err != nil {
				s.log.Warn("auto_block sync failed", zap.Error(err))
			}
		}
	}
}

func (s *Service) syncOnce(ctx context.Context) error {
	if s.cfg.ProtectNodeIP {
		if err := s.refreshNodeWhitelist(ctx); err != nil {
			s.log.Warn("refresh node whitelist failed", zap.Error(err))
		}
	}

	snap, err := s.rt.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("runtime snapshot: %w", err)
	}

	whitelist := s.currentWhitelist()
	candidates := sanitizeIPs(snap.WrongIP, whitelist)
	if len(candidates) > 0 {
		if err = s.api.PostBlockIP(ctx, candidates); err != nil {
			s.log.Warn("post block ip failed", zap.Int("count", len(candidates)), zap.Error(err))
		}
	}

	blockedRows, err := s.api.GetBlockIP(ctx)
	if err != nil {
		return fmt.Errorf("get block ip: %w", err)
	}
	unblockRows, err := s.api.GetUnblockIP(ctx)
	if err != nil {
		return fmt.Errorf("get unblock ip: %w", err)
	}

	blocked := parseRowIPSet(blockedRows, "ip")
	unblock := parseRowIPSet(unblockRows, "ip")
	desired := make(map[netip.Addr]struct{}, len(blocked))
	for ip := range blocked {
		if _, ok := unblock[ip]; ok {
			continue
		}
		if _, ok := whitelist[ip]; ok {
			continue
		}
		desired[ip] = struct{}{}
	}
	if err = s.be.Reconcile(ctx, desired); err != nil {
		return fmt.Errorf("backend reconcile: %w", err)
	}
	s.log.Debug("auto_block reconciled", zap.Int("posted", len(candidates)), zap.Int("active", len(desired)))
	return nil
}

func (s *Service) currentWhitelist() map[netip.Addr]struct{} {
	out := make(map[netip.Addr]struct{}, len(s.cfg.StaticWhitelist)+len(s.node))
	for _, item := range s.cfg.StaticWhitelist {
		if ip, ok := parseSingleIP(item); ok {
			out[ip] = struct{}{}
		}
	}
	for ip := range s.node {
		out[ip] = struct{}{}
	}
	return out
}

func (s *Service) refreshNodeWhitelist(ctx context.Context) error {
	rows, err := s.api.GetNodes(ctx)
	if err != nil {
		return err
	}
	next := make(map[netip.Addr]struct{})
	for _, row := range rows {
		for _, key := range []string{"node_ip", "ip"} {
			raw, ok := row[key]
			if !ok {
				continue
			}
			for _, part := range strings.Split(strings.TrimSpace(fmt.Sprint(raw)), ",") {
				if ip, ok := parseSingleIP(part); ok {
					next[ip] = struct{}{}
				}
			}
		}
	}
	s.node = next
	return nil
}

func sanitizeIPs(items []string, whitelist map[netip.Addr]struct{}) []string {
	set := make(map[netip.Addr]struct{})
	for _, item := range items {
		ip, ok := parseSingleIP(item)
		if !ok {
			continue
		}
		if _, blocked := whitelist[ip]; blocked {
			continue
		}
		set[ip] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for ip := range set {
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

func parseRowIPSet(rows []map[string]any, key string) map[netip.Addr]struct{} {
	out := make(map[netip.Addr]struct{})
	for _, row := range rows {
		raw, ok := row[key]
		if !ok {
			continue
		}
		for _, part := range strings.Split(fmt.Sprint(raw), ",") {
			if ip, ok := parseSingleIP(part); ok {
				out[ip] = struct{}{}
			}
		}
	}
	return out
}

func parseSingleIP(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}
	if host, _, ok := strings.Cut(raw, ":"); ok && strings.Count(raw, ":") == 1 {
		raw = strings.TrimSpace(host)
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}
