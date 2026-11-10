/**
 * 用户账号状态 DTO（`api/iam/v1/iam.go` + `internal/logic/iam/user_status.go`）。
 *
 * 后端真实能力（A5 复核）：
 * - 只有 `GET /admin/users/:id/status` 与 `PUT /admin/users/:id/status`；
 * - **没有**用户列表 / 用户详情 / 搜索 / 分页 / 删除接口；
 * - `users` 表只有 `id / username / password_hash / status / auth_epoch / created_at / updated_at`，
 *   没有昵称、手机号、最后登录时间等字段。
 *
 * 因此本阶段不展示、也不请求任何用户敏感认证数据（密码哈希、auth_epoch 等）。
 */
export interface UserStatusDTO {
  id: number;
  /** 1 启用 / 0 禁用 */
  status: 0 | 1;
}

/** 禁用/启用用户请求：`reason` 后端必填（trim 后非空、≤255 字符） */
export interface UpdateUserStatusPayload {
  status: 0 | 1;
  reason: string;
}
