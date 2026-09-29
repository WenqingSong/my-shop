# Cleaner Findings

## Review Target

- Task：`admin-identity-rbac`（mode: re_review，复审 finding `CLEAN-001`）
- Contract：`contract.md`，Decision Status = APPROVED（Owner 2026-09-29 确认）
- 任务基线 Base commit：`ed7da0c5198c944ee1d05bfbbe26bc479ca49190`（分支 `feat/rbac`，合并 MR #6）
- 当前审查版本：HEAD `e8f727eebf770a1404ee6de3eb3e3674d5b96570` + 未提交工作区改动（`git status --short` 有 10 个已修改文件，即 CLEAN-001 的修复，尚未 commit）
- 复审对象：`CLEAN-001`（路由前缀偏离 Contract）的修复 Diff = `git diff`（工作区相对 HEAD）涉及：
  `api/admin/v1/admin.go`、`api/iam/v1/iam.go`、`api/categories/v1/categories.go`、`internal/cmd/routes_admin.go`、`internal/cmd/routes_frontend.go`、`internal/cmd/routes_test.go`、`internal/controller/admin/admin_test.go`、`internal/controller/categories/categories_test.go`、`internal/controller/iam/iam_test.go`、`docs/design/iam.md`
- 生产代码与测试均以「当前工作区」为准（`go build`/`go vet`/`go test`/`go test -race` 均基于当前工作区执行）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `internal/boot/seed.go#seedSuperAdmin` 幂等 + 未配置密码 fail-fast；`seed_test.go#TestSeedSuperAdminIdempotent/TestSeedSuperAdminMissingPasswordFails/TestSeedSuperAdminConcurrent` |
| AC-002 | PASS | `admin_test.go#TestAdminLogin` 断言 `type=admin` 与防枚举；`middleware/auth_test.go#TestUserAuthRejectsAdminToken` 断言管理员 token 访问用户侧 403 |
| AC-003 | PASS | `admin_test.go#TestAdminMe` 断言 id/username/is_super/roles |
| AC-004 | PASS | `admin_test.go#TestAdminCreateDeleteAndProtection` 创建普通管理员（`is_super=0`）、`/admin/register` 404 无公开注册 |
| AC-005 | PASS | `admin_test.go#TestAdminDisableImmediateRevocation` 禁用后旧 token 401、重登 401、启用后可重登 |
| AC-006 | PASS | `TestAdminCreateDeleteAndProtection` 删除普通管理员成功且查库确认 |
| AC-007 | PASS | 超级管理员禁用/删除/改角色 → 2005 且 `is_super/status` 不变 |
| AC-008 | PASS | 普通管理员自禁用/自删/自改角色 → 2006 且无写入 |
| AC-009 | PASS | `admin_test.go#TestRolePermissionManagement` 角色 CRUD + 重名 2008 |
| AC-010 | PASS | 同测试 权限 CRUD + code 重复 2010 |
| AC-011 | PASS | 同测试 角色↔权限分配/移除（含幂等） |
| AC-012 | PASS | 同测试 管理员↔角色分配/移除，`/admin/me` 展示角色 |
| AC-013 | PASS | 无 token → 401：`TestAdminMe`、`auth_test.go#TestAdminAuthRequiresToken` |
| AC-014 | PASS | 前台用户 token → 403：`TestAuthorizationMatrix`、`auth_test.go#TestAdminAuthRejectsUserToken`、`categories_test.go#TestCategoryWriteAuthorization` |
| AC-015 | PASS | 无权限 → 403：`TestAuthorizationMatrix`、`auth_test.go#TestRequirePermissionWithoutPermission` |
| AC-016 | PASS | 有权限 → 成功：`auth_test.go#TestRequirePermissionWithPermission`、`TestAuthorizationMatrix` |
| AC-017 | PASS | 超级管理员放行：`auth_test.go#TestRequirePermissionSuperAdminBypass`、`TestAuthorizationMatrix` |
| AC-018 | PASS | 403 后查库无写入：`TestAuthorizationMatrix`、`TestCategoryWriteAuthorization` |
| AC-019 | PASS | 请求体目标 id 不作为权限证明：`TestAuthorizationMatrix`（bob 经 body 提权 → 403 且 `admin_roles` 无新增） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无错误 |
| `go vet ./...` | PASS | 退出码 0，无告警（基线 `categories_test.go` 的 `GenerateWithSecret` 2 参编译错误已随本任务修复） |
| `go test -p 1 ./...` | PASS | 全部包 ok（`internal/controller/admin`、`internal/controller/categories`、`internal/controller/iam`、`internal/middleware`、`internal/boot` 等） |
| `go test -race -p 1 ./...` | PASS | 全包 ok，无数据竞争（seed 并发、并发登出、并发同名创建均通过） |
| MySQL/Redis | READY | 容器 `my-shop-mysql`、`my-shop-redis` 均 healthy |
| 路由表一致性 | PASS | `cmd/routes_test.go#TestRouteTable` 断言 Contract 原路径存在、`/api/v1`、`/admin/v1`、`/admin/register` 均不存在；`api/*/v1/*.go` 的 `g.Meta path` 与注册路径一致 |

## Findings

### CLEAN-001：路由前缀偏离已批准 Contract，擅自改变公开接口路径

- Severity：P1
- Status：CLOSED
- Location：`internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go`、`api/iam/v1/iam.go`、`api/admin/v1/admin.go`、`api/categories/v1/categories.go`、`internal/cmd/routes_test.go`、`docs/design/iam.md`
- AC / Invariant：Contract「Interfaces and Data」路由表 +「Allowed / Forbidden Changes」禁止条款；Task Scope 路由
- Trigger：复审验证——检查工作区相对 HEAD 的 `git diff`，确认前台/后台/分类写接口路径已恢复 Contract 原路径
- Actual（修复后）：前台 `/register`、`/login`、`/me`、`/logout`、`/categories`、`/categories/:id`；后台 `/admin/login`、`/admin/me`、`/admin/logout`、`/admin/admins...`、`/admin/roles...`、`/admin/permissions...`；分类写接口回到 `/categories`（`POST/PUT/DELETE`）并挂 `AdminAuth + RequirePermission(category:*)`。`routes_test.go` 的 `want` 列表锁定原路径、`forbidden` 列表锁定 `/api/v1`、`/admin/v1`、`/admin/register` 不存在。`docs/design/iam.md` 同步恢复原路径。
- Expected：与 Contract 一致 —— 已满足
- Impact：已消除公开接口破坏风险
- Evidence：`git diff`（工作区）显示 10 个文件均回退路径；`go test -p 1 ./...` 全通过（含 `TestRouteTable`）
- 关闭依据：修复 Diff 与 Contract 一致，回归测试（`TestRouteTable`、各 `*_test.go` 路径断言）通过，未引入新偏离。

### CLEAN-002：禁用管理员登录返回码与错误密码不一致（信息泄露边缘）

- Severity：P3（不阻塞 CLEAN，Owner 自行决定）
- Status：OPEN
- Location：`internal/logic/admin/admin.go#Login`（第 111~116 行）
- AC / Invariant：Contract Error Semantics（2002 防枚举）；AC-005
- Trigger：对「已禁用且密码正确」的管理员调用 `/admin/login`，与「密码错误」对比返回 code
- Actual：禁用 + 正确密码 → 1002（UNAUTHORIZED）；密码错误/不存在 → 2002（INVALID_CREDENTIALS）。攻击者若能命中正确密码，可据此区分「账号已禁用」
- Expected：可选加固——禁用账号登录失败统一返回 2002，避免泄露账号状态；或维持现状（凭据先于状态校验，属 Contract 已确认顺序「校验凭据 → 校验 status」）
- Impact：极低（需已掌握正确密码，且 username 存在性已由 2002 掩盖）
- Evidence：`Login` 先 bcrypt 比对再查 status；`TestAdminDisableImmediateRevocation` 仅断言重登 `Status==401`，未断言具体 code
- Required Fix Boundary：如处理，仅调整禁用分支的错误码为 2002 并同步测试，不改变认证/授权判定顺序与其它公开语义。

无其它 actionable findings。
