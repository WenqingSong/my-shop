import { authGetData, authPostData } from '@/api/auth-request'

/**
 * 当前登录态相关 API（需要有效 access token）。
 *
 * 事实来源：
 * - api/iam/v1/iam.go（MeReq/MeRes、LogoutReq/LogoutRes）
 * - internal/cmd/routes_frontend.go（/me 挂 Auth；/logout 挂 AuthSignatureOnly）
 */

/** GET /me 响应。 */
export interface CurrentUser {
  id: number
  username: string
}

/** GET /me：401 时由 authGetData 尝试一次 refresh 后重试。 */
export function fetchCurrentUser(signal?: AbortSignal): Promise<CurrentUser> {
  return authGetData<CurrentUser>('/me', { signal })
}

/**
 * POST /logout：撤销当前 sid 会话与对应 refresh family。
 * 成功时后端返回 data=null；重复登出也是幂等成功。
 */
export function logoutRequest(): Promise<null> {
  return authPostData<null>('/logout')
}
