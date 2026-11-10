/**
 * 管理员管理 API。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `POST   /admin/admins`                    `admin:create`
 * - `PUT    /admin/admins/:id/status`         `admin:disable`
 * - `DELETE /admin/admins/:id`                `admin:delete`
 * - `POST   /admin/admins/:id/roles`          `admin:assign_role`
 * - `DELETE /admin/admins/:id/roles/:role_id` `admin:assign_role`
 *
 * 后端**没有**管理员列表 / 详情接口，前端不提供任何"列表"或"资料修改"能力。
 */
import { http } from "@/utils/http";
import type {
  AssignAdminRolePayload,
  CreateAdminPayload,
  CreateAdminResult,
  UpdateAdminStatusPayload
} from "../types/admin";

/** 创建普通管理员（后端固定 is_super=0、status=1） */
export function createAdmin(payload: CreateAdminPayload) {
  return http.post<CreateAdminResult, typeof payload>(
    "/admin/admins",
    { data: payload },
    { silent: true }
  );
}

/** 禁用/启用管理员（1 启用 / 0 禁用） */
export function updateAdminStatus(
  id: number,
  payload: UpdateAdminStatusPayload
) {
  return http.request<null>(
    "put",
    `/admin/admins/${id}/status`,
    { data: payload },
    { silent: true }
  );
}

/** 删除管理员（后端事务级联删除 admin_roles） */
export function deleteAdmin(id: number) {
  return http.request<null>("delete", `/admin/admins/${id}`, undefined, {
    silent: true
  });
}

/** 为管理员分配角色（后端幂等：重复分配无副作用） */
export function assignAdminRole(id: number, payload: AssignAdminRolePayload) {
  return http.post<null, typeof payload>(
    `/admin/admins/${id}/roles`,
    { data: payload },
    { silent: true }
  );
}

/** 移除管理员角色（后端幂等） */
export function removeAdminRole(id: number, roleId: number) {
  return http.request<null>(
    "delete",
    `/admin/admins/${id}/roles/${roleId}`,
    undefined,
    { silent: true }
  );
}
