import { ApiError, type RequestOptions } from '@/api/client'
import { getData, postData } from '@/api/envelope'
import { refreshSession } from '@/auth/refresh-session'
import { clearAuthTokens } from '@/auth/token-store'

/**
 * 需要登录态的请求包装：
 * - 首次 401 → 尝试一次 refresh → 成功后用新 access token 重试一次；
 * - refresh 失败 → 清空内存 Token，让 Auth 状态回到未登录；
 * - 重试仍 401 → 清空 Token 并抛出，避免 UI 停在“已登录”假象。
 *
 * 公开接口（商品/分类/登录/注册）不使用本包装，避免无意义的 refresh。
 */
async function with401Refresh<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run()
  } catch (error) {
    if (!(error instanceof ApiError) || !error.isUnauthorized) {
      throw error
    }

    const refreshed = await refreshSession()

    if (!refreshed) {
      clearAuthTokens()
      throw error
    }

    try {
      return await run()
    } catch (retryError) {
      if (retryError instanceof ApiError && retryError.isUnauthorized) {
        clearAuthTokens()
      }
      throw retryError
    }
  }
}

/** 带 401 自动刷新的 GET。 */
export function authGetData<T>(
  path: string,
  options?: Omit<RequestOptions, 'method' | 'body'>,
): Promise<T> {
  return with401Refresh(() => getData<T>(path, options))
}

/** 带 401 自动刷新的 POST。 */
export function authPostData<T>(
  path: string,
  body?: unknown,
  options?: Omit<RequestOptions, 'method' | 'body'>,
): Promise<T> {
  return with401Refresh(() => postData<T>(path, body, options))
}
