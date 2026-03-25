#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${ROOT_DIR}/tmp/shadowsocks-go-soak"
LOG="${ROOT_DIR}/tmp/phase6_soak.log"

mkdir -p "${ROOT_DIR}/tmp"

echo "[1/3] building shadowsocks-go"
go build -o "$BIN" "$ROOT_DIR"

echo "[2/3] starting soak workload"
echo "binary=$BIN" > "$LOG"
echo "start=$(date -u +%FT%TZ)" >> "$LOG"

echo "[3/3] instructions"
cat <<'EOF'
1) 使用你的测试配置启动服务（建议 driver=ssr，开启 UDP）。
2) 并发发 UDP 流量，并每 10-30 秒触发一次 reload。
3) 观察：
   - lsof -p <pid> | wc -l
   - ps -o pid,rss,vsz,etime,command -p <pid>
   - go tool pprof /debug/pprof/goroutine
4) 目标：30-60 分钟内 FD 与 goroutine 不持续增长。
EOF

echo "done"
