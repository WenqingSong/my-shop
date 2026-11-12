import { ShoppingCart } from 'lucide-react'
import { Link } from 'react-router'

import { useCartCount } from '@/hooks/use-cart'
import { cn } from '@/utils/cn'

/**
 * Header 购物车入口。
 *
 * 数量来自 GET /cart 的共享缓存（queryKeys.cart.list），与购物车页面同一份数据，
 * 不会为徽标单独发请求；未登录时 useCartCount 直接返回 0，不发起任何请求。
 * 徽标数字为「商品件数之和」，仅作提示，不代表订单金额或结算结果。
 */
export function CartEntry({ className, showLabel = false }: { className?: string; showLabel?: boolean }) {
  const count = useCartCount()
  const accessibleLabel = count > 0 ? `购物车，${String(count)} 件商品` : '购物车'

  return (
    <Link
      to="/cart"
      aria-label={accessibleLabel}
      className={cn(
        'relative inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded-md px-2 py-2',
        'text-sm font-medium text-muted-foreground transition-colors duration-150',
        'hover:bg-gray-100 hover:text-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
        className,
      )}
    >
      <span className="relative inline-flex">
        <ShoppingCart className="size-5" aria-hidden="true" />
        {count > 0 ? (
          <span
            aria-hidden="true"
            className="absolute -right-2 -top-2 grid h-4 min-w-4 place-items-center rounded-full bg-promo px-1 text-[10px] font-semibold leading-none tabular-nums text-primary-foreground"
          >
            {count > 99 ? '99+' : count}
          </span>
        ) : null}
      </span>
      {showLabel ? <span className="hidden lg:inline">购物车</span> : null}
    </Link>
  )
}
