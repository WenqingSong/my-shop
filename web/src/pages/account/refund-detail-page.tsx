import { AlertTriangle, ArrowLeft, CheckCircle2, Info } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'

import { ApiError } from '@/api/client'
import type { Refund } from '@/api/refunds'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PagePlaceholder } from '@/components/common/page-placeholder'
import { PriceDisplay } from '@/components/common/price-display'
import { RefundStatusBadge } from '@/components/refund/refund-status-badge'
import {
  RevokeRefundDialog,
  type RevokeRefundFeedback,
} from '@/components/refund/revoke-refund-dialog'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderDetail } from '@/hooks/use-orders'
import { useRefundDetail } from '@/hooks/use-refunds'
import { cn } from '@/utils/cn'
import {
  describeRefundStatus,
  describeRefundStatusHint,
  formatRefundTime,
} from '@/utils/refunds'

/**
 * 关联订单信息。
 *
 * 退款结构只有 `order_id`，订单号/金额等快照需要再查一次订单详情
 * （`GET /orders/:id`，同一批缓存由订单详情页共享）。
 * 订单查询失败不影响退款详情主体展示，只把订单号降级为 id。
 */
function RelatedOrder({ orderId }: { orderId: number }) {
  const orderQuery = useOrderDetail(orderId)

  if (orderQuery.isPending) {
    return <span className="text-muted-foreground">正在加载关联订单</span>
  }

  if (orderQuery.isError || !orderQuery.data) {
    return (
      <Link
        to={`/account/orders/${String(orderId)}`}
        className="break-all font-mono text-[13px] text-primary hover:underline"
      >
        订单 #{String(orderId)}
      </Link>
    )
  }

  return (
    <Link
      to={`/account/orders/${String(orderId)}`}
      className="break-all font-mono text-[13px] text-primary hover:underline"
    >
      {orderQuery.data.order_no}
    </Link>
  )
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-2">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-right text-foreground">{children}</dd>
    </div>
  )
}

function RefundDetailContent({ refund }: { refund: Refund }) {
  // 撤销反馈由本组件持有：撤销成功后入口组件会卸载，反馈不能留在入口组件内部。
  const [revokeFeedback, setRevokeFeedback] = useState<RevokeRefundFeedback | null>(null)

  // 审核/处理信息只在后端真的写入了对应字段时展示，不补占位、不推断流程。
  const hasReview = refund.reviewed_at !== null || refund.review_reason !== ''
  const hasExecute = refund.executed_at !== null || refund.execute_reason !== ''

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Button asChild variant="ghost" size="sm" className="-ml-2 mb-2">
          <Link to="/account/refunds">
            <ArrowLeft aria-hidden="true" />
            返回退款列表
          </Link>
        </Button>

        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-xl font-semibold text-foreground">退款详情</h2>
            <p className="mt-1 break-all font-mono text-[13px] text-muted-foreground">
              退款编号 #{String(refund.id)}
            </p>
          </div>
          <RefundStatusBadge status={refund.status} className="mt-1" />
        </div>
      </div>

      <section
        aria-labelledby="refund-summary-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="refund-summary-heading" className="text-sm font-semibold text-foreground">
          退款概况
        </h3>

        <dl className="mt-3 flex flex-col gap-2.5 text-sm">
          <InfoRow label="当前状态">{describeRefundStatus(refund.status)}</InfoRow>
          <InfoRow label="关联订单">
            <RelatedOrder orderId={refund.order_id} />
          </InfoRow>
          <InfoRow label="退款金额">
            <PriceDisplay value={refund.amount} size="md" />
          </InfoRow>
          <InfoRow label="申请时间">{formatRefundTime(refund.created_at)}</InfoRow>
          <InfoRow label="最近更新">{formatRefundTime(refund.updated_at)}</InfoRow>
        </dl>

        <p className="mt-3 rounded-lg border border-border bg-gray-50 px-3 py-2 text-sm leading-6 text-muted-foreground">
          {describeRefundStatusHint(refund.status)}
        </p>
      </section>

      <section
        aria-labelledby="refund-reason-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="refund-reason-heading" className="text-sm font-semibold text-foreground">
          申请原因
        </h3>
        <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-foreground">
          {refund.reason || '—'}
        </p>
      </section>

      {hasReview ? (
        <section
          aria-labelledby="refund-review-heading"
          className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
        >
          <h3 id="refund-review-heading" className="text-sm font-semibold text-foreground">
            审核信息
          </h3>
          <dl className="mt-3 flex flex-col gap-2.5 text-sm">
            <InfoRow label="审核时间">{formatRefundTime(refund.reviewed_at)}</InfoRow>
            <InfoRow label="审核说明">{refund.review_reason || '—'}</InfoRow>
          </dl>
        </section>
      ) : null}

      {hasExecute ? (
        <section
          aria-labelledby="refund-execute-heading"
          className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
        >
          <h3 id="refund-execute-heading" className="text-sm font-semibold text-foreground">
            处理信息
          </h3>
          <dl className="mt-3 flex flex-col gap-2.5 text-sm">
            <InfoRow label="处理时间">{formatRefundTime(refund.executed_at)}</InfoRow>
            <InfoRow label="处理说明">{refund.execute_reason || '—'}</InfoRow>
          </dl>
        </section>
      ) : null}

      <section
        aria-labelledby="refund-actions-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="refund-actions-heading" className="text-sm font-semibold text-foreground">
          可执行操作
        </h3>

        {refund.status === 'applying' ? (
          <>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              申请尚未审核，可以撤销。撤销后订单保持「已支付」，也不会恢复库存或返还优惠券。
            </p>
            <div className="mt-4">
              <RevokeRefundDialog refund={refund} onResult={setRevokeFeedback} />
            </div>
          </>
        ) : (
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            {refund.status === 'pending_refund'
              ? '商家已审核通过，正在处理退款，此阶段不可撤销。'
              : '当前状态没有可执行的用户操作。'}
          </p>
        )}

        {revokeFeedback ? (
          <div
            role={revokeFeedback.tone === 'error' ? 'alert' : 'status'}
            className={cn(
              'mt-4 flex items-start gap-2 rounded-lg border px-3 py-2 text-sm',
              revokeFeedback.tone === 'error'
                ? 'border-danger/20 bg-danger-soft text-danger'
                : 'border-success/20 bg-success-soft text-success',
            )}
          >
            {revokeFeedback.tone === 'error' ? (
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            ) : (
              <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            )}
            {revokeFeedback.text}
          </div>
        ) : null}
      </section>

      <section
        aria-labelledby="refund-fund-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="refund-fund-heading" className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
          <Info className="size-4 text-muted-foreground" aria-hidden="true" />
          关于退款资金
        </h3>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          退款状态反映项目内部业务处理进度，不代表真实资金已经退回。项目使用模拟支付，
          没有第三方支付渠道、支付流水与到账回调，因此本页不展示退款渠道、支付流水号或预计到账时间。
        </p>
        <p className="mt-2 text-xs leading-5 text-muted-foreground">
          本页仅展示服务端实际返回的退款字段与时间；后端没有面向用户的退款历史/审计接口，
          因此不提供状态时间轴。
        </p>
      </section>
    </div>
  )
}

export function RefundDetailPage() {
  useDocumentTitle('退款详情 | SurgeCart 商城')

  const params = useParams()
  const refundId = Number(params.id ?? '')
  const isValidRefundId = Number.isSafeInteger(refundId) && refundId > 0

  const refundQuery = useRefundDetail(isValidRefundId ? refundId : 0)

  if (!isValidRefundId) {
    return (
      <PagePlaceholder
        eyebrow="退款"
        title="退款信息缺失"
        description="链接中的退款编号无效，无法展示退款详情。"
        actions={
          <Button asChild>
            <Link to="/account/refunds">返回退款列表</Link>
          </Button>
        }
      />
    )
  }

  if (refundQuery.isPending) {
    return <LoadingState label="正在加载退款详情" />
  }

  if (refundQuery.isError) {
    const notFound = refundQuery.error instanceof ApiError && refundQuery.error.isNotFound

    if (notFound) {
      return (
        <PagePlaceholder
          eyebrow="404"
          title="退款不存在或无权查看"
          description="该退款申请可能不属于当前账号，或链接已失效。"
          actions={
            <Button asChild>
              <Link to="/account/refunds">返回退款列表</Link>
            </Button>
          }
        />
      )
    }

    return (
      <ErrorState
        title="退款详情加载失败"
        description="退款详情暂时无法获取，请稍后重试。"
        onRetry={() => void refundQuery.refetch()}
      />
    )
  }

  if (!refundQuery.data) {
    return (
      <PagePlaceholder
        eyebrow="退款"
        title="退款信息缺失"
        description="服务端没有返回退款数据，请返回退款列表重试。"
        actions={
          <Button asChild>
            <Link to="/account/refunds">返回退款列表</Link>
          </Button>
        }
      />
    )
  }

  return <RefundDetailContent refund={refundQuery.data} />
}
