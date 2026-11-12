/**
 * 后台用户管理 DTO（`api/iam/v1/iam.go` + `internal/logic/iam/user_status.go`）。
 *
 * 后端真实能力：
 * - `GET /admin/users`         后台分页列表（`user:read`，按 id 降序）
 * - `GET /admin/users/:id`     后台详情（`user:read`，不存在返回 2015）
 * - `GET /admin/users/:id/status` 账号状态（`user:read`）
 * - `PUT /admin/users/:id/status` 禁用/启用（`user:status`，body 必带 reason）
 *
 * 字段白名单（后端只查询 `id / username / status / created_at`）：
 * 不包含 `password_hash`、`auth_epoch` 等认证材料，也没有昵称、手机号、
 * 最后登录时间等 `users` 表不存在的字段，前端不得虚构。
 */

/** 后台用户视图（列表与详情共用同一字段集） */
export interface AdminUserDTO {
  id: number;
  username: string;
  /** 1 启用 / 0 禁用 */
  status: 0 | 1;
  /** 注册时间（后端 `gtime` 字符串，原样展示） */
  created_at: string;
}

/**
 * 列表查询参数：只有后端真实支持的 `page` / `size`。
 *
 * 后端约定：`page` 默认 1、`size` 默认 20、上限 100；越界或非整数返回 400 (1001)。
 * 后端**没有**关键词/状态筛选参数，前端不得传不存在的字段。
 */
export interface AdminUserListQuery {
  page?: number;
  size?: number;
}

/** 列表响应（分页字段由后端回显，前端以响应为准） */
export interface AdminUserListResult {
  items: AdminUserDTO[];
  total: number;
  page: number;
  size: number;
}

/** 账号状态 DTO（`GET /admin/users/:id/status`，只有 id + status） */
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
