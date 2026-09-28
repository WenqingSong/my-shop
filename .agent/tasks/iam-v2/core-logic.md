# Core Logic Review

Cleaner 审查通过（`CLEAN`）。以下为 Owner 应亲自理解并验证的两条核心机制。

## CL-001：鉴权 fail-closed（Redis 故障时拒绝放行）

- Owner 需要理解：受保护接口在「验签 + 校验 exp」之后，还必须确认 Redis 会话「存在、未撤销且 `user_id == sub`」；一旦 Redis 查询失败（不可用/超时/类型错误），必须拒绝放行（返回 401），否则已登出的 token 可能在 Redis 故障窗口重新获得访问权限。这是本任务最重要的安全边界（INV-006）。
- 生产代码：`internal/middleware/auth.go` `Auth`（第 19-39 行，fail-closed 分支 25-31 行）+ `internal/auth/session.go` `ValidateSession`（第 84-101 行）
- 关键测试：`internal/controller/iam/iam_test.go` `TestRedisWrongTypeFailClosed`（第 557-573 行）
- 基线验证：`go test ./internal/controller/iam/ -run TestRedisWrongTypeFailClosed -v`，预期 PASS（Redis 类型错误 → `/me` 返回 401/1002）
- 可选 Mutation：将 `internal/middleware/auth.go` 中 `if err != nil { ... r.SetError(...); return }` 的 fail-closed 分支改为放行（`r.SetCtxVar(principalKey, p); r.Middleware.Next()`）
- 预期失败：`TestRedisWrongTypeFailClosed` 必须失败——Redis 查询报错时被放行，`/me` 返回 200 而非 401
- 恢复确认：恢复原 fail-closed 代码后再次运行上述命令，测试重新 PASS

## CL-002：登出用逻辑撤销（revoked 标记 + 保留 TTL + 幂等 + sid 来自 token）

- Owner 需要理解：登出不物理删除 session，而是在 Redis 中把 `revoked` 置 1（不可逆 0→1），key 保留至 TTL 自然过期；重复登出幂等成功；撤销目标 sid 只能来自验签后的 token/Principal，不接受客户端请求体指定（INV-003 / INV-004 / INV-005 / AC-006）。
- 生产代码：`internal/auth/session.go` `RevokeSession`（Lua 原子撤销，第 103-110 行，脚本 29-35 行）+ `internal/logic/iam/iam.go` `Logout`（第 128-133 行）+ `internal/controller/iam/iam.go` `Logout`（第 40-49 行，从 `Principal.Sid` 取 sid）
- 关键测试：`internal/controller/iam/iam_test.go` `TestLogoutRevokesSessionThenMe401`（第 410-438 行）+ `TestLogoutIgnoresClientSid`（第 533-554 行）
- 基线验证：`go test ./internal/controller/iam/ -run 'TestLogoutRevokesSessionThenMe401|TestLogoutIgnoresClientSid' -v`，预期 PASS
- 可选 Mutation：将 `revokeScript` 改为 `redis.call('DEL', KEYS[1])`（物理删除而非置 `revoked=1`）
- 预期失败：`TestLogoutRevokesSessionThenMe401` 中「session key 仍存在且 `TTL>0`」的断言必须失败（`sessionFields` 变空、`sessionTTL` 返回负数）
- 恢复确认：恢复 Lua 脚本为逻辑标记后再次运行上述命令，测试重新 PASS
