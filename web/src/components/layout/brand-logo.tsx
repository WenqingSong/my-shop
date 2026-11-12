import { ShoppingBag } from 'lucide-react'
import { Link } from 'react-router'

import { cn } from '@/utils/cn'

export interface BrandLogoProps {
  className?: string
}

/** 品牌标识：图标 + 字标，始终回到首页。 */
export function BrandLogo({ className }: BrandLogoProps) {
  return (
    <Link
      to="/"
      aria-label="SurgeCart 商城首页"
      className={cn(
        'group inline-flex items-center gap-2.5 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-2',
        className,
      )}
    >
      <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground shadow-xs transition-colors duration-150 group-hover:bg-primary-hover">
        <ShoppingBag className="size-[18px]" aria-hidden="true" />
      </span>
      <span className="flex flex-col leading-none">
        <span className="text-[17px] font-semibold tracking-tight text-foreground">SurgeCart</span>
        <span className="mt-1 text-[10px] font-medium uppercase tracking-[0.2em] text-muted-foreground">
          Shop
        </span>
      </span>
    </Link>
  )
}
