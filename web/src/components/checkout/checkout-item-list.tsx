import type { CartItem } from '@/api/types'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { cartItemSubtotal, cartItemUnitPrice, describeUnavailableReason } from '@/utils/cart'

/**
 * 结算页商品清单。
 *
 * 数据来源是购物车（服务端 GET /cart），不是前端拼装：
 * 提交订单时后端同样按「全部已勾选条目」重新定价与校验，
 * 因此这里展示的单价/小计只是下单前的预览。
 */
export function CheckoutItemList({ items }: { items: CartItem[] }) {
  return (
    <section
      aria-labelledby="checkout-items-heading"
      className="rounded-xl border border-border bg-surface p-5 shadow-card"
    >
      <div className="flex items-center justify-between gap-3">
        <h2 id="checkout-items-heading" className="text-base font-semibold text-foreground">
          商品清单
        </h2>
        <span className="text-xs text-muted-foreground">共 {items.length} 种</span>
      </div>

      <ul className="mt-4 divide-y divide-border">
        {items.map((item) => {
          const name = item.product_name.trim() || '已删除的商品'
          const unitPrice = cartItemUnitPrice(item)
          const subtotal = cartItemSubtotal(item)

          return (
            <li key={item.id} className="flex gap-4 py-4 first:pt-0 last:pb-0">
              <ProductImage
                src={item.product_main_image}
                alt={name}
                className="size-16 shrink-0 rounded-lg border border-border"
              />

              <div className="min-w-0 flex-1">
                <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">{name}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  规格：{item.sku_name.trim() || '—'}
                </p>
                {!item.available ? (
                  <p className="mt-1 text-xs font-medium text-warning">
                    {describeUnavailableReason(item.unavailable_reason)}
                  </p>
                ) : null}
                {item.insufficient ? (
                  <p className="mt-1 text-xs font-medium text-warning">
                    库存不足，当前库存 {item.stock ?? 0} 件
                  </p>
                ) : null}

                <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">
                    {unitPrice !== null ? (
                      <>
                        单价 <PriceDisplay value={unitPrice} size="sm" tone="neutral" /> ×{' '}
                        {item.quantity}
                      </>
                    ) : (
                      '价格不可用'
                    )}
                  </span>
                  <span className="text-sm font-medium text-foreground">
                    {subtotal !== null ? (
                      <PriceDisplay value={subtotal} size="sm" />
                    ) : (
                      <span className="text-xs text-muted-foreground">—</span>
                    )}
                  </span>
                </div>
              </div>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
