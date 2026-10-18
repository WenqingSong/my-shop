# Owner Core Logic

本任务的核心机制集中在 Nginx 反向代理的两个安全/正确性边界。其余内容（编排、健康检查、错误页、Request ID 日志字段）为支撑性配置，不单独列出。

## CL-001：客户端 IP 防伪造（单跳信任边界）

- Owner 需要理解：后端 GoFrame `GetClientIp()` 取 `X-Forwarded-For` 的**第一项**，若 Nginx 用 `$proxy_add_x_forwarded_for`（把客户端值拼在最前）或透传客户端伪造头，则任何客户端都能伪造 `X-Forwarded-For: 1.2.3.4` 污染 IAM 会话记录的设备 IP。正确做法是 Nginx **覆盖式**重写为 `$remote_addr`（Nginx 真实对端 IP），丢弃客户端伪造值。
- 生产代码：`nginx/nginx.conf`（`location /` 内 `proxy_set_header X-Forwarded-For $remote_addr;`、`proxy_set_header X-Real-IP $remote_addr;`）
- 关键测试：`scripts/test-nginx.sh` 第 30~37 行（断言覆盖式重写 + 断言 `$proxy_add_x_forwarded_for` 不得出现）
- 基线验证：`bash scripts/test-nginx.sh`，预期通过
- 可选 Mutation：把 `proxy_set_header X-Forwarded-For $remote_addr;` 改为 `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`
- 预期失败：`test-nginx.sh` 第 35~36 行 `grep -qF '$proxy_add_x_forwarded_for'` 命中 → `fail` 退出 1
- 恢复确认：改回 `$remote_addr` 后再次 `bash scripts/test-nginx.sh`，预期通过

## CL-002：非幂等请求零自动重试（避免代理引入重复写入）

- Owner 需要理解：秒杀下单/下单/支付等 POST/PUT/DELETE 请求，若代理在上游连接失败/超时时自动重试，会向上游发起第二次请求，可能造成重复业务写入。`proxy_next_upstream off` 保证代理层**任何方法都不重试**；「恰好执行一次」由业务层幂等机制（`idempotency_key`、一人一单、条件状态更新 `RowsAffected`）保证，二者职责不同、不得混淆。
- 生产代码：`nginx/nginx.conf`（`location /` 内 `proxy_next_upstream off;`）
- 关键测试：`scripts/test-nginx.sh` 第 39~44 行（断言 `proxy_next_upstream off` + 断言 `non_idempotent` 不得出现）
- 基线验证：`bash scripts/test-nginx.sh`，预期通过
- 可选 Mutation：把 `proxy_next_upstream off;` 改为 `proxy_next_upstream error timeout;`
- 预期失败：`test-nginx.sh` 第 40 行 `grep -qF 'proxy_next_upstream off;'` 未命中 → `fail` 退出 1
- 恢复确认：改回 `proxy_next_upstream off;` 后再次 `bash scripts/test-nginx.sh`，预期通过
