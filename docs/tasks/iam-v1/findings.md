# Cleaner Findings

## Review

- Review Target:
  - Task: docs/tasks/iam-v1/task.md
  - Contract: docs/tasks/iam-v1/contract.md（状态 `APPROVED`，Owner 于 2026-09-27 确认）
  - 任务基线（task.md 记录）：`43567bf0ba3de796170e8942caad16a77d3f39bb`（分支 `feat/auth`，working tree clean）
  - 当前 HEAD：`ba7560f`（`chore: 清理已删除的任务草稿文件`），其下无已提交业务代码变更
  - 已跟踪修改（工作区）：`go.mod`、`go.sum`、`internal/boot/boot.go`、`internal/cmd/cmd.go`、`internal/logic/logic.go`、`manifest/config/config.yaml`
  - 新增未跟踪文件：`api/iam/v1/iam.go`、`internal/auth/jwt.go`、`internal/auth/jwt_test.go`、`internal/codes/codes.go`、`internal/controller/iam/iam.go`、`internal/controller/iam/iam_test.go`、`internal/logic/iam/iam.go`、`internal/logic/iam/iam_test.go`、`internal/middleware/auth.go`、`internal/middleware/principal.go`、`internal/middleware/response.go`、`internal/service/iam.go`
  - 验证环境：MySQL 8.0（容器 `my-shop-mysql`，healthy）、Redis 7（容器 `my-shop-redis`，healthy）均运行中
- Task: docs/tasks/iam-v1/task.md
- Result: CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | `internal/controller/iam/iam_test.go` `TestIAMEndToEnd`：合法注册返回 200/0，`data.id>0`；查库 `password_hash` 长度 60（bcrypt）且 != 明文；同密码注册 alice/bob 哈希不同（含盐）。真实 MySQL 集成验证通过。 |
| AC-002 | PASS | 同测试：username 过短/非法字符集、password 过短/过长均返回 400 code=1001；逐项 `userCount`=0 确认未写入。 |
| AC-003 | PASS | 同测试：重复注册 alice 返回 409 code=2001；查库 `password_hash` 不变（未覆盖）。`TestConcurrentRegisterSameUsername` 额外验证并发同名仅 1 成功、9 冲突。 |
| AC-004 | PASS | 同测试：正确凭据登录返回 access_token；解码声明 `sub==user.id`、`iss==surgecart`、含 `iat`/`exp` 且 `exp-iat==3600s`；`token_type=="Bearer"`、`expires_in==3600`。 |
| AC-005 | PASS | 同测试：错误密码与不存在用户返回相同 401 code=2002 与相同 message/status（防枚举），且不签发 token。 |
| AC-006 | PASS | 同测试：有效 token 访问 `/me` 返回 200/0，`data.id==aliceID` 且 `data.username=="alice"`，id 与 token `sub` 一致。 |
| AC-007 | PASS | 同测试：缺失/`Basic` 格式/坏签名/过期/错误 issuer 五类 token 均 401 code=1002，且 `data==nil`（无用户数据）。 |
| AC-008 | PASS | `internal/cmd/cmd.go`：`/register`、`/login` 直接挂在公开 group；`/me` 挂在 `middleware.Auth` 保护的嵌套 group。集成测试中无 token 访问 `/register`/`/login` 成功、无 token 访问 `/me` 401，边界验证通过。 |
| AC-009 | PASS | `internal/auth/jwt.go` `Secret()` 从 `g.Cfg().GetEffective("auth.jwt.secret")` 读取；`TestSecretFromEnv` 验证 `AUTH_JWT_SECRET` 环境变量覆盖生效；`TestSecretRejectsTooShort` 验证过短密钥被拒。全量检索无密钥硬编码于 Go 源码（dev 默认值仅在 `config.yaml`，经 Contract Owner Decision #5 明确授权）。 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | 退出码 0，无输出 |
| `go vet ./...` | PASS | 退出码 0，无输出 |
| `go test ./...` | PASS | 全部 ok；`controller/iam` 0.776s（含真实 MySQL 集成） |
| `go test -race ./internal/controller/iam/ ./internal/auth/ ./internal/logic/iam/` | PASS | 全部 ok；`controller/iam` 7.387s（并发注册无 data race） |
| `contract.md` 存在且 `APPROVED` | PASS | 状态 `APPROVED`，Owner Decision Record 完整 |
| 实现与 Contract 一致性 | PASS | HS256+`auth.jwt.secret`(≥32B) / bcrypt cost=10 / 启动幂等 DDL / 统一登录错误+假哈希 / 错误码体系(0,1000-1005,2001-2002) / 中间件 401+Principal，均与 Selected Design 一致 |

## Findings

### CLEAN-001：`/health` 成功响应的 message 由空串变为 "OK"

- Severity: P3
- Status: OPEN
- File: internal/middleware/response.go:23-27（及 internal/cmd/cmd.go 将 `ghttp.MiddlewareHandlerResponse` 替换为 `middleware.Response`）
- Location: `Response` 成功分支写入 `Message: codes.Message(codes.CodeOK)`（"OK"）
- Acceptance Criteria / Invariant: Contract Forbidden Changes「不改动 /health 行为与响应格式」
- Trigger: 任何对 `/health` 的成功请求
- Actual Behavior: 响应 `{code:0,message:"OK",data:{...}}`；替换前 GoFrame 内置 `ghttp.MiddlewareHandlerResponse` 成功时 message 为空串
- Expected Behavior: 无（本行为更贴近 README 文档 `{"code":0,"message":"OK",...}`，且 Contract 已授权「新增项目级响应中间件（保持 {code,message,data}）」）
- Impact: 极低。仅 `message` 字段从 "" 变为 "OK"；`/health` 的 200 状态、`status`/`time` 字段与 `{code,message,data}` 结构不变，`/health` 逻辑永不返回错误，故错误码→HTTP 映射改动不触发。
- Evidence: `git diff internal/cmd/cmd.go` 显示中间件替换；`internal/middleware/response.go:22-37`；`internal/controller/health/health_test.go` 通过
- Required Fix Boundary: 无需修复（非阻塞）。若 Owner 要求 `/health` 严格逐字节不变，可对 health 路由单独保留旧中间件；否则维持现状即可。

### CLEAN-002：INV-004「耗时相近」未被测试保护

- Severity: P3
- Status: OPEN
- File: internal/logic/iam/iam.go:92-100
- Location: `Login` 用户不存在分支的 `dummyPasswordHash` 假 bcrypt 比对
- Acceptance Criteria / Invariant: Contract INV-004「登录失败不泄露用户名是否存在（…耗时相近）」
- Trigger: 移除/注释 `_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, ...)` 后运行 `TestIAMEndToEnd`
- Actual Behavior: 现有测试仅断言不存在用户与错误密码的 `code/message/status` 相同（`iam_test.go:227-235`），未断言响应耗时；移除假比对后测试仍通过
- Expected Behavior: 耗时对齐属于 INV-004 的一部分，理想情况下应有测试或说明其保护方式
- Impact: 低。耗时对齐是实现层面防枚举的纵深措施，且时序断言易抖动；Contract Verification Requirements 对 INV-004 也只要求 code/message/status 一致，故不构成阻塞缺口。
- Evidence: `internal/controller/iam/iam_test.go:227-235` 无耗时断言；`internal/logic/iam/iam.go:92-95`
- Required Fix Boundary: 无需强制修复。可选：补充一次宽松的耗时相近断言（如不存在用户与错误密码耗时同量级），或由 Owner 明确接受「仅 code/message/status 一致」为当前验收口径。

## Notes

- 其余各项 INV-001~INV-005 均有对应测试或运行证据支撑（并发唯一性、bcrypt 哈希、`/me` 鉴权、防枚举 code/message/status、密钥注入与启动校验），无额外开放 Finding。
