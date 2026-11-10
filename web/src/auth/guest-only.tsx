import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'

import { readAuthLocationState, resolveRedirectTarget } from '@/auth/redirect'
import { useAuthStatus } from '@/auth/use-auth'

/**
 * 仅未登录用户可访问的路由（登录/注册）。
 * 已登录时跳回来源页或首页，避免重复登录。
 *
 * 注意：status=loading 时仍渲染表单，否则登录成功写入 Token 后的 /me 加载
 * 会把正在提交的登录页卸载，导致表单状态与错误提示丢失。
 */
export function GuestOnly({ children }: { children: ReactNode }) {
  const { status } = useAuthStatus()
  const location = useLocation()

  if (status === 'authenticated') {
    const { from } = readAuthLocationState(location.state)
    return <Navigate to={resolveRedirectTarget(from)} replace />
  }

  return <>{children}</>
}
