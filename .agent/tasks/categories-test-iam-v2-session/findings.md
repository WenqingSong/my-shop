# Cleaner Findings

## Review Target

- 分支：`fix/categories-test-iam-v2-session`
- Base commit：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（Task 基线，working tree clean）
- 当前 HEAD：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（Coder 未提交，仅工作区改动）
- 相关变更（唯一跟踪文件）：`internal/controller/categories/categories_test.go`
  - 移除未使用的 `internal/auth` import；
  - `setupCategoriesServer` 新增 `DELETE FROM users` 与 `g.Redis().FlushDB(ctx)` 幂等清理；
  - 裸签 token 替换为 `registerAndLogin`（`/register` + `/login` → `access_token`）。
- 新增文件：`.agent/tasks/categories-test-iam-v2-session/`（任务产物，非代码）
- 与任务前修改的区分：基线 working tree clean；当前唯一跟踪修改即上述 `categories_test.go`，`git diff` 与上述描述一致，无遗漏新增代码文件。

## Result

BLOCKED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `go test ./internal/controller/categories/...` 编译通过并全部通过（多次运行 ok）。 |
| AC-002 | PASS | token 来自真实 `/register`+`/login` 链路；create/update/delete 携带该 token 均 `assertOK`（无 401 误拒），证明 sid 非空且 session 有效（中间件 fail-closed，任一不满足即 401）。sid/session 的显式断言由 `iam_test.go` `TestIAMEndToEnd` 覆盖；本测试包的显式断言在 Task Verification 中标注为「可在测试内」（可选）。 |
| AC-003 | PASS（仅包内） | categories 测试包连续运行两次均通过（0.747s / 0.676s），无 409 或残留 session 干扰。 |
| AC-004 | PASS | `go test -race -run TestConcurrentCreateSameName ./internal/controller/categories/...` 通过。 |
| AC-005 | FAIL | `go vet ./...` 退出码 0；但 `go test ./...` 与 `go test ./internal/controller/...` 在包并行运行时稳定失败（详见 Verification 与 Findings）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go test ./internal/controller/categories/...` | PASS | `ok ... categories 0.639s` |
| categories 包连跑两次（幂等） | PASS | 两次均 `ok` |
| `go test -race -run TestConcurrentCreateSameName ./internal/controller/categories/...` | PASS | `ok ... categories 3.437s` |
| `go vet ./...` | PASS | 退出码 0 |
| `go test ./internal/controller/iam/...`（单独） | PASS | `ok ... iam 5.510s` |
| `go test -count=1 ./internal/controller/...` ×5 | FAIL（5/5） | `--- FAIL: TestIAMEndToEnd`，`iam_test.go:252: register duplicate alice: status=200 code=0` |
| `go test -count=1 ./...` ×3 | FAIL（3/3） | 交替出现 `TestIAMEndToEnd` 失败与 `TestCategoriesEndToEnd` 失败 |
| `go test -count=2 ./internal/controller/...` | FAIL | `TestIAMEndToEnd`：`/me` 返回 401（会话被 FlushDB 清掉） |

## Findings

No actionable finding for Coder（阻塞原因属 Task 内部冲突，非 Coder 可在此 Scope 内自行修复的实现缺陷）。

### 阻塞事实（Task 内部冲突）

- **AC / 约束冲突项**：AC-005（`go test ./...` 退出码为 0）与 Task Scope 规定的幂等清理策略相互冲突。
- **冲突描述**：Task Scope 要求 categories 测试包在启动前执行全局 `DELETE FROM users` + `g.Redis().FlushDB(ctx)`，并在 Relevant Context 中明确以 `internal/controller/iam/iam_test.go` 的 `setupIAMServer` 为「参照实现范式」。但 `setupIAMServer` 同样对**同一 MySQL `users` 表**与**同一 Redis DB 0**执行 `DELETE FROM users` + `FlushDB`。
- **触发条件**：`go test ./...`（或 `go test ./internal/controller/...`）默认并行运行包，两个测试包在共享 MySQL/Redis 上同时做破坏性清理，互相擦除对方状态：
  1. categories 的 `DELETE FROM users` 清掉 IAM 已注册的 `alice` → IAM `TestIAMEndToEnd` 的「重复注册」断言失败（`register duplicate alice: status=200 code=0`，预期 409/2001）。
  2. 任一包的 `FlushDB` 清掉对方已建立的 session → 对方受保护接口被 401 误拒（`TestCategoriesEndToEnd`/`TestIAMEndToEnd` 的 `/me`、写接口失败）。
- **实际影响**：AC-005 无法稳定满足；`go test ./...` 稳定失败（验证 8+ 次，仅首次偶发通过）。该干扰由本任务新增的 `DELETE FROM users` + `FlushDB` 引入（基线中 categories 包不触碰 `users` 与 Redis，IAM 包不触碰 `categories`，二者无交集）。
- **为何不建 CLEAN Finding**：Coder 严格按 Task Scope 的明文指示实现；修复需要「测试包之间的 MySQL/Redis 隔离（独立 Redis DB / 独立 schema 或表前缀）或测试串行执行」等跨文件、跨包的设计决策，超出「仅修改 `categories_test.go` 一个文件」的 Scope，且需 Owner 拍板，不应要求 Coder 在无决策下自行猜测新方案。

### 需要的决策（Owner）

1. 测试隔离策略：为不同集成测试包分配独立 Redis DB（或独立 MySQL schema/表前缀），使 `DELETE FROM users`/`FlushDB` 不再跨包互相影响；
2. 或明确 `go test ./...` 以 `-p 1`（包串行）方式执行，并同步更新 AC-005；
3. 或调整 Task Scope 的清理方式（例如仅删除本测试用户名、仅清理本包 session key），并同步修订 IAM 包以保持一致。
