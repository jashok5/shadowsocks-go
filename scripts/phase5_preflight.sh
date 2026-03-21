#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v task >/dev/null 2>&1; then
  echo "未检测到 task，请先安装 Task（https://taskfile.dev/installation/）"
  exit 1
fi

echo "[1/5] task test"
task test

echo "[2/5] verify config exists"
test -f "configs/config.example.yaml"

echo "[3/5] verify key config fields"
grep -E "^\s*driver:\s*" configs/config.example.yaml >/dev/null
grep -E "^\s*id:\s*" configs/config.example.yaml >/dev/null
grep -E "^\s*token:\s*" configs/config.example.yaml >/dev/null

echo "[4/5] dry run build"
task build

echo "[5/5] done"
echo "preflight success"
