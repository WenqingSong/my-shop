import { useQuery } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { quoteOrder } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import type { CartItem } from '@/api/types'

/**
 * 订单试算指纹：`sku_id:quantity:current_price`，按 sku_id 排序。
 *
 * 把「当前单价」也放进指纹，是因为购物车缓存刷新后同一 SKU 的价格可能已经变化；
 * 价格变化必须产生新的试算 key，否则会把旧价算出的金额当成最新结果。
 */
export function buildQuoteLinesSignature(items: readonly CartItem[]): string {
  return [...items]
    .sort((a, b) => a.sku_id - b.sku_id)
    .map((item) => `${String(item.sku_id)}:${String(item.quantity)}:${String(item.current_price ?? -1)}`)
    .join(',')
}

export interface OrderQuoteInput {
  /** 未选择地址时为 null：此时不发起试算（后端 address_id 必填）。 */
  addressId: number | null
  /** 0 表示不使用优惠券。 */
  couponId: number
  /** 参与试算的已勾选条目；为空时不发起试算。 */
  items: readonly CartItem[]
}

/**
 * 服务端订单试算（POST /orders/quote）。
 *
 * 关键约束：
 * - addressId / couponId / 已勾选行（含单价）全部进入 Query Key：任一变化都会重新试算，
 *   而不是复用上一份金额；
 * - 不做 `keepPreviousData`：新输入在返回前没有数据，页面必须展示「试算中」而不是旧金额；
 * - 失败不降级：调用方必须展示错误与重试入口，禁止用购物车金额冒充试算结果；
 * - 试算不检查库存、不锁定优惠券、不创建订单，成功也不代表可以买到。
 */
export function useOrderQuote({ addressId, couponId, items }: OrderQuoteInput) {
  const enabled = addressId !== null && items.length > 0
  const lines = buildQuoteLinesSignature(items)

  return useQuery({
    queryKey: queryKeys.orders.quote({ addressId: addressId ?? 0, couponId, lines }),
    queryFn: ({ signal }) =>
      quoteOrder(
        {
          source: 'cart',
          address_id: addressId as number,
          // 0/缺省都表示不使用优惠券；只在确实选券时传。
          ...(couponId > 0 ? { coupon_id: couponId } : {}),
        },
        signal,
      ),
    enabled,
    staleTime: 0,
    // 4xx 是确定性业务结果（地址/券/购物车不可用），重试没有意义。
    retry: (failureCount, error) =>
      failureCount < 1 &&
      !(error instanceof ApiError && error.status !== undefined && error.status >= 400 && error.status < 500),
  })
}
