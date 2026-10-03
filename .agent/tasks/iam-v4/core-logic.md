Owner Verification Status: ACCEPTED

# Owner 核心逻辑验证

本文件整理真正决定安全与一致性的核心机制，供 Owner 通过「代码位置 → 业务后果 → 测试失败」理解因果关系，无需逐行 Review 全部 Diff。

## CL-001：refresh 轮换的并发仅一次 + 过期/reuse 判定

- Owner 需要理解：同一 refresh token 并发提交必须至多一次轮换成功；后到者若因「并发已轮换」→ 判定 reuse 并全量撤销，若因「恰逢过期」→ 仅返回过期（2013）而**不得**连带撤销全账号。若两者混淆，会把一次普通过期误判为「被盗重放」，强制该用户全端重新登录。
- 生产代码：`internal/logic/iam/refresh.go` `Refresh`（47-117 行）、`classifyRefreshRow`（122-133 行）、`handleRefreshNotRotated`（155-165 行）、`rotateRefresh`（170-206 行，事务内条件 `UPDATE ... WHERE token_hash=? AND revoked_at IS NULL AND expires_at > NOW()` + 行锁串行化）。
- 关键测试：`internal/logic/iam/refresh_test.go` `TestHandleRefreshNotRotatedExpired`（78-128 行）与 `TestClassifyRefreshRow`（28-73 行）；`internal/controller/iam/refresh_test.go` `TestConcurrentRefreshSingleRotation`（287-347 行）。
- 基线验证：`go test -p 1 ./internal/logic/iam/ ./internal/controller/iam/` 全绿；`go test -race -p 1 -run 'TestConcurrentRefreshSingleRotation' ./internal/controller/iam/` 通过。
- 可选 Mutation：把 `handleRefreshNotRotated` 改为无条件 `revokeAllByUser` + 返回 `codes.CodeRefreshTokenReuse`（退化回「统一按 reuse」）。
- 预期失败：`TestHandleRefreshNotRotatedExpired` 失败——过期 token 被返回 2014（期望 2013），且有效 token B 被误撤销（`revoked_at IS NULL` 计数不为 1）。
- 恢复确认：还原 `handleRefreshNotRotated` 为重读 + `respondRefreshRow`，再次 `go test -p 1 ./internal/logic/iam/` 全绿。
- Owner 结论：ACCEPTED。

## CL-002：reuse 全量撤销 family + 全部会话

- Owner 需要理解：已轮换 token 被重放（被盗场景）时，MySQL 中该用户**全部** refresh families 的撤销是必须成功的权威安全边界（失败则 reuse 处理失败并返回 500）；access session 仅尝试立即全量撤销（best-effort）。Redis 撤销失败时，已有 access token 最长可能继续有效至 session TTL；但即使如此，因全部 refresh families 已在 MySQL 撤销，任何旧 refresh token 或其后代都无法再换取新的 access token。若只撤销旧 token 而不撤销整个 family，重放后家族内最新后代仍有效，被盗凭据继续可被滥用。
- 生产代码：`internal/logic/iam/refresh.go` `respondRefreshRow` reuse 分支（137-143 行）+ `revokeAllByUser`（211-219 行，MySQL 撤销 family 为权威、Redis 撤销会话 best-effort）+ `revokeAllFamiliesByUser`（273-282 行）。
- 关键测试：`internal/controller/iam/refresh_test.go` `TestRefreshReuseRevokesFamily`（204-244 行）。
- 基线验证：`go test -p 1 -run 'TestRefreshReuseRevokesFamily' ./internal/controller/iam/` 通过。
- 可选 Mutation：把 `revokeAllFamiliesByUser` 改为 `return nil`（不执行 UPDATE）。
- 预期失败：`TestRefreshReuseRevokesFamily` 失败——`activeFamilyCount` 期望 0 实际 1（family 未被撤销）。
- 恢复确认：还原 `revokeAllFamiliesByUser` 的 UPDATE，再次运行该测试全绿。
- Owner 结论：ACCEPTED（含 Redis session 撤销 best-effort 的安全降级边界）。
