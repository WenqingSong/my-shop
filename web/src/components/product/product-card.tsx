import { Link } from 'react-router'

import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { cn } from '@/utils/cn'

/** 卡片只需要展示字段，列表 Product 与推荐位快照都能直接复用。 */
export interface ProductCardData {
  id: number
  name: string
  brand?: string
  main_image: string
  /** 整数分 */
  price: number
}

export interface ProductCardProps {
  product: ProductCardData
  className?: string
}

/**
 * 商品卡片（DESIGN_SYSTEM.md「商品卡片规范」）：
 * 1:1 主图、固定两行标题、价格使用 PriceDisplay，整卡为唯一链接。
 */
export function ProductCard({ product, className }: ProductCardProps) {
  return (
    <Link
      to={`/products/${String(product.id)}`}
      aria-label={`查看商品：${product.name}`}
      className={cn(
        'group flex h-full flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-card',
        'transition-colors duration-150 hover:border-brand-300 focus-visible:border-brand-300',
        className,
      )}
    >
      <ProductImage src={product.main_image} alt={product.name} className="aspect-square w-full" />
      <div className="flex min-w-0 flex-1 flex-col p-3 sm:p-4">
        <h3 className="line-clamp-2 min-h-10 text-sm font-medium leading-5 text-foreground">
          {product.name}
        </h3>
        <p className="mt-1 truncate text-xs text-muted-foreground">
          {product.brand?.trim() ? product.brand : '商品'}
        </p>
        <div className="mt-auto pt-3">
          <PriceDisplay value={product.price} size="md" />
        </div>
      </div>
    </Link>
  )
}
