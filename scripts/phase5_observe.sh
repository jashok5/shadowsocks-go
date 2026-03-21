#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

LOG_DIR="${LOG_DIR:-$ROOT_DIR/.phase5}"
LOG_FILE="$LOG_DIR/canary.log"

if [[ ! -f "$LOG_FILE" ]]; then
  echo "log file not found: $LOG_FILE"
  exit 1
fi

echo "=== phase5 observation summary ==="
echo "log: $LOG_FILE"

sync_ok=$(rg -c "sync cycle complete" "$LOG_FILE" || true)
sync_failed=$(rg -c "sync failed" "$LOG_FILE" || true)
api_retry=$(rg -c "api call retrying" "$LOG_FILE" || true)
runtime_pressure=$(rg -c "runtime pressure" "$LOG_FILE" || true)

echo "sync_ok=$sync_ok"
echo "sync_failed=$sync_failed"
echo "api_retry=$api_retry"
echo "runtime_pressure_logs=$runtime_pressure"

echo "recent sync lines:"
rg "sync cycle complete|sync failed|runtime pressure" "$LOG_FILE" | tail -n 20 || true
