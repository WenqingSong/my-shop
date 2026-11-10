/**
 * 权限管理 API。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET    /admin/permissions`       `permission:list`（全部权限定义，无分页）
 * - `POST   /admin/permissions`       `permission:create`
 * - `PUT    /admin/permissions/:id`   `permission:update`
 * - `DELETE /admin/permissions/:id`   `permission:delete`
 *
 * 重要：列表返回的是**系统全部权限定义**，不能当作"当前管理员有效权限"，
 * 也不能当作"某个角色已有的权限"。
 */
import { http } from "@/utils/http";
import type {
  CreatePermissionPayload,
  PermissionDTO,
  UpdatePermissionPayload
} from "../types/permission";

/** 全部权限定义（按 id 升序） */
export function listPermissions() {
  return http.get<{ items: PermissionDTO[] }, undefined>(
    "/admin/permissions",
    undefined,
    { silent: true }
  );
}

/** 创建权限 */
export function createPermission(payload: CreatePermissionPayload) {
  return http.post<{ id: number }, typeof payload>(
    "/admin/permissions",
    { data: payload },
    { silent: true }
  );
}

/** 更新权限（仅提交变更字段） */
export function updatePermission(id: number, payload: UpdatePermissionPayload) {
  return http.request<PermissionDTO>(
    "put",
    `/admin/permissions/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除权限（后端事务级联删除 role_permissions） */
export function deletePermission(id: number) {
  return http.request<null>("delete", `/admin/permissions/${id}`, undefined, {
    silent: true
  });
}
