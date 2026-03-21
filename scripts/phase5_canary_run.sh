#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v task >/dev/null 2>&1; then
  echo "未检测到 task，请先安装 Task（https://taskfile.dev/installation/）"
  exit 1
fi

LOG_DIR="${LOG_DIR:-$ROOT_DIR/.phase5}"
mkdir -p "$LOG_DIR"

BIN="$LOG_DIR/node"
LOG_FILE="$LOG_DIR/canary.log"
PID_FILE="$LOG_DIR/canary.pid"

echo "building binary..."
task build
cp "$ROOT_DIR/bin/node" "$BIN"

if [[ -f "$PID_FILE" ]]; then
  old_pid="$(cat "$PID_FILE")"
  if kill -0 "$old_pid" 2>/dev/null; then
    echo "existing canary process found: $old_pid"
    exit 1
  fi
fi

echo "starting canary..."
"$BIN" >"$LOG_FILE" 2>&1 &
echo $! >"$PID_FILE"

echo "canary started pid=$(cat "$PID_FILE")"
echo "log file: $LOG_FILE"
echo "stop: scripts/phase5_canary_stop.sh"
