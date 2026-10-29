# Owner Core Logic 验证卡

本文件承载值得 Owner 理解和亲自验证的核心机制（验证卡），不承担 Owner 决策状态权威。Owner 决策状态（`state.owner.status`/`state.owner.review_target`）由 OwnerGate 在 Owner 明确决定后持久化。

本任务（IAM V5 用户账号状态管理）的核心是「禁用即时失效 + 重新启用不复活旧凭证」。下面两个机制直接决定安全与数据一致性。

## CL-001：auth_epoch 版本比对兜底（禁用即时失效 + 重新启用不复活旧凭证）

- Owner 需要理解：用户每次被「启用→禁用」时 `users.auth_epoch` 单调 +1，登录/刷新签发的凭证（Redis 会话 Hash 与 refresh_tokens 行）会绑定签发那一刻的 `auth_epoch`。鉴权与刷新时都要求「当前 `users.auth_epoch` == 凭证绑定的 `auth_epoch`」。因此：① 禁用后旧凭证即使 Redis 撤销失败残留、JWT 也未过期，仍因版本落后而被 401 拒绝（不依赖 JWT 过期、不只在登录时检查）；② 重新启用不递减版本、不重签，旧凭证永久失效，必须重新登录。若删掉这处版本比对，被禁用用户的旧凭证在 Redis 撤销失败时会复活，属于安全漏洞。
- 生产代码：
  - `internal/logic/iam/user_status.go:92-99`（禁用实际迁移 `auth_epoch = auth_epoch + 1`；启用不递增）
  - `internal/middleware/auth.go:50-58`（`Auth` 每请求查 `users.status/auth_epoch`，要求 `status==1` 且 `session.auth_epoch == users.auth_epoch`）
  - `internal/logic/iam/refresh.go:67-76`（`Refresh` 要求 `status==1` 且 `row.auth_epoch == users.auth_epoch`，且新凭证继承 `row.auth_epoch`，绝不重读最新版本签发）
- 关键测试：
  - `TestUserStatusVersionMismatchRejectsUnrevokedStaleSession`（`internal/cmd/user_status_test.go`）
  - `TestUserStatusVersionMismatchRejectsUnrevokedStaleRefresh`（`internal/cmd/user_status_test.go`）
- 基线验证：`go test ./internal/cmd -run TestUserStatusVersionMismatch -count=1` → 2 个用例 PASS（旧 access/旧 refresh 均 401/1002）。
- 可选 Mutation：删除 `Auth` 的 `user.AuthEpoch != sessionEpoch` 与 `Refresh` 的 `user.AuthEpoch != row.AuthEpoch` 判定。
- 预期失败：上述两个测试退化为 `status=200 code=0` 而 FAIL（`user_status_test.go:370`、`:408`），证明版本比对是拒绝旧凭证的唯一屏障。
- 恢复确认：还原代码后重跑 `go test ./internal/cmd -run TestUserStatusVersionMismatch -count=1` 恢复 PASS。

## CL-002：状态更新事务原子性 + SELECT ... FOR UPDATE 三态判定

- Owner 需要理解：禁用/启用必须在一个 MySQL 事务内原子完成「更新 `users.status`（禁用时 `auth_epoch+1`）+ 写审计 +（禁用）撤销该用户全部 refresh family」，并先用 `SELECT ... FOR UPDATE` 锁目标行再判定三态：无行 → 404 无写入；`status == 目标` → 幂等 no-op（不写审计、不递增版本、不撤销）；否则实际迁移。这保证并发禁用/启用串行化、审计与迁移一一对应，且「先查再写」的 TOCTOU 不会导致重复递增版本或重复审计。Redis 会话撤销在事务提交后 best-effort（失败仅日志，由 CL-001 版本比对兜底），不破坏事务原子性。
- 生产代码：`internal/logic/iam/user_status.go:50-151`（`UpdateUserStatus`：`LockUpdate()` + 三态判定 + 事务内 family 撤销 + 事务后 `RevokeAllSessions` best-effort）。
- 关键测试：
  - `TestUserStatusConcurrentDisableIdempotent`（`internal/cmd/user_status_test.go`）：8 并发禁用，断言 `auth_epoch=1`、审计仅 1 条。
  - `TestUserStatusDisableRevokesAccessRefreshAndReEnable`：重复禁用断言 epoch 不重复递增、审计不重复写入。
- 基线验证：`go test ./internal/cmd -run 'TestUserStatusConcurrentDisableIdempotent|TestUserStatusDisableRevokesAccessRefreshAndReEnable' -count=1` → PASS。
- 可选 Mutation：将「已一致 → 幂等 no-op」的判定去掉（无条件更新 + 写审计），或去掉 `LockUpdate()`。
- 预期失败：并发/重复禁用断言失败（`auth_epoch` 递增多次或审计条数 > 1）。
- 恢复确认：还原代码后重跑上述测试恢复 PASS。
