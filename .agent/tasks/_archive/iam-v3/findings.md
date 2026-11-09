# Cleaner Findings

## Review Target

- 分支：`feat/iam-v3`
- 基线 Commit：`3e61dc339adf6d3b731b4f69272392c26fd79cdf`（Task Review Baseline 一致，任务开始时 working tree clean）
- 审查对象 Commit：`029141327d4d4246697062dba0f8fcdcc9bb1d64`（`feat(iam): 新增多设备会话管理与主动撤销`），当前 HEAD，working tree clean
- 审查 Diff：`3e61dc3..0291413`（15 个文件，+981/-21）
  - 已跟踪改动：`api/iam/v1/iam.go`、`docs/design/iam.md`、`internal/auth/session.go`、`internal/cmd/routes_frontend.go`、`internal/cmd/routes_test.go`、`internal/codes/codes.go`、`internal/controller/iam/iam.go`、`internal/controller/iam/iam_test.go`、`internal/logic/iam/iam.go`、`internal/service/iam.go`
  - 新增文件：`.agent/tasks/iam-v3/{task,contract,findings,core-logic,delivery}.md`
- 关键配置/迁移版本：无新增迁移（`token_version` 决策为不引入，会话元数据仅落 Redis）；`docs/design/iam.md` 更新为 IAM V3（Design Impact=UPDATE）
- 说明：审查过程中观察到 HEAD 由 `3e61dc3`（含未提交修改）切换为 `0291413`（clean），即 Coder 在审查期间提交了工作。本次审查以稳定 Commit `0291413` 为准，其内容与最初观察到的未提交 diff 一致，未因提交动作发生实质变化。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestListSessionsMultiDevice`：同一用户 `registerAndLogin` + `loginOnly` 产生两个不同 sid（`sidA1 != sidA2` 断言），各自 session 均存在未撤销 |
| AC-002 | PASS | `TestListSessionsMultiDevice`：列表返回全部会话、`login_at > 0`、`current` 唯一且等于当前 sid；换 B token 列表不含 alice 会话 |
| AC-003 | PASS | `TestRevokeSessionById`：`DELETE /sessions/{A1}` 后 `revoked=1`、key/TTL 保留、A1 token 401、A2 与 B 仍 200 |
| AC-004 | PASS | `TestRevokeOthers`：revoke-others 后 A2/A3 token 401、A1 仍 200 且 `revoked=0`、A2/A3 `revoked=1` |
| AC-005 | PASS | `TestRevokeAll`：revoke-all 后 A1/A2 均 401，两个 sid 均 `revoked=1` |
| AC-006 | PASS | `TestRevokeNonOwnedSession`：A 撤销 B 的 sid 与非法 sid 均 404/2011，B 无变化（`revoked=0`）、B 仍 200 |
| AC-007 | PASS | `TestRevokeSessionIdempotent`：重复撤销本人会话两次均 200/0 |
| AC-008 | PASS | `TestListSessionsRedisErrorReturns500`（索引 WRONGTYPE → 500/1000，不 fail-open）；`TestRedisWrongTypeFailClosed`（session WRONGTYPE → 鉴权 401）。注意：数据操作失败路径的“底层错误日志”仅部分落地（见 CLEAN-002） |
| AC-009 | PASS | `go test -p 1 ./...` 全绿；`TestIAMEndToEnd`/`TestLogoutRevokesSessionThenMe401`/`TestLogoutDoesNotAffectOtherSessions` 验证 `/login`/`/logout`/`/me` 语义未回归 |
| AC-010 | PASS | `docs/design/iam.md` 新增 §3.5 会话索引、§4.4 列表/撤销、错误码 2011、失败语义，与 Contract 及实现一致（四者核对见下） |
| AC-011 | PASS | Contract `Owner Decision Record`（2026-10-04）确认不引入 `token_version` 与 `jti`；实现无对应列/声明；设计文档同步“无 token_version、无标准 jti” |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| go build ./... | PASS | 无输出，退出码 0 |
| go vet ./... | PASS | 无输出，退出码 0 |
| gofmt -l（改动文件） | PASS | 输出为空 |
| go test -p 1 ./... | PASS | 全部包 `ok`（含 controller/iam、middleware、migrations 等） |
| go test -race ./internal/controller/iam/... | PASS | `ok ... 50.775s` |
| go test ./... （并行，非官方命令） | 部分 FAIL | `internal/middleware`、`internal/controller/sku` 因 `admins` 表不存在报错——这是共享 MySQL/Redis 下跨包并行导致的既有测试隔离问题，官方入口 `scripts/test.sh` 明确要求 `-p 1` 串行；与本次 IAM V3 改动无关（单独/串行运行均通过） |
| Design 四者一致性 | PASS | Task ↔ Contract（APPROVED）↔ docs/design/iam.md ↔ 实现：身份域仅前台、4 个接口、ZSET 索引 `iam:user:{id}:sessions`、错误码 2011、撤销逻辑标记、不引入 token_version/jti，均一致 |

## Findings

### CLEAN-001：撤销指定会话后未从用户索引移除该 sid

- Severity：P3
- Status：OPEN
- Location：`internal/logic/iam/iam.go` `RevokeSessionByID`（经 `auth.RevokeSession` 仅 `HSET revoked=1`，无 `ZREM`）
- AC / Invariant：INV-006（索引为枚举优化、Hash 为权威）；与 `revoke-others`/`revoke-all` 的索引清理行为一致性
- Trigger：`DELETE /sessions/{sid}` 撤销本人某个会话后，该 sid 仍保留在 `iam:user:{userID}:sessions` 中
- Actual：单会话撤销不清理索引，已撤销成员滞留索引直至 TTL 自然过期；`ListSessions` 读取时以 `info.Revoked` 正确过滤，功能无错误
- Expected：撤销后将该 sid 从索引移除（与 `revoke-others`/`revoke-all` 的 Lua 脚本一致），避免索引残留积累
- Impact：仅索引轻微膨胀（TTL 自愈）、与批量撤销清理行为不一致，无正确性/安全影响
- Evidence：`revokeOthersScript`/`revokeAllScript` 均 `ZREM`/`DEL` 索引，而 `RevokeSessionByID → auth.RevokeSession`（`EVAL revokeScript`）不触碰索引；`TestRevokeSessionById` 断言 `revoked=1` 与列表只剩 A2，但未断言索引成员被清理（列表通过 `info.Revoked` 过滤，掩盖了该残留）
- Required Fix Boundary：撤销指定会话成功后，从该用户索引移除目标 sid（可复用 `RemoveSessionFromIndex`）；不得改变撤销语义（仍逻辑标记 `revoked=1`、保留 key 与 TTL），不得影响越权防护与幂等

### CLEAN-002：会话列表/撤销数据操作失败未显式记录底层错误日志

- Severity：P3
- Status：OPEN
- Location：`internal/logic/iam/iam.go` `ListSessions`/`RevokeSessionByID`/`RevokeOtherSessions`/`RevokeAllSessions`（均 `codes.Wrap(CodeInternalError, ...)` 返回但未 `glog` 记录）
- AC / Invariant：AC-008 / INV-005（“Redis 失败返回 500 + 日志”）
- Trigger：Redis 不可用/类型错误时调用列表或撤销接口
- Actual：正确返回 500/1000、错误未吞掉（`gerror.WrapCode` 保留原始错误链）、不 fail-open；但逻辑层未显式记录底层 Redis 错误，`Response` 中间件也只写响应不落日志，与鉴权中间件 `Auth` 显式 `glog.Errorf` 的行为不一致
- Expected：数据操作阶段 Redis 失败在返回 500 的同时，服务端记录底层错误日志（与 `Auth` 中间件一致）
- Impact：Redis 故障时缺少可定位的底层错误日志，可观测性/排障略弱；正确性与安全边界不受影响
- Evidence：`Auth` 中间件对 `ValidateSession` 错误 `glog.Errorf`；逻辑层四个新方法仅 `Wrap` 返回；`TestListSessionsRedisErrorReturns500` 只断言 500/1000 与 `data=null`，未断言日志
- Required Fix Boundary：在列表/撤销数据操作失败的 error 返回路径补一条 `glog.Errorf`（含底层错误链）；不得改变返回错误码/HTTP 状态、不得 fail-open、不得泄漏内部信息到响应

（无 P0/P1/P2 Finding。上述两项 P3 由 Owner 决定是否处理，不阻塞 CLEAN。）
