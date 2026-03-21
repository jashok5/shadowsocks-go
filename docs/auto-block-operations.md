# Auto Block Operations

## Goal

Provide Python `auto_block.py` equivalent behavior in a Go-native way:

- report suspicious source IPs to panel (`func/block_ip`)
- pull global block/unblock lists (`func/block_ip`, `func/unblock_ip`)
- enforce local deny set via pluggable backend (`noop` / `ipset` / `nft`)

## Architecture

- Collector: runtime snapshot `WrongIP` (from SSR driver)
- Controller: `internal/autoblock/service.go`
- Enforcement backend: `internal/autoblock/backend.go`
- Lifecycle: started/stopped by transfer service (`internal/transfer/service.go`)

Design principles:

- main sync loop and auto_block are isolated goroutines
- failures are logged and retried on next interval, no process panic
- backend is interface-based, easy to swap per host capability

## Config

`configs/config.yaml` example:

```yaml
security:
  auto_block:
    enabled: true
    backend: nft
    sync_interval: 60s
    protect_node_ip: true
    static_whitelist:
      - 127.0.0.1
      - ::1
```

Fields:

- `enabled`: enable auto block worker
- `backend`: `noop` / `ipset` / `nft`
- `sync_interval`: reconcile interval
- `protect_node_ip`: auto-whitelist node IPs from panel `nodes` API
- `static_whitelist`: local permanent whitelist

## Backend Selection

- `noop`: dry-run style, computes desired block set but does not touch firewall
- `ipset`: uses `ipset + iptables/ip6tables`
- `nft`: uses `nftables` set/rule model (recommended on modern Linux)

## nft Backend Behavior

On first reconcile it ensures:

- table: `inet shadowsocks_go`
- sets: `block_v4` (`ipv4_addr`), `block_v6` (`ipv6_addr`)
- chain: `input` (hook input, priority 0, policy accept)
- rules:
  - `ip saddr @block_v4 drop`
  - `ip6 saddr @block_v6 drop`

Then each cycle only diffs elements in sets (add/remove incremental IPs).

## Runbook

1. Start with `backend: noop` for 30-60 minutes.
2. Confirm logs have stable `auto_block reconciled` lines.
3. Switch to `backend: nft` on one canary host.
4. Verify panel block/unblock actions are reflected locally.
5. Roll out in batches.

## Verification Commands (Linux)

Check service logs:

```bash
journalctl -u shadowsocks-go -f | grep auto_block
```

Check nft objects:

```bash
sudo nft list table inet shadowsocks_go
```

Check blocked element count:

```bash
sudo nft list set inet shadowsocks_go block_v4
sudo nft list set inet shadowsocks_go block_v6
```

## Rollback

- Fast rollback: set `security.auto_block.enabled: false` and restart service.
- Backend-only rollback: switch `backend` to `noop` and restart service.

Notes:

- `Close()` removes runtime-added IP elements from managed sets.
- The nft table/chain/set scaffolding is kept for next startup.

## Troubleshooting

- `backend requires linux`: backend selected on non-Linux host.
- `nft ... failed`: missing nft command or insufficient privilege.
- no block effect:
  - confirm panel `func/block_ip` has data
  - confirm local whitelist is not filtering target IP
  - confirm nft table/rules exist and packet path hits input hook
