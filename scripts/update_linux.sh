#!/usr/bin/env bash
set -euo pipefail

GITHUB_REPO="jashok5/shadowsocks-go"
GITHUB_API="https://api.github.com/repos/${GITHUB_REPO}"

INSTALL_DIR="/opt/shadowsocks-node"
SERVICE_NAME="shadowsocks-node"
RELEASE_TAG="latest"
CFG_PATH=""

usage() {
  cat <<EOF
用法:
  sudo ./update_linux.sh [--version <tag>] [--install-dir <path>] [--service-name <name>] [--config <path>]

参数:
  --version       指定发布版本标签（例如 v1.2.3，默认 latest）
  --install-dir   安装目录（默认 /opt/shadowsocks-node）
  --service-name  systemd 服务名（默认 shadowsocks-node）
  --config        配置文件路径（默认 <install-dir>/config.yaml）
  -h, --help      显示帮助

说明:
  - 自动按发布版本 config.example.yaml 合并现有配置
  - 旧配置项覆盖新模板同名项；新模板新增项保留默认值
  - 更新完成后自动重启 systemd 服务
EOF
}

if [[ "${EUID}" -ne 0 ]]; then
  echo "请使用 root 运行本脚本"
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      if [[ $# -lt 2 ]]; then
        echo "--version 缺少参数"
        exit 1
      fi
      RELEASE_TAG="$2"
      shift 2
      ;;
    --install-dir)
      if [[ $# -lt 2 ]]; then
        echo "--install-dir 缺少参数"
        exit 1
      fi
      INSTALL_DIR="$2"
      shift 2
      ;;
    --service-name)
      if [[ $# -lt 2 ]]; then
        echo "--service-name 缺少参数"
        exit 1
      fi
      SERVICE_NAME="$2"
      shift 2
      ;;
    --config)
      if [[ $# -lt 2 ]]; then
        echo "--config 缺少参数"
        exit 1
      fi
      CFG_PATH="$2"
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

if command -v yq >/dev/null 2>&1; then
  YQ_BIN="yq"
else
  echo "未找到 yq，无法自动合并配置文件，请先安装 yq v4+"
  exit 1
fi

BIN_PATH="${INSTALL_DIR}/node"
if [[ -z "$CFG_PATH" ]]; then
  CFG_PATH="${INSTALL_DIR}/config.yaml"
fi

if [[ ! -d "$INSTALL_DIR" ]]; then
  echo "安装目录不存在: $INSTALL_DIR"
  exit 1
fi

if [[ ! -x "$BIN_PATH" ]]; then
  echo "未找到可执行文件: $BIN_PATH"
  exit 1
fi

if [[ ! -f "$CFG_PATH" ]]; then
  echo "未找到配置文件: $CFG_PATH"
  exit 1
fi

if ! systemctl status "$SERVICE_NAME" >/dev/null 2>&1; then
  echo "未找到 systemd 服务: $SERVICE_NAME"
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

merge_config() {
  local new_example="$1"
  local old_config="$2"
  local merged_output="$3"

  "$YQ_BIN" eval-all 'select(fileIndex == 0) * select(fileIndex == 1)' "$new_example" "$old_config" > "$merged_output"
}

resolve_release

echo "目标版本: $RELEASE_VERSION"
echo "目标架构: linux/$GOARCH"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "下载 node 程序..."
download_text "$BINARY_URL" > "$tmp_dir/$ASSET_NAME"

echo "下载 checksums.txt..."
download_text "$CHECKSUM_URL" > "$tmp_dir/checksums.txt"

echo "下载 config.example.yaml..."
download_text "https://raw.githubusercontent.com/${GITHUB_REPO}/${RELEASE_VERSION}/configs/config.example.yaml" > "$tmp_dir/config.example.yaml"

echo "校验 node 程序..."
verify_binary_checksum "$tmp_dir/$ASSET_NAME" "$tmp_dir/checksums.txt"

echo "合并配置文件..."
merge_config "$tmp_dir/config.example.yaml" "$CFG_PATH" "$tmp_dir/config.merged.yaml"

backup_path="${BIN_PATH}.bak.$(date +%Y%m%d%H%M%S)"
cp -f "$BIN_PATH" "$backup_path"

config_backup_path="${CFG_PATH}.bak.$(date +%Y%m%d%H%M%S)"
cp -f "$CFG_PATH" "$config_backup_path"

install -m 0755 "$tmp_dir/$ASSET_NAME" "$BIN_PATH"
chmod +x "$BIN_PATH"

install -m 0644 "$tmp_dir/config.merged.yaml" "${CFG_PATH}.tmp"
mv -f "${CFG_PATH}.tmp" "$CFG_PATH"

echo "重启服务: $SERVICE_NAME"
systemctl restart "$SERVICE_NAME"

echo
echo "更新完成"
echo "备份文件: $backup_path"
echo "配置备份: $config_backup_path"
echo "当前版本: $($BIN_PATH --version)"
echo "服务状态: systemctl status $SERVICE_NAME"
echo "实时日志: journalctl -u $SERVICE_NAME -f"
