import { RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'

import type { OrderQuote } from '@/api/orders'
import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'

/** 试算状态：idle 表示前置条件不满足，本来就不应发起试算。 */
export type QuoteState = 'idle' | 'pending' | 'error' | 'success'

export interface CheckoutSummaryProps {
  /** 已勾选商品件数（仅用于展示）。 */
  quantity: number
  /** 服务端试算结果；不是成功状态时必须为 null。 */
  quote: OrderQuote | null
  quoteState: QuoteState
  /** 试算失败的用户可读原因。 */
  quoteErrorMessage?: string
  /** 前置条件不满足（未选地址、无有效商品等）时的说明。 */
  quoteIdleReason?: string
  onRetryQuote: () => void
  /** 是否选了优惠券（区分「未使用」与「优惠为 0」）。 */
  couponSelected: boolean
  /** 取消选券并重新试算（券被服务端拒绝时提供）。 */
  onClearCoupon: () => void
  /** 不可购买条目导致无法提交。 */
  blocked: boolean
  /** 未选择地址等前置条件不满足。 */
  disabled: boolean
  isSubmitting: boolean
  /** 只有试算成功后才允许提交。 */
  canSubmit: boolean
  onSubmit: () => void
}

function AmountRow({
  label,
  value,
  hint,
  strong = false,
}: {
  label: string
  value: ReactNode
  hint?: string
  strong?: boolean
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className={strong ? 'font-medium text-foreground' : 'text-muted-foreground'}>{label}</dt>
      <dd className="flex items-baseline gap-1.5 text-right">
        {value}
        {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
      </dd>
    </div>
  )
}

/**
 * 结算页金额与提交区域。
 *
 * 金额口径（全部来自 `POST /orders/quote`，前端不做任何金额运算）：
 * 商品原价 - 优惠券优惠 - 会员优惠 - 积分优惠 + 运费 = 应付金额。
 *
 * 重要边界：
 * - 试算只读：不检查库存、不锁定优惠券、不创建订单，成功也不代表库存已锁定；
 * - 试算失败时这里没有任何金额可展示，也不会退回购物车金额充当最终金额；
 * - 提交后最终金额以 `POST /orders` 返回的订单快照为准（成功页展示）。
 */
export function CheckoutSummary({
  quantity,
  quote,
  quoteState,
  quoteErrorMessage,
  quoteIdleReason,
  onRetryQuote,
  couponSelected,
  onClearCoupon,
  blocked,
  disabled,
  isSubmitting,
  canSubmit,
  onSubmit,
}: CheckoutSummaryProps) {
  const disabledReason = blocked
    ? '已勾选商品中存在不可购买商品，请返回购物车调整。'
    : disabled
      ? '请选择收货地址后再提交订单。'
      : !canSubmit && quoteState === 'pending'
        ? '正在向服务端试算金额…'
        : null

  return (
    <aside
      aria-label="订单金额"
      className="rounded-xl border border-border bg-surface p-5 shadow-card lg:sticky lg:top-24"
    >
      <h2 className="text-base font-semibold text-foreground">订单金额</h2>

      <dl className="mt-4 space-y-3 border-b border-border pb-4 text-sm">
        <AmountRow
          label="商品件数"
          value={<span className="tabular-nums text-foreground">{quantity} 件</span>}
        />

        {quote ? (
          <>
            <AmountRow
              label="商品原价"
              value={<PriceDisplay value={quote.original_amount} size="md" />}
            />
            <AmountRow
              label="优惠券优惠"
              value={
                <span className="inline-flex items-baseline gap-1 text-foreground">
                  <span className="text-sm">-</span>
                  <PriceDisplay value={quote.coupon_discount} size="sm" tone="neutral" />
                </span>
              }
              hint={couponSelected ? undefined : '未使用'}
            />
            <AmountRow
              label="会员优惠"
              value={
                <span className="inline-flex items-baseline gap-1 text-foreground">
                  <span className="text-sm">-</span>
                  <PriceDisplay value={quote.member_discount} size="sm" tone="neutral" />
                </span>
              }
              hint={quote.member_discount > 0 ? undefined : '无会员权益'}
            />
            <AmountRow
              label="积分优惠"
              value={
                <span className="inline-flex items-baseline gap-1 text-foreground">
                  <span className="text-sm">-</span>
                  <PriceDisplay value={quote.points_discount} size="sm" tone="neutral" />
                </span>
              }
              hint={quote.points_discount > 0 ? undefined : '暂未开放'}
            />
            <AmountRow
              label="运费"
              value={
                <span className="inline-flex items-baseline gap-1 text-foreground">
                  <span className="text-sm">+</span>
                  <PriceDisplay value={quote.shipping_fee} size="sm" tone="neutral" />
                </span>
              }
              hint={quote.shipping_fee > 0 ? undefined : '免运费'}
            />
          </>
        ) : null}
      </dl>

      {quoteState === 'pending' ? (
        <p role="status" className="mt-4 text-sm text-muted-foreground">
          正在向服务端试算金额…
        </p>
      ) : null}

      {quoteState === 'idle' ? (
        <p className="mt-4 text-sm leading-6 text-muted-foreground">
          {quoteIdleReason ?? '请选择收货地址与要结算的商品后再试算金额。'}
        </p>
      ) : null}

      {quoteState === 'error' ? (
        <div
          role="alert"
          className="mt-4 rounded-lg border border-danger/20 bg-danger-soft px-3 py-2.5 text-sm leading-6 text-danger"
        >
          <p>{quoteErrorMessage ?? '金额试算失败，请重试。'}</p>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={onRetryQuote}>
              <RefreshCw aria-hidden="true" />
              重新试算
            </Button>
            {couponSelected ? (
              <Button variant="ghost" size="sm" onClick={onClearCoupon}>
                不使用优惠券并重试
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}

      {quote ? (
        <div className="mt-4 flex items-baseline justify-between gap-3">
          <span className="text-sm font-medium text-foreground">应付金额</span>
          <PriceDisplay value={quote.payable_amount} size="lg" />
        </div>
      ) : (
        <div className="mt-4 flex items-baseline justify-between gap-3">
          <span className="text-sm font-medium text-foreground">应付金额</span>
          <span className="text-sm text-muted-foreground">待服务端试算</span>
        </div>
      )}

      <p className="mt-3 rounded-lg bg-gray-25 px-3 py-2 text-xs leading-5 text-muted-foreground">
        金额来自服务端试算：商品原价 − 优惠券优惠 − 会员优惠 − 积分优惠 + 运费 = 应付金额。
        试算不检查库存、不锁定优惠券、不创建订单；提交时服务端会重新计算，最终金额以下单返回为准。
      </p>

      {disabledReason ? (
        <p className="mt-3 text-xs leading-5 text-warning">{disabledReason}</p>
      ) : null}

      <Button
        size="lg"
        className="mt-4 w-full"
        onClick={onSubmit}
        isLoading={isSubmitting}
        disabled={blocked || disabled || !canSubmit}
      >
        提交订单
      </Button>

      <p className="mt-2 text-xs leading-5 text-muted-foreground">
        提交后将按当前收货地址、优惠券与已勾选商品创建订单；重复提交会由服务端幂等键去重。
      </p>
    </aside>
  )
}
