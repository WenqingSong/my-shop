import { Heart, MapPin, Package, UserRound } from 'lucide-react'
import { Link } from 'react-router'

import { useAuthStatus, useLogout } from '@/auth/use-auth'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'

const entries = [
  {
    to: '/account/orders',
    title: '我的订单',
    description: '按状态查看订单、商品明细、金额与收货快照。',
    icon: Package,
  },
  {
    to: '/account/profile',
    title: '个人资料',
    description: '查看当前账号的用户名与用户 ID。',
    icon: UserRound,
  },
  {
    to: '/account/addresses',
    title: '收货地址',
    description: '新增、编辑、删除收货地址，设置默认地址。',
    icon: MapPin,
  },
  {
    to: '/account/favorites',
    title: '我的收藏',
    description: '查看已收藏商品，取消不再关注的收藏。',
    icon: Heart,
  },
]

export function AccountOverviewPage() {
  useDocumentTitle('账户概览 | SurgeCart 商城')

  const { user } = useAuthStatus()
  const logout = useLogout()

  return (
    <div className="flex flex-col gap-6">
      <section
        aria-labelledby="account-summary"
        className="rounded-2xl border border-border bg-surface p-6 shadow-card"
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

      <section aria-labelledby="account-entries" className="flex flex-col gap-4">
        <h2 id="account-entries" className="text-base font-semibold text-foreground">
          账户功能
        </h2>
        <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {entries.map((entry) => {
            const Icon = entry.icon

            return (
              <li key={entry.to}>
                <Link
                  to={entry.to}
                  className="flex h-full flex-col rounded-xl border border-border bg-surface p-5 shadow-card transition-colors duration-150 hover:border-brand-300"
                >
                  <span className="grid size-10 place-items-center rounded-lg bg-primary-soft text-primary">
                    <Icon className="size-5" aria-hidden="true" />
                  </span>
                  <span className="mt-4 text-sm font-semibold text-foreground">{entry.title}</span>
                  <span className="mt-1.5 text-sm leading-6 text-muted-foreground">
                    {entry.description}
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      </section>

      <p className="text-xs leading-5 text-muted-foreground">
        当前登录状态仅保存在页面内存中，刷新页面后需要重新登录。
      </p>
    </div>
  )
}
