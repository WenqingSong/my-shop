# Cleaner Findings

## Review Target

- 任务基线（TaskBuilder 记录）：`f0bf42cbf8592215a76173ef75d2eac5078960e2`（分支 `feat/iam-user-status`，任务开始时 working tree clean，与 `origin/develop` 一致）。
- 实现证据 Commit C1：`35cbe607b390c2e2914de1eedb4010557e076d81`（`feat(iam-v5): 实现用户账号状态管理…`）。
- Coder metadata Commit C2：`7fd7579733b55a365057c8f7685396a7ad2a6e19`（`chore(iam-v5): 提交 Review Request`，仅改 `state.yaml` 的 `review.status/target`）。
- 审查对象：C1（业务代码 + 测试），非 C2、非当前 working tree。
- 当前 HEAD：`7fd7579`（== C2）；`git status --short` 为空（working tree clean）。
- 相关变更（C1 的 16 个文件）：`api/iam/v1/iam.go`、`internal/auth/session.go`、`internal/boot/boot_migration_test.go`、`internal/boot/seed.go`、`internal/cmd/flashsale_test.go`、`internal/cmd/routes_admin.go`、`internal/cmd/user_status_test.go`、`internal/codes/codes.go`、`internal/controller/iam/iam.go`、`internal/logic/iam/iam.go`、`internal/logic/iam/refresh.go`、`internal/logic/iam/user_status.go`、`internal/middleware/auth.go`、`internal/migrations/migrations_test.go`、`internal/migrations/sql/20261001000019_user_status.up.sql`、`internal/service/iam.go`；另有设计文档 `docs/design/{iam,rbac,migration}.md`（commit `dc9923f`）。
- 全局资源：migration `20261001000019`（Registry 已 RESERVED，拥有方 `iam-v5`）；错误码 `2015`（IAM 域 2000-2999 内，无新域）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 启用用户正确凭据登录成功，access token 访问 `/me` 200。`TestUserStatusDisableRevokesAccessRefreshAndReEnable` 断言禁用前 `/me` 200；既有 `controller/iam` 登录测试覆盖登录主流程。 |
| AC-002 | PASS | 禁用用户登录 401/1002，`data=null`（无 token、无会话）。同测试断言 `login.Status==401 && Code==1002 && Data==nil`。 |
| AC-003 | PASS | 禁用用户 refresh 401 且无新 token。同测试断言 refresh 401 且 `Data==nil`。注：禁用事务已撤销 refresh family，故 code 为 2012（INVALID）而非 1002，符合 Contract「`respondRefreshRow` 先于 status/版本校验」的判定顺序；AC-003 仅要求 401 拒绝，满足。 |
| AC-004 | PASS | 禁用后旧 access token 401/1002。同测试断言旧 access1 访问 `/me` 401。 |
| AC-005 | PASS | 多设备（access1/access2）禁用后全部 401。同测试遍历两个 token 断言 401。 |
| AC-006 | PASS | 重新启用后重新登录成功并访问 `/me` 200。同测试。 |
| AC-007 | PASS | 重新启用后旧 access/旧 refresh 仍 401，不复活。同测试断言旧 token 401。注：该 401 来自「会话已被 Redis 撤销」而非「版本不匹配」，「Redis 撤销失败时的版本兜底」分支无测试（见 CLEAN-001）。 |
| AC-008 | PASS | 普通用户 token 调状态接口 403/1003 且无写入。`TestUserStatusAuthorizationAndNotFound` 断言 403 且查库无写入。 |
| AC-009 | PASS | 无权限管理员 403、仅 `user:read` 管理员 PUT 403、持有 `user:status` 管理员可操作、超管放行。同测试覆盖 bob/carol/dave/超管。 |
| AC-010 | PASS | 重复禁用幂等（不递增版本、不重复审计）。同测试断言 epoch 仍 1、audit 仍 1。重复启用未显式测，但三态判定对称（`status==目标 → no-op`），风险低。 |
| AC-011 | PASS | 目标不存在 404/2015 且无写入。同测试断言 404/2015。 |
| AC-012 | PASS | `AdminAuth`+`RequirePermission` 双层保护；id 来自 URL path（`in:"path"`）；响应仅 `{id,status}`，无 `password_hash`/token/Redis 凭据。 |
| AC-013 | PASS | 状态落库 + 审计字段完整（操作者/目标/前后状态/原因/结果）。`TestUserStatusAuditFields` 逐字段断言。 |
| AC-014 | NOT_VERIFIED | 实现 fail-closed 正确（`Auth` 中间件 `ValidateSession`/`findUserAuth` 的 err 分支均 401），但无故障注入测试证明，见 CLEAN-002。 |
| AC-015 | PASS | `go test -p 1 ./...` 全量通过；`type=user`/`type=admin` 隔离回归正常（`identity_isolation_test`、`middleware/auth_test` 等）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出。 |
| `go vet ./...` | PASS | 退出码 0，无输出。 |
| `go test -p 1 ./...` | PASS | 全量串行通过（含 `internal/cmd`、`internal/controller/iam`、`internal/migrations` 等，MySQL/Redis 容器就绪）。 |
| `go test ./internal/cmd -run TestUserStatus -count=1` | PASS | 4 个用例全通过。 |
| 三边一致性 Registry↔Contract↔实现 | PASS | `origin/develop` 的 `.agent/registry/migrations.md` 含 `20261001000019 | user_status | iam-v5 | RESERVED`；Contract 声明 `20261001000019`；实现 `20261001000019_user_status.up.sql`。错误码 `2015` 在 IAM 域 2000-2999（ACTIVE），无需新域。 |
| `scripts/check-registry.sh` | 见备注 | 报「20261001000019 未在 Registry 登记」：脚本读取本地 feature branch 的 Registry（不含 develop 上由 Analyst 直接 push 的 RESERVED 条目），非真实漂移。已核对 `origin/develop` 确有该条 RESERVED。 |

## Findings

### CLEAN-001：auth_epoch 版本不匹配的拒绝作用缺少测试保护

- Severity：P2
- Status：OPEN
- Location：`internal/middleware/auth.go`（`Auth` 中间件 `user.AuthEpoch != sessionEpoch` 判定）、`internal/logic/iam/refresh.go`（`Refresh` 中 `user.AuthEpoch != row.AuthEpoch` 判定）
- AC / Invariant：INV-001、INV-003、AC-004、AC-007；Contract Verification「INV-003（重点）→ 禁用时模拟 Redis 撤销失败，随后重新启用：旧 access/旧 refresh token 仍 401（版本落后）」
- Trigger：禁用用户但 Redis 会话撤销失败（或撤销后会话残留未撤销）；或登录/刷新与禁用并发导致新凭证绑定旧版本
- Actual：`TestUserStatusDisableRevokesAccessRefreshAndReEnable` 中，禁用时 `RevokeAllSessions` 成功撤销了全部会话，重新启用后旧 access 的 401 完全来自「会话已撤销」，而非「版本不匹配」。删除 `Auth` 的 `user.AuthEpoch != sessionEpoch` 或 `Refresh` 的 `user.AuthEpoch != row.AuthEpoch` 判定后，现有测试仍全部通过（401 仍由 session 撤销/status 拒绝产生）。
- Expected：应有集成测试在「会话未被撤销（模拟 Redis 撤销失败）但版本落后」场景下，断言旧 access/旧 refresh 仍 401（仅版本比对兜底）；该测试在删除版本比对后必须失败。
- Impact：`auth_epoch` 机制的核心价值（重新启用不复活旧凭证、禁用即时失效的最后防线）无测试保护；未来破坏版本比对不会被发现，可能导致被禁用用户的旧凭证在 Redis 撤销失败时复活。
- Evidence：`user_status_test.go` 全流程中 access1/access2 在禁用时被 `RevokeAllSessions` 撤销；测试仅断言 epoch 值（=1、不变），未断言版本不匹配的拒绝行为。
- Required Fix Boundary：补充集成测试覆盖「禁用时模拟 Redis 会话撤销失败（如撤销后将 session 的 `revoked` 置回 0，或构造 `RevokeAllSessions` 失败）→ 重新启用 → 旧 access/旧 refresh 仍 401（版本落后）」，且该测试在删除版本比对后必须失败。

### CLEAN-002：认证链 fail-closed（AC-014）缺少测试

- Severity：P2
- Status：OPEN
- Location：`internal/middleware/auth.go`（`Auth` 中间件 `ValidateSession` / `findUserAuth` 的 err 分支）
- AC / Invariant：AC-014；Contract「AC-014 → 模拟 users.status/auth_epoch 查询失败或 Redis 失败，断言 fail-closed（拒绝放行）」
- Trigger：`Auth` 中间件查询 Redis 会话失败（如 session key 被置为非 Hash 类型触发 WRONGTYPE）或查询 `users.status/auth_epoch` 失败
- Actual：代码路径正确（err → 401 fail-closed，底层错误仅日志），但无任何集成测试模拟该故障，无法证明「故障时绝不放行禁用用户」。
- Expected：补充故障注入测试，模拟 Redis 会话查询失败，断言受保护接口 401 且不放行。
- Impact：AC-014 无运行证据，fail-closed 行为若被错误修改（如 err 时放行）不会被测试发现。
- Evidence：全仓库 `*_test.go` 无前台 Auth fail-closed 用例；`TestAdminLogoutRedisErrorReturns500` 仅覆盖管理员登出 Redis 错误，未覆盖前台 `Auth`。
- Required Fix Boundary：补充前台 `Auth` 的 Redis 查询失败 fail-closed 测试（可复用 session key 置 string 触发 `HGetAll` WRONGTYPE 的既有技巧），断言 401 且不放行。
