#!/usr/bin/env bash
set -euo pipefail

CFG_PATH="/opt/shadowsocks-node/config.yaml"
SERVICE_NAME="shadowsocks-node"
TOKEN=""

usage() {
  cat <<'EOF'
用法:
  sudo ./scripts/set_panel_token.sh --token <PANEL_TOKEN>

可选参数:
  --config <path>        配置文件路径（默认 /opt/shadowsocks-node/config.yaml）
  --service <name>       systemd 服务名（默认 shadowsocks-node）
  --token <token>        直接传入 token
  --token-file <path>    从文件读取 token（推荐，避免出现在命令历史）
  -h, --help             显示帮助

说明:
  - 依赖 yq v4+
  - 修改 panel.token 后会自动重启服务
EOF
}

if [[ "${EUID}" -ne 0 ]]; then
  echo "请使用 root 运行本脚本"
  exit 1
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      [[ $# -ge 2 ]] || { echo "--config 缺少参数"; exit 1; }
      CFG_PATH="$2"
      shift 2
      ;;
    --service)
      [[ $# -ge 2 ]] || { echo "--service 缺少参数"; exit 1; }
      SERVICE_NAME="$2"
      shift 2
      ;;
    --token)
      [[ $# -ge 2 ]] || { echo "--token 缺少参数"; exit 1; }
      TOKEN="$2"
      shift 2
      ;;
    --token-file)
      [[ $# -ge 2 ]] || { echo "--token-file 缺少参数"; exit 1; }
      [[ -f "$2" ]] || { echo "token 文件不存在: $2"; exit 1; }
      TOKEN="$(tr -d '\r\n' < "$2")"
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

if [[ -z "${TOKEN}" ]]; then
  echo "必须提供 --token 或 --token-file"
  exit 1
fi

if ! command -v yq >/dev/null 2>&1; then
  echo "未找到 yq，请安装 yq v4+"
  exit 1
fi

if [[ ! -f "$CFG_PATH" ]]; then
  echo "配置文件不存在: $CFG_PATH"
  exit 1
fi

if ! systemctl status "$SERVICE_NAME" >/dev/null 2>&1; then
  echo "未找到 systemd 服务: $SERVICE_NAME"
  exit 1
fi

backup_path="${CFG_PATH}.bak"
cp -f "$CFG_PATH" "$backup_path"

export PANEL_TOKEN="$TOKEN"
yq eval -i '.panel.token = strenv(PANEL_TOKEN)' "$CFG_PATH"

echo "已更新 panel.token，重启服务: $SERVICE_NAME"
systemctl restart "$SERVICE_NAME"

echo "完成"
echo "配置文件: $CFG_PATH"
echo "备份文件: $backup_path"
