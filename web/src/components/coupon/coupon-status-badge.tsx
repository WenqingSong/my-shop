import type { UserCouponStatus } from '@/api/coupons'
import { cn } from '@/utils/cn'
import { describeCouponStatus, describeCouponStatusTone, type CouponStatusTone } from '@/utils/coupons'

const toneClass: Record<CouponStatusTone, string> = {
  success: 'bg-success-soft text-success',
  warning: 'bg-warning-soft text-warning',
  neutral: 'bg-gray-100 text-gray-600',
}

export interface CouponStatusBadgeProps {
  status: UserCouponStatus | string
  className?: string
}

/**
 * 优惠券状态徽标。
 *
 * 只映射后端真实存在的 3 个状态（available/locked/used）；
 * 后端没有 expired 状态，过期只体现为使用有效期已过，由服务端下单时拒绝。
 */
export function CouponStatusBadge({ status, className }: CouponStatusBadgeProps) {
  return (
    <span
      data-coupon-status={status}
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium',
        toneClass[describeCouponStatusTone(status)],
        className,
      )}
    >
      {describeCouponStatus(status)}
    </span>
  )
}
