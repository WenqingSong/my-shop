# Cleaner Findings

## Review Target

- 任务：`.agent/tasks/identity-domain-isolation/task.md`（identity-domain-isolation）
- 审查模式：initial_review
- 基线 Commit：`fa4a9d9c5e5aad24dc7f9583c6857669bcc4066e`（task.md 声明的 Base commit，为当前 HEAD 的直接祖先，生产代码与文档基线）
- 当前 HEAD：`e8f727eebf770a1404ee6de3eb3e3674d5b96570`（仅新增/修改 `.agent/tasks/*` 任务管理文档，不含任何生产代码或 `docs/design/iam.md` 变更）
- 当前分支：`feat/depart-cmd`（含 `fa4a9d9`，与 task.md 中 `feat/rbac` 表述差异为分支演进，不影响基线定位）
- 本次实现对象（相对基线 `fa4a9d9`）：
  - `docs/design/iam.md`（modified，未提交，AC-008 文档）
  - `internal/cmd/identity_isolation_test.go`（新增/untracked，AC-001~007 集成测试）
- 无生产代码改动；`docs/design/iam.md` 在 `fa4a9d9` 与 `e8f727e` 之间无差异，故文档基线即 `fa4a9d9`，实现变更可完全区分。
- 环境：MySQL 8.0 + Redis 7 由 `docker-compose.yml` 提供，审查时均 healthy。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestIdentityDomainLoginSourceIsolation`：仅 `admins` 账号调 `/api/v1/login` → 401/`2002`、`data=null`、`iam:session:*` 无新增（真实路由 + MySQL/Redis 实测通过） |
| AC-002 | PASS | `TestIdentityDomainLoginSourceIsolation`：仅 `users` 账号调 `/admin/v1/login` → 401/`2002`、`data=null`、`iam:admin:session:*` 无新增 |
| AC-003 | PASS | `TestIdentityDomainSameNameDifferentPasswords`：同名 `shared` 两表不同密码四向登录，成功侧 `type` 正确、失败侧 401/`2002` |
| AC-004 | PASS | `TestIdentityDomainTokenTypeIsolation`：user token 打 `/admin/v1/me`、admin token 打 `/api/v1/me` 均 403/`1003`，走真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes` |
| AC-005 | PASS | `TestIdentityDomainSessionKeyIsolation`：前台登录仅创建 `iam:session:{sid}`、后台登录仅创建 `iam:admin:session:{sid}`，双向断言无越界 key |
| AC-006 | PASS | `TestAdminLogoutFullChain`（me→logout→revoked=1→me 401→重复 logout 200→前台 session 不受影响）+ `TestAdminLogoutRedisErrorReturns500`（Redis 错误 → 500/`1000` 不吞错） |
| AC-007 | PASS | `TestIdentityDomainLoginSourceIsolation` 中 `POST /admin/v1/register` → 404，且 `TestRouteTable` 显式断言该路径不在路由表 |
| AC-008 | PASS | `docs/design/iam.md` 新增 5.1「身份域隔离」，显式写明「两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API」与四要素（独立凭据表 + Token type + 独立 Session Key + 独立认证中间件） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0 |
| `go vet ./...` | PASS | 退出码 0 |
| `go test -p 1 ./...` | PASS | 全包通过，含 `internal/cmd`（3.479s） |
| `go test -race -p 1 ./internal/cmd/ ./internal/controller/iam/ ./internal/controller/admin/ ./internal/middleware/ ./internal/auth/ ./internal/logic/iam/ ./internal/logic/admin/` | PASS | 全包通过，无 race 报错 |
| 环境 | PASS | `docker compose ps`：MySQL 8.0 / Redis 7 均 healthy；`mysqld is alive` |

说明：审查复用了 Coder 未自验过的新测试文件并独立运行（含 `-race`），未依赖 Coder 旧日志；测试命令与断言均未放宽。

## Findings

No actionable findings.

审查要点核对（无 Finding）：
- 登录数据源隔离由 `sIam.Login`→`findUserByUsername`（`Model("users")`）与 `sAdmin.Login`→`findAdminByUsername`（`Model("admins")`）独立保证，测试用「不同密码」构造出可区分错误实现的判别性用例。
- Token type 隔离由 `parseUserPrincipal`/`parseAdminPrincipal` 的 `type` 校验实现（不符 → 403/`1003`），与已确认 Contract 一致。
- Session Key 隔离由 `auth.SessionKey`/`AdminSessionKey` 独立前缀 + `RevokeSession`/`RevokeAdminSession` 各自 Lua 撤销实现；logout 幂等（key 不存在返回 0 视为已登出）且 Redis 错误经 `RevokeAdminSession` 返回 error → 500/`1000`。
- 生产代码相对基线零改动，符合任务「主要补测试 + 文档」的假设与 Scope。
