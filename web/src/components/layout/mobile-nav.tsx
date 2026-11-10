import { Menu, ShoppingCart } from 'lucide-react'
import { Link } from 'react-router'

import { useAuthStatus, useLogout } from '@/auth/use-auth'
import { useCartCount } from '@/hooks/use-cart'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/stores/ui-store'
import { cn } from '@/utils/cn'

const navigationItems = [
  { to: '/', label: '首页' },
  { to: '/products', label: '全部商品' },
]

const linkClass =
  'rounded-md px-3 py-2.5 text-sm font-medium text-foreground transition-colors hover:bg-gray-100'

/** 移动端导航抽屉：复用 Dialog 的可访问性能力（focus trap / Esc / 遮罩关闭）。 */
export function MobileNav() {
  const isOpen = useUiStore((state) => state.isMobileNavOpen)
  const setOpen = useUiStore((state) => state.setMobileNavOpen)
  const { status, user } = useAuthStatus()
  const logout = useLogout()
  const cartCount = useCartCount()

  const handleLogout = () => {
    setOpen(false)
    logout.mutate()
  }

  return (
    <Dialog open={isOpen} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="lg:hidden" aria-label="打开商城导航">
          <Menu className="size-5" aria-hidden="true" />
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-sm gap-0 overflow-hidden p-0">
        <div className="border-b border-border px-5 py-4">
          <DialogTitle className="text-base">商城导航</DialogTitle>
          <DialogDescription className="mt-1 text-xs leading-5">
            商品浏览已开放；登录后可使用账户功能。
          </DialogDescription>
        </div>

        <nav aria-label="移动端主导航" className="flex flex-col gap-0.5 p-2">
          {navigationItems.map((item) => (
            <DialogClose asChild key={item.to}>
              <Link to={item.to} className={linkClass}>
                {item.label}
              </Link>
            </DialogClose>
          ))}

          <DialogClose asChild>
            <Link
              to="/cart"
              className={cn(linkClass, 'flex items-center justify-between gap-2')}
              aria-label={cartCount > 0 ? `购物车，${String(cartCount)} 件商品` : '购物车'}
            >
              <span className="inline-flex items-center gap-2">
                <ShoppingCart className="size-4" aria-hidden="true" />
                购物车
              </span>
              {cartCount > 0 ? (
                <span className="inline-flex min-w-5 items-center justify-center rounded-full bg-promo px-1.5 py-0.5 text-[11px] font-semibold leading-none tabular-nums text-primary-foreground">
                  {cartCount > 99 ? '99+' : cartCount}
                </span>
              ) : null}
            </Link>
          </DialogClose>

          <div className="mt-2 border-t border-border px-1 pt-2">
            {status === 'loading' ? (
              <span className="block px-2 py-2.5 text-sm text-muted-foreground" aria-live="polite">
                账户加载中
              </span>
            ) : null}

            {status === 'error' ? (
              <span className="block px-2 py-2.5 text-sm text-muted-foreground">
                账户信息加载失败，请稍后重试
              </span>
            ) : null}

            {status === 'authenticated' && user ? (
              <>
                <DialogClose asChild>
                  <Link to="/account" className={linkClass}>
                    账户（{user.username}）
                  </Link>
                </DialogClose>
                <button
                  type="button"
                  onClick={handleLogout}
                  disabled={logout.isPending}
                  className="block w-full rounded-md px-3 py-2.5 text-left text-sm font-medium text-muted-foreground transition-colors hover:bg-gray-100 hover:text-foreground disabled:opacity-50"
                >
                  退出登录
                </button>
              </>
            ) : null}

            {status === 'unauthenticated' ? (
              <>
                <DialogClose asChild>
                  <Link to="/login" className={linkClass}>
                    登录
                  </Link>
                </DialogClose>
                <DialogClose asChild>
                  <Link to="/register" className={linkClass}>
                    注册
                  </Link>
                </DialogClose>
              </>
            ) : null}
          </div>
        </nav>
      </DialogContent>
    </Dialog>
  )
}
