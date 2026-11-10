import { postData } from '@/api/envelope'

/**
 * 用户认证 API（公开接口，不带 401 自动刷新，避免登录失败触发 refresh 递归）。
 *
 * 事实来源：
 * - api/iam/v1/iam.go（RegisterReq/Res、LoginReq/Res、RefreshReq/Res）
 * - internal/cmd/routes_frontend.go（/register、/login、/refresh 均无 Auth 中间件）
 * - internal/logic/iam/iam.go、internal/logic/iam/refresh.go
 */

export interface LoginPayload {
  username: string
  password: string
}

export interface RegisterPayload {
  username: string
  password: string
}

export interface RegisterResult {
  id: number
  username: string
}

/**
 * 登录/刷新返回的双 token。
 * - access_token：HS256 JWT，1 小时有效，通过 Authorization: Bearer 使用；
 * - refresh_token：64 位 hex 明文，仅本次响应出现，30 天绝对有效，轮换后旧 token 立即失效；
 * - token_type 恒为 "Bearer"；expires_in 为 access token 有效期（秒）。
 */
export interface AuthTokenResult {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
}

/** POST /login */
export function loginRequest(payload: LoginPayload): Promise<AuthTokenResult> {
  return postData<AuthTokenResult>('/login', payload)
}

/** POST /register（成功只返回 id/username，不签发 token） */
export function registerRequest(payload: RegisterPayload): Promise<RegisterResult> {
  return postData<RegisterResult>('/register', payload)
}

/**
 * POST /refresh（refresh token 自身即凭证）
 * 后端会轮换 refresh token，旧 token 再次使用会触发 reuse 全量撤销。
 */
export function refreshTokens(refreshToken: string): Promise<AuthTokenResult> {
  return postData<AuthTokenResult>('/refresh', { refresh_token: refreshToken })
}
