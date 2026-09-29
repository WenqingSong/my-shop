# Core Logic Review

Cleaner 已确认 CLEAN。以下为 Owner 应亲自验证的核心机制，均为决定身份域隔离安全的真实代码路径。

## CL-001：登录数据源隔离（前台查 `users`、后台查 `admins`）

- Owner 需要理解：登录是否成功由「目标登录接口对应的凭据表」决定，而不是由「这个人是谁」决定。若前台登录错误查询 `admins` 表，后台管理员即可用管理员凭据登录前台拿到 `type=user` Token，身份域隔离被击穿。
- 生产代码：
  - `internal/logic/iam/iam.go` `findUserByUsername`（`g.DB().Model("users")`，仅查 `users`）
  - `internal/logic/admin/admin.go` `findAdminByUsername`（`g.DB().Model("admins")`，仅查 `admins`）
- 关键测试：
  - `internal/cmd/identity_isolation_test.go` `TestIdentityDomainLoginSourceIsolation`（AC-001/AC-002）
  - `internal/cmd/identity_isolation_test.go` `TestIdentityDomainSameNameDifferentPasswords`（AC-003，同名不同密码四向登录）
- 基线验证：`go test -p 1 -run 'TestIdentityDomain' ./internal/cmd/` → 全部 PASS
- 可选 Mutation：把 `iam.go` 中 `findUserByUsername` 的 `g.DB().Model("users")` 临时改为 `g.DB().Model("admins")`
- 预期失败：`TestIdentityDomainLoginSourceIsolation`（admin-only 账号会错误登录成功，期望 401/2002）与 `TestIdentityDomainSameNameDifferentPasswords`（前台用 user-password 登录将失败，期望 200）必须失败
- 恢复确认：改回 `users`，再次运行 `go test -p 1 -run 'TestIdentityDomain' ./internal/cmd/` 全绿

## CL-002：Token type 双向隔离（`type=user` 被 `AdminAuth` 拒绝、`type=admin` 被前台 `Auth` 拒绝）

- Owner 需要理解：Token 声明 `type` 是身份域标识，前后台认证中间件各自只接受自己域的 `type`，且「type 不符」判定为 403（`1003`）而非 401。若移除该校验，跨域 Token 会因会话前缀不同最终落到 401，破坏 401/403 语义契约，且削弱身份域边界表达。
- 生产代码：
  - `internal/middleware/auth.go` `parseUserPrincipal`（`claims.Type != auth.TypeUser` → `CodeForbidden`）
  - `internal/middleware/auth.go` `parseAdminPrincipal`（`claims.Type != auth.TypeAdmin` → `CodeForbidden`）
  - `internal/auth/jwt.go`（`Claims.Type`，`Generate` 传 `auth.TypeUser`/`auth.TypeAdmin`）
- 关键测试：
  - `internal/cmd/identity_isolation_test.go` `TestIdentityDomainTokenTypeIsolation`（AC-004，真实路由）
  - `internal/middleware/auth_test.go` `TestUserAuthRejectsAdminToken` / `TestAdminAuthRejectsUserToken`（中间件级）
- 基线验证：`go test -p 1 -run 'TestIdentityDomainTokenTypeIsolation' ./internal/cmd/` 与 `go test -p 1 -run 'Test.*Rejects.*Token' ./internal/middleware/` → 全部 PASS
- 可选 Mutation：删除 `parseAdminPrincipal` 中的 `if claims.Type != auth.TypeAdmin { r.SetError(codes.New(codes.CodeForbidden)); return nil, false }` 整段
- 预期失败：`TestIdentityDomainTokenTypeIsolation` 中「user token 打 `/admin/v1/me`」不再得到 403/1003（会因 admin 会话不存在落到 401），断言失败
- 恢复确认：还原 type 校验，再次运行 `go test -p 1 -run 'TestIdentityDomainTokenTypeIsolation' ./internal/cmd/` 全绿
