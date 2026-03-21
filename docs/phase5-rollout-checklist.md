# Phase 5 Rollout Checklist

## 1) Preflight

- Build passes: `go test ./...`
- Config verified: `configs/config.example.yaml`
- Runtime driver selected for rollout: `runtime.driver: ss`
- Node ID and token match production node
- Host limits checked (`ulimit -n`, CPU, memory)

## 2) Canary Start

- Start Go node on one canary host only
- Keep Python node available for immediate rollback
- Observe 15-30 minutes before wider rollout

Key signals:

- `sync cycle complete` exists every update interval
- `runtime sync reconciled` has stable added/updated/removed counts
- `runtime pressure` does not show continuously rising `max_tcp_drop/max_udp_drop`
- `sync failed` and `api call retrying` are occasional, not continuous

## 3) Parallel Observation (Python vs Go)

Compare same window (at least 30 min):

- Traffic upload/download trends are in same magnitude
- Alive IP reports are non-empty and periodic
- Detect log reports are non-empty when expected
- Node info update is periodic and successful

## 4) Rollback Conditions

Rollback immediately if any condition persists > 5 minutes:

- No successful `sync cycle complete`
- API post failures keep increasing without recovery
- Port runtime repeatedly churns (high start/reload/stop oscillation)
- Sustained drop growth (`max_tcp_drop` or `max_udp_drop` keeps rising quickly)
- User traffic reports drop to near zero unexpectedly

## 5) Rollback Procedure

1. Stop Go node process.
2. Restore Python node process on the same host.
3. Verify Python node resumes traffic + API reporting.
4. Capture Go logs and config for incident analysis.
5. Open fix task before next rollout.

## 6) Full Rollout Gate

Proceed to full rollout only when:

- Canary runs stable for 24 hours
- No rollback condition triggered
- Traffic and API reporting are consistent with Python baseline
