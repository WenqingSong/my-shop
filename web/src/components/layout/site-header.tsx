import { LayoutGrid } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router'

import { PageContainer } from '@/components/common/page-container'
import { BrandLogo } from '@/components/layout/brand-logo'
import { CartEntry } from '@/components/layout/cart-entry'
import { HeaderAuthControl } from '@/components/layout/header-auth'
import { MobileNav } from '@/components/layout/mobile-nav'
import { SearchBox } from '@/components/layout/search-box'
import { cn } from '@/utils/cn'

function NavItem({ to, children, end = false }: { to: string; children: ReactNode; end?: boolean }) {
  return (
    <NavLink
      to={to}
      end={end}
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
          <CartEntry showLabel />
          <span className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />
          <HeaderAuthControl />
        </div>
      </PageContainer>

      <div className="border-t border-border">
        <PageContainer className="flex h-12 items-center gap-4 sm:gap-6">
          <Link
            to="/products"
            className="inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-primary"
          >
            <LayoutGrid className="size-4" aria-hidden="true" />
            全部商品分类
          </Link>
          <span className="h-4 w-px bg-border" aria-hidden="true" />
          <nav aria-label="主导航" className="flex items-center gap-5">
            <NavItem to="/" end>
              首页
            </NavItem>
            <NavItem to="/products">全部商品</NavItem>
          </nav>
        </PageContainer>
      </div>

      <div className="border-t border-border px-4 py-3 md:hidden">
        <SearchBox />
      </div>
    </header>
  )
}
