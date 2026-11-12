/**
 * 用户管理 API（后台域）。
 *
 * 真实路由（`internal/cmd/routes_admin.go` + `api/iam/v1/iam.go`）：
 * - `GET  /admin/users`                 `user:read`  分页列表（id 降序）
 * - `GET  /admin/users/:id`             `user:read`  详情（不存在 2015）
 * - `GET  /admin/users/:id/status`      `user:read`  账号状态
 * - `PUT  /admin/users/:id/status`      `user:status`（body 必带 reason）
 *
 * 列表参数只有 `page` / `size`：后端没有关键词、状态筛选或排序参数，
 * 前端不得传不存在的查询字段，也不得在本地伪造分页。
 */
import { http } from "@/utils/http";
import type {
  AdminUserDTO,
  AdminUserListQuery,
  AdminUserListResult,
  UpdateUserStatusPayload,
  UserStatusDTO
} from "../types/user";

/** 后台用户分页列表（默认 page=1、size=20，上限 100） */
export function listUsers(query: AdminUserListQuery = {}) {
  return http.get<AdminUserListResult, undefined>(
    "/admin/users",
    { params: query },
    { silent: true }
  );
}

/** 后台用户详情；目标不存在时后端返回 2015（404） */
export function getUser(id: number) {
  return http.get<AdminUserDTO, undefined>(`/admin/users/${id}`, undefined, {
    silent: true
  });
}

/** 查询目标用户账号状态 */
export function getUserStatus(id: number) {
  return http.get<UserStatusDTO, undefined>(
    `/admin/users/${id}/status`,
    undefined,
    { silent: true }
  );
}

/**
 * 禁用/启用目标用户。
 *
 * `reason` 会写入 `user_status_audits`，后端要求非空且 ≤255 字符；
 * 禁用会递增 `auth_epoch` 并撤销该用户全部 refresh token（后端保证）。
 */
export function updateUserStatus(id: number, payload: UpdateUserStatusPayload) {
  return http.request<null>(
    "put",
    `/admin/users/${id}/status`,
    { data: payload },
    { silent: true }
  );
}
