# Cleaner Findings

## Review Target

- Task：`.agent/tasks/iam-v2/task.md`（IAM V2：JWT + Redis 有状态会话与登出）
- Contract：`.agent/tasks/iam-v2/contract.md`，状态 `APPROVED`（Owner 于 2026-09-27 确认）
- 任务基线（task.md 记录）：`8e4ff4df6536383a4720630c7b62e03bd0e5689f`（分支 `feat/auth-v2`，working tree clean，无已有未提交修改）
- 当前 HEAD：`8e4ff4df6536383a4720630c7b62e03bd0e5689f`（与基线一致，全部变更在工作区未提交）
- 已跟踪修改（11 个文件）：
  `api/iam/v1/iam.go`、`internal/auth/jwt.go`、`internal/auth/jwt_test.go`、`internal/cmd/cmd.go`、`internal/controller/iam/iam.go`、`internal/controller/iam/iam_test.go`、`internal/logic/iam/iam.go`、`internal/middleware/auth.go`、`internal/middleware/principal.go`、`internal/service/iam.go`、`manifest/config/config.yaml`
- 新增未跟踪文件：
  `internal/auth/session.go`、`internal/auth/session_test.go`、`docs/design/iam.md`、`.agent/tasks/iam-v2/`（协作文件）
- 验证环境：MySQL 8.0（容器 `my-shop-mysql`，healthy）、Redis 7（容器 `my-shop-redis`，healthy）均运行中

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | `TestIAMEndToEnd`：登录成功后 `claims.Sid != ""`，`sessionFields` 断言 `user_id==aliceID`、`revoked=="0"`，`sessionTTL>0`；sid 与 Redis key 一致。 |
| AC-002 | PASS | `TestIAMEndToEnd`：携带有效 token 访问 `/me` 返回 200/0，`data.id==aliceID`、`data.username=="alice"`，id 与 token `sub` 一致。 |
| AC-003 | PASS | `TestLogoutRevokesSessionThenMe401`：logout 200/0 且 data=null；随后 Redis `revoked=="1"`；同 token 再访问 `/me` 返回 401/1002 且 data=null。 |
| AC-004 | PASS | 同测试断言 key 仍在且 `sessionTTL>0`（未 DEL）；`TestSessionTTLExpiryReturns401`（`AUTH_SESSION_TTL=1`）验证 TTL 到期后 key 消失、`/me` 仍 401。 |
| AC-005 | PASS | `TestIAMEndToEnd` badCases（缺失/Basic 格式/坏签名/过期/错误 issuer 均 401/1002）；`TestSessionMissingTokenReturns401`（删除 key）、`TestTokenWithoutSidReturns401`、`TestUnknownSidTokenReturns401` 覆盖验签通过但 session 缺失/无 sid/未知 sid。 |
| AC-006 | PASS | `TestLogoutRequiresToken`（无 token 401）；`TestLogoutIdempotent`（重复登出均 200/0）；`TestLogoutIgnoresClientSid`（请求体 sid 被忽略，只撤销当前 token 的 sid）。 |
| AC-007 | PASS | `TestLogoutDoesNotAffectOtherSessions`：登出 tokenA 后 tokenA 401、tokenB 仍 200 且返回 bob。 |
| AC-008 | PASS | 各测试断言 logout 成功 200/0、已撤销访问 401/1002，body 统一 `{code,message,data}`（`codes.go` 复用 1002，不新增错误码）。 |
| AC-009 | PASS | `session.go` 经 `g.Redis()` 访问（无硬编码地址）、`auth.Secret()` 读密钥；`SessionTTL` 读 `auth.session.ttl`（默认 3600、须 >0），`TestSessionTTLEnvOverride`/`TestSessionTTLRejectsNonPositive` 验证环境变量覆盖与非法值拒绝；`config.yaml` 新增 `auth.session.ttl: 3600`。 |
| AC-010 | PASS | `docs/design/iam.md` 存在，覆盖架构/数据模型/鉴权·登出·撤销流程/安全边界/错误码/配置，与 Contract 及实现一致（Lua 撤销、AuthSignatureOnly、fail-closed、错误码复用等）。 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | 退出码 0，无输出 |
| `go vet ./...` | PASS | 退出码 0，无输出 |
| `gofmt -l`（本次变更文件） | PASS | 无输出（格式合规） |
| `go test ./...` | PASS | 全部 ok；`controller/iam` 5.488s（真实 MySQL+Redis 集成） |
| `go test -race ./internal/controller/iam/ ./internal/auth/ ./internal/logic/iam/` | PASS | 全部 ok；`controller/iam` 27.177s（并发注册/并发登出无 data race） |
| Contract 状态 `APPROVED` | PASS | Owner Decision Record 完整 |
| 实现与 Contract 一致性 | PASS | sid 生成 128-bit hex / JWT 自定义 `sid` 声明 / `iam:session:{sid}` Hash(user_id,revoked) / TTL 对齐 / Auth 两层中间件 / fail-closed 401 / 复用 1002 / logout 幂等 + Lua 撤销 / `/register`、`/health` 未改动 |
| 环境变量覆盖 | PASS | `AUTH_SESSION_TTL` 经 `TestSessionTTLEnvOverride`、`TestSessionTTLExpiryReturns401` 实测生效 |

## Findings

### CLEAN-001：`ValidateSession` 的 `user_id↔sub` 交叉校验分支无测试覆盖

- Severity：P3
- Status：OPEN
- Location：`internal/auth/session.go:97-99`（`fields[sessionFieldUserID] != strconv.FormatInt(userID, 10)` 分支）
- AC / Invariant：Contract INV-001「sid↔user 绑定一致」的纵深防御分支；设计文档 4.2 第 4 步「user_id == sub」
- Trigger：构造一个验签通过、sid 指向某有效会话但 `sub` 为另一用户的 token（测试持有 `testJWTSecret`，可伪造）后访问 `/me`
- Actual：`ValidateSession` 存在 4 个判定（存在 / 未撤销 / user_id 匹配 / 通过），当前仅前两者 + 会话缺失有测试；`user_id` 不匹配分支无任何测试，删除或改为恒真后现有测试仍全部通过
- Expected：应有一条覆盖「session 存在且未撤销，但 `user_id != sub` → 401」的测试，能区分该分支被错误修改
- Impact：低。该分支为纵深防御（正常攻击需先取得签名密钥），缺失不产生直接可利用漏洞，但削弱「测试能识别错误实现」的保障
- Evidence：`iam_test.go` 中 `TestUnknownSidTokenReturns401` 用的是不存在的 sid（命中「存在」分支），`TestSessionMissingTokenReturns401` 是删除 key（同为「存在」分支），均未触达 `user_id` 不匹配分支
- Required Fix Boundary：新增一条测试——用 `signTokenWithSid` 签 `sub` 为 A、`sid` 为 B 的有效会话，断言 `/me` 返回 401/1002；不得改变现有鉴权语义

### CLEAN-002：登录「写 session 失败 → 500」的 fail-closed 分支无测试覆盖

- Severity：P3
- Status：OPEN
- Location：`internal/logic/iam/iam.go:111-113`（`CreateSession` 失败返回 500 分支）
- AC / Invariant：Contract Failure Semantics「登录写 session 失败 → 登录失败 500，不签发 token」
- Trigger：使 `auth.CreateSession` 的 Redis 写入失败（如停 Redis 或注入错误）后调用 `/login`
- Actual：代码分支正确（`CreateSession` 错误 → `Wrap(CodeInternalError)` → 500，且不返回 token），但无测试覆盖；`TestRedisWrongTypeFailClosed` 只覆盖了鉴权侧（`ValidateSession` 错误 → 401），登录侧对称的 fail-closed 分支未验证
- Expected：应有一条测试证明「Redis 写入失败时登录返回 500 且不签发 token」
- Impact：低。分支逻辑简单、与鉴权侧对称，风险低；但 INV-006 的登录侧证据缺失
- Evidence：`iam_test.go` 无任何登录失败（Redis 错误）用例；`TestRedisWrongTypeFailClosed` 仅验证鉴权中间件
- Required Fix Boundary：新增登录侧 fail-closed 测试（可注入 Redis 写错误或停容器），断言登录 500 且无 access_token；不改动登录语义

## Notes

- `CreateSession` 的 `HSet` 与 `Expire` 为两步非原子操作：若 `HSet` 成功而 `Expire` 失败（极窄窗口），会遗留一个无 TTL 的孤儿 session（sid 不返回给任何人，无安全影响，仅存储残留，且与 Contract「孤儿由 TTL 自愈」的表述略有出入）。属极低概率边缘场景，不单独建立 Finding，随 CLEAN-002 的登录侧加固一并考虑即可。
- 其余 INV-001~INV-006 均有对应测试或运行证据支撑（sid 绑定、鉴权门槛、逻辑撤销、会话隔离、sid 来源、fail-closed），无其他开放 Finding。
