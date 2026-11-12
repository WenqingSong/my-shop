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
import { ApplyRefundDialog } from '@/components/refund/apply-refund-dialog'
import { RefundStatusBadge } from '@/components/refund/refund-status-badge'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderDetail } from '@/hooks/use-orders'
import { useRefundList } from '@/hooks/use-refunds'
import {
  describeCancelReason,
  formatOrderAddress,
  formatOrderTime,
  orderTotalQuantity,
} from '@/utils/orders'
import {
  canApplyRefund,
  describeRefundStatus,
  findActiveRefund,
  formatRefundTime,
} from '@/utils/refunds'

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

/**
 * 订单与退款的联动区（W4-03）。
 *
 * 数据全部来自 `GET /refunds?order_id=`（后端真实支持的服务端过滤），
 * 不使用订单结构里不存在的退款字段。
 *
 * 申请入口规则（严格对齐后端前置条件）：
 * - 订单必须是 `paid(20)`（`internal/logic/refund/refund.go:159-161`）；
 * - 同一订单至多一条有效申请（applying/pending_refund/refund_failed），
 *   已存在有效申请时不再展示入口，避免必然触发的 18004；
 * - 退款列表尚未加载完成时不展示入口（避免给出"可能点不通"的操作）。
 */
function OrderRefundSection({ order }: { order: Order }) {
  const refundsQuery = useRefundList(order.id)
  const refunds = refundsQuery.data?.items ?? []
  const activeRefund = findActiveRefund(refunds)
  const showApplyEntry = refundsQuery.isSuccess && canApplyRefund(order) && !activeRefund

  return (
    <section
      aria-labelledby="order-refund-heading"
      className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id="order-refund-heading" className="text-sm font-semibold text-foreground">
          退款
        </h3>
        <Button asChild variant="ghost" size="sm">
          <Link to="/account/refunds">全部退款记录</Link>
        </Button>
      </div>

      {refundsQuery.isPending ? (
        <p className="mt-2 text-sm text-muted-foreground">正在加载该订单的退款信息…</p>
      ) : null}

      {refundsQuery.isError ? (
        <p role="alert" className="mt-2 text-sm text-danger">
          该订单的退款信息加载失败，暂时无法判断是否可以申请退款，请稍后刷新页面重试。
        </p>
      ) : null}

      {refundsQuery.isSuccess && refunds.length === 0 ? (
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          该订单没有退款申请。
          {order.status === 'paid'
            ? '订单已支付且未发货，可以申请整单退款。'
            : '只有「已支付且未发货」的订单可以申请退款。'}
        </p>
      ) : null}

      {refunds.length > 0 ? (
        <ul className="mt-3 divide-y divide-border">
          {refunds.map((refund) => (
            <li key={refund.id} className="flex flex-wrap items-center justify-between gap-2 py-3 first:pt-0 last:pb-0">
              <div className="min-w-0">
                <p className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-[13px] font-medium text-foreground">
                    退款 #{String(refund.id)}
                  </span>
                  <RefundStatusBadge status={refund.status} />
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  申请时间 {formatRefundTime(refund.created_at)} · 原因：{refund.reason || '—'}
                </p>
              </div>
              <div className="flex items-center gap-3">
                <PriceDisplay value={refund.amount} size="sm" tone="neutral" />
                <Button asChild variant="outline" size="sm">
                  <Link
                    to={`/account/refunds/${String(refund.id)}`}
                    aria-label={`查看退款 ${String(refund.id)} 详情`}
                  >
                    查看详情
                  </Link>
                </Button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      {showApplyEntry ? (
        <div className="mt-4">
          <ApplyRefundDialog order={order} />
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            仅支持整单退款；提交后需要商家审核，申请成功不代表退款完成或资金已退回。
          </p>
        </div>
      ) : null}

      {activeRefund ? (
        <p className="mt-3 text-sm leading-6 text-muted-foreground">
          该订单存在处理中的退款申请（{describeRefundStatus(activeRefund.status)}），暂不可重复申请。
        </p>
      ) : null}

      {order.status === 'refunded' ? (
        <p className="mt-3 text-sm leading-6 text-muted-foreground">
          该订单已完成退款处理，订单状态为「已退款」。
        </p>
      ) : null}
    </section>
  )
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

      <OrderRefundSection order={order} />

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
        本页提供订单查询、订单操作（模拟支付 / 取消 / 确认收货）与整单退款申请。
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
