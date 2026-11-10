import { Loader2, LogOut, UserRound } from 'lucide-react'
import { Link } from 'react-router'

import { useAuthStatus, useLogout } from '@/auth/use-auth'
import { Button } from '@/components/ui/button'

const actionClass =
  'inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded-md px-2 py-2 text-sm font-medium text-muted-foreground transition-colors duration-150 hover:bg-gray-100 hover:text-foreground'

/**
 * Header 认证入口：
 * - 未登录：登录 / 注册链接；
 * - 已登录：用户名（进入账户页）+ 退出登录；
 * - 加载中 / 加载失败：不显示错误的“未登录”状态，避免误判。
 */
export function HeaderAuthControl() {
  const { status, user, refetch } = useAuthStatus()
  const logout = useLogout()

  if (status === 'loading') {
    return (
      <span
        aria-live="polite"
        className="inline-flex items-center gap-2 px-2 py-2 text-sm text-muted-foreground"
      >
        <Loader2 className="size-5 animate-spin" aria-hidden="true" />
        <span className="hidden lg:inline">账户加载中</span>
      </span>
    )
  }

  if (status === 'error') {
    return (
      <button
        type="button"
        onClick={() => void refetch()}
        aria-label="重新加载账户信息"
        className={actionClass}
      >
        <UserRound className="size-5" aria-hidden="true" />
        <span className="hidden lg:inline">账户重试</span>
      </button>
    )
  }

  if (status === 'authenticated' && user) {
    return (
      <div className="flex items-center gap-0.5">
        <Link to="/account" aria-label={`账户：${user.username}`} className={actionClass}>
          <UserRound className="size-5" aria-hidden="true" />
          <span className="hidden max-w-[96px] truncate lg:inline">{user.username}</span>
        </Link>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => logout.mutate()}
          isLoading={logout.isPending}
          aria-label="退出登录"
          title="退出登录"
          className="gap-1.5 px-2"
        >
          <LogOut aria-hidden="true" />
          <span className="hidden lg:inline">退出</span>
        </Button>
      </div>
    )
  }

  return (
    <Link to="/login" aria-label="登录 / 注册" className={actionClass}>
      <UserRound className="size-5" aria-hidden="true" />
      <span className="hidden lg:inline">登录 / 注册</span>
    </Link>
  )
}
