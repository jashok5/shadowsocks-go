package autoblock

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
)

type Backend interface {
	Name() string
	Reconcile(context.Context, map[netip.Addr]struct{}) error
	Close(context.Context) error
}

func newBackend(name string, log *zap.Logger) Backend {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "noop":
		return newNoopBackend()
	case "ipset":
		return newIPSetBackend(log)
	case "nft":
		return newNFTBackend(log)
	default:
		if log != nil {
			log.Warn("unknown auto_block backend, fallback noop", zap.String("backend", name))
		}
		return newNoopBackend()
	}
}

type noopBackend struct {
	mu     sync.Mutex
	active map[netip.Addr]struct{}
}

func newNoopBackend() *noopBackend {
	return &noopBackend{active: make(map[netip.Addr]struct{})}
}

func (b *noopBackend) Name() string { return "noop" }

func (b *noopBackend) Reconcile(_ context.Context, desired map[netip.Addr]struct{}) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = cloneAddrSet(desired)
	return nil
}

func (b *noopBackend) Close(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = make(map[netip.Addr]struct{})
	return nil
}

type ipsetBackend struct {
	log  *zap.Logger
	mu   sync.Mutex
	init bool

	v4Set  string
	v6Set  string
	active map[netip.Addr]struct{}
}

type nftBackend struct {
	log  *zap.Logger
	mu   sync.Mutex
	init bool

	table  string
	v4Set  string
	v6Set  string
	chain  string
	active map[netip.Addr]struct{}
}

func newIPSetBackend(log *zap.Logger) *ipsetBackend {
	return &ipsetBackend{
		log:    log,
		v4Set:  "shadowsocks-go-block-v4",
		v6Set:  "shadowsocks-go-block-v6",
		active: make(map[netip.Addr]struct{}),
	}
}

func newNFTBackend(log *zap.Logger) *nftBackend {
	return &nftBackend{
		log:    log,
		table:  "shadowsocks_go",
		v4Set:  "block_v4",
		v6Set:  "block_v6",
		chain:  "input",
		active: make(map[netip.Addr]struct{}),
	}
}

func (b *ipsetBackend) Name() string { return "ipset" }

func (b *nftBackend) Name() string { return "nft" }

func (b *ipsetBackend) Reconcile(ctx context.Context, desired map[netip.Addr]struct{}) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if runtime.GOOS != "linux" {
		return fmt.Errorf("ipset backend requires linux")
	}
	if !b.init {
		if err := b.ensureBase(ctx); err != nil {
			return err
		}
		b.init = true
	}

	for ip := range desired {
		if _, ok := b.active[ip]; ok {
			continue
		}
		if err := b.addIP(ctx, ip); err != nil {
			return err
		}
	}
	for ip := range b.active {
		if _, ok := desired[ip]; ok {
			continue
		}
		if err := b.delIP(ctx, ip); err != nil {
			return err
		}
	}
	b.active = cloneAddrSet(desired)
	return nil
}

func (b *ipsetBackend) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if runtime.GOOS != "linux" {
		return nil
	}
	ips := sortedAddrList(b.active)
	for _, ip := range ips {
		_ = b.delIP(ctx, ip)
	}
	b.active = make(map[netip.Addr]struct{})
	return nil
}

func (b *nftBackend) Reconcile(ctx context.Context, desired map[netip.Addr]struct{}) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if runtime.GOOS != "linux" {
		return fmt.Errorf("nft backend requires linux")
	}
	if !b.init {
		if err := b.ensureBase(ctx); err != nil {
			return err
		}
		b.init = true
	}

	for ip := range desired {
		if _, ok := b.active[ip]; ok {
			continue
		}
		if err := b.addIP(ctx, ip); err != nil {
			return err
		}
	}
	for ip := range b.active {
		if _, ok := desired[ip]; ok {
			continue
		}
		if err := b.delIP(ctx, ip); err != nil {
			return err
		}
	}
	b.active = cloneAddrSet(desired)
	return nil
}

func (b *nftBackend) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if runtime.GOOS != "linux" {
		return nil
	}
	ips := sortedAddrList(b.active)
	for _, ip := range ips {
		_ = b.delIP(ctx, ip)
	}
	b.active = make(map[netip.Addr]struct{})
	return nil
}

func (b *ipsetBackend) ensureBase(ctx context.Context) error {
	if err := runCommand(ctx, "ipset", "create", b.v4Set, "hash:ip", "family", "inet", "-exist"); err != nil {
		return fmt.Errorf("create ipset v4: %w", err)
	}
	if err := runCommand(ctx, "ipset", "create", b.v6Set, "hash:ip", "family", "inet6", "-exist"); err != nil {
		return fmt.Errorf("create ipset v6: %w", err)
	}
	if err := runCommand(ctx, "iptables", "-C", "INPUT", "-m", "set", "--match-set", b.v4Set, "src", "-j", "DROP"); err != nil {
		if err := runCommand(ctx, "iptables", "-I", "INPUT", "-m", "set", "--match-set", b.v4Set, "src", "-j", "DROP"); err != nil {
			return fmt.Errorf("ensure iptables rule: %w", err)
		}
	}
	if err := runCommand(ctx, "ip6tables", "-C", "INPUT", "-m", "set", "--match-set", b.v6Set, "src", "-j", "DROP"); err != nil {
		if err := runCommand(ctx, "ip6tables", "-I", "INPUT", "-m", "set", "--match-set", b.v6Set, "src", "-j", "DROP"); err != nil {
			return fmt.Errorf("ensure ip6tables rule: %w", err)
		}
	}
	if b.log != nil {
		b.log.Info("auto_block ipset backend initialized", zap.String("v4_set", b.v4Set), zap.String("v6_set", b.v6Set))
	}
	return nil
}

func (b *nftBackend) ensureBase(ctx context.Context) error {
	if err := runCommandAllowExists(ctx, "nft", "add", "table", "inet", b.table); err != nil {
		return fmt.Errorf("create nft table: %w", err)
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "set", "inet", b.table, b.v4Set, "{", "type", "ipv4_addr", ";", "flags", "interval", ";", "}"); err != nil {
		return fmt.Errorf("create nft v4 set: %w", err)
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "set", "inet", b.table, b.v6Set, "{", "type", "ipv6_addr", ";", "flags", "interval", ";", "}"); err != nil {
		return fmt.Errorf("create nft v6 set: %w", err)
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "chain", "inet", b.table, b.chain, "{", "type", "filter", "hook", "input", "priority", "0", ";", "policy", "accept", ";", "}"); err != nil {
		return fmt.Errorf("create nft input chain: %w", err)
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "rule", "inet", b.table, b.chain, "ip", "saddr", "@"+b.v4Set, "drop"); err != nil {
		return fmt.Errorf("create nft v4 rule: %w", err)
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "rule", "inet", b.table, b.chain, "ip6", "saddr", "@"+b.v6Set, "drop"); err != nil {
		return fmt.Errorf("create nft v6 rule: %w", err)
	}
	if b.log != nil {
		b.log.Info("auto_block nft backend initialized", zap.String("table", b.table), zap.String("v4_set", b.v4Set), zap.String("v6_set", b.v6Set))
	}
	return nil
}

func (b *ipsetBackend) addIP(ctx context.Context, ip netip.Addr) error {
	name := b.v4Set
	if ip.Is6() {
		name = b.v6Set
	}
	if err := runCommand(ctx, "ipset", "add", name, ip.String(), "-exist"); err != nil {
		return fmt.Errorf("ipset add %s: %w", ip.String(), err)
	}
	return nil
}

func (b *ipsetBackend) delIP(ctx context.Context, ip netip.Addr) error {
	name := b.v4Set
	if ip.Is6() {
		name = b.v6Set
	}
	if err := runCommand(ctx, "ipset", "del", name, ip.String()); err != nil {
		return fmt.Errorf("ipset del %s: %w", ip.String(), err)
	}
	return nil
}

func (b *nftBackend) addIP(ctx context.Context, ip netip.Addr) error {
	set := b.v4Set
	if ip.Is6() {
		set = b.v6Set
	}
	if err := runCommandAllowExists(ctx, "nft", "add", "element", "inet", b.table, set, "{", ip.String(), "}"); err != nil {
		return fmt.Errorf("nft add %s: %w", ip.String(), err)
	}
	return nil
}

func (b *nftBackend) delIP(ctx context.Context, ip netip.Addr) error {
	set := b.v4Set
	if ip.Is6() {
		set = b.v6Set
	}
	if err := runCommandAllowNotFound(ctx, "nft", "delete", "element", "inet", b.table, set, "{", ip.String(), "}"); err != nil {
		return fmt.Errorf("nft del %s: %w", ip.String(), err)
	}
	return nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runCommandAllowExists(ctx context.Context, name string, args ...string) error {
	err := runCommand(ctx, name, args...)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "File exists") {
		return nil
	}
	return err
}

func runCommandAllowNotFound(ctx context.Context, name string, args ...string) error {
	err := runCommand(ctx, name, args...)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "No such file") || strings.Contains(err.Error(), "No such element") {
		return nil
	}
	return err
}

func cloneAddrSet(in map[netip.Addr]struct{}) map[netip.Addr]struct{} {
	out := make(map[netip.Addr]struct{}, len(in))
	for ip := range in {
		out[ip] = struct{}{}
	}
	return out
}

func sortedAddrList(set map[netip.Addr]struct{}) []netip.Addr {
	out := make([]netip.Addr, 0, len(set))
	for ip := range set {
		out = append(out, ip)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
