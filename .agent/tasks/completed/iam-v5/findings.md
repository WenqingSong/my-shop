# Cleaner Findings

## Review Target

- 任务基线（TaskBuilder 记录）：`f0bf42cbf8592215a76173ef75d2eac5078960e2`（分支 `feat/iam-user-status`，任务开始时 working tree clean，与 `origin/develop` 一致）。
- 首次审查实现证据 Commit C1：`35cbe607b390c2e2914de1eedb4010557e076d81`（业务代码 + 测试），结论 `CHANGES_REQUIRED`，开放 `CLEAN-001`（P2）、`CLEAN-002`（P2）。
- 本次复审对象（修复后 implementation Evidence Commit C1）：`60a28775c52754f6d7ad69bf54eeabafd1753d90`（`test(iam-v5): 补充版本不匹配与 fail-closed 回归测试（CLEAN-001/002）`，在 `35cbe60` 基础上追加 3 个回归测试，未改业务代码）。
- Coder metadata Commit C2：`b09bcd2687ecd86d4a078b746eca9a2b2dea1a3e`（`chore(iam-v5): 更新 Review Request target`，仅改 `state.yaml` 的 `review.target`）。
- 审查对象：C1（`60a2877`），非 C2、非当前 working tree。当前 HEAD：`b09bcd2`（== C2）；`git status --short` 为空（working tree clean）。
- 修复 Diff（`57406b6..60a2877`）：仅 `internal/cmd/user_status_test.go`（+75）与 `internal/controller/iam/iam_test.go`（+25）两个测试文件；未改动任何生产代码、迁移、设计文档或 Registry。
- 全局资源：migration `20261001000019`（Registry `origin/develop` 已 `RESERVED`，拥有方 `iam-v5`）；错误码 `2015`（IAM 域 2000-2999 内，无新域）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | 启用用户正确凭据登录成功，access 访问 `/me` 200。`TestUserStatusDisableRevokesAccessRefreshAndReEnable` 断言禁用前 `/me` 200。 |
| AC-002 | PASS | 禁用用户登录 401/1002，`data=null`（无 token、无会话）。同测试断言 `Status==401 && Code==1002 && Data==nil`。 |
| AC-003 | PASS | 禁用用户 refresh 401 且无新 token。同测试断言 refresh 401 且 `Data==nil`（code 为 2012，因禁用事务已撤销 family，符合 Contract 判定顺序；AC-003 仅要求 401 拒绝）。 |
| AC-004 | PASS | 禁用后旧 access token 401/1002。同测试断言旧 access1 访问 `/me` 401。 |
| AC-005 | PASS | 多设备（access1/access2）禁用后全部 401。同测试遍历两个 token 断言 401。 |
| AC-006 | PASS | 重新启用后重新登录成功并访问 `/me` 200。同测试。 |
| AC-007 | PASS | 重新启用后旧 access/旧 refresh 仍 401，不复活。同测试覆盖「会话已撤销」路径；`TestUserStatusVersionMismatchRejectsUnrevokedStaleSession`/`...StaleRefresh` 覆盖「撤销失败残留、版本落后仍 401」路径（本次修复补上，见 CLEAN-001 关闭证据）。 |
| AC-008 | PASS | 普通用户 token 调状态接口 403/1003 且无写入。`TestUserStatusAuthorizationAndNotFound` 断言 403 且查库无写入。 |
| AC-009 | PASS | 无权限管理员 403、仅 `user:read` 管理员 PUT 403、持有 `user:status` 管理员可操作、超管放行。同测试覆盖。 |
| AC-010 | PASS | 重复禁用幂等（不递增版本、不重复审计）；并发重复禁用经 `SELECT ... FOR UPDATE` 仅一次实际迁移。`TestUserStatusConcurrentDisableIdempotent` 断言 epoch 仍 1、审计仍 1。 |
| AC-011 | PASS | 目标不存在 404/2015 且无写入。同测试断言 404/2015。 |
| AC-012 | PASS | `AdminAuth`+`RequirePermission` 双层保护；id 来自 URL path（`in:"path"`）；响应仅 `{id,status}`，无 `password_hash`/token/Redis 凭据。 |
| AC-013 | PASS | 状态落库 + 审计字段完整（操作者/目标/前后状态/原因/结果）。`TestUserStatusAuditFields` 逐字段断言。 |
| AC-014 | PASS | fail-closed：DB 侧 `TestAuthDBQueryFailClosed`（RENAME users 触发 1146 → 401/1002、`data=null`）；Redis 侧 `TestRedisWrongTypeFailClosed`（session key 置 string 触发 WRONGTYPE → 401/1002）。二者均真实触发 err 分支并断言不放行。 |
| AC-015 | PASS | `go test -p 1 ./...` 全量通过；`type=user`/`type=admin` 隔离回归正常。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出。 |
| `go vet ./...` | PASS | 退出码 0，无输出。 |
| `go test -p 1 ./...` | PASS | 全量串行通过（MySQL/Redis 容器 `my-shop-mysql`/`my-shop-redis` 均 healthy）。 |
| `go test ./internal/cmd -run TestUserStatusVersionMismatch -count=1` | PASS | 2 个用例通过（access 侧 401/1002、refresh 侧 401/1002）。 |
| `go test ./internal/controller/iam -run 'TestAuthDBQueryFailClosed\|TestRedisWrongTypeFailClosed' -count=1` | PASS | 2 个用例通过，日志确认真实触发 1146 / WRONGTYPE。 |
| Mutation（隔离 worktree，可逆，已还原删除） | PASS | ① 移除 `Auth` 的 `user.AuthEpoch != sessionEpoch` 与 `Refresh` 的 `user.AuthEpoch != row.AuthEpoch` → 两个版本不匹配测试均从预期 401 退化为 200 而 FAIL（`user_status_test.go:370`、`408`）；② 将 `findUserAuth` 的 err 分支改为 fail-open（合成启用用户）→ `TestAuthDBQueryFailClosed` FAIL（期望 401 得 500）。证明测试可区分错误实现，非「看起来通过」。 |
| 三边一致性 Registry↔Contract↔实现 | PASS | `origin/develop` `.agent/registry/migrations.md` 含 `20261001000019 \| user_status \| iam-v5 \| RESERVED`；Contract 声明 `20261001000019`；实现 `20261001000019_user_status.up.sql`。错误码 `2015` 在 IAM 域 2000-2999（ACTIVE），无新域。 |
| 四者一致 Task↔Contract↔Design↔实现 | PASS | `docs/design/iam.md`（auth_epoch/状态/刷新版本绑定/2015）、`rbac.md`（`user:read`/`user:status`）、`migration.md`（`20261001000019`）均已同步；`error-codes.md` 为域级文档（其 §8 明确域内具体码由模块 Design 记录），无新域故无需更新。 |

## Findings

### CLEAN-001：auth_epoch 版本不匹配的拒绝作用缺少测试保护

- Severity：P2
- Status：CLOSED
- Location：`internal/middleware/auth.go:56`、`internal/logic/iam/refresh.go:74`
- AC / Invariant：INV-001、INV-003、AC-004、AC-007
- 关闭证据：Coder 在 `60a2877` 新增 `TestUserStatusVersionMismatchRejectsUnrevokedStaleSession`（access 侧）与 `TestUserStatusVersionMismatchRejectsUnrevokedStaleRefresh`（refresh 侧），分别构造「禁用时 Redis/refresh family 撤销失败、会话/refresh 残留未撤销」→ 重新启用后断言旧凭证仍 401/1002（仅版本比对兜底）。Mutation 验证：删除版本比对后两测试均从 401 退化为 200 而 FAIL，证明能区分错误实现。
- Required Fix Boundary 已满足：测试覆盖「未撤销但版本落后」场景，且删除版本比对后必失败。

### CLEAN-002：认证链 fail-closed（AC-014）缺少测试

- Severity：P2
- Status：CLOSED
- Location：`internal/middleware/auth.go:50-54`（`findUserAuth` err 分支）
- AC / Invariant：AC-014
- 关闭证据：`TestAuthDBQueryFailClosed`（`60a2877` 新增，RENAME users 触发 1146 → 401/1002 且 `data=null`）覆盖 DB 侧；`TestRedisWrongTypeFailClosed`（既存）覆盖 Redis 侧 `ValidateSession` err 分支。Mutation 验证：将 err 分支改为 fail-open 后 `TestAuthDBQueryFailClosed` FAIL（期望 401 得 500）。
- Required Fix Boundary 已满足：前台 Auth 的 DB 与 Redis 查询失败均 fail-closed 401 且不放行，有测试保护。

No actionable findings remain（P0/P1/P2 全部关闭，无 P3）。
