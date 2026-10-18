# Task: 后台身份与 RBAC（管理员 + 角色权限）

## Goal

新增独立后台管理员身份与 RBAC 权限体系：管理员可登录、查看当前信息、被创建/禁用/删除；具备角色与权限的 CRUD 及分配能力；所有管理接口经授权中间件保护，严格区分 401（未认证）与 403（已认证但无权限）；初始化一个不可被任何操作影响的超级管理员；禁止公开注册管理员。

## Scope

- 数据表：`admins`、`roles`、`permissions`、`admin_roles`、`role_permissions`，沿用 `boot.Bootstrap` 幂等建表。
- 管理员接口：`/admin/login`、`/admin/me`、`/admin/logout`（无 `/admin/register`）。
- 管理员管理：创建、禁用、删除。
- 角色 CRUD、权限 CRUD、角色↔权限分配/移除、管理员↔角色分配/移除。
- 超级管理员初始化（`is_super=1`，配置/环境变量幂等 seed）。
- 401/403 语义区分与安全验收矩阵。

## Out of Scope

- 前台用户 RBAC（前台用户无角色）。
- 商品表与商品关联（遗留缺口，另立任务）。
- 权限缓存（每请求查库，缓存后续优化）。
- 登录限流、验证码、账号锁定、审计日志。
- 角色继承/层级、多租户、Token 刷新。

## Acceptance Criteria

### 管理员身份

- [ ] AC-001：服务启动时按配置幂等创建超级管理员（`is_super=1`，存在则跳过且不覆盖密码）；生产未配置密码时显式失败，不静默用弱默认值建号。
- [ ] AC-002：管理员用正确凭据调用 `/admin/login` 成功，签发 `typ=admin` 的 Token；前台用户凭据无法登录管理员接口，管理员凭据无法登录前台 `/login`。
- [ ] AC-003：`/admin/me` 返回当前管理员 `id/username/is_super/所属角色`。
- [ ] AC-004：有权限管理员可创建新管理员；不存在任何公开注册管理员的入口（包含尝试访问 `/admin/register` 类路径）。
- [ ] AC-005：管理员被禁用后，其已有会话立即失效（再访问受保护接口返回 401，重新登录也被拒）。
- [ ] AC-006：有权限管理员可删除普通管理员。
- [ ] AC-007：超级管理员不可被任何操作禁用、删除、降级或改角色（返回明确拒绝，且不产生写入）。
- [ ] AC-008：普通管理员不能禁用/删除/降级自己，避免锁死（返回明确拒绝）。

### RBAC

- [ ] AC-009：有权限管理员可对角色进行创建、查看、更新、删除。
- [ ] AC-010：有权限管理员可对权限进行创建、查看、更新、删除。
- [ ] AC-011：有权限管理员可为角色分配/移除权限。
- [ ] AC-012：有权限管理员可为管理员分配/移除角色。

### 授权语义（安全验收矩阵）

- [ ] AC-013：无 Token 访问管理接口 → 401。
- [ ] AC-014：前台用户合法 Token 访问管理接口 → 403。
- [ ] AC-015：已认证管理员但无所需权限 → 403。
- [ ] AC-016：持有对应权限的管理员 → 成功。
- [ ] AC-017：超级管理员 → 无论是否显式持有权限均成功（按规则放行）。
- [ ] AC-018：以上所有被拒绝的请求，均不产生任何数据库写入。
- [ ] AC-019：请求体中的目标角色/管理员 ID 不能作为调用者权限证明（无权管理员不能通过请求体提权自己或越权操作他人）。

## Relevant Context

### 已核实事实

- 技术栈：GoFrame v2.10.3 / Go 1.23.0；模块 `cnb.cool/go-cloud-devops/my-shop`；分层为 `api/controller/service/logic`（Register 模式）。
- 现有 `internal/auth`：
  - `jwt.go`：HS256，`Claims` 目前仅有自定义字段 `Sid`（`RegisteredClaims` 含 `sub/iss/iat/exp`）；`Issuer="surgecart"`、`ExpiresIn=3600`；密钥读 `auth.jwt.secret`（`AUTH_JWT_SECRET`），最短 32 字节。
  - `session.go`：会话 key 前缀 `iam:session:{sid}`，Hash 字段 `user_id/revoked`，TTL 默认 3600；提供 `CreateSession/ValidateSession/RevokeSession`。
- 现有 `internal/middleware`：
  - `Auth`/`AuthSignatureOnly`：解析 Bearer、验签、校验会话，注入 `Principal{UserID,Sid}`。
  - `Response`：统一 `{code,message,data}`，并按业务错误码映射 HTTP 状态。
- 现有错误码：`1002 CodeUnauthorized→401`、`1003 CodeForbidden→403` 已存在；`2001 CodeUsernameExists→409`、`2002 CodeInvalidCredentials→401`；IAM 段 2000-2999 已开，Category 段 3000-3999 已开。
- `boot.Bootstrap` 幂等建表 `users`、`categories`（`ensureTables`），是新增表的挂载点。
- 路由在 `internal/cmd/cmd.go`：分类写操作（`POST/PUT/DELETE /categories...`）当前挂在 `middleware.Auth` 下，本任务需迁到 `AdminAuth + RequirePermission(...)` 保护。
- `AGENTS.md` 第 11 节明确 RBAC 为已知遗留缺口，须显式设计。
- 配置 `manifest/config/config.yaml` 支持环境变量覆盖（键 `.`→`_` 大写，如 `AUTH_JWT_SECRET`）。

### Owner 已确认的决定（Analyst 直接采纳为 contract 依据）

- D1 身份分离：新建 `admins` 表，`users` 不动。
- D2 超级管理员用 `is_super` 标志。
- D3 权限清单按当前需要创建：当前需保护分类写操作、管理员管理、角色管理、权限管理。
- D4 超级管理员不可被任何操作影响；普通管理员不能操作自己。
- D5 权限每请求查库：当前不做缓存。
- D6 最小扩展复用：复用 JWT 验签、Bearer 解析、Session、Principal、Context、错误响应；JWT 增加 `type=user/admin`；用户与管理员使用不同 Session 前缀；分别提供 `UserAuth` 与 `AdminAuth`；`/admin` 使用 `AdminAuth + RequirePermission(...)`；前台合法 Token 访问后台返回 403；不复制两套 JWT/Session 实现。

### Assumption

- 授权中间件采用 `AdminAuth + RequirePermission(...)` 组合（与 D6 一致；具体中间件签名与上下文契约由 Analyst 起草、Owner 确认）。

### OPEN QUESTION

- 见下节 Analyst Questions（不阻塞任务创建，交 Analyst 分析、Owner 确认）。

## Verification

- AC-002/003/013/014 → HTTP 集成测试：分别用无 Token、前台用户 Token、管理员 Token 调用 `/admin/me`，断言 401/403/200。
- AC-015/016/017 → 构造普通管理员（无权限）、有权限管理员、超级管理员调用同一受保护接口，断言 403/成功/成功。
- AC-018 → 用无权限管理员调用"创建角色"等写接口，断言 403 后查库确认 `roles/admins` 等无新增行。
- AC-019 → 构造"A 试图通过请求体 `role_id` 给自己提权/给 B 分配越权角色"，断言 403 且无写入。
- AC-005 → 禁用管理员后，用其旧 Token 访问受保护接口，断言 401。
- AC-007/008 → 对超级管理员执行禁用/删除、普通管理员操作自己，断言明确拒绝且无写入。
- 通用：`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：这是权限模型与重要安全边界，涉及身份区分、授权中间件、401/403 语义、超级管理员初始化与自管理防护，且 401/403 判定顺序（认证 vs 授权）会直接决定安全结果。

## Analyst Questions

1. 权限 code 命名规范与最小清单（资源:动作，如 `category:create/admin:manage`），覆盖当前全部受保护资源。
2. JWT 类型声明的确切字段与取值（`typ` vs `aud` 或自定义 `type`）及与现有 `sub/sid` 的兼容。
3. 用户/管理员 Session 前缀的确切命名。
4. 错误码分配：沿用 IAM 2000 段扩展，还是新开 RBAC 段；具体码值。
5. "禁用管理员使既有会话立即失效"的实现（Admin Session 校验时读 `status`，还是禁用时主动撤销其全部 Session）。
6. 删除管理员时 `admin_roles`、会话的清理边界。
7. 超级管理员初始化的配置键与环境变量命名（建议 `admin.super.username/password` + `ADMIN_SUPER_*`）。
8. `/admin` 下资源路由命名（`admins/roles/permissions`）与请求/响应契约。

## Review Baseline

- Base commit：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（分支 `feat/rbac`，合并 MR #6）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：不适用。

## Initial Route

READY_FOR_ANALYST
