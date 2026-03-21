# SSR 全量重构计划（Python -> Go）

基于原版 Python 代码实读，Go 侧按“行为一致优先”推进 SSR 全量迁移。

## 目标

- 完整支持 SSR：`method + protocol + obfs`
- 与 Python 版本在 TCP/UDP、多用户、审计、限速、黑名单等行为一致
- 支持动态热更新（用户、规则、端口）

## Python 对齐结论（摘要）

- TCP 上行顺序：`obfs.server_decode -> decrypt -> protocol.server_post_decrypt`
- TCP 下行顺序：`protocol.server_pre_encrypt -> encrypt -> obfs.server_encode`
- UDP 顺序：`decrypt_all -> protocol.server_udp_post_decrypt`（上行）/ `protocol.server_udp_pre_encrypt -> encrypt_all`（下行）
- `protocol` 与 `obfs` 都走 plugin 接口，但位置不同；`protocol_param` 与 `obfs_param` 分别进入各自插件
- 审计规则：TCP 仅首包阶段检测；UDP 仅新 client_pair 检测

## Go 迁移分层

- `internal/ssr/pipeline`：协议处理链与上下文
- `internal/ssr/plugin/protocol`：auth/auth_chain/verify/plain
- `internal/ssr/plugin/obfs`：tls/http/plain
- `internal/ssr/cipher`：method 注册、EVP_BytesToKey、stream/aead 兼容
- `internal/ssr/relay/tcp|udp`：stage 状态机与转发

## 分阶段执行

1. 第 1 批：框架层（接口、上下文、plain 协议链、method 注册）
2. 第 2 批：cipher 兼容（先 `chacha20-ietf`、`aes-*-cfb/gcm`）
3. 第 3 批：protocol（`auth_aes128_md5`、`auth_sha1*`）
4. 第 4 批：obfs（`tls1.2_ticket_auth`、`http_simple`）
5. 第 5 批：TCP/UDP stage 行为对齐、MU 与审计对齐
6. 第 6 批：联调灰度与回归矩阵

## 当前进度

- 已完成 Python SSR 行为拆解
- 已开始 Go SSR 框架代码落地（见 `internal/ssr/*`）
