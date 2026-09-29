# Owner 核心逻辑验证

> 仅在 Cleaner 判定 `CLEAN` 后填写。以下验证卡让 Owner 通过「代码位置 → 业务后果 → 测试失败」理解最核心的安全不变量，无需逐行 Review 全部 Diff。

## CL-001：授权判定（RequirePermission 每请求查库 + 超级管理员放行）

- Owner 需要理解：后台写/管理接口由「AdminAuth（认证）+ RequirePermission(code)（授权）」双层保护。`RequirePermission` 每次请求都从 DB 经 `admin_roles → role_permissions → permissions` 查当前管理员是否命中指定权限 code；超级管理员（`is_super=1`）无条件放行；查不到权限 → 403，DB 查询失败 → 500（fail-closed，绝不进 handler）。一旦「超级管理员放行」被去掉或「未命中仍放行」，普通管理员即可越权执行分类写、角色/权限/管理员管理等操作。
- 生产代码：`internal/middleware/auth.go` —— `RequirePermission`（约 124~148 行）与 `hasPermission`（约 230~242 行）
- 关键测试：
  - `internal/middleware/auth_test.go#TestRequirePermissionWithPermission`（有权限放行）
  - `internal/middleware/auth_test.go#TestRequirePermissionWithoutPermission`（无权限 403）
  - `internal/middleware/auth_test.go#TestRequirePermissionSuperAdminBypass`（超级管理员无显式权限也放行）
  - `internal/controller/admin/admin_test.go#TestAuthorizationMatrix`（端到端授权矩阵 + 403 无写入）
- 基线验证：`go test -p 1 ./internal/middleware/ ./internal/controller/admin/` —— 应全部 `ok`
- 可选 Mutation：把 `RequirePermission` 里的 `if p.IsSuper { r.Middleware.Next(); return }` 整段删除（或改成先查权限再放行）
- 预期失败：`TestRequirePermissionSuperAdminBypass` 必须失败——超级管理员无显式持有 `role:list` 权限时将被 403，证明「超级管理员按规则放行」是真实被测试保护的不变量
- 恢复确认：恢复该段代码，重新运行 `go test -p 1 ./internal/middleware/ ./internal/controller/admin/`，应重新全部通过

## CL-002：超级管理员与自操作防护（checkTargetAdmin）

- Owner 需要理解：所有「以他人为目标的写操作」（禁用/启用、删除、分配/移除角色）必须先过 `checkTargetAdmin(currentAdminID, targetID)`：目标不存在 → 404(2003)；目标是超级管理员 → 403(2005)；目标就是调用者自己 → 403(2006)。调用者身份取自已认证的 `AdminPrincipal.AdminID`（JWT sub），目标 id 来自 URL 路径，绝不信任请求体。破坏这条校验将导致超级管理员被降权/删除，或普通管理员自锁死/自我提权。
- 生产代码：`internal/logic/admin/admin.go` —— `checkTargetAdmin`（约 541~556 行），被 `UpdateAdminStatus`/`DeleteAdmin`/`AssignAdminRole`/`RemoveAdminRole` 复用
- 关键测试：`internal/controller/admin/admin_test.go#TestAdminCreateDeleteAndProtection`（超级管理员 2005、自操作 2006、普通管理员可操作他人）
- 基线验证：`go test -p 1 ./internal/controller/admin/ -run TestAdminCreateDeleteAndProtection` —— 应 `ok`
- 可选 Mutation：把 `checkTargetAdmin` 中 `if target.IsSuper == superFlag { return codes.New(codes.CodeSuperAdminProtected) }` 删除（或把 `target.ID == currentAdminID` 的判断删掉）
- 预期失败：`TestAdminCreateDeleteAndProtection` 必须失败——删除 super admin 会返回成功而非 2005（且随后查库确认 super admin 被误删），证明「超级管理员不可被任何操作影响」是被真实测试保护的不变量
- 恢复确认：恢复该校验，重新运行 `go test -p 1 ./internal/controller/admin/ -run TestAdminCreateDeleteAndProtection`，应重新通过
