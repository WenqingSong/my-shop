/**
 * 用户账号状态 API（后台域）。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET /admin/users/:id/status`  `user:read`
 * - `PUT /admin/users/:id/status`  `user:status`（body 必带 reason）
 *
 * 后端没有用户列表/详情接口，因此本模块只有"按 id 查询状态"与"改状态"。
 */
import { http } from "@/utils/http";
import type { UpdateUserStatusPayload, UserStatusDTO } from "../types/user";

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
