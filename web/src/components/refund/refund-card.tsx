import { Link } from 'react-router'

import type { Refund } from '@/api/refunds'
import { PriceDisplay } from '@/components/common/price-display'
import { RefundStatusBadge } from '@/components/refund/refund-status-badge'
import { Button } from '@/components/ui/button'
import { formatRefundTime } from '@/utils/refunds'

export interface RefundCardProps {
  refund: Refund
  /**
   * 关联订单号。
   *
   * 后端退款结构只有 `order_id`（软引用），**没有内嵌订单快照**；
   * 订单号由调用方从 `GET /orders` 建立映射后传入。
   * 映射不可用时为 undefined，卡片退化为展示订单 id。
   */
  orderNo?: string
}

/**
 * 退款记录卡片。
 *
 * 展示字段全部来自后端退款申请：
 * 退款编号 / 关联订单 / 退款金额 / 申请原因 / 状态 / 申请时间。
 *
 * 不展示：
 * - 退款渠道（后端不存在，项目也没有真实支付渠道）；
 * - 预计到账时间（后端不存在该字段，也不能承诺）；
 * - 审核人/执行人（`reviewed_by`/`executed_by` 是管理员内部 id，对用户无意义）。
 */
export function RefundCard({ refund, orderNo }: RefundCardProps) {
  return (
    <article className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-xs text-muted-foreground">退款编号</span>
          <span className="font-mono text-[13px] font-medium tracking-tight text-foreground">
            #{refund.id}
          </span>
          <span className="text-xs text-muted-foreground">
            申请时间 {formatRefundTime(refund.created_at)}
          </span>
        </div>
        <RefundStatusBadge status={refund.status} />
      </header>

      <div className="grid gap-3 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
        <dl className="flex min-w-0 flex-col gap-2 text-sm">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <dt className="text-muted-foreground">关联订单</dt>
            <dd className="min-w-0">
              <Link
                to={`/account/orders/${String(refund.order_id)}`}
                className="break-all font-mono text-[13px] font-medium text-primary hover:underline"
                aria-label={`查看关联订单 ${orderNo ?? `#${String(refund.order_id)}`}`}
              >
                {orderNo ?? `订单 #${String(refund.order_id)}`}
              </Link>
            </dd>
          </div>
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <dt className="shrink-0 text-muted-foreground">退款原因</dt>
            <dd className="min-w-0 break-words text-foreground">{refund.reason || '—'}</dd>
          </div>
        </dl>

        <div className="flex items-end justify-between gap-4 sm:flex-col sm:items-end sm:gap-2">
          <div className="text-right">
            <p className="text-[11px] text-muted-foreground">退款金额</p>
            <PriceDisplay value={refund.amount} size="md" />
          </div>
          <Button asChild variant="outline" size="sm">
            <Link
              to={`/account/refunds/${String(refund.id)}`}
              aria-label={`查看退款 ${String(refund.id)} 详情`}
            >
              查看详情
            </Link>
          </Button>
        </div>
      </div>

      <footer className="border-t border-border bg-gray-50/60 px-4 py-2.5">
        <p className="text-xs leading-5 text-muted-foreground">
          退款金额由服务端按订单实付金额派生；状态反映项目内部业务处理进度，不代表真实资金已经退回。
        </p>
      </footer>
    </article>
  )
}
