import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import type { CartSummary } from '@/utils/cart'

export interface CheckoutSummaryProps {
  summary: CartSummary
  /** 不可购买条目导致无法提交。 */
  blocked: boolean
  /** 未选择地址等前置条件不满足。 */
  disabled: boolean
  isSubmitting: boolean
  onSubmit: () => void
}

/**
 * 结算页金额与提交区域。
 *
 * 金额口径必须说清楚：
 * - 「商品合计（预估）」= 已勾选且可购买条目的当前单价 × 数量，由购物车数据算出；
 * - 这不是后端订单试算：后端没有独立试算接口，正式金额只在 POST /orders 事务中计算；
 * - 运费与优惠券查询接口未接入，页面不展示虚构的优惠/运费数字。
 */
export function CheckoutSummary({
  summary,
  blocked,
  disabled,
  isSubmitting,
  onSubmit,
}: CheckoutSummaryProps) {
  const disabledReason = blocked
    ? '已勾选商品中存在不可购买商品，请返回购物车调整。'
    : disabled
      ? '请选择收货地址后再提交订单。'
      : null

  return (
    <aside
      aria-label="订单金额"
      className="rounded-xl border border-border bg-surface p-5 shadow-card lg:sticky lg:top-24"
    >
      <h2 className="text-base font-semibold text-foreground">订单金额</h2>

      <dl className="mt-4 space-y-3 border-b border-border pb-4 text-sm">
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">商品件数</dt>
          <dd className="tabular-nums text-foreground">{summary.selectedQuantity} 件</dd>
        </div>
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">商品合计（预估）</dt>
          <dd>
            <PriceDisplay value={summary.estimatedTotal} size="md" />
          </dd>
        </div>
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">运费</dt>
          <dd className="text-xs text-muted-foreground">暂未接入</dd>
        </div>
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">优惠</dt>
          <dd className="text-xs text-muted-foreground">暂未接入</dd>
        </div>
      </dl>

      <div className="mt-4 flex items-baseline justify-between gap-3">
        <span className="text-sm font-medium text-foreground">应付金额（预估）</span>
        <PriceDisplay value={summary.estimatedTotal} size="lg" />
      </div>

      <p className="mt-3 rounded-lg bg-gray-25 px-3 py-2 text-xs leading-5 text-muted-foreground">
        以上为购物车预估，不是后端订单试算结果。最终应付金额、优惠与库存由服务端在提交订单时计算并返回。
      </p>

      {disabledReason ? (
        <p className="mt-3 text-xs leading-5 text-warning">{disabledReason}</p>
      ) : null}

      <Button
        size="lg"
        className="mt-4 w-full"
        onClick={onSubmit}
        isLoading={isSubmitting}
        disabled={blocked || disabled}
      >
        提交订单
      </Button>

      <p className="mt-2 text-xs leading-5 text-muted-foreground">
        提交后将按当前收货地址与已勾选商品创建订单；重复提交会由服务端幂等键去重。
      </p>
    </aside>
  )
}
