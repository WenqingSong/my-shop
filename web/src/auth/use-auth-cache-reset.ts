import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useSyncExternalStore } from 'react'

import { clearUserScopedQueries } from '@/auth/user-cache'
import { getAuthSessionSnapshot, subscribeAuthSession } from '@/auth/token-store'

/**
 * 会话结束后清理账号私有缓存。
 *
 * 触发路径：
 * - 主动退出（useLogout 也会立即清理，两者使用同一份清单）；
 * - refresh 失败导致 Token 被清空（例如会话被服务端撤销）；
 * - 401 重试仍失败后 `clearAuthTokens()`。
 *
 * 用户的订单/退款/优惠券/地址/收藏/购物车缓存绝不能留给下一个登录用户，
 * 否则同一标签页切换账号会看到上一位用户的私有数据。
 */
export function useAuthCacheReset(): void {
  const queryClient = useQueryClient()
  const hasSession = useSyncExternalStore(
    subscribeAuthSession,
    getAuthSessionSnapshot,
    getAuthSessionSnapshot,
  )
  const previousHasSession = useRef(hasSession)

  useEffect(() => {
    if (previousHasSession.current && !hasSession) {
      clearUserScopedQueries(queryClient)
    }

    previousHasSession.current = hasSession
  }, [hasSession, queryClient])
}
