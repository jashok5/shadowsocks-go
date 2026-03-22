#!/usr/bin/env bash
set -euo pipefail

GITHUB_REPO="jashok5/shadowsocks-go"
GITHUB_API="https://api.github.com/repos/${GITHUB_REPO}"
RAW_BASE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main"
INSTALL_DIR="/opt/shadowsocks-node"
SERVICE_NAME="shadowsocks-node"
BIN_PATH="$INSTALL_DIR/node"
CFG_PATH="$INSTALL_DIR/config.yaml"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

usage() {
  cat <<EOF
用法:
  sudo ./deploy_linux.sh [--node-id <id>] [--api-url <url>] [--api-token <token>] [--version <tag>]

参数:
  --node-id   节点 ID（数字）
  --api-url   API 地址（例如 https://example.com）
  --api-token API Token
  --version   指定发布版本标签（例如 v1.2.3，默认 latest）
  -h, --help  显示帮助

说明:
  - 如果未传 --node-id、--api-url 或 --api-token，会进入交互输入模式
  - 传参和交互可混用，缺哪个补哪个
EOF
}

if [[ "${EUID}" -ne 0 ]]; then
  echo "请使用 root 运行本脚本"
  exit 1
fi

if command -v curl >/dev/null 2>&1; then
  FETCH_CMD="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  FETCH_CMD="wget -qO-"
else
  echo "未找到 curl/wget，请先安装其中一个"
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  SHA_CMD="shasum -a 256"
else
  echo "未找到 sha256sum/shasum，无法校验下载文件"
  exit 1
fi

if command -v jq >/dev/null 2>&1; then
  JQ_BIN="jq"
else
  JQ_BIN=""
fi

NODE_ID="${NODE_ID:-}"
API_URL="${API_URL:-}"
API_TOKEN="${API_TOKEN:-}"
RELEASE_TAG="${RELEASE_TAG:-latest}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node-id)
      if [[ $# -lt 2 ]]; then
        echo "--node-id 缺少参数"
        exit 1
      fi
      NODE_ID="$2"
      shift 2
      ;;
    --api-url)
      if [[ $# -lt 2 ]]; then
        echo "--api-url 缺少参数"
        exit 1
      fi
      API_URL="$2"
      shift 2
      ;;
    --api-token)
      if [[ $# -lt 2 ]]; then
        echo "--api-token 缺少参数"
        exit 1
      fi
      API_TOKEN="$2"
      shift 2
      ;;
    --version)
      if [[ $# -lt 2 ]]; then
        echo "--version 缺少参数"
        exit 1
      fi
      RELEASE_TAG="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1"
      usage
      exit 1
      ;;
  esac
done

if [[ -z "${NODE_ID}" ]]; then
  read -r -p "请输入 Node ID: " NODE_ID
fi
if [[ -z "${NODE_ID}" || ! "${NODE_ID}" =~ ^[0-9]+$ ]]; then
  echo "Node ID 必须是数字"
  exit 1
fi

if [[ -z "${API_URL}" ]]; then
  read -r -p "请输入 API URL (例如 https://example.com): " API_URL
fi
if [[ -z "${API_URL}" ]]; then
  echo "API URL 不能为空"
  exit 1
fi

if [[ -z "${API_TOKEN}" ]]; then
  read -r -s -p "请输入 API Token: " API_TOKEN
  echo
fi
if [[ -z "${API_TOKEN}" ]]; then
  echo "API Token 不能为空"
  exit 1
fi

ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64)
    GOARCH="amd64"
    ;;
  aarch64|arm64)
    GOARCH="arm64"
    ;;
  *)
    echo "不支持的架构: $ARCH_RAW"
    exit 1
    ;;
esac

ASSET_NAME="node_linux_${GOARCH}"

download_text() {
  local url="$1"
  $FETCH_CMD "$url"
}

resolve_release() {
  local release_json
  if [[ "$RELEASE_TAG" == "latest" ]]; then
    release_json="$(download_text "$GITHUB_API/releases/latest")"
  else
    release_json="$(download_text "$GITHUB_API/releases/tags/$RELEASE_TAG")"
  fi

  if [[ -n "$JQ_BIN" ]]; then
    RELEASE_VERSION="$(printf '%s' "$release_json" | jq -r '.tag_name // empty')"
    BINARY_URL="$(printf '%s' "$release_json" | jq -r --arg name "$ASSET_NAME" '.assets[] | select(.name == $name) | .browser_download_url' | awk 'NR==1{print; exit}')"
    CHECKSUM_URL="$(printf '%s' "$release_json" | jq -r '.assets[] | select(.name == "checksums.txt") | .browser_download_url' | awk 'NR==1{print; exit}')"
  else
    RELEASE_VERSION="$(printf '%s' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | awk 'NR==1{print; exit}')"
    BINARY_URL="$(printf '%s' "$release_json" | sed -n "s|.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\([^\"]*/${ASSET_NAME}\)\".*|\1|p" | awk 'NR==1{print; exit}')"
    CHECKSUM_URL="$(printf '%s' "$release_json" | sed -n 's|.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*/checksums.txt\)".*|\1|p' | awk 'NR==1{print; exit}')"
  fi

  if [[ -z "$RELEASE_VERSION" ]]; then
    echo "无法解析 release tag"
    exit 1
  fi
  if [[ -z "$BINARY_URL" ]]; then
    echo "release $RELEASE_VERSION 中未找到资产: $ASSET_NAME"
    exit 1
  fi
  if [[ -z "$CHECKSUM_URL" ]]; then
    echo "release $RELEASE_VERSION 中未找到 checksums.txt"
    exit 1
  fi
}

verify_binary_checksum() {
  local bin_file="$1"
  local checksum_file="$2"
  local expected
  local actual

  expected="$(awk -v name="$ASSET_NAME" '$2==name || $2=="*"name {print tolower($1); exit}' "$checksum_file")"
  if [[ -z "$expected" ]]; then
    echo "checksums.txt 中未找到 $ASSET_NAME"
    exit 1
  fi
  actual="$( $SHA_CMD "$bin_file" | awk '{print tolower($1)}' )"
  if [[ "$expected" != "$actual" ]]; then
    echo "二进制校验失败"
    echo "expected: $expected"
    echo "actual:   $actual"
    exit 1
  fi
}

mkdir -p "$INSTALL_DIR"

resolve_release

echo "发布版本: $RELEASE_VERSION"
echo "目标架构: linux/$GOARCH"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "下载 node 程序..."
download_text "$BINARY_URL" > "$tmp_dir/$ASSET_NAME"

echo "下载 checksums.txt..."
download_text "$CHECKSUM_URL" > "$tmp_dir/checksums.txt"

echo "校验 node 程序..."
verify_binary_checksum "$tmp_dir/$ASSET_NAME" "$tmp_dir/checksums.txt"

install -m 0755 "$tmp_dir/$ASSET_NAME" "$BIN_PATH"
chmod +x "$BIN_PATH"

echo "下载 config.example.yaml..."
download_text "$RAW_BASE_URL/configs/config.example.yaml" > "$CFG_PATH"

tmp_file="$(mktemp)"
awk -v node_id="$NODE_ID" -v api_url="$API_URL" -v api_token="$API_TOKEN" '
BEGIN { section = "" }
/^[[:space:]]*node:[[:space:]]*$/ { section = "node"; print; next }
/^[[:space:]]*api:[[:space:]]*$/ { section = "api"; print; next }
/^[a-zA-Z0-9_]+:[[:space:]]*$/ {
  if ($0 !~ /^[[:space:]]*node:[[:space:]]*$/ && $0 !~ /^[[:space:]]*api:[[:space:]]*$/) {
    section = ""
  }
}
section == "node" && /^[[:space:]]*id:[[:space:]]*/ {
  print "  id: " node_id
  next
}
section == "api" && /^[[:space:]]*url:[[:space:]]*/ {
  print "  url: " api_url
  next
}
section == "api" && /^[[:space:]]*token:[[:space:]]*/ {
  print "  token: " api_token
  next
}
{ print }
' "$CFG_PATH" > "$tmp_file"
mv "$tmp_file" "$CFG_PATH"

cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=EasySSR Go Node Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$BIN_PATH --config $CFG_PATH --log-level info --log-format json
Restart=always
RestartSec=3
LimitNOFILE=1048576
KillSignal=SIGTERM
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"

echo
echo "部署完成"
echo "配置文件: $CFG_PATH"
echo "服务状态: systemctl status $SERVICE_NAME"
echo "实时日志: journalctl -u $SERVICE_NAME -f"
