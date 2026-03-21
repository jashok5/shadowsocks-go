# shadowsocks-go

基于 Go 重构的 Shadowsocks 节点端服务，负责：

- 周期性从 API 拉取节点信息、用户信息、审计规则
- 动态管理端口运行时（启动、重载、停止）
- 上报用户流量、在线 IP、审计日志、节点状态

## 1. 编译步骤

要求：

- Go 版本：建议与 `go.mod` 保持一致
- Linux/macOS 均可编译（生产建议 Linux）
- 构建工具：Task（https://taskfile.dev/）

步骤：

```bash
git clone <你的仓库地址>
cd shadowsocks-go
go mod tidy
task test
task build
```

环境缺少 Task 时会提示安装方式（可执行 `task --list` 验证安装）。
环境缺少 upx 时，`task build` / `task build:linux:amd64` 会给出安装提示并中止。

可选预检查：

```bash
./scripts/phase5_preflight.sh
```

## 2. 配置文件说明

默认配置文件：`configs/config.example.yaml`

建议部署前先复制一份私有配置：

```bash
cp configs/config.example.yaml configs/config.yaml
```

然后在私有配置中填写真实 `node.id`、`api.url`、`api.token`。

示例：

```yaml
node:
  id: 5
  get_port_offset_by_node_name: true

api:
  interface: modwebapi
  url: https://example.com
  token: your-token
  timeout: 10s
  retry_max: 2
  retry_backoff: 500ms
  retry_max_backoff: 5s

sync:
  update_interval: 60s
  failure_base_wait: 3s
  failure_max_wait: 60s

update:
  enabled: false
  repository: jashok5/shadowsocks-go
  check_interval: 1h
  timeout: 30s
  allow_prerelease: false

runtime:
  driver: ss
  reconcile_workers: 8

log:
  level: info
  format: console
```

字段说明：

- `node.id`：节点 ID，对应后端节点配置
- `node.get_port_offset_by_node_name`：是否按节点名解析端口偏移（会从当前节点名称中提取 `#<offset>` 覆盖 `nodeinfo.port_offset`，如 `HK #9900`）
- `node.get_port_offset_by_node_name` 优先级：开启后且节点名能解析出 `#<offset>` 时，优先使用节点名 offset；否则回退使用 API 返回的 `nodeinfo.port_offset`
- `node.enable_mu_host_rule`：是否启用 MU host 规则（多用户 host 校验）
- `node.mu_regex`：MU host 模板，兼容 `%id/%suffix/%Nm`（如 `%5m%id.%suffix`）
- `node.mu_suffix`：MU host 后缀（如 `example.com`）
- `api.interface`：接口类型，当前使用 `modwebapi`
- `api.url`：后端 API 地址（不带 `/mod_mu` 也可）
- `api.token`：API 鉴权 token
- `api.timeout`：单次 HTTP 请求超时
- `api.retry_max`：单次 API 调用最大重试次数（不含首次）
- `api.retry_backoff`：重试基础退避时间
- `api.retry_max_backoff`：重试退避最大上限
- `sync.update_interval`：同步周期
- `sync.failure_base_wait`：同步失败后的基础退避
- `sync.failure_max_wait`：同步失败退避上限
- `runtime.driver`：运行时驱动，`mock` 或 `ss`
- `runtime.reconcile_workers`：端口收敛并发 worker 数
- `runtime.on_unsupported_cipher`：不支持加密算法时策略，`skip`（跳过用户）或 `fail`（当前同步失败）
- `runtime.dial_timeout`：TCP/UDP 上游目标连接超时
- `runtime.dns_prefer_ipv4`：上游解析时优先 IPv4
- `runtime.dns_resolver`：指定 DNS 服务器（如 `1.1.1.1:53`，为空则用系统解析）
- `runtime.switchrule.enabled`：启用用户过滤规则
- `runtime.switchrule.mode`：过滤模式，`none` 或 `expr`
- `runtime.switchrule.expr`：表达式模式（示例：`is_multi_user==1 && method==chacha20-ietf`）
- `security.auto_block.enabled`：是否启用自动封禁 worker
- `security.auto_block.backend`：封禁后端，`noop`、`ipset` 或 `nft`
- `security.auto_block.sync_interval`：自动封禁收敛周期
- `security.auto_block.protect_node_ip`：是否自动保护节点 IP 不被封禁
- `security.auto_block.static_whitelist`：本地静态白名单 IP
- `log.level`：日志级别（`debug/info/warn/error`）
- `log.format`：日志格式（`console/json`）

Auto block 运维说明见：`docs/auto-block-operations.md`
同步与端口偏移优先级说明见：`docs/runtime-sync-behavior.md`

节点端能力边界说明：当前不支持 Python 旧版 `redirect` 行为（`tcprelay.py` 的重定向链路），按默认直连目标地址处理。

### 2.1 Linux 一键部署（从 GitHub Release）

部署脚本：

`https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/deploy_linux.sh`

脚本会自动：

- 从 GitHub Release 下载对应架构二进制（`node_linux_amd64` 或 `node_linux_arm64`）
- 下载 `checksums.txt` 并进行 SHA256 校验
- 下载 `configs/config.example.yaml` 生成运行配置
- 写入并启动 systemd 服务 `shadowsocks-node`

示例（非交互模式）：

```bash
curl -fsSL https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/deploy_linux.sh | \
  sudo bash -s -- --node-id 1 --api-url https://api.example.com --api-token your_token --version v1.0.0
```

示例（交互模式，手动输入参数）：

```bash
curl -fsSL https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/deploy_linux.sh | sudo bash
```

参数说明：

- `--node-id`：节点 ID（必填，可交互输入）
- `--api-url`：后端 API 地址（必填，可交互输入）
- `--api-token`：后端 API Token（必填，可交互输入，输入时不回显）
- `--version`：指定发布标签（可选，默认 `latest`）

常用依赖安装（缺少 `jq` 时）：

- Debian/Ubuntu：`sudo apt-get update && sudo apt-get install -y jq`
- RHEL/CentOS/Rocky/AlmaLinux：`sudo dnf install -y jq`（旧版系统可用 `sudo yum install -y jq`）

### 2.2 Linux 手动升级（保留旧配置并合并新字段）

升级脚本：

`https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/update_linux.sh`

脚本会自动：

- 从 GitHub Release 下载对应架构二进制（`node_linux_amd64` 或 `node_linux_arm64`）
- 下载 `checksums.txt` 并进行 SHA256 校验
- 下载目标版本 `configs/config.example.yaml`，与本机 `config.yaml` 合并
- 合并规则：旧配置覆盖同名项；新模板新增字段使用默认值
- 备份旧二进制和旧配置后，重启 systemd 服务

依赖：

- `yq`（v4+，用于 YAML 深度合并）

示例（升级到 latest）：

```bash
curl -fsSL https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/update_linux.sh | sudo bash
```

示例（指定版本）：

```bash
curl -fsSL https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/update_linux.sh | \
  sudo bash -s -- --version v1.0.0
```

示例（自定义安装目录/服务名/配置路径）：

```bash
curl -fsSL https://raw.githubusercontent.com/jashok5/shadowsocks-go/main/scripts/update_linux.sh | \
  sudo bash -s -- --install-dir /opt/shadowsocks-node --service-name shadowsocks-node --config /opt/shadowsocks-node/config.yaml
```

参数说明：

- `--version`：指定发布标签（可选，默认 `latest`）
- `--install-dir`：安装目录（可选，默认 `/opt/shadowsocks-node`）
- `--service-name`：systemd 服务名（可选，默认 `shadowsocks-node`）
- `--config`：配置文件路径（可选，默认 `<install-dir>/config.yaml`）

## 3. 如何运行

### 3.1 直接运行

说明：项目入口已调整为根目录 `main.go`，默认主包路径为 `.`。

```bash
go run . --config configs/config.example.yaml
```

或运行编译产物：

```bash
./bin/node --config configs/config.example.yaml
```

### 3.2 运行参数说明

- `--config`
  - 作用：指定配置文件路径
  - 默认值：`configs/config.example.yaml`
  - 示例：`./bin/node --config /etc/shadowsocks-go/config.yaml`

- `--log-level`
  - 作用：覆盖配置文件中的日志级别
  - 可选值：`debug`、`info`、`warn`、`error`
  - 默认值：空（不覆盖，使用配置文件）
  - 示例：`./bin/node --log-level debug`

- `--log-format`
  - 作用：覆盖配置文件中的日志格式
  - 可选值：`console`、`json`
  - 默认值：空（不覆盖，使用配置文件）
  - 示例：`./bin/node --log-format json`

- `--version`
  - 作用：输出版本信息并退出
  - 示例：`./bin/node --version`

版本输出示例：

```text
version=v1.0.0 commit=abc1234 build_time=2026-03-21T09:30:00Z
```

组合示例：

```bash
./bin/node --config /etc/shadowsocks-go/config.yaml --log-level info --log-format json
```

## 3.4 版本号管理

- 当前版本号来源：`version.go` 中的 `version` 变量（发布前手动修改）
- 建议约定：`version.go` 的版本号与 GitHub Release tag 保持一致（如 `v1.0.0`）
- 建议发布流程：
  1) 修改 `version.go` 为目标版本
  2) 手动触发 GitHub Action（`Build and Release`）并填写相同的 `tag`
  3) Action 自动构建、打 tag、创建 Release

### 3.3 灰度运行脚本

```bash
./scripts/phase5_canary_run.sh
./scripts/phase5_observe.sh
./scripts/phase5_canary_stop.sh
```

## 4. 如何优雅退出

程序支持 `SIGINT/SIGTERM` 优雅退出：

- Ctrl+C（前台）
- `kill -TERM <pid>`（后台）

退出时会：

- 停止同步循环
- 关闭 runtime 端口监听
- 等待连接处理协程退出

不建议使用 `kill -9`，会跳过清理流程。

## 5. Linux 服务管理（systemd）

### 5.1 示例 service 文件

保存为 `/etc/systemd/system/shadowsocks-go.service`：

```ini
[Unit]
Description=Shadowsocks Go Node Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/opt/shadowsocks-go
ExecStart=/opt/shadowsocks-go/bin/node --config /opt/shadowsocks-go/configs/config.example.yaml --log-level info --log-format json
Restart=always
RestartSec=3
LimitNOFILE=1048576
KillSignal=SIGTERM
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
```

### 5.2 常用管理命令

```bash
sudo systemctl daemon-reload
sudo systemctl enable shadowsocks-go
sudo systemctl start shadowsocks-go
sudo systemctl status shadowsocks-go
sudo systemctl restart shadowsocks-go
sudo systemctl stop shadowsocks-go
```

查看日志：

```bash
sudo journalctl -u shadowsocks-go -f
```

## 6. 生产建议

- `runtime.driver` 生产环境使用 `ss`或者`ssr`
- 灰度发布时先单机观察，再逐步放量
- 重点关注日志：`sync failed`、`api call retrying`、`runtime pressure`
- 发布与回滚请配合 `docs/phase5-rollout-checklist.md`
