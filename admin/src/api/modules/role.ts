/**
 * 角色管理 API。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET    /admin/roles`                            `role:list`（全量，无分页参数）
 * - `POST   /admin/roles`                            `role:create`
 * - `PUT    /admin/roles/:id`                        `role:update`
 * - `DELETE /admin/roles/:id`                        `role:delete`
 * - `POST   /admin/roles/:id/permissions`            `role:assign_permission`
 * - `DELETE /admin/roles/:id/permissions/:permission_id` `role:assign_permission`
 *
 * 后端**没有**角色详情、角色已有权限查询、角色状态字段。
 */
import { http } from "@/utils/http";
import type {
  AssignRolePermissionPayload,
  CreateRolePayload,
  RoleDTO,
  UpdateRolePayload
} from "../types/role";

/** 全部角色（按 id 升序，后端一次性返回） */
export function listRoles() {
  return http.get<{ items: RoleDTO[] }, undefined>("/admin/roles", undefined, {
    silent: true
  });
}

/** 创建角色 */
export function createRole(payload: CreateRolePayload) {
  return http.post<{ id: number }, typeof payload>(
    "/admin/roles",
    { data: payload },
    { silent: true }
  );
}

/** 更新角色（仅提交变更字段） */
export function updateRole(id: number, payload: UpdateRolePayload) {
  return http.request<RoleDTO>(
    "put",
    `/admin/roles/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除角色（后端事务级联删除 role_permissions 与 admin_roles） */
export function deleteRole(id: number) {
  return http.request<null>("delete", `/admin/roles/${id}`, undefined, {
    silent: true
  });
}

/** 为角色授予权限（后端幂等，重复授予无副作用） */
export function assignRolePermission(
  roleId: number,
  payload: AssignRolePermissionPayload
) {
  return http.post<null, typeof payload>(
    `/admin/roles/${roleId}/permissions`,
    { data: payload },
    { silent: true }
  );
}

/** 移除角色权限（后端幂等，即使本来没有该权限也返回成功） */
export function removeRolePermission(roleId: number, permissionId: number) {
  return http.request<null>(
    "delete",
    `/admin/roles/${roleId}/permissions/${permissionId}`,
    undefined,
    { silent: true }
  );
}
