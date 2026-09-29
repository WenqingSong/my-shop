# Technical Contract

## Decision Status

APPROVED

## Problem

新增独立后台管理员身份与 RBAC 权限体系。管理员与前台用户身份分离，管理员凭 `type=admin` 的 JWT 访问 `/admin` 接口，经「认证（AdminAuth）+ 授权（RequirePermission）」两层中间件保护；严格区分 401（未认证）与 403（已认证但无权）；启动时幂等 seed 一个不可被任何操作影响的超级管理员；禁止公开注册管理员。核心设计问题集中在：JWT 类型声明的载体、管理员会话模型、401/403 判定顺序、权限 code 清单与粒度、超级管理员初始化与自管理防护、错误码分段与多表写的一致性。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3 / Go 1.23.0，模块 `cnb.cool/go-cloud-devops/my-shop`，分层 `api/controller/service/logic`（Register 模式），无 dao/model 层，无迁移框架。
- VERIFIED：`internal/auth/jwt.go` 的 `Claims` 内嵌 `gojwt.RegisteredClaims` + 自定义 `Sid`；`Issuer="surgecart"`、`ExpiresIn=3600`、HS256、密钥 `auth.jwt.secret`/`AUTH_JWT_SECRET` ≥32 字节；`Generate(ctx, userID, sid)` / `Parse` 无类型区分。
- VERIFIED：`internal/auth/session.go` 仅用户会话：key `iam:session:{sid}`，Hash 字段 `user_id`/`revoked`，TTL 默认 3600，`CreateSession/ValidateSession/RevokeSession`；无管理员会话、无 user/admin 维度索引。
- VERIFIED：`internal/middleware/auth.go` 的 `Auth`/`AuthSignatureOnly` 复用 `authenticate`（验签+exp → 解析 sub/sid → 校验 session → 注入 `Principal{UserID, Sid}`）；`Principal` 仅 `UserID int64` + `Sid string`，无类型、无 `IsSuper`、无权限。无 `RequirePermission` 中间件。
- VERIFIED：`internal/middleware/response.go` 统一 `{code,message,data}`，`codes.HTTPStatus` 映射 code→HTTP 状态；`internal/codes/codes.go` 已含 `1002→401`、`1003→403`、`1004→404`、`1001→400`、`2001→409`、`2002→401`，IAM 段 2000-2999 与 Category 段 3000-3999 已开。
- VERIFIED：`boot.Bootstrap` → `ensureTables` 幂等建 `users`/`categories`；`internal/cmd/cmd.go` 中分类写操作（`POST/PUT/DELETE /categories...`）当前挂 `middleware.Auth`（仅登录，无角色区分），本任务需迁到 `AdminAuth + RequirePermission`。
- VERIFIED：配置 `manifest/config/config.yaml` 支持环境变量覆盖（`.`→`_` 大写，如 `AUTH_JWT_SECRET`）；`docker-compose.yml` 编排 MySQL 8.0 + Redis 7；环境每日重置、数据不持久。
- VERIFIED：登录/注册/登出的错误语义见 `docs/design/iam.md` 与 `.agent/tasks/iam-v2/contract.md`（APPROVED）：写 session 失败→登录失败 500、鉴权查 Redis 失败 fail-closed 401、logout 幂等。
- VERIFIED（基线缺陷）：`internal/controller/categories/categories_test.go:152` 调用 `auth.GenerateWithSecret([]byte(testJWTSecret), 1)`（2 参），与 iam-v2 后 3 参签名不匹配，`go vet ./...` 在基线即失败；`go build ./...` 通过（不编译 `_test.go`）。该测试同时仍用 `middleware.Auth` + 用户 token 调用分类写接口，本任务迁移分类写操作时必须同步改写。
- UNKNOWN：CI（CNB DinD）是否对 Redis 做独立健康检查未直接验证；超级管理员密码是否需要 dev 默认值由 Owner 定。

## Recommendation

RECOMMENDATION（关键取舍见下，待 Owner 确认）：

1. **JWT 类型**：`Claims` 新增自定义声明 `Type string`（JSON 键 `type`，取值 `"user"`/`"admin"`），不用标准 `typ`（其语义为 JWT 媒体类型）也不用 `aud`（`RegisteredClaims.Audience` 为 `[]string`、语义更偏受众且库可能严格校验）。`sub` 仍为实体 id（用户 id 或管理员 id，视 `type` 而定）。`auth.Generate`/`GenerateWithSecret` 增加 `typ` 参数。
2. **管理员会话**：独立前缀 `iam:admin:session:{sid}`，Hash 字段 `admin_id` + `revoked`，TTL 复用 `auth.session.ttl`；在 `internal/auth/session.go` 集中新增 `CreateAdminSession/ValidateAdminSession/RevokeAdminSession`，禁止散落字面量。
3. **中间件**：保留现有 `Auth`/`AuthSignatureOnly` 为用户侧（内部加 `type=user` 校验）；新增 `AdminAuth`/`AdminAuthSignatureOnly`（加 `type=admin` 校验 + 校验 `admins.status`）与 `RequirePermission(code)`。判定顺序：验签+exp → 401；`type` 不符 → 403；会话无效 → 401；`status=禁用/不存在` → 401；权限不足 → 403；DB 授权查询失败 → fail-closed 500（绝不进 handler）。
4. **权限粒度**：`资源:动作` 细粒度，seed 16 个权限（见 Interfaces），`RequirePermission` 按 code 匹配（不按 id）。
5. **禁用即时失效**：不建 admin→session 索引、不主动撤销，`AdminAuth` 每请求查 `admins.status`（禁用/不存在→401）。
6. **删除清理**：物理删除 + 事务级联删除关联行（见一致性），不主动清 Redis 会话（靠 DB「不存在→401」自愈 + TTL）。
7. **超级管理员**：`is_super=1` 仅由启动 seed 创建，任何接口不能创建第二个；seed 幂等（存在则跳过且不覆盖密码）；密码无 dev 默认值，未配置且需创建时启动 fail-fast。
8. **错误码**：沿用 IAM 2000 段扩展（admin 属身份域），新增 2003-2010，复用 1001/1002/1003/2002。

关键取舍：**认证与授权分层**——`AdminAuth` 只做「你是谁」（401/403 的身份判定），`RequirePermission` 只做「你能做什么」（403 的授权判定）。代价是管理员请求多一次 DB 查询（认证查 admins、授权查权限），与 D5「每请求查库、不做缓存」一致，换取 401/403 语义清晰、可独立测试，且不把「用户 token 打后台」误判为 401。

## Selected Design

Owner 已确认（2026-09-29），采纳 Recommendation 全部 8 点：

1. **JWT 类型**：`Claims` 新增自定义声明 `Type string`（JSON 键 `type`，取值 `"user"`/`"admin"`），不用 `typ`/`aud`；`sub` 为实体 id（用户或管理员，视 `type`）。`Generate`/`GenerateWithSecret` 增加 `typ` 参数。
2. **管理员会话**：前缀 `iam:admin:session:{sid}`，Hash 字段 `admin_id`/`revoked`，TTL 复用 `auth.session.ttl`；`internal/auth/session.go` 集中新增 `CreateAdminSession/ValidateAdminSession/RevokeAdminSession`。
3. **中间件分层**：`AdminAuth`（认证：验签→401、`type≠admin`→403、会话失效→401、`status=禁用/不存在`→401）+ `RequirePermission(code)`（授权：`IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500）；现有 `Auth`/`AuthSignatureOnly` 加 `type=user` 校验。
4. **权限粒度**：细粒度 `资源:动作`，seed 16 个权限（见 Interfaces），按 code 匹配。
5. **禁用即时失效**：每请求查 `admins.status`（禁用/不存在→401），不建 session 索引、不主动撤销。
6. **删除清理**：物理删除 + 事务级联删除关联行（见 Failure and Consistency Semantics），不主动清 Redis 会话。
7. **超级管理员**：`is_super=1` 仅由启动 seed 创建；seed 幂等（存在则跳过且不覆盖密码）；密码无 dev 默认值，未配置且需创建时启动 fail-fast。
8. **错误码**：沿用 IAM 2000 段，新增 2003-2010，复用 1001/1002/1003/2002。

## Interfaces and Data

### 路由（`internal/cmd/cmd.go`）

| 方法/路径 | 保护 | 权限 code | 说明 |
| --- | --- | --- | --- |
| `POST /admin/login` | 公开 | - | 管理员登录，签 `type=admin` token |
| `GET /admin/me` | `AdminAuth` | - | 返回 `id/username/is_super/roles` |
| `POST /admin/logout` | `AdminAuthSignatureOnly` | - | 幂等撤销当前 admin 会话 |
| `POST /admin/admins` | `AdminAuth` | `admin:create` | 创建普通管理员（`is_super=0`） |
| `PUT /admin/admins/:id/status` | `AdminAuth` | `admin:disable` | 禁用/启用（`status` 0/1） |
| `DELETE /admin/admins/:id` | `AdminAuth` | `admin:delete` | 删除普通管理员 |
| `POST /admin/admins/:id/roles` | `AdminAuth` | `admin:assign_role` | 分配角色 |
| `DELETE /admin/admins/:id/roles/:role_id` | `AdminAuth` | `admin:assign_role` | 移除角色 |
| `POST /admin/roles` | `AdminAuth` | `role:create` | 创建角色 |
| `GET /admin/roles` | `AdminAuth` | `role:list` | 角色列表 |
| `PUT /admin/roles/:id` | `AdminAuth` | `role:update` | 更新角色 |
| `DELETE /admin/roles/:id` | `AdminAuth` | `role:delete` | 删除角色 |
| `POST /admin/roles/:id/permissions` | `AdminAuth` | `role:assign_permission` | 角色分配权限 |
| `DELETE /admin/roles/:id/permissions/:permission_id` | `AdminAuth` | `role:assign_permission` | 角色移除权限 |
| `POST /admin/permissions` | `AdminAuth` | `permission:create` | 创建权限 |
| `GET /admin/permissions` | `AdminAuth` | `permission:list` | 权限列表 |
| `PUT /admin/permissions/:id` | `AdminAuth` | `permission:update` | 更新权限 |
| `DELETE /admin/permissions/:id` | `AdminAuth` | `permission:delete` | 删除权限 |
| `POST/PUT/DELETE /categories...` | 迁移为 `AdminAuth` | `category:create/update/delete` | 现有分类写接口 |

无 `/admin/register`（公开注册禁止）；无 `/admin/admins` 列表（Scope 仅创建/禁用/删除，`/admin/me` 覆盖自身信息）。

### 权限 seed 清单（16 个，幂等按 `code` 唯一）

`category:create`、`category:update`、`category:delete`；`admin:create`、`admin:disable`、`admin:delete`、`admin:assign_role`；`role:create`、`role:list`、`role:update`、`role:delete`、`role:assign_permission`；`permission:create`、`permission:list`、`permission:update`、`permission:delete`。

### 数据表（幂等 `CREATE TABLE IF NOT EXISTS`，挂 `boot.ensureTables`）

```sql
CREATE TABLE IF NOT EXISTS admins (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username      VARCHAR(24)     NOT NULL,
  password_hash VARCHAR(60)     NOT NULL,
  status        TINYINT         NOT NULL DEFAULT 1,  -- 1 启用 / 0 禁用
  is_super      TINYINT         NOT NULL DEFAULT 0,  -- 0/1
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_admin_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS roles (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)     NOT NULL,
  description VARCHAR(255)    NOT NULL DEFAULT '',
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_role_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS permissions (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code        VARCHAR(64)     NOT NULL,
  name        VARCHAR(64)     NOT NULL,
  description VARCHAR(255)    NOT NULL DEFAULT '',
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_permission_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS admin_roles (
  admin_id BIGINT UNSIGNED NOT NULL,
  role_id  BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (admin_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id       BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- 不引入外键（沿用 users/categories 风格）；关联表用复合主键保证唯一；引用目标是否存在由应用层校验（返回 404）。
- `permissions.code` 为稳定 key，`RequirePermission` 按 code 匹配（经 `admin_roles → role_permissions → permissions`）。

### 配置（`manifest/config/config.yaml`）

```yaml
admin:
  super:
    # 超级管理员用户名（默认 admin，非机密）。
    username: "admin"
    # 超级管理员初始密码（仅用于「超级管理员尚不存在时」创建）。
    # ⚠️ 无开发默认值：未配置且需创建时启动失败，禁止静默用弱默认值建号。
    # 生产/开发均通过环境变量 ADMIN_SUPER_PASSWORD 注入。
    password: ""
```

对应环境变量：`ADMIN_SUPER_USERNAME`、`ADMIN_SUPER_PASSWORD`。

### 代码接口变更

- `auth.Claims` 新增 `Type string`（`json:"type,omitempty"`）；常量 `auth.TypeUser="user"`、`auth.TypeAdmin="admin"`。
- `auth.Generate(ctx, typ string, id int64, sid string)` / `GenerateWithSecret(secret []byte, typ string, id int64, sid string)`；`Parse` 原样返回 `Type`。
- `internal/auth/session.go` 新增管理员会话三函数（前缀 `iam:admin:session:`，字段 `admin_id`/`revoked`）。
- 新增 `AdminPrincipal{AdminID int64, Sid string, IsSuper bool}` 与独立 context key + `AdminPrincipalFromContext`。
- 新增 `middleware.AdminAuth`、`AdminAuthSignatureOnly`、`RequirePermission(code string)`；现有 `Auth`/`AuthSignatureOnly` 内部增加 `type=user` 校验。
- `internal/codes` 新增 2003-2010（见 Error Semantics）。
- 新增 `api/admin/v1`、`internal/controller/admin`、`internal/service`（IAdmin）+ `internal/logic/admin`（Register 模式）；`internal/cmd/cmd.go` 挂载路由；`internal/logic/logic.go` 注册。

## Business Invariants

- INV-001（超级管理员唯一且不可变）：全库仅一个 `is_super=1`，由启动 seed 创建；任何接口不能禁用/删除/降级/改其角色（返回 403 `SUPER_ADMIN_PROTECTED`，无写入）。对应 AC-007。
- INV-002（自操作防护）：普通管理员不能禁用/删除/变更自身角色（返回 403 `SELF_OPERATION_FORBIDDEN`，无写入）；目标身份取自已认证 `AdminPrincipal.AdminID`，不接受请求体指定。对应 AC-008/019。
- INV-003（授权不可伪造）：授权只看调用者已认证身份的权限；无权调用者访问写接口返回 403 且不产生任何 DB 写入；请求体中的目标 admin/role id 不作为调用者权限证明。对应 AC-015/018/019。
- INV-004（401/403 严格区分）：无/非法/过期/会话失效/被禁用 token → 401；有效 token 但 `type` 不符或权限不足 → 403。对应 AC-013/014/015/016/017。
- INV-005（类型隔离）：仅 `type=admin` token 能通过 `AdminAuth`；`type=user` token 访问 `/admin` → 403；管理员凭据无法登录前台 `/login`。对应 AC-002/014。
- INV-006（用户名唯一）：`admins.username` 唯一由 DB `UNIQUE` 约束保证，并发创建同名仅一个成功（409）。对应 AC-004 与并发边界。

## Failure and Consistency Semantics

- 事实来源：MySQL `admins`（身份 + status + is_super）、`roles`/`permissions`/`admin_roles`/`role_permissions`（授权授予）；Redis `iam:admin:session:{sid}`（会话状态）；JWT（`type + sub + sid + exp` 签名凭证，非事实来源）。
- 管理员登录（同步）：校验凭据（bcrypt，防枚举对齐耗时）→ 校验 `status`（禁用→401）→ 生成 sid → 写 admin session（失败→500 不签 token）→ 签 `type=admin` JWT。
- 认证（每请求）：验签+exp 失败→401；`type≠admin`→403；admin session 不存在/撤销/`admin_id` 不匹配→401；Redis 查询失败→401（fail-closed）；`admins` 不存在/`status=0`→401（禁用即时失效）。
- 授权（每请求）：`IsSuper`→放行；否则查权限 code 集合，命中→放行，未命中→403；DB 查询失败→fail-closed 500（绝不进 handler，无写入）。
- 写操作（同步）：创建管理员/角色/权限为单表写，唯一冲突映射 409；删除管理员=事务删除 `admins`+`admin_roles`；删除角色=事务删除 `roles`+`role_permissions`+`admin_roles`；删除权限=事务删除 `permissions`+`role_permissions`；关键删除/更新核对 `RowsAffected`（目标不存在→404）。
- 删除/禁用不清 Redis：删除后靠「admins 不存在→401」即时失效，孤儿 session 由 TTL 自愈；禁用后靠「status=0→401」即时失效，无需 session 索引。
- seed（启动）：`is_super=1` 不存在则创建（bcrypt），存在则跳过且不覆盖密码；密码未配置且需创建→启动 fail-fast。
- 重试/重复：logout 幂等；登录重试产生新 sid（多会话并存）；分配/移除角色或权限幂等（复合主键，重复分配无副作用）。
- 无 MQ、无异步、无跨系统事务；Redis 仅承载会话状态，授权判定不依赖 Redis。

## Error Semantics

沿用 `{code,message,data}`，`internal/codes` 集中定义；IAM 2000 段扩展：

| code | 语义 | HTTP |
| --- | --- | --- |
| 1001 | INVALID_ARGUMENT | 400 |
| 1002 | UNAUTHORIZED（认证失败/禁用/会话失效） | 401 |
| 1003 | FORBIDDEN（无权限/type 不符） | 403 |
| 1004 | NOT_FOUND | 404 |
| 2002 | INVALID_CREDENTIALS（管理员登录失败，防枚举） | 401 |
| 2003 | ADMIN_NOT_FOUND | 404 |
| 2004 | ADMIN_USERNAME_EXISTS | 409 |
| 2005 | SUPER_ADMIN_PROTECTED | 403 |
| 2006 | SELF_OPERATION_FORBIDDEN | 403 |
| 2007 | ROLE_NOT_FOUND | 404 |
| 2008 | ROLE_NAME_EXISTS | 409 |
| 2009 | PERMISSION_NOT_FOUND | 404 |
| 2010 | PERMISSION_CODE_EXISTS | 409 |

## Allowed / Forbidden Changes

允许：
- 新增 5 张表 DDL 与 `boot.ensureTables` 扩展；新增权限 seed 与超级管理员 seed。
- 修改 `auth.Claims`/`Generate` 签名、新增管理员会话函数、`AdminPrincipal`、`AdminAuth`/`AdminAuthSignatureOnly`/`RequirePermission`。
- 新增 `api/admin/v1`、`internal/controller/admin`、`internal/service`、`internal/logic/admin`、错误码、`admin.super.*` 配置。
- 迁移分类写接口到 `AdminAuth + RequirePermission`；同步改写 `internal/controller/categories/categories_test.go`（含修复 `GenerateWithSecret` 3 参调用与改用 admin token）。

禁止：
- 不新增 `/admin/register` 或任何公开注册入口；不改变 `/health`、前台 `/register`/`/login`/`/me`/`/logout` 的公开行为（用户侧增加 `type=user` 校验除外）。
- 不引入权限缓存、登录限流/验证码/锁定、审计日志、角色继承、多租户、Token 刷新（均 Out of Scope）。
- 不硬编码超级管理员密码或 JWT 密钥；不把密码/密钥写入日志或响应；不 fail-open 放行未通过认证/授权校验的请求。
- 不提供创建 `is_super=1` 的接口（唯一超级管理员只由 seed 产生）；不允许把请求体中的目标 id 当作调用者权限证明。

## Verification Requirements

- INV-001 → 对超级管理员执行禁用/删除/改角色，断言 403 `2005` 且 `admins/admin_roles` 无变化；启动两次确认只存在一个 `is_super=1` 且密码不被覆盖。
- INV-002 → 普通管理员 A 对自身执行禁用/删除/改角色，断言 403 `2006` 且无写入。
- INV-003/004 → 无 token→401；前台用户 token 访问 `/admin/me`→403；无权限管理员调用「创建角色」→403 后查库 `roles` 无新增；`type=user` token 访问分类写接口→403。
- INV-005 → 管理员登录成功 token `type=admin`；前台用户凭据调 `/admin/login` 失败；管理员凭据调 `/login` 失败。
- INV-006 → 并发创建同名管理员仅一个成功、其余 409，查库无重复行。
- 禁用即时失效 → 禁用管理员后用其旧 token 访问受保护接口→401；重新登录→401。
- AC-002/003 → `/admin/login` 成功、`/admin/me` 返回 `id/username/is_super/roles`。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`（需修复基线 `categories_test.go` 编译错误）；MySQL/Redis 集成验证说明容器就绪。

## Open Risks

- 删除被 `RequirePermission` 引用的权限 code 会令所有非超级管理员失去对应访问（超级管理员仍放行）；属调用方责任，可接受，需在实现注释/文档中提示。
- 认证与授权各多一次 DB 查询（每请求查库，符合 D5）；管理员请求延迟略增，非阻塞。
- 管理员会话与用户会话共用同一 Redis 与 TTL 配置（`auth.session.ttl`），不独立调优；当前范围可接受。

## Owner Decision Record

Owner 于 2026-09-29 确认以下 5 项决定（对应 Analyst 待决问题）：

1. **权限粒度**：细粒度 `资源:动作`，seed 16 个权限（非粗粒度 `:manage`）。
2. **JWT 类型字段**：自定义 `type`（`"user"`/`"admin"`）。
3. **禁用失效**：每请求查 `admins.status`（禁用/不存在→401）。
4. **超级管理员密码**：无 dev 默认值，未配置且需创建时启动 fail-fast；接受。
5. **错误码**：沿用 IAM 2000 段新增 2003-2010。

其余设计细节（会话前缀 `iam:admin:session:`、判定顺序、删除清理边界、路由命名、`admin.super.username` 默认 `admin`）属 Recommendation 已给出且不改变 Scope/AC/公开行为的实现细节，已一并落地。

适用范围：仅 admin-identity-rbac 本任务。关键问题均已解决，Contract 标记 APPROVED，可交 Coder 实现。
