import { ArrowLeft, Info } from 'lucide-react'
import { Link, useParams } from 'react-router'

import { ApiError } from '@/api/client'
import type { Order } from '@/api/orders'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PagePlaceholder } from '@/components/common/page-placeholder'
import { PriceDisplay } from '@/components/common/price-display'
import { OrderActions } from '@/components/order/order-actions'
import { OrderProgress } from '@/components/order/order-progress'
import { OrderStatusBadge } from '@/components/order/order-status-badge'
import { ProductImage } from '@/components/product/product-image'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderDetail } from '@/hooks/use-orders'
import {
  describeCancelReason,
  formatOrderAddress,
  formatOrderTime,
  orderTotalQuantity,
} from '@/utils/orders'

/** 时间节点：只展示后端实际返回（非 null）的字段，缺失时整行不渲染。 */
function buildTimeline(order: Order): { label: string; value: string }[] {
  const entries: { label: string; value: string | null }[] = [
    { label: '下单时间', value: order.created_at },
    { label: '支付时间', value: order.paid_at },
    { label: '发货时间', value: order.shipped_at },
    { label: '收货时间', value: order.received_at },
    { label: '完成时间', value: order.completed_at },
    { label: '取消时间', value: order.cancelled_at },
    { label: '退款时间', value: order.refunded_at },
  ]

  return entries
    .filter((entry): entry is { label: string; value: string } => entry.value !== null)
    .map((entry) => ({ label: entry.label, value: formatOrderTime(entry.value) }))
}

function OrderDetailContent({ order }: { order: Order }) {
  const timeline = buildTimeline(order)
  const quantity = orderTotalQuantity(order)

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Button asChild variant="ghost" size="sm" className="-ml-2 mb-2">
          <Link to="/account/orders">
            <ArrowLeft aria-hidden="true" />
            返回订单列表
          </Link>
        </Button>

        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-xl font-semibold text-foreground">订单详情</h2>
            <p className="mt-1 break-all font-mono text-[13px] text-muted-foreground">
              {order.order_no}
            </p>
          </div>
          <OrderStatusBadge status={order.status} className="mt-1" />
        </div>
      </div>

      <OrderActions order={order} />

      <section
        aria-labelledby="order-progress-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="order-progress-heading" className="text-sm font-semibold text-foreground">
          订单进度
        </h3>
        <OrderProgress status={order.status} className="mt-4" />

        <p className="mt-4 flex items-start gap-1.5 text-xs leading-5 text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          进度由当前订单状态推导，不是服务端返回的历史事件记录。
        </p>

        {order.status === 'pending_payment' && order.expire_at ? (
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            支付截止时间（服务端返回）：
            <span className="ml-1 tabular-nums text-foreground">
              {formatOrderTime(order.expire_at)}
            </span>
            。超时后由服务端自动取消订单，页面不提供前端倒计时与取消操作。
          </p>
        ) : null}

        {order.status === 'cancelled' ? (
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            取消原因：{describeCancelReason(order.cancel_reason)}
          </p>
        ) : null}
      </section>

      <section
        aria-labelledby="order-items-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="order-items-heading" className="text-sm font-semibold text-foreground">
          商品明细
        </h3>
        <ul className="mt-3 divide-y divide-border">
          {order.items.map((item) => (
            <li key={item.id} className="flex gap-3 py-3 first:pt-0 last:pb-0">
              <ProductImage
                src={item.product_main_image}
                alt={item.product_name}
                className="size-16 shrink-0 rounded-lg border border-border"
              />
              <div className="min-w-0 flex-1">
                <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">
                  {item.product_name}
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">规格：{item.sku_name}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">数量：{item.quantity}</p>
              </div>
              <div className="shrink-0 text-right">
                <p className="text-[11px] text-muted-foreground">成交单价</p>
                <PriceDisplay value={item.price} size="sm" tone="neutral" />
              </div>
            </li>
          ))}
        </ul>
        {order.items.length === 0 ? (
          <p className="mt-3 text-sm text-muted-foreground">该订单没有商品明细。</p>
        ) : null}
      </section>

      <section
        aria-labelledby="order-amount-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="order-amount-heading" className="text-sm font-semibold text-foreground">
          金额
        </h3>
        <dl className="mt-3 flex flex-col gap-2 text-sm">
          <div className="flex items-center justify-between gap-3">
            <dt className="text-muted-foreground">商品总金额</dt>
            <dd>
              <PriceDisplay value={order.original_amount} size="sm" tone="neutral" />
            </dd>
          </div>
          <div className="flex items-center justify-between gap-3">
            <dt className="text-muted-foreground">优惠金额</dt>
            <dd>
              <PriceDisplay value={order.discount_amount} size="sm" tone="neutral" />
            </dd>
          </div>
          <div className="flex items-center justify-between gap-3 border-t border-border pt-2">
            <dt className="font-medium text-foreground">应付金额</dt>
            <dd>
              <PriceDisplay value={order.total_amount} size="md" />
            </dd>
          </div>
        </dl>
        <p className="mt-2 text-right text-xs text-muted-foreground">
          三项金额均为下单时由服务端计算并写入的订单快照，前端不做任何金额运算。
        </p>
        {order.user_coupon_id !== null ? (
          <p className="mt-1 text-right text-xs text-muted-foreground">
            本单使用了优惠券（券 id：{order.user_coupon_id}）
          </p>
        ) : null}
      </section>

      <section
        aria-labelledby="order-address-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="order-address-heading" className="text-sm font-semibold text-foreground">
          收货信息
        </h3>
        <p className="mt-3 text-sm font-medium text-foreground">
          {order.recipient_name}
          <span className="ml-2 font-normal text-muted-foreground">{order.phone}</span>
        </p>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">{formatOrderAddress(order)}</p>
        <p className="mt-2 text-xs leading-5 text-muted-foreground">
          收货信息为下单时写入订单的快照，不会随后续修改地址簿而变化。
        </p>
      </section>

      <section
        aria-labelledby="order-time-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="order-time-heading" className="text-sm font-semibold text-foreground">
          时间信息
        </h3>
        <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2">
          {timeline.map((entry) => (
            <div key={entry.label} className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">{entry.label}</dt>
              <dd className="tabular-nums text-foreground">{entry.value}</dd>
            </div>
          ))}
        </dl>
        <p className="mt-2 text-xs text-muted-foreground">
          仅展示服务端实际返回的时间字段（原值展示，前端不做时区换算）；未发生的节点不显示。
        </p>
      </section>

      <p className="text-xs leading-5 text-muted-foreground">
        本页提供订单查询与订单操作（模拟支付 / 取消 / 确认收货）；退款与优惠券属于后续阶段。
        当前订单共 {quantity} 件商品。
      </p>
    </div>
  )
}

export function OrderDetailPage() {
  useDocumentTitle('订单详情 | SurgeCart 商城')

  const params = useParams()
  const orderId = Number(params.id ?? '')
  const isValidOrderId = Number.isSafeInteger(orderId) && orderId > 0

  const orderQuery = useOrderDetail(isValidOrderId ? orderId : 0)

  if (!isValidOrderId) {
    return (
      <PagePlaceholder
        eyebrow="订单"
        title="订单信息缺失"
        description="链接中的订单编号无效，无法展示订单详情。"
        actions={
          <Button asChild>
            <Link to="/account/orders">返回订单列表</Link>
          </Button>
        }
      />
    )
  }

  if (orderQuery.isPending) {
    return <LoadingState label="正在加载订单详情" />
  }

  if (orderQuery.isError) {
    const notFound = orderQuery.error instanceof ApiError && orderQuery.error.isNotFound

    if (notFound) {
      return (
        <PagePlaceholder
          eyebrow="404"
          title="订单不存在或无权查看"
          description="该订单可能不属于当前账号，或链接已失效。"
          actions={
            <Button asChild>
              <Link to="/account/orders">返回订单列表</Link>
            </Button>
          }
        />
      )
    }

    return (
      <ErrorState
        title="订单详情加载失败"
        description="订单详情暂时无法获取，请稍后重试。"
        onRetry={() => void orderQuery.refetch()}
      />
    )
  }

  if (!orderQuery.data) {
    return (
      <PagePlaceholder
        eyebrow="订单"
        title="订单信息缺失"
        description="服务端没有返回订单数据，请返回订单列表重试。"
        actions={
          <Button asChild>
            <Link to="/account/orders">返回订单列表</Link>
          </Button>
        }
      />
    )
  }

  return <OrderDetailContent order={orderQuery.data} />
}
