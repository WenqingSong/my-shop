import { Menu } from 'lucide-react'
import { Link } from 'react-router'

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

const placeholderItems = ['全部商品分类', '购物车', '登录 / 注册']

/** 移动端导航抽屉：复用 Dialog 的可访问性能力（focus trap / Esc / 遮罩关闭）。 */
export function MobileNav() {
  const isOpen = useUiStore((state) => state.isMobileNavOpen)
  const setOpen = useUiStore((state) => state.setMobileNavOpen)

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
            W0 阶段仅开放首页，商品与账户能力将在后续阶段接入。
          </DialogDescription>
        </div>
        <nav aria-label="移动端主导航" className="flex flex-col gap-0.5 p-2">
          <DialogClose asChild>
            <Link
              to="/"
              className="rounded-md px-3 py-2.5 text-sm font-medium text-foreground transition-colors hover:bg-gray-100"
            >
              首页
            </Link>
          </DialogClose>
          {placeholderItems.map((item) => (
            <span
              key={item}
              aria-disabled="true"
              className="cursor-not-allowed rounded-md px-3 py-2.5 text-sm text-muted-foreground"
            >
              {item}
            </span>
          ))}
        </nav>
      </DialogContent>
    </Dialog>
  )
}
