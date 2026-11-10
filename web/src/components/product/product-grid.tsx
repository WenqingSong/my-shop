import { ProductCard, type ProductCardData } from '@/components/product/product-card'
import { cn } from '@/utils/cn'

export interface ProductGridProps {
  products: ProductCardData[]
  className?: string
}

/** 商品栅格：1440px 下 5 列，1024px 下 4 列，768px 下 3 列，手机 2 列。 */
export function ProductGrid({ products, className }: ProductGridProps) {
  return (
    <ul className={cn('grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5', className)}>
      {products.map((product) => (
        <li key={product.id} className="min-w-0">
          <ProductCard product={product} />
        </li>
      ))}
    </ul>
  )
}

export interface ProductGridSkeletonProps {
  count?: number
  className?: string
}

/** 列表骨架屏：保持与真实卡片一致的占位高度，避免加载完成时页面跳动。 */
export function ProductGridSkeleton({ count = 10, className }: ProductGridSkeletonProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn('grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5', className)}
    >
      <span className="sr-only">正在加载商品</span>
      {Array.from({ length: count }, (_, index) => (
        <div
          key={index}
          aria-hidden="true"
          className="overflow-hidden rounded-xl border border-border bg-surface shadow-card"
        >
          <div className="aspect-square w-full animate-pulse bg-gray-100" />
          <div className="space-y-2 p-4">
            <div className="h-4 w-4/5 animate-pulse rounded-sm bg-gray-100" />
            <div className="h-4 w-3/5 animate-pulse rounded-sm bg-gray-100" />
            <div className="h-5 w-2/5 animate-pulse rounded-sm bg-gray-100" />
          </div>
        </div>
      ))}
    </div>
  )
}
