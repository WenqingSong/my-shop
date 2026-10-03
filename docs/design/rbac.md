# RBAC 授权设计（Role-Based Access Control）

本文面向项目接手者，说明后台管理员的**授权（Authorization）**模型：角色、权限、绑定关系、超级管理员与权限边界。事实来源为 `admin-identity-rbac` 最终 APPROVED Contract 与最终实现。

与 `iam.md` 的分工：`iam.md` 负责 **Authentication**（JWT 类型声明、管理员会话、`AdminAuth`/`AdminAuthSignatureOnly` 中间件、身份域隔离）；本文负责 **Authorization**（`RequirePermission`、`roles`/`permissions` 数据模型、绑定关系、超级管理员保护、401/403 判定）。

## 1. 职责与边界

后台管理员与前台用户身份分离；管理员凭 `type=admin` JWT 访问 `/admin` 接口，经「认证（`AdminAuth`）+ 授权（`RequirePermission`）」两层中间件保护。严格区分 401（未认证）与 403（已认证但无权）。启动时幂等 seed 一个不可被任何操作影响的超级管理员；禁止公开注册管理员。

## 2. 数据模型

### 2.1 `admins`（管理员身份）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `username` | VARCHAR(24) | 非空，`uk_admin_username` 唯一 |
| `password_hash` | VARCHAR(60) | 非空，bcrypt |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `is_super` | TINYINT | 非空默认 0（`1=超级管理员`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

### 2.2 授权授予模型（4 表）

| 表 | 字段 | 说明 |
| --- | --- | --- |
| `roles` | `id`/`name`/`description`/时间戳 | 角色，`uk_role_name` 唯一 |
| `permissions` | `id`/`code`/`name`/`description`/时间戳 | 权限，`uk_permission_code` 唯一；`code` 为稳定 key |
| `admin_roles` | `admin_id`/`role_id` | 管理员↔角色，复合主键 `(admin_id, role_id)` |
| `role_permissions` | `role_id`/`permission_id` | 角色↔权限，复合主键 `(role_id, permission_id)` |

- 不引入外键（沿用 `users`/`categories` 风格）；关联表用复合主键保证唯一；引用目标是否存在由应用层校验（返回 404）。
- 授权匹配链：`admins → admin_roles → role_permissions → permissions`，`RequirePermission` 按 `permissions.code` 匹配。

### 2.3 权限粒度与清单

权限粒度 `资源:动作`。当前 seed 25 个权限（按 `code` 唯一）：

```text
category:create/update/delete                        （3）
product:create/update/on_shelf/off_shelf             （4）
sku:create/update/delete                             （3）
inventory:increase/deduct                            （2）
admin:create/disable/delete/assign_role              （4）
role:create/list/update/delete/assign_permission     （5）
permission:create/list/update/delete                 （4）
```

## 3. 业务不变量

- INV-001（超级管理员唯一且不可变）：全库仅一个 `is_super=1`，由启动 seed 创建；任何接口不能禁用/删除/降级/改其角色（403 `2005`，无写入）。
- INV-002（自操作防护）：普通管理员不能禁用/删除/变更自身角色（403 `2006`，无写入）；目标身份取自已认证 `AdminPrincipal.AdminID`，不接受请求体指定。
- INV-003（授权不可伪造）：授权只看调用者已认证身份的权限；无权调用者访问写接口返回 403 且不产生任何 DB 写入；请求体中的目标 admin/role id 不作为调用者权限证明。
- INV-004（401/403 严格区分）：无/非法/过期/会话失效/被禁用 token → 401；有效 token 但 `type` 不符或权限不足 → 403。
- INV-005（用户名唯一）：`admins.username` 唯一由 DB `UNIQUE` 约束保证，并发创建同名仅一个成功（409）。

## 4. 一致性模型与失败语义

- 事实来源：MySQL `admins`（身份 + status + is_super）、`roles`/`permissions`/`admin_roles`/`role_permissions`（授权授予）；Redis `iam:admin:session:{sid}`（会话状态，见 `iam.md`）；JWT（签名凭证，非事实来源）。
- 授权判定（每请求）：`IsSuper` 直接放行；否则查权限 code 集合，命中放行，未命中 403；DB 授权查询失败 fail-closed 500（绝不进 handler，无写入）。
- 写操作（同步）：创建管理员/角色/权限为单表写，唯一冲突映射 409；删除管理员=事务删除 `admins`+`admin_roles`；删除角色=事务删除 `roles`+`role_permissions`+`admin_roles`；删除权限=事务删除 `permissions`+`role_permissions`；关键删除/更新核对 `RowsAffected`（目标不存在→404）。
- 删除/禁用不清 Redis：靠「admins 不存在/禁用→401」即时失效，孤儿 session 由 TTL 自愈（见 `iam.md`）。
- seed（启动）：`is_super=1` 不存在则创建（bcrypt），存在则跳过且不覆盖密码；密码未配置且需创建时启动 fail-fast。
- 重试/重复：分配/移除角色或权限幂等（复合主键，重复分配无副作用）。
- 无 MQ、无异步、无跨系统事务；授权判定不依赖 Redis。

## 5. 安全与权限边界

- `AdminAuth`（认证：验签→401、`type≠admin`→403、会话失效→401、`admins` 不存在/禁用→401）+ `RequirePermission(code)`（授权：`IsSuper` 放行、命中放行、未命中 403、DB 查询失败 fail-closed 500）。
- 后台判定顺序：验签+exp → 401；`type≠admin` → 403；会话无效 → 401；`admins` 不存在/禁用 → 401；权限不足 → 403。
- 无 `/admin/register`（公开注册禁止）；无 `/admin/admins` 列表（`/admin/me` 覆盖自身信息）。
- 超级管理员密码无 dev 默认值，经 `admin.super.password` / `ADMIN_SUPER_PASSWORD` 注入；不硬编码凭据。

## 6. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 1002 | UNAUTHORIZED（认证失败/禁用/会话失效） | 401 |
| 1003 | FORBIDDEN（无权限/type 不符） | 403 |
| 2002 | INVALID_CREDENTIALS（管理员登录失败，防枚举） | 401 |
| 2003 | ADMIN_NOT_FOUND | 404 |
| 2004 | ADMIN_USERNAME_EXISTS | 409 |
| 2005 | SUPER_ADMIN_PROTECTED | 403 |
| 2006 | SELF_OPERATION_FORBIDDEN | 403 |
| 2007 | ROLE_NOT_FOUND | 404 |
| 2008 | ROLE_NAME_EXISTS | 409 |
| 2009 | PERMISSION_NOT_FOUND | 404 |
| 2010 | PERMISSION_CODE_EXISTS | 409 |

## 7. 跨模块关系

- 认证部分（JWT `type` 声明、`iam:admin:session:` 会话、`AdminAuth`/`AdminAuthSignatureOnly` 中间件、身份域隔离）见 `iam.md`。
- 各业务模块的写接口经 `RequirePermission` 挂载对应权限 code：`category:*`/`product:*`/`sku:*`/`inventory:*`，见各模块 Design。
- 授权数据模型（4 表）经 golang-migrate baseline（`20261001000001`）建表，见 `migration.md`。

## 8. Deferred / 已知留白

- 删除被 `RequirePermission` 引用的权限 code 会令所有非超级管理员失去对应访问（超级管理员仍放行）；属调用方责任。
- 认证与授权各多一次 DB 查询（每请求查库，不做权限缓存）；管理员请求延迟略增，非阻塞。
- 管理员会话与用户会话共用同一 Redis 与 TTL 配置（`auth.session.ttl`），不独立调优。
