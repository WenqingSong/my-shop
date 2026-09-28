# Task: 修复 categories 测试包以适配 IAM V2 有状态会话

## Goal

让 `internal/controller/categories` 测试包在 IAM V2「有状态会话」语义下能编译并通过，使分类写接口（create/update/delete）的集成测试在新鉴权下可正常验证，且不回归分类 V1 已验收的只读/写接口行为。

## Scope

- 仅修改 `internal/controller/categories/categories_test.go` 一个文件。
- 将 `setupCategoriesServer` 中裸签 token 的旧调用替换为通过 `/register` + `/login` 登录链路获取的、背后有真实 Redis session 的 token。
- 增加幂等清理：启动测试服务器前 `DELETE FROM users`（清用户表）与 `g.Redis().FlushDB(ctx)`（清 Redis），保证 register/login 可重复执行。

## Out of Scope

- 不修改 `internal/auth/*`、`internal/middleware/*` 或任何 IAM 实现代码。
- 不修改 categories 业务逻辑、错误码、路由与既有公开接口行为。
- 不新增/删除分类业务测试用例（仅适配既有测试以通过新鉴权）。
- 不调整 IAM V2 已确认的 session 语义、撤销语义、错误码或 `/register`、`/login` 的响应结构。

## Acceptance Criteria

- [ ] AC-001 在项目根目录执行 `go test ./internal/controller/categories/...`，测试包编译通过（不再因 `auth.GenerateWithSecret` 旧 2 参调用导致编译失败），且该包内全部测试通过。
- [ ] AC-002 测试通过 `/register` + `/login` 链路获得 token：该 token 携带非空 sid，且 Redis 中存在与之匹配、未撤销、`user_id == sub` 的 session；受保护写接口（create/update/delete）在携带该 token 时返回业务成功，不再因「无 session」被 401 误拒。
- [ ] AC-003 测试 setup 具备幂等性：每次运行前清空 users 表并 FlushDB Redis，使得 register/login 可重复执行；重复运行测试不会因重复用户名 409 或残留 session 干扰结果。
- [ ] AC-004 `TestConcurrentCreateSameName` 通过：并发同名创建仅一个成功、其余返回 3002 冲突，不存在因 401 导致的误失败。
- [ ] AC-005 在项目根目录执行 `go test ./...` 与 `go vet ./...`，两者退出码均为 0。

## Relevant Context

已核实事实：

- `auth.GenerateWithSecret` 新签名为 `GenerateWithSecret(secret []byte, userID int64, sid string) (string, error)`（`internal/auth/jwt.go`），旧 2 参调用已无法编译。
- `middleware.Auth` 在验签 + 校验 exp 后调用 `auth.ValidateSession(ctx, sid, userID)`；session 不存在、已撤销（`revoked=1`）或 Redis 查询失败均 fail-closed 返回 401（code 1002）。因此「补参裸签、无真实 session」的 token 会被 401 拒绝。
- `/login` 成功响应的 `data` 为 JSON 对象，字段含 `access_token`（`internal/logic/iam/iam.go` 返回 `AccessToken`）、`token_type`、`expires_in`；`apiResult.Data` 是 `json.RawMessage`，需反序列化后取 `access_token`。
- `internal/controller/iam/iam_test.go` 的 `setupIAMServer` 已演示幂等清理（`DELETE FROM users` + `g.Redis().FlushDB(ctx)`）与 register/login 链路，可作为参照实现范式。
- `categories_test.go` 中 `auth` 包当前仅用于 `GenerateWithSecret` 一处；替换为登录链路后需同步移除未使用的 `auth` import，否则会因 `imported and not used` 编译失败。
- 无 token 访问写接口的既有预期为 401/1002（测试中 AC-004/006/009 已断言），本次不改变该行为。
- Git 基线：HEAD `ed7da0c5198c944ee1d05bfbbe26bc479ca49190`，分支 `fix/categories-test-iam-v2-session`，working tree clean。

Assumption：

- 采用固定测试用户名（如 `cat_admin`）+ 固定密码，配合 `DELETE FROM users` 清理保证可重复执行；具体用户名/密码由 Coder 自定，不影响可观察行为。
- register/login 的成功判定沿用 IAM 测试约定（HTTP 200 且 code=0）。

## Verification

- AC-001 → `go test ./internal/controller/categories/...`，需 MySQL 与 Redis 容器就绪（`docker compose up -d` 后）。
- AC-002 → 观察测试输出：受保护写接口断言通过，无 401 误失败；可在测试内通过 `auth.ParseWithSecret` 解码 token 确认 `sid` 非空。
- AC-003 → 连续运行该测试包两次，均通过且无 409/残留干扰。
- AC-004 → `go test -run TestConcurrentCreateSameName ./internal/controller/categories/...` 通过。
- AC-005 → 项目根目录执行 `go test ./...`、`go vet ./...`，退出码为 0。

## Complexity

NORMAL

原因：Owner 已明确修复方案（走 register/login 链路 + 幂等清理），无架构、数据模型或协议层面的未决选择；IAM V2 的 session 语义、错误码与响应结构已由 iam-v2 Contract 确认，本任务只是让既有测试适配该语义。改动限定在单个测试文件，业务规则无歧义，无需 Analyst 调查或 Owner 再决策。

## Review Baseline

- Base commit：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（分支 `fix/categories-test-iam-v2-session`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 重叠修改的区分方式：不适用；本任务唯一产出为对 `internal/controller/categories/categories_test.go` 的修改。

## Initial Route

READY_FOR_CODER
