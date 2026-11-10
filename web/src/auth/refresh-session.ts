import { refreshTokens } from '@/api/auth'
import { clearAuthTokens, getRefreshToken, setAuthTokens } from '@/auth/token-store'

/**
 * access token 刷新（单飞）。
 *
 * 后端 refresh 采用轮换 + reuse 检测：同一个 refresh token 被并发提交两次，
 * 后到者会被判定为重放并撤销该用户全部会话。因此前端必须保证：
 * - 任意时刻最多一个 refresh 请求在途；
 * - 刷新成功后立刻替换内存中的双 token；
 * - 若刷新期间用户已退出或重新登录，丢弃旧请求结果，避免“登出后又被刷新回来”。
 */
let refreshPromise: Promise<boolean> | null = null

async function performRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken()

  if (!refreshToken) {
    return false
  }

  try {
    const result = await refreshTokens(refreshToken)

    // 刷新期间 token 被替换（重新登录）或清空（登出）：旧结果必须丢弃。
    if (getRefreshToken() !== refreshToken) {
      return false
    }

    setAuthTokens(result)
    return true
  } catch {
    // 仅当失败的仍是当前 refresh token 时才清空，避免旧请求清掉新会话。
    if (getRefreshToken() === refreshToken) {
      clearAuthTokens()
    }
    return false
  }
}

/** 尝试刷新 access token；并发调用共享同一个在途请求。 */
export function refreshSession(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = performRefresh().finally(() => {
      refreshPromise = null
    })
  }

  return refreshPromise
}

/** 测试隔离：清空在途刷新状态。 */
export function resetRefreshStateForTests(): void {
  refreshPromise = null
}
