import { Ticket } from 'lucide-react'
import { useMemo } from 'react'

import type { UserCoupon } from '@/api/coupons'
import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import {
  describeCouponName,
  describeCouponStatus,
  describeCouponThreshold,
  describeCouponValidity,
  evaluateCouponForCheckout,
  formatCentsAsYuan,
  sortCouponsForCheckout,
} from '@/utils/coupons'
import { cn } from '@/utils/cn'

export interface CheckoutCouponSectionProps {
  coupons: readonly UserCoupon[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  /** 当前选择的券 id；null 表示不使用优惠券。 */
  selectedCouponId: number | null
  onSelect: (couponId: number | null) => void
  /** 已勾选商品的预估金额（整数分），仅用于前端估算门槛。 */
  estimatedGoodsAmount: number
}

/**
 * 结算页优惠券选择（W4-04）。
 *
 * 重要边界：
 * - 后端**没有**「订单可用券查询 / 优惠试算」接口，本组件给出的「可选/不可选/预估优惠」
 *   全部是前端基于券状态、有效期、门槛与预估商品金额的**估算**；
 * - 最终可用性、优惠金额与订单金额由 `POST /orders` 服务端计算（19007 表示券不可用）；
 * - 不提供金额输入，用户只能选择「用哪张券」或「不使用」；
 * - 后端没有商品/分类适用范围字段，因此这里没有范围匹配判断。
 */
export function CheckoutCouponSection({
  coupons,
  isLoading,
  isError,
  onRetry,
  selectedCouponId,
  onSelect,
  estimatedGoodsAmount,
}: CheckoutCouponSectionProps) {
  const entries = useMemo(
    () =>
      sortCouponsForCheckout(
        coupons.map((coupon) => ({
          coupon,
          evaluation: evaluateCouponForCheckout(coupon, estimatedGoodsAmount),
        })),
      ),
    [coupons, estimatedGoodsAmount],
  )

  const selectableCount = entries.filter((entry) => entry.evaluation.selectable).length

  return (
    <section
      aria-labelledby="checkout-coupon-heading"
      className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id="checkout-coupon-heading" className="text-base font-semibold text-foreground">
          优惠券
        </h2>
        {!isLoading && !isError ? (
          <span className="text-xs text-muted-foreground tabular-nums">
            共 {coupons.length} 张，其中 {selectableCount} 张可选
          </span>
        ) : null}
      </div>

      <p className="mt-1.5 text-xs leading-5 text-muted-foreground">
        优惠券适用性及最终优惠金额以服务端下单校验结果为准。当前选择只影响本单提交的优惠券 id。
      </p>

      {isLoading ? (
        <p className="mt-3 text-sm text-muted-foreground" role="status">
          正在加载优惠券…
        </p>
      ) : null}

      {isError ? (
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <p role="alert" className="text-sm text-danger">
            优惠券加载失败，可以不使用优惠券继续下单，或重新加载后再选。
          </p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            重新加载优惠券
          </Button>
        </div>
      ) : null}

      {!isLoading && !isError && coupons.length === 0 ? (
        <div className="mt-3 flex items-start gap-2 rounded-lg border border-dashed border-border-strong bg-gray-25 px-3 py-2.5 text-sm leading-6 text-muted-foreground">
          <Ticket className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <span>
            暂无优惠券。本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口。
          </span>
        </div>
      ) : null}

      {!isLoading && !isError && coupons.length > 0 ? (
        <fieldset className="mt-3">
          <legend className="sr-only">选择优惠券</legend>

          <label
            className={cn(
              'flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-sm transition-colors',
              selectedCouponId === null
                ? 'border-primary bg-primary-soft/60 text-foreground'
                : 'border-border hover:border-brand-300',
            )}
          >
            <span className="flex items-center gap-2">
              <input
                type="radio"
                name="checkout-coupon"
                value="none"
                checked={selectedCouponId === null}
                onChange={() => onSelect(null)}
                className="size-4 accent-primary"
              />
              不使用优惠券
            </span>
            <span className="text-xs text-muted-foreground">按服务端原价计算</span>
          </label>

          <ul className="mt-2 flex flex-col gap-2">
            {entries.map(({ coupon, evaluation }) => {
              const disabled = !evaluation.selectable
              const checked = selectedCouponId === coupon.id

              return (
                <li key={coupon.id}>
                  <label
                    className={cn(
                      'flex cursor-pointer flex-col gap-2 rounded-lg border px-3 py-2.5 text-sm transition-colors',
                      checked
                        ? 'border-primary bg-primary-soft/60'
                        : 'border-border hover:border-brand-300',
                      disabled && 'cursor-not-allowed opacity-60 hover:border-border',
                    )}
                  >
                    <span className="flex flex-wrap items-center justify-between gap-2">
                      <span className="flex min-w-0 items-center gap-2">
                        <input
                          type="radio"
                          name="checkout-coupon"
                          value={String(coupon.id)}
                          checked={checked}
                          disabled={disabled}
                          onChange={() => onSelect(coupon.id)}
                          aria-describedby={
                            evaluation.reason ? `coupon-reason-${String(coupon.id)}` : undefined
                          }
                          className="size-4 accent-primary"
                        />
                        <span className="min-w-0 truncate font-medium text-foreground">
                          {describeCouponName(coupon)}
                        </span>
                      </span>

                      <span className="flex items-center gap-2">
                        <span className="text-xs text-muted-foreground">减免</span>
                        <PriceDisplay value={coupon.discount_amount} size="sm" />
                      </span>
                    </span>

                    <span className="flex flex-wrap items-center gap-x-3 gap-y-1 pl-6 text-xs text-muted-foreground">
                      <span>{describeCouponThreshold(coupon.threshold_amount)}</span>
                      <span>有效期 {describeCouponValidity(coupon)}</span>
                      <span>状态 {describeCouponStatus(coupon.status)}</span>
                      {evaluation.selectable ? (
                        <span className="text-foreground">
                          预估优惠 {formatCentsAsYuan(evaluation.estimatedDiscount)}（以服务端为准）
                        </span>
                      ) : null}
                    </span>

                    {evaluation.reason ? (
                      <span
                        id={`coupon-reason-${String(coupon.id)}`}
                        className="pl-6 text-xs text-warning"
                      >
                        {evaluation.reason}
                      </span>
                    ) : null}
                  </label>
                </li>
              )
            })}
          </ul>
        </fieldset>
      ) : null}
    </section>
  )
}
