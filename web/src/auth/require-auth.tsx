import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'

import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'
import { RouteLoading } from '@/components/common/route-loading'
import { useAuthStatus } from '@/auth/use-auth'

/**
 * 受保护路由：
 * - 认证状态加载中：展示路由级 Loading，不提前跳登录（避免刷新时闪一下登录页）；
 * - 未登录：跳 /login，并携带当前地址，登录成功后原路返回；
 * - /me 非 401 失败：展示重试，不把用户误判为未登录。
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { status, refetch } = useAuthStatus()
  const location = useLocation()

  if (status === 'loading') {
    return <RouteLoading />
  }

  if (status === 'error') {
    return (
      <PageContainer className="py-16">
        <ErrorState
          title="账户信息加载失败"
          description="当前登录状态暂时无法确认，请重试。"
          onRetry={() => void refetch()}
        />
      </PageContainer>
    )
  }

  if (status === 'unauthenticated') {
    return (
      <Navigate
        to="/login"
        replace
        state={{ from: `${location.pathname}${location.search}${location.hash}` }}
      />
    )
  }

  return <>{children}</>
}
