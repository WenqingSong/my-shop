import { Link } from 'react-router'

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
 * - 不是订单试算结果：后端没有独立试算接口，最终应付金额只在创建订单事务中计算；
 * - 存在不可购买商品时不提供结算入口（后端会 9003 拒绝），避免必然失败的提交。
 */
export function CartSummaryPanel({ summary }: CartSummaryPanelProps) {
  const hasSelected = summary.selectedCount > 0
  const blocked = summary.blockedSelectedCount > 0

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
        运费与优惠暂未接入；最终应付金额以提交订单后服务端返回为准，购物车金额仅供预览。
      </p>

      {blocked ? (
        <p className="mt-2 text-xs leading-5 text-warning">
          已勾选商品中有 {summary.blockedSelectedCount} 件不可购买，无法结算，请取消勾选或删除后再试。
        </p>
      ) : null}

      {!hasSelected ? (
        <Button size="lg" className="mt-5 w-full" disabled aria-describedby="cart-checkout-note">
          请选择商品
        </Button>
      ) : blocked ? (
        <Button
          size="lg"
          className="mt-5 w-full"
          disabled
          aria-describedby="cart-checkout-note"
          title="存在不可购买商品，请先调整购物车"
        >
          去结算
        </Button>
      ) : (
        <Button asChild size="lg" className="mt-5 w-full">
          <Link to="/checkout">去结算</Link>
        </Button>
      )}

      <p id="cart-checkout-note" className="mt-2 text-xs leading-5 text-muted-foreground">
        结算页会按已勾选商品与收货地址发起下单，金额由服务端计算。
      </p>
    </aside>
  )
}
