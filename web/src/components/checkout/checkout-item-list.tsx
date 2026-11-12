import { useMemo } from 'react'

import type { OrderQuoteItem } from '@/api/orders'
import type { CartItem } from '@/api/types'
import type { QuoteState } from '@/components/checkout/checkout-summary'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { describeUnavailableReason } from '@/utils/cart'

export interface CheckoutItemListProps {
  items: CartItem[]
  /** 服务端试算明细（按 sku_id 关联），仅试算成功后提供。 */
  quoteItems?: readonly OrderQuoteItem[]
  quoteState: QuoteState
}

/**
 * 结算页商品清单。
 *
 * 商品信息来自购物车（GET /cart），单价与小计只使用服务端试算明细：
 * 试算未完成/失败时不展示客户端金额，避免把购物车估算当作服务端结果。
 */
export function CheckoutItemList({ items, quoteItems, quoteState }: CheckoutItemListProps) {
  const priceBySku = useMemo(() => {
    const map = new Map<number, OrderQuoteItem>()
    for (const item of quoteItems ?? []) {
      map.set(item.sku_id, item)
    }
    return map
  }, [quoteItems])

  return (
    <section
      aria-labelledby="checkout-items-heading"
      className="rounded-xl border border-border bg-surface p-5 shadow-card"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="checkout-items-heading" className="text-base font-semibold text-foreground">
          商品清单
        </h2>
        <span className="text-xs text-muted-foreground">
          共 {items.length} 种
          {quoteState === 'success' ? ' · 单价与小计来自服务端试算' : ''}
        </span>
      </div>

      <ul className="mt-4 divide-y divide-border">
        {items.map((item) => {
          const name = item.product_name.trim() || '已删除的商品'
          const quoted = priceBySku.get(item.sku_id)

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
                  {quoted ? (
                    <>
                      <span className="text-xs text-muted-foreground">
                        单价 <PriceDisplay value={quoted.price} size="sm" tone="neutral" /> ×{' '}
                        {quoted.quantity}
                      </span>
                      <span className="text-sm font-medium text-foreground">
                        <PriceDisplay value={quoted.amount} size="sm" />
                      </span>
                    </>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      {quoteState === 'error' ? '试算失败，无法展示该行金额' : '金额试算中…'}
                    </span>
                  )}
                </div>
              </div>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
