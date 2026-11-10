import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSyncExternalStore } from 'react'
import { useNavigate } from 'react-router'

import { fetchCurrentUser, logoutRequest, type CurrentUser } from '@/api/account'
import { loginRequest, registerRequest, type RegisterResult } from '@/api/auth'
import { ApiError } from '@/api/client'
import { queryKeys } from '@/api/query-keys'
import {
  clearAuthTokens,
  getAuthSessionSnapshot,
  setAuthTokens,
  subscribeAuthSession,
} from '@/auth/token-store'

export type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated' | 'error'

export const AUTH_ME_STALE_TIME = 5 * 60_000

/**
 * 统一认证状态（只读）：
 * - Token 是否存在由内存 Token Store + useSyncExternalStore 提供；
 * - 当前用户信息由 TanStack Query 的 /me 查询提供，多个组件共享同一个 key，天然去重；
 * - 无 Token 时不发 /me 请求，避免匿名用户产生无意义请求。
 */
export function useAuthStatus() {
  const hasSession = useSyncExternalStore(
    subscribeAuthSession,
    getAuthSessionSnapshot,
    getAuthSessionSnapshot,
  )

  const meQuery = useQuery({
    queryKey: queryKeys.auth.me(),
    queryFn: ({ signal }) => fetchCurrentUser(signal),
    enabled: hasSession,
    staleTime: AUTH_ME_STALE_TIME,
    // 401 已经由 authGetData 做过一次 refresh + 重试；仍失败说明会话不可恢复，不再重试。
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.isUnauthorized) && failureCount < 1,
  })

  const user = hasSession ? (meQuery.data ?? null) : null
  const status: AuthStatus = !hasSession
    ? 'unauthenticated'
    : user
      ? 'authenticated'
      : meQuery.isError
        ? 'error'
        : 'loading'

  return {
    status,
    user,
    hasSession,
    error: meQuery.error,
    refetch: meQuery.refetch,
  }
}

export interface LoginValues {
  username: string
  password: string
}

export type RegisterValues = LoginValues

/**
 * 登录：写入内存 Token 后立即请求 /me，确保跳转前用户信息已就绪。
 * 若 /me 失败（例如账号刚被禁用），回滚 Token，避免 Header 处于半登录状态。
 */
export function useLogin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (values: LoginValues): Promise<CurrentUser> => {
      const tokens = await loginRequest(values)
      setAuthTokens(tokens)

      try {
        return await queryClient.fetchQuery({
          queryKey: queryKeys.auth.me(),
          queryFn: ({ signal }) => fetchCurrentUser(signal),
          staleTime: AUTH_ME_STALE_TIME,
        })
      } catch (error) {
        clearAuthTokens()
        queryClient.removeQueries({ queryKey: queryKeys.auth.all })
        throw error
      }
    },
  })
}

/** 注册：后端只返回 id/username，不签发 Token；成功后由页面跳转登录页。 */
export function useRegister() {
  return useMutation<RegisterResult, Error, RegisterValues>({
    mutationFn: (values) => registerRequest(values),
  })
}

/**
 * 退出登录：
 * - 先尝试调用 POST /logout 撤销服务端会话与 refresh family；
 * - 无论服务端是否成功（网络中断/已过期），本地都必须清空 Token 与用户缓存，
 *   保证 UI 立即回到未登录状态；服务端残留会话最多存活一个 session TTL。
 */
export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  return useMutation({
    mutationFn: async () => {
      try {
        await logoutRequest()
      } catch {
        // 本地登出优先，忽略服务端错误（文档记录该失败语义）。
      } finally {
        clearAuthTokens()
        queryClient.removeQueries({ queryKey: queryKeys.auth.all })
      }
    },
    onSuccess: () => {
      navigate('/', { replace: true })
    },
  })
}
