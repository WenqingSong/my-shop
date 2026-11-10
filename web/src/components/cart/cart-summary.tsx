import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import type { CartSummary } from '@/utils/cart'

export interface CartSummaryPanelProps {
  summary: CartSummary
}

/**
 * 购物车金额汇总。
 *
 * 这里的「商品合计（预估）」= 已勾选且可购买条目的（当前单价 × 数量）之和，
 * 仅用于购物车展示：
 * - 不含运费、优惠券、活动价与库存最终裁决；
 * - 与 W3-02 的订单试算结果无关，最终应付金额以 Go 后端为准；
 * - 「去结算」在 W3-02 开放前保持禁用，不制造死链，也不提前实现结算。
 */
export function CartSummaryPanel({ summary }: CartSummaryPanelProps) {
  const hasSelected = summary.selectedCount > 0

  return (
    <aside
      aria-label="购物车金额汇总"
      className="rounded-xl border border-border bg-surface p-5 shadow-card lg:sticky lg:top-24"
    >
      <h2 className="text-base font-semibold text-foreground">金额汇总</h2>

      <dl className="mt-4 space-y-3 border-b border-border pb-4 text-sm">
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">已选商品</dt>
          <dd className="tabular-nums text-foreground">{summary.selectedQuantity} 件</dd>
        </div>
        <div className="flex items-center justify-between gap-3">
          <dt className="text-muted-foreground">商品合计（预估）</dt>
          <dd>
            <PriceDisplay value={summary.estimatedTotal} size="md" />
          </dd>
        </div>
      </dl>

      <p className="mt-3 text-xs leading-5 text-muted-foreground">
        运费、优惠与最终应付金额以订单试算结果为准，购物车金额仅供预览。
      </p>

      {summary.blockedSelectedCount > 0 ? (
        <p className="mt-2 text-xs leading-5 text-warning">
          已勾选商品中有 {summary.blockedSelectedCount} 件不可购买，未计入预估金额。
        </p>
      ) : null}

      <Button
        size="lg"
        className="mt-5 w-full"
        disabled
        aria-describedby="cart-checkout-note"
        title="订单试算与结算将在 W3-02 开放"
      >
        {hasSelected ? '去结算' : '请选择商品'}
      </Button>
      <p id="cart-checkout-note" className="mt-2 text-xs leading-5 text-muted-foreground">
        订单试算、结算与下单将在 W3-02 开放，本期购物车不会创建订单。
      </p>
    </aside>
  )
}
