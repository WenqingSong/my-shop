import { Link } from 'react-router'

import type { Order } from '@/api/orders'
import type { Refund } from '@/api/refunds'
import { OrderStatusBadge } from '@/components/order/order-status-badge'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { RefundStatusBadge } from '@/components/refund/refund-status-badge'
import { Button } from '@/components/ui/button'
import { formatOrderTime, orderTotalQuantity } from '@/utils/orders'

export interface OrderCardProps {
  order: Order
  /**
   * 该订单最新一条退款记录（W4-03）。
   *
   * 订单结构里没有退款字段，数据由列表页用一次 `GET /refunds` 建立 order_id 映射后传入；
   * 映射不可用（未登录态切换、退款列表加载失败）时为 undefined，卡片不展示退款徽标。
   */
  latestRefund?: Refund
}

/**
 * 订单列表卡片。
 *
 * 展示字段全部来自后端订单快照：
 * - 订单号 / 创建时间 / 状态；
 * - 每个订单项的 商品图、商品名、SKU 名、成交单价、数量；
 * - 订单应付金额（total_amount，后端计算）。
 *
 * 不做任何金额推导：卡片上没有"小计"之类的客户端计算结果，
 * 合计直接展示 `order.total_amount`，避免与后端事务口径产生偏差。
 */
export function OrderCard({ order, latestRefund }: OrderCardProps) {
  const quantity = orderTotalQuantity(order)

  return (
    <article className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <span className="font-mono text-[13px] font-medium tracking-tight text-foreground">
            {order.order_no}
          </span>
          <span className="text-xs text-muted-foreground">
            下单时间 {formatOrderTime(order.created_at)}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {latestRefund ? (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              退款
              <RefundStatusBadge status={latestRefund.status} />
            </span>
          ) : null}
          <OrderStatusBadge status={order.status} />
        </div>
      </header>

      {order.items.length > 0 ? (
        <ul className="divide-y divide-border px-4">
          {order.items.map((item) => (
            <li key={item.id} className="flex gap-3 py-3">
              <ProductImage
                src={item.product_main_image}
                alt={item.product_name}
                className="size-16 shrink-0 rounded-lg border border-border"
              />
              <div className="min-w-0 flex-1">
                <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">
                  {item.product_name}
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  规格：{item.sku_name} · 数量 {item.quantity}
                </p>
              </div>
              <div className="shrink-0 text-right">
                <p className="text-[11px] text-muted-foreground">成交单价</p>
                <PriceDisplay value={item.price} size="sm" tone="neutral" />
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="px-4 py-4 text-sm text-muted-foreground">该订单没有商品明细。</p>
      )}

      <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-border bg-gray-50/60 px-4 py-3">
        <p className="text-xs text-muted-foreground">共 {quantity} 件商品</p>

        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground">应付金额</span>
          <PriceDisplay value={order.total_amount} size="md" />
          <Button asChild variant="outline" size="sm">
            <Link
              to={`/account/orders/${String(order.id)}`}
              aria-label={`查看订单 ${order.order_no} 详情`}
            >
              查看详情
            </Link>
          </Button>
        </div>
      </footer>
    </article>
  )
}
