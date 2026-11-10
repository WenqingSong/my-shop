import { Menu } from 'lucide-react'
import { Link } from 'react-router'

import { useAuthStatus, useLogout } from '@/auth/use-auth'
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

          <span
            aria-disabled="true"
            className="cursor-not-allowed rounded-md px-3 py-2.5 text-sm text-muted-foreground"
          >
            购物车（W3 开放）
          </span>

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
