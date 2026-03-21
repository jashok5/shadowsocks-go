# Python/Go Parallel Observation Guide

## Goal

Validate Go node behavior against Python node under similar traffic.

## Window

- Minimum: 30 minutes
- Recommended: 2-24 hours

## Compare Items

1. Sync cadence

- Go: count `sync cycle complete`
- Python: count periodic update loop logs

2. Traffic reporting

- Compare traffic post frequency and total volume trend
- Allow small drift, reject major sustained divergence

3. Runtime stability

- Go: `runtime sync reconciled` and `runtime pressure`
- Python: port start/stop churn logs

4. API reliability

- Go: `api call retrying`/`sync failed`
- Python: API exception logs

## Practical Steps

1. Run Go canary on a single host.
2. Keep Python running on equivalent host/group for baseline.
3. Collect logs from both sides in same timeframe.
4. Summarize metrics every 10 minutes.
5. Decide keep/rollback using checklist criteria.
