# Cleaner Findings

## Review Target

- Task：`admin-identity-rbac`（mode: initial_review）
- Contract：`contract.md`，Decision Status = APPROVED（Owner 2026-09-29 确认）
- 任务基线 Base commit：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（分支 `feat/rbac`，合并 MR #6）
- 当前审查版本：HEAD `fa4a9d9c5e5aad24dc7f9583c6857669bcc4066e`，working tree clean（`git status --short` 为空）
- 基线后提交（7 个）：`8996902`(docs) → `ae4d228`(表/seed) → `23923ba`(auth) → `930b724`(admin v1) → `b1b657b`(分类迁移) → `d7edc1f`(统一路由前缀) → `fa4a9d9`(docs 路由)
- 任务前已有修改：无（基线 clean）。区分方式：`git diff ed7da0c..HEAD`。
- 相关文件：`api/admin/v1/admin.go`、`api/iam/v1/iam.go`、`api/categories/v1/categories.go`、`internal/auth/{jwt,session}.go`、`internal/middleware/{auth,principal}.go`、`internal/boot/{boot,seed}.go`、`internal/codes/codes.go`、`internal/cmd/{cmd,routes_admin,routes_frontend}.go`、`internal/controller/admin/*`、`internal/logic/admin/*`、`internal/service/admin.go`、`manifest/config/config.yaml` 及相应 `*_test.go`。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `seedSuperAdmin` 幂等 + 未配置密码 fail-fast；`internal/boot/seed_test.go` 覆盖幂等、无密码失败、并发 seed |
| AC-002 | PASS | `internal/controller/admin/admin_test.go#TestAdminLogin` 验证 `type=admin`、防枚举；`internal/middleware/auth_test.go#TestUserAuthRejectsAdminToken` 验证管理员 token 访问用户侧 403 |
| AC-003 | PASS | `admin_test.go#TestAdminMe` 验证 id/username/is_super/roles |
| AC-004 | PASS | `admin_test.go#TestAdminCreateDeleteAndProtection` 验证创建普通管理员、`/admin/v1/register` 404 无公开注册 |
| AC-005 | PASS | `admin_test.go#TestAdminDisableImmediateRevocation` 验证禁用后旧 token 401、重登 401 |
| AC-006 | PASS | 同 `TestAdminCreateDeleteAndProtection` 删除普通管理员成功且查库确认 |
| AC-007 | PASS | 超级管理员禁用/删除/改角色 → 2005 且 `is_super/status` 不变 |
| AC-008 | PASS | 普通管理员自禁用/自删/自改角色 → 2006 且无写入 |
| AC-009 | PASS | `admin_test.go#TestRolePermissionManagement` 角色 CRUD |
| AC-010 | PASS | 同测试 权限 CRUD |
| AC-011 | PASS | 同测试 角色↔权限分配/移除（含幂等） |
| AC-012 | PASS | 同测试 管理员↔角色分配/移除，`/admin/v1/me` 展示角色 |
| AC-013 | PASS | 无 token → 401：`admin_test.go#TestAdminMe`、`middleware/auth_test.go#TestAdminAuthRequiresToken` |
| AC-014 | PASS | 前台用户 token → 403：`admin_test.go#TestAuthorizationMatrix`、`auth_test.go#TestAdminAuthRejectsUserToken`、`categories_test.go#TestCategoryWriteAuthorization` |
| AC-015 | PASS | 无权限 → 403：`TestAuthorizationMatrix`、`auth_test.go#TestRequirePermissionWithoutPermission` |
| AC-016 | PASS | 有权限 → 成功：`auth_test.go#TestRequirePermissionWithPermission`、`TestAuthorizationMatrix` |
| AC-017 | PASS | 超级管理员放行：`auth_test.go#TestRequirePermissionSuperAdminBypass`、`TestAuthorizationMatrix` |
| AC-018 | PASS | 403 后查库无写入：`TestAuthorizationMatrix`、`categories_test.go#TestCategoryWriteAuthorization` |
| AC-019 | PASS | 请求体目标 id 不作为权限证明：`TestAuthorizationMatrix`（bob 经 body 提权 → 403 且 `admin_roles` 无新增） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无错误输出 |
| `go vet ./...` | PASS | 无告警（基线 `categories_test.go` 的 `GenerateWithSecret` 2 参编译错误已随本任务修复） |
| `go test -p 1 ./...` | PASS | 全部包 ok：auth、boot、cmd、controller/admin、controller/categories、controller/iam、logic/*、middleware |
| MySQL/Redis | READY | docker 容器 `my-shop-mysql`、`my-shop-redis` 均为 healthy，3306/6379 可达 |

## Findings

### CLEAN-001：路由前缀偏离已批准 Contract，擅自改变公开接口路径

- Severity：P1
- Status：OPEN
- Location：`internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go`、`api/iam/v1/iam.go`、`api/admin/v1/admin.go`、`api/categories/v1/categories.go`、`internal/cmd/routes_test.go`（提交 `d7edc1f`、`fa4a9d9`）
- AC / Invariant：Contract「Interfaces and Data」路由表 +「Allowed / Forbidden Changes」禁止条款（“不改变 `/health`、前台 `/register`/`/login`/`/me`/`/logout` 的公开行为”）；Task Scope（`/admin/login`、`/admin/me`、`/admin/logout`；分类写接口 `POST/PUT/DELETE /categories...` 迁移）
- Trigger：按已确认契约调用旧路径（`POST /login`、`GET /me`、`POST /admin/login`、`POST /categories` 等）得到 404。
- Actual：实现把前台 IAM/分类读接口统一加 `/api/v1` 前缀、后台接口统一加 `/admin/v1` 前缀，并把分类写接口从 `/categories...` 迁到 `/admin/v1/categories...`。`routes_test.go` 的 `forbidden` 列表甚至把旧路径（`POST /register`、`POST /login`、`GET /me`、`POST /logout`、`GET /categories`、`POST /categories`、`POST /admin/login` 等）断言为“不应存在”，`docs/design/iam.md` 也同步改成了新路径——即测试与文档都固化并“锁定”了这一偏离，但 Contract 从未被 Owner 重新批准。
- Expected：保持 Contract 已确认路径——前台 `/register`/`/login`/`/me`/`/logout` 不变（仅用户侧加 `type=user` 校验）；后台 `/admin/login`、`/admin/me`、`/admin/logout`、`/admin/admins`、`/admin/roles`、`/admin/permissions`（无 `/v1`）；分类写接口仍在 `/categories...`，仅中间件从 `Auth` 迁移为 `AdminAuth + RequirePermission`。
- Impact：违反 APPROVED Contract 的明确禁止条款，改变已确认的公开接口；任何按既有契约调用旧路径的客户端（含外部/未纳入本仓库的前端消费者）会 404，属于未经授权的公开行为破坏。
- Evidence：`git diff ed7da0c..HEAD -- internal/cmd/cmd.go` 显示基线 `/register`/`/login`/`/me`/`/logout`/`/categories`；现 `routes_frontend.go`/`routes_admin.go` 全部带 `/api/v1`、`/admin/v1`；`routes_test.go:91-115` 把旧路径列为 forbidden；`docs/design/iam.md` diff 同步改写。
- Required Fix Boundary：恢复 Contract 约定的路由路径（前台 `/register`/`/login`/`/me`/`/logout`/`/categories`，后台 `/admin/...`，分类写接口 `/categories...` + `AdminAuth`+`RequirePermission`），并同步修正测试与文档；或取得 Owner 对 `/api/v1`、`/admin/v1` 前缀方案的正式重新确认并更新 `contract.md`/`task.md`。不得以“统一前缀”为由擅自改变前台公开路径。
