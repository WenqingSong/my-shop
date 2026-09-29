# Task: 身份域隔离（前台用户 vs 后台管理员）

## Goal

确保前台用户与后台管理员是两个互相隔离的身份域，并以真实 HTTP + MySQL + Redis 集成测试锁定该边界：

- 前台登录只能查询 `users`，签发 `type=user` Token，建立 `iam:session:{sid}`；
- 后台登录只能查询 `admins`，签发 `type=admin` Token，建立 `iam:admin:session:{sid}`；
- `type=user` Token 必须被 `AdminAuth` 拒绝，`type=admin` Token 必须被前台 `Auth` 拒绝；
- 前后台 Session Key 双向隔离，互不读取/撤销；管理员登出真实撤销管理员会话且幂等；
- 不存在公开管理员注册入口；
- 架构文档明确「两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API」，后端身份域隔离由独立凭据表 + Token type + 独立 Session Key + 独立认证中间件保证。

## Scope

- 补充真实 HTTP 集成测试（走真实路由 → Response → Auth/AdminAuth → Controller → Logic → MySQL/Redis），覆盖：
  - 前台/后台登录数据源隔离（AC-001、AC-002）；
  - 同名不同密码账号空间隔离（AC-003）；
  - Token 类型双向隔离（AC-004）；
  - Redis Session Key 双向隔离（AC-005）；
  - 管理员登出与幂等（AC-006）；
  - 无公开管理员注册入口（AC-007）。
- 仅当测试证明现有生产行为不符合 AC 时，才做最小必要的生产代码修复。
- 更新架构文档，将身份域边界写清楚（AC-008）。
- 删除或修正「因为前后台是两个前端，所以管理员不可能调用前台接口」一类错误表达（若存在）。

## Out of Scope

- 修改 RBAC 数据模型或权限 code；
- 新增 Refresh Token；
- 修改 JWT 有效期；
- 修改 Session TTL；
- 合并 `users` 与 `admins`；
- 禁止两个表出现相同 username；
- 新增前端页面；
- 重构无关业务模块；
- 新增管理员注册能力；
- 重新设计 401/403 或业务错误码语义（沿用现有 `1002 UNAUTHORIZED→401`、`1003 FORBIDDEN→403`、`2002 INVALID_CREDENTIALS→401` 等约定）。

## Owner Decisions

| 项 | 前台用户身份域 | 后台管理员身份域 |
| --- | --- | --- |
| Credential source | `users` | `admins` |
| Login endpoint | `POST /api/v1/login` | `POST /admin/v1/login` |
| JWT type | `user` | `admin` |
| Middleware | `Auth` | `AdminAuth` |
| Session key | `iam:session:{sid}` | `iam:admin:session:{sid}` |

## Identity Domain Invariants

1. 前台登录只能查询 `users`，不得查询 `admins`。
2. 后台登录只能查询 `admins`，不得查询 `users`。
3. `type=user` Token 必须被 `AdminAuth` 拒绝。
4. `type=admin` Token 必须被前台 `Auth` 拒绝。
5. 前后台客户端分离不是安全控制，后端必须独立验证身份域。
6. `users` 与 `admins` 是两个独立账号空间。
7. 同一个 username 允许同时存在于 `users` 和 `admins`，分别代表两个独立账号。
8. 是否认证成功由目标登录接口对应的数据表决定，而不是由「这个人是不是管理员」决定。
9. 不得新增公开管理员注册接口。
10. 保持现有 401/403 和业务错误码约定，不在本任务中重新设计错误语义。

## Acceptance Criteria

- [ ] AC-001 前台登录数据源隔离：只存在于 `admins` 表、而不存在于 `users` 表的账号凭据调用 `POST /api/v1/login` 必须返回认证失败（401，`2002`），并且不得签发 Token、不得创建前台 Session（`iam:session:*` 无新增）。
- [ ] AC-002 后台登录数据源隔离：只存在于 `users` 表、而不存在于 `admins` 表的账号凭据调用 `POST /admin/v1/login` 必须返回认证失败（401，`2002`），并且不得签发 Token、不得创建管理员 Session（`iam:admin:session:*` 无新增）。
- [ ] AC-003 同名账号空间隔离：分别在 `users` 和 `admins` 创建相同 username、不同密码（`shared/user-password` 与 `shared/admin-password`），必须满足：
  - `/api/v1/login` + `user-password` → 成功，`type=user`；
  - `/api/v1/login` + `admin-password` → 认证失败；
  - `/admin/v1/login` + `admin-password` → 成功，`type=admin`；
  - `/admin/v1/login` + `user-password` → 认证失败。
  以此证明两个登录接口只查询各自的数据表。
- [ ] AC-004 Token 类型隔离：`type=user` Token 访问后台管理接口、`type=admin` Token 访问前台受保护接口，均按现有契约返回 403（`1003`，`type` 不符），且必须通过真实 HTTP 路由与中间件验证，不能只调用认证函数。
- [ ] AC-005 Session Key 隔离：前台登录只创建 `iam:session:{sid}`，后台登录只创建 `iam:admin:session:{sid}`；两者不得互相读取或撤销对方的 Session。
- [ ] AC-006 管理员登出：管理员登录后（1）用 Admin Token 可访问 `/admin/v1/me`；（2）调用 `/admin/v1/logout` 成功；（3）对应管理员 Session 被撤销；（4）原 Token 再访问 `/admin/v1/me` 必须失败；（5）再次调用 `/admin/v1/logout` 按当前设计保持幂等（返回 200/0）；（6）不影响任何前台用户 Session；（7）Redis 查询失败时不得伪装成登出成功（返回 500 `1000`，不吞错误）。
- [ ] AC-007 无公开管理员注册入口：`POST /admin/v1/register` 必须不存在并返回 404。
- [ ] AC-008 文档语义准确：架构文档必须明确「两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API」；后端身份域隔离依靠「独立凭据表 + Token type + 独立 Session Key + 独立认证中间件」。

## Relevant Context

### 已核实事实

- 前台登录只查 `users`：`internal/logic/iam/iam.go` 的 `findUserByUsername` 使用 `g.DB().Model("users")`。
- 后台登录只查 `admins`：`internal/logic/admin/admin.go` 的 `findAdminByUsername` 使用 `g.DB().Model("admins")`。
- JWT `type` 生成：`auth.Generate(ctx, typ, id, sid)`；IAM 登录传 `auth.TypeUser`、Admin 登录传 `auth.TypeAdmin`；`Claims.Type` JSON 键为 `type`，`ParseWithSecret` 原样返回 `Type`。
- `middleware.Auth` 经 `parseUserPrincipal` 要求 `Type==auth.TypeUser`，否则 `403 CodeForbidden`；`middleware.AdminAuth` 经 `parseAdminPrincipal` 要求 `Type==auth.TypeAdmin`，否则 `403 CodeForbidden`。二者「type 不符」均判 403，而非 401。
- Session Key：`auth.CreateSession` → `SessionKey`（`iam:session:{sid}`）；`auth.CreateAdminSession` → `AdminSessionKey`（`iam:admin:session:{sid}`）；`RevokeSession`/`RevokeAdminSession` 各自只操作自己的前缀。
- `/admin/v1/logout`：路由挂 `AdminAuthSignatureOnly`（仅验签 + `type=admin`，不查会话），Controller 取 `AdminPrincipal.Sid`，Logic 调 `auth.RevokeAdminSession`（Lua，key 不存在视为已登出，Redis 错误返回 error 由调用方转 500）。
- 无 `/admin/v1/register`：`internal/cmd/routes_admin.go` 未注册该路径；`routes_test.go` 显式断言 `POST /admin/v1/register` 不存在。
- 错误码约定（`internal/codes/codes.go`）：`1002 UNAUTHORIZED→401`、`1003 FORBIDDEN→403`、`2002 INVALID_CREDENTIALS→401`、`1000 INTERNAL_ERROR→500`、`1004 NOT_FOUND→404`。
- 现有测试覆盖：
  - `internal/controller/iam/iam_test.go`：用户注册/登录/me/logout/session 撤销/幂等/TTL 等（前台域）。
  - `internal/controller/admin/admin_test.go`：管理员登录/me/创建/删除/禁用/RBAC 授权矩阵；`TestAuthorizationMatrix` 已覆盖「`type=user` token 访问 `/admin/v1/me` → 403」。
  - `internal/middleware/auth_test.go`：`TestAdminAuthRejectsUserToken`（user→admin 403）、`TestUserAuthRejectsAdminToken`（admin→user 403），但使用自定义测试路由而非生产 `RegisterFrontendRoutes`/`RegisterAdminRoutes`。
  - 覆盖缺口：① 无 `/admin/v1/logout` 集成测试；② 无「同名 username 两表不同密码」测试；③ 无「登录只创建本域 Session Key、不创建对方域 Key」断言；④ 无「admin token 走真实前台路由 `/api/v1/me` → 403」断言（仅中间件级测试）。
- 测试需串行：`scripts/test.sh` 注释说明共享同一批 MySQL 表，需 `go test -p 1 ./...` 避免跨包数据竞争 flaky。
- `docs/design/iam.md` 当前只描述前台 `users` + `iam:session` 域，未覆盖 `admins` + `iam:admin:session` + `type=admin` + `AdminAuth`；未发现「管理员不可能访问前台」类显式错误表述（AC-008 要求显式补写边界）。

### Assumption

- 现有生产代码已满足绝大多数身份域不变量，本任务预计主要是补集成测试 + 更新文档，生产代码改动应最小；仅当测试暴露不符合 AC 的行为时才修改最小必要生产代码。
- AC-004 的「401 或 403」按已确认契约落实为：`type` 不符 → 403（`1003`）；缺失/非法/过期/会话失效 → 401（`1002`）。这已在现有中间件中实现，测试只需锁定，不重新设计。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实路由与真实 MySQL/Redis，不得只调用 Controller/Logic/辅助函数；断言 HTTP status、JSON 业务 code、Token `type`、Redis Session Key 及登出后 Session 状态、拒绝场景无意外 DB 写入、拒绝场景不创建错误身份域 Session；使用相互独立的测试账号并包含同名不同密码场景。

- AC-001/AC-002 → 仅插入 `admins`（或 `users`）单表账号，分别调用 `/api/v1/login`、`/admin/v1/login`，断言 401/`2002`、无 token、`iam:session:*`/`iam:admin:session:*` 无新增。
- AC-003 → 同名 `shared` 两表各建账号、不同密码，四向登录断言（成功侧 `type` 正确、失败侧 401/`2002`）。
- AC-004 → 用真实 `RegisterFrontendRoutes`/`RegisterAdminRoutes`：user token 打 `/admin/v1/me`、admin token 打 `/api/v1/me`，断言 403/`1003`。
- AC-005 → 登录后分别断言仅存在本域 Session Key、不存在对方域 Key。
- AC-006 → `/admin/v1/logout` 全链路：logout 前 me 200、logout 200、session `revoked=1`、me 401、重复 logout 200、前台用户 session 不受影响、Redis 错误场景 logout 返回 500（不吞错）。
- AC-007 → `POST /admin/v1/register` 返回 404（可用 `TestRouteTable` 已断言 + 真实请求）。
- AC-008 → 检查 `docs/design/iam.md` 或新增后台身份域文档，确认写明「两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API」与四要素边界。
- 通用命令：`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；认证/Session 及相关集成测试另执行 `go test -race -p 1 ./internal/controller/iam/ ./internal/controller/admin/ ./internal/middleware/ ./internal/auth/ ./internal/logic/iam/ ./internal/logic/admin/`。

## Complexity

NORMAL

原因：身份域边界的关键设计（`type` 声明、`iam:admin:session:` 前缀、`AdminAuth`/`RequirePermission` 分层、401/403 判定顺序）已在 `.agent/tasks/admin-identity-rbac/contract.md`（APPROVED）与 `.agent/tasks/iam-v2/contract.md` 中由 Owner 确认，本任务无新架构/协议/数据模型选择。生产代码已实现绝大多数不变量，剩余工作是可观测的测试补齐 + 文档澄清 + 仅在测试暴露缺口时做最小修复，无需要 Analyst 调查或 Owner 决策的开放问题。

## Review Baseline

- Base commit：`fa4a9d9c5e5aad24dc7f9583c6857669bcc4066e`（分支 `feat/rbac`）
- 任务开始时已有修改：无（`git status --short` 为空，working tree clean）
- 重叠修改的区分方式：不适用（无已有未提交修改）

## Initial Route

READY_FOR_CODER
