package runtime

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func dialTCPWithConfig(ctx context.Context, cfg PortConfig, target string) (net.Conn, error) {
	timeout := cfg.DialTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	network := "tcp"
	if cfg.DNSPreferIPv4 {
		network = "tcp4"
	}
	d := net.Dialer{Timeout: timeout}
	if r := buildResolver(cfg, timeout); r != nil {
		d.Resolver = r
	}
	return d.DialContext(ctx, network, target)
}

func resolveUDPAddrWithConfig(ctx context.Context, cfg PortConfig, target string) (*net.UDPAddr, error) {
	network := "udp"
	if cfg.DNSPreferIPv4 {
		network = "udp4"
	}
	resolver := buildResolver(cfg, cfg.DialTimeout)
	if resolver == nil {
		return net.ResolveUDPAddr(network, target)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		return net.ResolveUDPAddr(network, target)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, timeoutOrDefault(cfg.DialTimeout, 8*time.Second))
	defer cancel()
	ips, err := resolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("empty dns result for %s", host)
	}
	chosen := ips[0].IP
	if cfg.DNSPreferIPv4 {
		for _, item := range ips {
			if v4 := item.IP.To4(); v4 != nil {
				chosen = v4
				break
			}
		}
	}
	return net.ResolveUDPAddr(network, net.JoinHostPort(chosen.String(), port))
}

func buildResolver(cfg PortConfig, timeout time.Duration) *net.Resolver {
	addr := strings.TrimSpace(cfg.DNSResolver)
	if addr == "" {
		return nil
	}
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "53")
	}
	timeout = timeoutOrDefault(timeout, 8*time.Second)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			if strings.HasSuffix(network, "4") {
				return d.DialContext(ctx, "udp4", addr)
			}
			if strings.HasSuffix(network, "6") {
				return d.DialContext(ctx, "udp6", addr)
			}
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

func timeoutOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
