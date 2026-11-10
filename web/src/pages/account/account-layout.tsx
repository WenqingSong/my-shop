import { Heart, MapPin, Package, ReceiptText, UserRound, LayoutDashboard } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'

import { PageContainer } from '@/components/common/page-container'
import { cn } from '@/utils/cn'

const accountNavItems = [
  { to: '/account', label: '账户概览', icon: LayoutDashboard, end: true },
  // 订单详情 /account/orders/:id 属于订单中心，因此不设置 end，保证子路由也高亮。
  { to: '/account/orders', label: '我的订单', icon: Package, end: false },
  // 退款详情 /account/refunds/:id 同理，保证子路由高亮。
  { to: '/account/refunds', label: '我的退款', icon: ReceiptText, end: false },
  { to: '/account/profile', label: '个人资料', icon: UserRound, end: false },
  { to: '/account/addresses', label: '收货地址', icon: MapPin, end: false },
  { to: '/account/favorites', label: '我的收藏', icon: Heart, end: false },
]

/**
 * 个人中心统一布局（/account/*）。
 * 路由守卫由 router 的 RequireAuth 统一包裹，子页面只关注自身内容。
 */
export function AccountLayout() {
  return (
    <PageContainer className="py-8 lg:py-10">
      <div className="flex flex-col gap-2">
        <p className="text-xs font-medium uppercase tracking-[0.18em] text-primary">Account</p>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">账户</h1>
        <p className="text-sm leading-6 text-muted-foreground">
          查看订单与退款、管理个人资料、收货地址与收藏商品。
        </p>
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-8">
        <nav aria-label="个人中心导航" className="min-w-0">
          <ul className="-mx-4 flex gap-1 overflow-x-auto px-4 pb-1 lg:mx-0 lg:flex-col lg:rounded-xl lg:border lg:border-border lg:bg-surface lg:p-2 lg:shadow-card">
            {accountNavItems.map((item) => {
              const Icon = item.icon

              return (
                <li key={item.to} className="shrink-0">
                  <NavLink
                    to={item.to}
                    end={item.end}
                    className={({ isActive }) =>
                      cn(
                        'inline-flex h-10 items-center gap-2 whitespace-nowrap rounded-md px-3 text-sm font-medium transition-colors duration-150',
                        'lg:flex lg:w-full',
                        isActive
                          ? 'bg-primary-soft text-primary'
                          : 'text-muted-foreground hover:bg-gray-100 hover:text-foreground',
                      )
                    }
                  >
                    <Icon className="size-4" aria-hidden="true" />
                    {item.label}
                  </NavLink>
                </li>
              )
            })}
          </ul>
        </nav>

        <div className="min-w-0">
          <Outlet />
        </div>
      </div>
    </PageContainer>
  )
}
