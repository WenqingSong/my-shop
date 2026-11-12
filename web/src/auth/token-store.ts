import { setAuthTokenProvider } from '@/api/client'
import type { AuthTokenResult } from '@/api/auth'

/**
 * 认证 Token 存储（仅内存）。
 *
 * 安全决策：
 * 后端只通过 JSON 返回 access/refresh token，没有 HttpOnly Cookie。
 * 把 30 天有效的 refresh token 写入 localStorage/sessionStorage 会让任意 XSS
 * 直接窃取长期凭证，因此这里不落任何 Web Storage：
 * - access token 与 refresh token 都只存在于当前 JS 运行时内存；
 * - 刷新页面/新开标签页后内存清空，需要重新登录；
 * - 页面内 access token 过期时，通过 POST /refresh 单飞刷新（见 refresh-session.ts）。
 *
 * 后端提供 refresh token（30 天、轮换、reuse 检测），但“安全持久化”需要
 * HttpOnly + Secure + SameSite Cookie 下发，当前契约不具备该能力，故不持久化。
 */
export interface AuthTokens {
  accessToken: string
  refreshToken: string
  /** access token 过期时间戳（毫秒），仅用于诊断/展示，不用于鉴权。 */
  expiresAt: number
}

let tokens: AuthTokens | null = null
let hasSessionSnapshot = false

const listeners = new Set<() => void>()

function emit(nextHasSession: boolean): void {
  if (nextHasSession === hasSessionSnapshot) {
    return
  }

  hasSessionSnapshot = nextHasSession

  for (const listener of listeners) {
    listener()
  }
}

/** 保存登录/刷新返回的双 token。 */
export function setAuthTokens(result: AuthTokenResult): void {
  tokens = {
    accessToken: result.access_token,
    refreshToken: result.refresh_token,
    expiresAt: Date.now() + result.expires_in * 1000,
  }
  emit(true)
}

/** 清空内存 Token（登出、刷新失败、会话失效）。 */
export function clearAuthTokens(): void {
  if (tokens === null) {
    return
  }

  tokens = null
  emit(false)
}

export function getAuthTokens(): AuthTokens | null {
  return tokens
}

export function getAccessToken(): string | null {
  return tokens?.accessToken ?? null
}

export function getRefreshToken(): string | null {
  return tokens?.refreshToken ?? null
}

export function hasAuthSession(): boolean {
  return tokens !== null
}

/** 供 useSyncExternalStore 订阅 Token 是否存在。 */
export function subscribeAuthSession(listener: () => void): () => void {
  listeners.add(listener)

  return () => {
    listeners.delete(listener)
  }
}

/** useSyncExternalStore 快照：只暴露布尔值，避免每次返回新对象导致无限渲染。 */
export function getAuthSessionSnapshot(): boolean {
  return hasSessionSnapshot
}

// 把内存 Token 接入现有 API Client 的 Token Provider 扩展点。
// getAccessToken 在每次请求时读取，登录/刷新后立即生效。
setAuthTokenProvider(getAccessToken)
