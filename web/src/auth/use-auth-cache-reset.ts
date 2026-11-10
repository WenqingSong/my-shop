import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useSyncExternalStore } from 'react'

import { queryKeys } from '@/api/query-keys'
import { getAuthSessionSnapshot, subscribeAuthSession } from '@/auth/token-store'

/**
 * 会话结束后清理用户私有缓存。
 *
 * 触发路径：
 * - 主动退出（useLogout 也会立即清理）；
 * - refresh 失败导致 Token 被清空（例如会话被服务端撤销）。
 *
 * 用户的地址/收藏缓存绝不能留给下一个登录用户，否则同一标签页切换账号会看到上一位用户的
 * 私有数据。这里监听“有会话 → 无会话”的转变，统一 removeQueries。
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
      queryClient.removeQueries({ queryKey: queryKeys.auth.all })
      queryClient.removeQueries({ queryKey: queryKeys.addresses.all })
      queryClient.removeQueries({ queryKey: queryKeys.favorites.all })
    }

    previousHasSession.current = hasSession
  }, [hasSession, queryClient])
}
