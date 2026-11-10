/**
 * 管理员 DTO（`api/admin/v1/admin.go` + `internal/logic/admin/admin.go`）。
 *
 * 后端真实能力（A5 复核）：
 * - `POST   /admin/admins`                  创建（username 3~24 位字母数字，password 8~24）
 * - `PUT    /admin/admins/:id/status`       禁用/启用（1 启用 / 0 禁用）
 * - `DELETE /admin/admins/:id`              删除（事务级联删除 admin_roles）
 * - `POST   /admin/admins/:id/roles`        分配角色（幂等）
 * - `DELETE /admin/admins/:id/roles/:role_id` 移除角色（幂等）
 * - **没有**管理员列表、管理员详情、资料修改接口。
 *
 * 保护规则（后端强制）：目标为超级管理员 → 2005；目标为调用者自身 → 2006。
 * 前端不复制这些规则做"预判放行"，只展示后端错误。
 */
export interface CreateAdminPayload {
  username: string;
  password: string;
}

export interface CreateAdminResult {
  id: number;
  username: string;
}

export interface UpdateAdminStatusPayload {
  status: 0 | 1;
}

export interface AssignAdminRolePayload {
  role_id: number;
}
