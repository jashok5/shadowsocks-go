# Runtime Unify Notes

This document records the runtime unification work for SS and SSR in the Go implementation.

## Goals

- Use a shared relay/session management model for SS and SSR.
- Keep protocol-specific encode/decode behavior in protocol adapters.
- Centralize handshake throttling, UDP association lifecycle, and observability.

## Completed Architecture

### Shared runtime primitives

- `handshakeGate`: unified handshake concurrency and per-IP acquire/release.
- `udpAssocStore`: shared TTL/LRU-backed UDP association cache wrapper.
- `udpAssocRunner`: shared UDP reader loop with timeout/context semantics.
- `udp_assoc_writer`: shared UDP write helpers with limiter-aware behavior.
- `udp_response_adapter`: protocol-specific UDP response writer adapters.
- `udp_assoc_error`: standardized UDP reader error classification.

### SS integration

- SS TCP/UDP paths use the shared handshake gate and UDP assoc store.
- SS UDP reader is served via shared runner + SS response adapter.

### SSR integration

- SSR TCP handshake path uses the shared handshake gate.
- SSR UDP no longer relies on protocol package `ShadowsocksRUDPMap`.
- SSR UDP reader is served via shared runner + SSR response adapter.

## Observability

Both SS and SSR expose UDP assoc runtime snapshots:

- reader metrics: active readers, packet count, error count
- alert snapshot: error delta per snapshot window + warning boolean

Configuration:

- `runtime.udp_assoc_error_delta_warn`: warning threshold for snapshot error delta.

Recommended starting values:

- low traffic node: `1` (surface any unexpected reader error quickly)
- medium traffic node: `3` (reduce noise from occasional network jitter)
- high traffic node: `5-10` (focus on sustained error bursts)

Tuning guidance:

- if warnings are noisy but service health is normal, raise threshold gradually by `+1` or `+2`
- if user-facing UDP failures are reported but warning frequency is low, lower threshold by `-1`
- review together with `error_kind` distribution in logs (`timeout/canceled/closed/io`)

Node-type presets:

- residential/broadband node (higher jitter/NAT noise): start with `3-5`
- IDC node (stable network, lower jitter): start with `1-3`
- cross-border node (complex path, variable quality): start with `5-10`

Adjustment cycle:

- observe at least one full traffic cycle (recommended `24h`) before changing threshold
- avoid large jumps; change by small increments and compare warning trend

## Operational Notes

- Long-running reload/UDP soak testing script: `scripts/phase6_udp_reload_soak.sh`.
- For production canary rollout, observe FD count, RSS growth, and UDP assoc alert deltas.
