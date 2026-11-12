/**
 * 权限 DTO（`api/admin/v1/admin.go`）。
 *
 * 后端真实字段只有 `id / code / name / description`：
 * - **没有**"所属业务域"字段，前端展示的 domain 由真实 `code` 的前缀推导；
 * - **没有**权限树 / 菜单数据；
 * - `GET /admin/permissions` 返回的是**系统全部权限定义**，
 *   不是"当前管理员的有效权限"，也不是"某个角色拥有的权限"。
 */
export interface PermissionDTO {
  id: number;
  /** 稳定唯一 key，「资源:动作」，例如 `product:create` */
  code: string;
  name: string;
  description: string;
}

export interface CreatePermissionPayload {
  code: string;
  name: string;
  description: string;
}

/** 更新权限：只提交需要变更的字段（后端为指针字段） */
export interface UpdatePermissionPayload {
  code?: string;
  name?: string;
  description?: string;
}
