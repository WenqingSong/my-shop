/**
 * 角色 DTO（`api/admin/v1/admin.go`）。
 *
 * 后端真实字段只有 `id / name / description`：
 * - **没有** status 字段（角色没有启用/停用语义）；
 * - **没有**角色详情接口，列表只返回上述三个字段；
 * - 角色已授权限由 `GET /admin/roles/:id/permissions` 返回（`role:list`）；
 * - **没有**系统内置角色的特殊保护（roles 表为空，也没有 seed 角色）。
 */
export interface RoleDTO {
  id: number;
  name: string;
  description: string;
}

export interface CreateRolePayload {
  name: string;
  description: string;
}

/** 更新角色：只提交需要变更的字段（后端为指针字段） */
export interface UpdateRolePayload {
  name?: string;
  description?: string;
}

export interface AssignRolePermissionPayload {
  permission_id: number;
}
