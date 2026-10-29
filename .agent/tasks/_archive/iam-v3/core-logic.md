# Owner 核心逻辑验证卡

仅 `CLEAN` 后填写。选择真正决定权限/安全/一致性的核心机制，普通 DTO、字段搬运与样板 CRUD 不列入。

## CL-001：撤销指定会话的越权防护（归属校验）

- Owner 需要理解：`DELETE /sessions/{sid}` 在写入前必须确认目标 sid 属于当前用户；若跳过该校验，用户 A 可伪造 sid 撤销用户 B 的会话（越权下线），这是本任务最核心的安全不变量（INV-002）。校验失败（非本人/不存在）统一 404/2011 且不产生任何写入、不泄露会话存在性。
- 生产代码：
  - `internal/logic/iam/iam.go` `RevokeSessionByID`（写前 `auth.SessionBelongsTo` 归属校验，非本人 → `codes.CodeSessionNotFound`）
  - `internal/auth/session.go` `SessionBelongsTo`（读 `iam:session:{sid}` 的 `user_id` 与传入 userID 比对）
- 关键测试：`internal/controller/iam/iam_test.go` `TestRevokeNonOwnedSession`
- 基线验证：`go test -p 1 ./internal/controller/iam/... -run TestRevokeNonOwnedSession -v`（预期 PASS：A 撤销 B 的 sid 与非法 sid 均 404/2011，B 的 `revoked` 仍为 "0"）
- 可选 Mutation：将 `RevokeSessionByID` 中的 `if !belongs { return codes.New(codes.CodeSessionNotFound) }` 改为 `if !belongs { return nil }`（即归属校验失败仍放行写入），临时破坏归属校验。
- 预期失败：`TestRevokeNonOwnedSession` 必须失败——A 撤销 B 的 sid 会返回 200 而非 404，且 `sessionFields(sidB)["revoked"]` 会从 "0" 变为 "1"（B 会话被越权撤销）。
- 恢复确认：还原该判断后重跑同一测试，恢复 PASS。

## CL-002：会话列表的归属隔离与 fail-closed

- Owner 需要理解：`GET /sessions` 以索引枚举后必须以 Session Hash（权威事实）逐条过滤——只返回 `user_id == 当前用户` 且未撤销、Hash 存在的会话；Redis 枚举/读取失败必须返回 500 而非伪造空列表（fail-open 会误导客户端“无会话”）。这是 INV-001 + INV-005 的组合不变量。
- 生产代码：
  - `internal/logic/iam/iam.go` `ListSessions`（逐条 `GetSession` 校验 `info == nil`（stale）、`info.Revoked`、`info.UserID != userID` 三重过滤；任一 Redis 读失败 `Wrap(CodeInternalError)`)
  - `internal/auth/session.go` `ListSessionIndex` / `GetSession`
- 关键测试：`internal/controller/iam/iam_test.go` `TestListSessionsMultiDevice`（归属隔离）、`TestListSessionsFiltersStaleMember`（stale 过滤）、`TestListSessionsRedisErrorReturns500`（fail-closed）
- 基线验证：`go test -p 1 ./internal/controller/iam/... -run 'TestListSessions' -v`（预期三例均 PASS）
- 可选 Mutation：将 `ListSessions` 中 `if info.UserID != userID { continue }` 改为无条件追加（即删除归属过滤）。
- 预期失败：`TestListSessionsMultiDevice` 必须失败——用 B 的 token 调用列表会返回包含 alice 的 sid（`bob list must not contain alice sessions` 断言失败）。
- 恢复确认：还原过滤后重跑 `go test -p 1 ./internal/controller/iam/... -run 'TestListSessions'`，恢复 PASS。
