import { LayoutGrid, ShoppingCart, UserRound } from 'lucide-react'
import type { ReactNode } from 'react'
import { NavLink } from 'react-router'

import { PageContainer } from '@/components/common/page-container'
import { BrandLogo } from '@/components/layout/brand-logo'
import { MobileNav } from '@/components/layout/mobile-nav'
import { SearchBox } from '@/components/layout/search-box'
import { cn } from '@/utils/cn'

interface HeaderActionProps {
  icon: ReactNode
  label: string
  hint: string
}

/** 尚未接入的阶段功能：保持布局占位、明确禁用原因，不制造死链。 */
function HeaderAction({ icon, label, hint }: HeaderActionProps) {
  return (
    <button
      type="button"
      disabled
      title={hint}
      aria-label={`${label}（${hint}）`}
      className="inline-flex shrink-0 cursor-not-allowed items-center gap-2 whitespace-nowrap rounded-md px-2 py-2 text-sm font-medium text-muted-foreground/80"
    >
      {icon}
      <span className="hidden lg:inline">{label}</span>
    </button>
  )
}

function NavItem({ to, children }: { to: string; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      end
      className={({ isActive }) =>
        cn(
          'rounded-sm py-1 text-sm font-medium transition-colors duration-150',
          isActive ? 'text-primary' : 'text-muted-foreground hover:text-foreground',
        )
      }
    >
      {children}
    </NavLink>
  )
}

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-surface/95 backdrop-blur-sm">
      <PageContainer className="flex h-16 items-center gap-2 sm:gap-4 lg:h-20 lg:gap-8">
        <MobileNav />
        <BrandLogo className="shrink-0" />
        <SearchBox className="mx-auto hidden min-w-0 flex-1 md:flex md:max-w-[560px]" />
        <div className="ml-auto flex shrink-0 items-center gap-1 md:gap-2">
          <HeaderAction
            icon={<ShoppingCart className="size-5" aria-hidden="true" />}
            label="购物车"
            hint="购物车将在 W1 阶段开放"
          />
          <span className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />
          <HeaderAction
            icon={<UserRound className="size-5" aria-hidden="true" />}
            label="登录 / 注册"
            hint="账号体系将在后续阶段接入"
          />
        </div>
      </PageContainer>

      <div className="border-t border-border">
        <PageContainer className="flex h-12 items-center gap-4 sm:gap-6">
          <button
            type="button"
            disabled
            title="商品分类将在 W1 阶段接入"
            className="inline-flex cursor-not-allowed items-center gap-1.5 text-sm font-medium text-muted-foreground"
          >
            <LayoutGrid className="size-4" aria-hidden="true" />
            全部商品分类
          </button>
          <span className="h-4 w-px bg-border" aria-hidden="true" />
          <nav aria-label="主导航" className="flex items-center gap-5">
            <NavItem to="/">首页</NavItem>
          </nav>
          <span className="ml-auto hidden items-center gap-1.5 rounded-full bg-primary-soft px-2.5 py-1 text-[11px] font-medium text-primary sm:inline-flex">
            <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
            W0 工程基线
          </span>
        </PageContainer>
      </div>

      <div className="border-t border-border px-4 py-3 md:hidden">
        <SearchBox />
      </div>
    </header>
  )
}
