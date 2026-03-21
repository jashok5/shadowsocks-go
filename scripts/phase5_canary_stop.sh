#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

LOG_DIR="${LOG_DIR:-$ROOT_DIR/.phase5}"
PID_FILE="$LOG_DIR/canary.pid"

if [[ ! -f "$PID_FILE" ]]; then
  echo "no pid file"
  exit 0
fi

pid="$(cat "$PID_FILE")"
if kill -0 "$pid" 2>/dev/null; then
  kill "$pid"
  echo "stopped pid=$pid"
else
  echo "process not running: $pid"
fi

rm -f "$PID_FILE"
