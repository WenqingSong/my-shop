import type { UserCoupon, UserCouponStatus } from '@/api/coupons'

/**
 * 优惠券展示与**估算**工具。
 *
 * 事实来源：
 * - 状态枚举：`api/coupon/v1/coupon.go:10-15`（available/locked/used，无 expired）；
 * - 字段：`UserCoupon`（模板名、门槛、减免、使用期快照）；
 * - 使用规则：`internal/logic/coupon/coupon.go:274-315`（归属、使用期、门槛、减免封顶）。
 *
 * 边界：
 * - 这里的所有「是否可用」「可减多少」都是**前端估算**，不是服务端裁决；
 * - 最终以 `POST /orders` 的校验与返回金额为准（19007 表示优惠券不可用）；
 * - 前端不新增后端不存在的状态（例如 expired）。
 */

export const COUPON_STATUS_LABELS: Record<UserCouponStatus, string> = {
  available: '可使用',
  locked: '已锁定',
  used: '已使用',
}

export function describeCouponStatus(status: UserCouponStatus | string): string {
  return COUPON_STATUS_LABELS[status as UserCouponStatus] ?? status
}

export type CouponStatusTone = 'success' | 'warning' | 'neutral'

export function describeCouponStatusTone(status: UserCouponStatus | string): CouponStatusTone {
  switch (status) {
    case 'available':
      return 'success'
    case 'locked':
      return 'warning'
    case 'used':
    default:
      return 'neutral'
  }
}

/** 状态筛选：全部 + 后端真实存在的 3 个状态（纯前端筛选）。 */
export const COUPON_STATUS_FILTERS: readonly { id: 'all' | UserCouponStatus; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'available', label: COUPON_STATUS_LABELS.available },
  { id: 'locked', label: COUPON_STATUS_LABELS.locked },
  { id: 'used', label: COUPON_STATUS_LABELS.used },
]

export type CouponStatusFilterId = 'all' | UserCouponStatus

export function filterCouponsByStatus(
  coupons: readonly UserCoupon[],
  filter: CouponStatusFilterId,
): UserCoupon[] {
  if (filter === 'all') {
    return [...coupons]
  }

  return coupons.filter((coupon) => coupon.status === filter)
}

/** 后端满减门槛文案（0 = 无门槛）。 */
export function describeCouponThreshold(thresholdAmount: number): string {
  if (thresholdAmount <= 0) {
    return '无门槛'
  }

  return `满 ${formatCentsAsYuan(thresholdAmount)} 可用`
}

/** 整数分 → 展示用「¥1,234.00」；仅用于文案拼接，不参与交易计算。 */
export function formatCentsAsYuan(value: number): string {
  const negative = value < 0
  const absolute = Math.abs(value)
  const yuan = Math.floor(absolute / 100)
  const fen = absolute % 100
  const integer = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(yuan)

  return `${negative ? '-' : ''}¥${integer}.${String(fen).padStart(2, '0')}`
}

/** 优惠券名称兜底（模板被删除时后端 JOIN 会返回空串）。 */
export function describeCouponName(coupon: Pick<UserCoupon, 'name'>): string {
  return coupon.name.trim() || '优惠券'
}

/**
 * 解析后端 `YYYY-MM-DD HH:mm:ss` 时间。
 *
 * 不做时区转换：项目后端 DATETIME 无时区，前端按本地时间解释以保持一致
 * （与 W4-01 记录的时间处理原则一致，避免把数据问题伪装成正确时间）。
 */
function parseBackendTime(value: string | null): number | null {
  if (!value) {
    return null
  }

  const normalized = value.includes('T') ? value : value.replace(' ', 'T')
  const parsed = Date.parse(normalized)

  return Number.isNaN(parsed) ? null : parsed
}

/** 使用有效期展示（只展示后端字段，不推断状态）。 */
export function describeCouponValidity(coupon: Pick<UserCoupon, 'use_start_time' | 'use_end_time'>): string {
  const start = coupon.use_start_time ? coupon.use_start_time.slice(0, 19) : null
  const end = coupon.use_end_time ? coupon.use_end_time.slice(0, 19) : null

  if (!start || !end) {
    return '有效期以服务端校验为准'
  }

  return `${start} 至 ${end}`
}

/**
 * 结算页候选项的**前端估算**结果。
 *
 * reason 非空表示不可选，但仍会展示给用户（不隐藏后端返回的数据），
 * 便于用户理解为什么这张券不能用。
 */
export interface CouponCheckoutEvaluation {
  selectable: boolean
  /** 不可选原因（前端估算）；可选时为 null。 */
  reason: string | null
  /** 预估优惠金额（整数分）= min(减免, 预估商品金额)；不可选时为 0。 */
  estimatedDiscount: number
}

/**
 * 估算某张券在当前结算金额下是否可选。
 *
 * 判据顺序（全部来自后端字段，不属于新增业务状态）：
 * 1. 状态必须为 available（服务端在 LockInTx 里用 `status=available` 条件更新权威判定）；
 * 2. 当前时间需落在 [use_start_time, use_end_time)（服务端用 SQL NOW() 判定）；
 * 3. 预估商品金额需 ≥ threshold_amount（服务端用订单 original_amount 判定）。
 *
 * 注意：没有「商品适用范围」字段，因此不存在范围匹配判断。
 */
export function evaluateCouponForCheckout(
  coupon: UserCoupon,
  estimatedGoodsAmount: number,
  now: number = Date.now(),
): CouponCheckoutEvaluation {
  if (coupon.status !== 'available') {
    return {
      selectable: false,
      reason:
        coupon.status === 'locked'
          ? '该券已被订单占用，暂不可用'
          : '该券已使用，不可重复使用',
      estimatedDiscount: 0,
    }
  }

  const start = parseBackendTime(coupon.use_start_time)
  const end = parseBackendTime(coupon.use_end_time)

  if (start !== null && now < start) {
    return { selectable: false, reason: '尚未到该券的使用开始时间（按有效期估算）', estimatedDiscount: 0 }
  }

  if (end !== null && now >= end) {
    return { selectable: false, reason: '已超出该券的使用有效期（按有效期估算）', estimatedDiscount: 0 }
  }

  if (estimatedGoodsAmount < coupon.threshold_amount) {
    return {
      selectable: false,
      reason: `商品金额未达到使用门槛（需满 ${formatCentsAsYuan(coupon.threshold_amount)}，按预估金额判断）`,
      estimatedDiscount: 0,
    }
  }

  return {
    selectable: true,
    reason: null,
    // 与后端一致的封顶规则：优惠金额不大于商品金额（INV-C06）。
    estimatedDiscount: Math.min(coupon.discount_amount, estimatedGoodsAmount),
  }
}

/** 不可选券排在后面，可选券按预估优惠从高到低。 */
export function sortCouponsForCheckout(
  entries: readonly { coupon: UserCoupon; evaluation: CouponCheckoutEvaluation }[],
): { coupon: UserCoupon; evaluation: CouponCheckoutEvaluation }[] {
  return [...entries].sort((a, b) => {
    if (a.evaluation.selectable !== b.evaluation.selectable) {
      return a.evaluation.selectable ? -1 : 1
    }

    if (a.evaluation.selectable && b.evaluation.selectable) {
      return b.evaluation.estimatedDiscount - a.evaluation.estimatedDiscount
    }

    return b.coupon.id - a.coupon.id
  })
}
