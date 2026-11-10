import { Link } from 'react-router'

import type { UserCoupon } from '@/api/coupons'
import { CouponStatusBadge } from '@/components/coupon/coupon-status-badge'
import { Button } from '@/components/ui/button'
import {
  describeCouponName,
  describeCouponThreshold,
  describeCouponValidity,
  formatCentsAsYuan,
} from '@/utils/coupons'

export interface CouponCardProps {
  coupon: UserCoupon
}

/**
 * 我的优惠券卡片。
 *
 * 展示字段全部来自 `GET /my/coupons`：
 * 券名、减免金额、使用门槛、有效期、状态、领取时间、关联订单。
 *
 * 不展示：
 * - 「适用范围」：后端券是平台通用券，没有商品/分类范围字段；
 * - 「已过期」状态：后端没有 expired 枚举，只有有效期字段。
 */
export function CouponCard({ coupon }: CouponCardProps) {
  return (
    <article className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
      <div className="flex flex-col gap-4 p-4 sm:flex-row sm:items-stretch sm:gap-5 sm:p-5">
        <div className="flex shrink-0 flex-row items-baseline gap-2 sm:w-40 sm:flex-col sm:items-start sm:justify-center sm:border-r sm:border-dashed sm:border-border sm:pr-5">
          <span className="text-3xl font-semibold leading-none tabular-nums text-price">
            {formatCentsAsYuan(coupon.discount_amount)}
          </span>
          <span className="text-xs text-muted-foreground">
            {describeCouponThreshold(coupon.threshold_amount)}
          </span>
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <h3 className="min-w-0 break-words text-sm font-semibold text-foreground">
              {describeCouponName(coupon)}
            </h3>
            <CouponStatusBadge status={coupon.status} />
          </div>

          <dl className="mt-2 flex flex-col gap-1 text-xs text-muted-foreground">
            <div className="flex flex-wrap gap-x-2">
              <dt>有效期</dt>
              <dd className="tabular-nums">{describeCouponValidity(coupon)}</dd>
            </div>
            <div className="flex flex-wrap gap-x-2">
              <dt>领取时间</dt>
              <dd className="tabular-nums">{coupon.received_at?.slice(0, 19) ?? '—'}</dd>
            </div>
            {coupon.locked_at ? (
              <div className="flex flex-wrap gap-x-2">
                <dt>锁定时间</dt>
                <dd className="tabular-nums">{coupon.locked_at.slice(0, 19)}</dd>
              </div>
            ) : null}
            {coupon.used_at ? (
              <div className="flex flex-wrap gap-x-2">
                <dt>使用时间</dt>
                <dd className="tabular-nums">{coupon.used_at.slice(0, 19)}</dd>
              </div>
            ) : null}
          </dl>

          {coupon.order_id !== null ? (
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <Button asChild variant="outline" size="sm">
                <Link
                  to={`/account/orders/${String(coupon.order_id)}`}
                  aria-label={`查看使用该优惠券的订单 #${String(coupon.order_id)}`}
                >
                  查看关联订单
                </Link>
              </Button>
              <span className="text-xs text-muted-foreground">
                订单 #{String(coupon.order_id)}
                {coupon.status === 'locked' ? '（下单已占用，支付或取消后更新）' : ''}
              </span>
            </div>
          ) : null}
        </div>
      </div>

      <footer className="border-t border-border bg-gray-50/60 px-4 py-2.5">
        <p className="text-xs leading-5 text-muted-foreground">
          {coupon.status === 'available'
            ? '可在结算页选择使用；是否满足使用条件以服务端下单校验为准。'
            : coupon.status === 'locked'
              ? '该券已被一笔未完成订单占用：支付后核销为「已使用」，取消订单后恢复「可使用」。'
              : '该券已核销，不可重复使用；退款成功也不会返还优惠券。'}
        </p>
      </footer>
    </article>
  )
}
