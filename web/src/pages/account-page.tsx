import { Link } from 'react-router'

import { useAuthStatus, useLogout } from '@/auth/use-auth'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'

/**
 * 账户页（W2-01 最小受保护页面）。
 *
 * 只展示当前登录用户与退出入口，用于验证认证状态与路由保护；
 * 个人资料编辑、收货地址、收藏、订单等能力属于后续任务，本期不实现。
 */
export function AccountPage() {
  useDocumentTitle('账户 | SurgeCart 商城')

  const { user } = useAuthStatus()
  const logout = useLogout()

  return (
    <PageContainer width="narrow" className="py-10 lg:py-14">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">账户</h1>
      <p className="mt-2 text-sm text-muted-foreground">查看当前登录状态与管理本次登录会话。</p>

      <section
        aria-labelledby="account-summary"
        className="mt-6 rounded-2xl border border-border bg-surface p-6 shadow-card"
      >
        <h2 id="account-summary" className="text-base font-semibold text-foreground">
          当前登录用户
        </h2>
        <dl className="mt-4 grid gap-2 text-sm sm:grid-cols-[96px_minmax(0,1fr)]">
          <dt className="text-muted-foreground">用户名</dt>
          <dd className="font-medium text-foreground">{user?.username ?? '—'}</dd>
          <dt className="text-muted-foreground">用户 ID</dt>
          <dd className="tabular-nums text-foreground">{user?.id ?? '—'}</dd>
        </dl>

        <div className="mt-6 flex flex-wrap items-center gap-3">
          <Button variant="outline" onClick={() => logout.mutate()} isLoading={logout.isPending}>
            退出登录
          </Button>
          <Button asChild variant="ghost">
            <Link to="/products">继续浏览商品</Link>
          </Button>
        </div>
      </section>

      <p className="mt-6 text-xs leading-5 text-muted-foreground">
        个人资料、收货地址、收藏与订单将在后续任务开放。当前登录状态仅保存在本页面内存中，刷新页面后需要重新登录。
      </p>
    </PageContainer>
  )
}
