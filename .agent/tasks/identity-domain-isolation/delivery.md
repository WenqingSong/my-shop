# Delivery Verification

## Milestone and Target
- Milestone：identity-domain-isolation（前台用户 vs 后台管理员身份域隔离）
- Delivery Target：工作区当前未提交状态（HEAD `e8f727eebf770a1404ee6de3eb3e3674d5b96570`）
- Cleaner Review Target：`docs/design/iam.md`（modified，AC-008）+ `internal/cmd/identity_isolation_test.go`（untracked，AC-001~007），无生产代码改动
- Target Match：YES（`git status`/`git diff --stat fa4a9d9` 与 Review Target 一致；无生产代码变更，Owner Mutation 已恢复）

## Environment
- OS：Debian GNU/Linux 12 (bookworm)
- Go：go1.24.1 linux/amd64（`go.mod` 声明 `go 1.23.0`，实际工具链更新，构建/测试均通过）
- GoFrame：v2.10.3（`go.mod`）
- MySQL：8.0（`docker-compose.yml`，容器 `my-shop-mysql` Up healthy，127.0.0.1:3306）
- Redis：7-alpine（容器 `my-shop-redis` Up healthy，127.0.0.1:6379）
- Docker：29.6.2 / Compose v5.3.1
- 配置来源：测试内 `t.Setenv("AUTH_JWT_SECRET", ...)`、`t.Setenv("ADMIN_SUPER_PASSWORD", ...)`；DB/Redis 使用 `manifest/config/config.yaml` 开发默认值（127.0.0.1:3306/6379）；未记录任何 Secret 值
- 测试数据与隔离：集成测试在 `setupIsolationServer` 中清空 `users/admins/roles/permissions/admin_roles/role_permissions` 并 `FlushDB`，再重建超级管理员；各用例使用相互独立账号（含同名不同密码 `shared`），串行执行（`-p 1`），测试自清理

## Verification
| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` 退出码 0 |
| Vet | PASS | `go vet ./...` 退出码 0 |
| 全量测试 | PASS | `go test -p 1 ./...` 全包 ok（含 `internal/cmd` 3.277s） |
| Race Test | PASS | `go test -race -p 1 -count=1 ./internal/cmd/ ./internal/controller/iam/ ./internal/controller/admin/ ./internal/middleware/ ./internal/auth/ ./internal/logic/iam/ ./internal/logic/admin/` 全包 ok，无 race 报错 |
| 身份域集成测试 | PASS | `go test -p 1 -count=1 -run 'TestIdentityDomain|TestAdminLogout' -v ./internal/cmd/` 6 个用例全部 PASS（`-count=1` 防缓存，本次实跑） |
| 环境就绪 | PASS | `docker compose ps`：MySQL 8.0 / Redis 7 均 healthy |

独立代码核对（与 AC 映射，非 Cleaner 结论）：
- 登录数据源隔离：`internal/logic/iam/iam.go:163` `findUserByUsername` 用 `Model("users")`；`internal/logic/admin/admin.go:559` `findAdminByUsername` 用 `Model("admins")`。
- Token type：`internal/middleware/auth.go:167/187` `parseUserPrincipal`/`parseAdminPrincipal` 校验 `claims.Type`，不符 → `CodeForbidden`（403）。
- Session Key：`internal/auth/session.go:52/117` `SessionKey`（`iam:session:`）/`AdminSessionKey`（`iam:admin:session:`）；`RevokeSession`/`RevokeAdminSession` 各自操作自己前缀。
- 管理员登出：`internal/logic/admin/admin.go:163` → `auth.RevokeAdminSession`（Lua），Redis 错误 → `CodeInternalError`（500）。
- 无公开注册：`internal/cmd/routes_admin.go` 未注册 `/admin/v1/register`；`routes_test.go:109` 显式断言该路径不在路由表。

## Acceptance Evidence
| AC | 本次运行结果 | 证据 |
|---|---|---|
| AC-001 | PASS | `TestIdentityDomainLoginSourceIsolation`：仅 `admins` 账号调 `/api/v1/login` → 401/`2002`、`data=null`、`iam:session:*` 无新增 |
| AC-002 | PASS | 同上：仅 `users` 账号调 `/admin/v1/login` → 401/`2002`、`data=null`、`iam:admin:session:*` 无新增 |
| AC-003 | PASS | `TestIdentityDomainSameNameDifferentPasswords`：同名 `shared` 两表不同密码四向登录，成功侧 `type` 正确、失败侧 401/`2002` |
| AC-004 | PASS | `TestIdentityDomainTokenTypeIsolation`：user token 打 `/admin/v1/me`、admin token 打 `/api/v1/me` 均 403/`1003`（真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes`） |
| AC-005 | PASS | `TestIdentityDomainSessionKeyIsolation`：前台仅 `iam:session:{sid}`、后台仅 `iam:admin:session:{sid}`，双向断言无越界 key |
| AC-006 | PASS | `TestAdminLogoutFullChain`（me 200 → logout 200 → revoked=1 → me 401 → 重复 logout 200 → 前台 session 不受影响）+ `TestAdminLogoutRedisErrorReturns500`（Redis WRONGTYPE → 500/`1000` 不吞错） |
| AC-007 | PASS | `TestIdentityDomainLoginSourceIsolation` 中 `POST /admin/v1/register` → 404 + `TestRouteTable` 断言不在路由表 |
| AC-008 | PASS | `docs/design/iam.md` 5.1「身份域隔离」显式写明「两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API」及四要素（独立凭据表 + Token type + 独立 Session Key + 独立认证中间件） |

## Not Executed
无。里程碑要求的核心检查（构建、vet、全量测试、race、身份域集成测试、环境就绪）均已在本轮独立执行并留存证据。

## Remaining Risks
- Go 工具链版本差异：`go.mod` 声明 `go 1.23.0`，验收环境为 `go1.24.1`，构建/测试均通过，属低风险，与 Task/Contract 声明的 Go 1.23.0 略有出入。
- 测试运行于开发环境共享 MySQL/Redis（默认 127.0.0.1），非生产；与本任务 Scope（开发环境）一致。

## Result
PASS
