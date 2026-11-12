import type { UserCoupon, UserCouponStatus } from '@/api/coupons'

/**
 * MSW 优惠券状态（仅测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 行为对齐 `internal/logic/coupon/coupon.go`：
 * - 列表：仅本人、按 id 倒序（LEFT JOIN 模板快照）；
 * - 下单用券：校验归属（19003）、状态 available、使用期、满减门槛（19007），
 *   优惠金额 = min(减免, 优惠前金额)；
 * - 下单锁定：available → locked（写 order_id/locked_at）；
 * - 支付核销：locked → used（写 used_at）；
 * - 取消释放：locked → available（清空 order_id/locked_at）；
 * - 退款不返还：Mock 不提供退款释放逻辑，与真实后端一致。
 *
 * 仅 Mock 提供的测试钩子（真实后端不存在）：
 * - `seedMockCoupon`：直接写入指定状态的券；
 * - `expireMockCoupon`：把使用有效期改到过去，构造「过期但状态仍 available」的场景。
 */

interface MockCouponRecord extends UserCoupon {
  /** 归属用户（真实接口不返回 user_id，Mock 内部用于隔离）。 */
  user_id: number
}

let coupons: MockCouponRecord[] = []
let sequence = 0

const DEFAULT_USE_START = '2025-01-01 00:00:00'
const DEFAULT_USE_END = '2027-01-01 00:00:00'

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

/** 剔除内部字段 user_id，返回与后端一致的结构。 */
function toUserCoupon(record: MockCouponRecord): UserCoupon {
  return {
    id: record.id,
    template_id: record.template_id,
    status: record.status,
    order_id: record.order_id,
    received_at: record.received_at,
    locked_at: record.locked_at,
    used_at: record.used_at,
    name: record.name,
    threshold_amount: record.threshold_amount,
    discount_amount: record.discount_amount,
    use_start_time: record.use_start_time,
    use_end_time: record.use_end_time,
  }
}

function parseTime(value: string | null): number | null {
  if (!value) {
    return null
  }

  const normalized = value.includes('T') ? value : value.replace(' ', 'T')
  const parsed = Date.parse(normalized)

  return Number.isNaN(parsed) ? null : parsed
}

export function resetMockCoupons(): void {
  coupons = []
  sequence = 0
}

export interface MockSeedCouponInput {
  status?: UserCouponStatus
  name?: string
  threshold_amount?: number
  discount_amount?: number
  use_start_time?: string | null
  use_end_time?: string | null
  order_id?: number | null
  received_at?: string
  locked_at?: string | null
  used_at?: string | null
}

/** 测试专用：直接写入一张指定状态的券（真实后端通过领取接口产生）。 */
export function seedMockCoupon(userId: number, input: MockSeedCouponInput = {}): UserCoupon {
  sequence += 1
  const receivedAt = input.received_at ?? nowText()
  const status = input.status ?? 'available'

  const record: MockCouponRecord = {
    id: sequence,
    user_id: userId,
    template_id: sequence,
    status,
    order_id: input.order_id ?? null,
    received_at: receivedAt,
    locked_at: input.locked_at ?? (status === 'locked' ? receivedAt : null),
    used_at: input.used_at ?? (status === 'used' ? receivedAt : null),
    name: input.name ?? `测试优惠券 ${String(sequence)}`,
    threshold_amount: input.threshold_amount ?? 0,
    discount_amount: input.discount_amount ?? 1_000,
    use_start_time: input.use_start_time === undefined ? DEFAULT_USE_START : input.use_start_time,
    use_end_time: input.use_end_time === undefined ? DEFAULT_USE_END : input.use_end_time,
  }

  coupons = [...coupons, record]

  return toUserCoupon(record)
}

/** 测试专用：把券的使用有效期改到过去（状态保持 available，模拟后端无 expired 状态）。 */
export function expireMockCoupon(couponId: number): void {
  coupons = coupons.map((coupon) =>
    coupon.id === couponId
      ? { ...coupon, use_start_time: '2020-01-01 00:00:00', use_end_time: '2021-01-01 00:00:00' }
      : coupon,
  )
}

/** 本人优惠券列表：按 id 倒序（与后端 ListMine 一致）。 */
export function listMockCoupons(userId: number): UserCoupon[] {
  return coupons
    .filter((coupon) => coupon.user_id === userId)
    .sort((a, b) => b.id - a.id)
    .map(toUserCoupon)
}

/** 按 id + 归属查券。 */
export function getMockCoupon(userId: number, couponId: number): UserCoupon | undefined {
  const coupon = coupons.find((item) => item.id === couponId && item.user_id === userId)
  return coupon ? toUserCoupon(coupon) : undefined
}

export type MockCouponMutationResult =
  | { ok: true }
  | { ok: false; status: number; code: number; message: string }

export type MockCouponEvaluation =
  | { ok: true; discountAmount: number }
  | { ok: false; status: number; code: number; message: string }

/**
 * 下单用券校验 + 计算优惠（对齐 GetDiscount，只读不落库）。
 *
 * 归属错误返回 19003，其余不可用一律 19007（与后端错误码语义一致）。
 */
export function evaluateMockCoupon(
  userId: number,
  couponId: number,
  originalAmount: number,
): MockCouponEvaluation {
  const coupon = coupons.find((item) => item.id === couponId)

  if (!coupon || coupon.user_id !== userId) {
    return { ok: false, status: 404, code: 19003, message: '优惠券不存在' }
  }

  // 注意：与后端 GetDiscount 一致，这里**不校验 status**。
  // 「券是否可用（available）」由下单事务内的预占闸门权威判定；
  // 若在此处校验，幂等重试（券已被本次下单锁定）会被误判为 19007。
  const now = Date.now()
  const start = parseTime(coupon.use_start_time)
  const end = parseTime(coupon.use_end_time)

  if ((start !== null && now < start) || (end !== null && now >= end)) {
    return { ok: false, status: 409, code: 19007, message: '优惠券不可用' }
  }

  if (originalAmount < coupon.threshold_amount) {
    return { ok: false, status: 409, code: 19007, message: '优惠券不可用' }
  }

  return { ok: true, discountAmount: Math.min(coupon.discount_amount, originalAmount) }
}

/** 下单预占：available → locked（写 order_id/locked_at）。 */
export function lockMockCoupon(userId: number, couponId: number, orderId: number): MockCouponMutationResult {
  const coupon = coupons.find((item) => item.id === couponId)

  if (!coupon || coupon.user_id !== userId) {
    return { ok: false, status: 404, code: 19003, message: '优惠券不存在' }
  }

  if (coupon.status !== 'available') {
    return { ok: false, status: 409, code: 19007, message: '优惠券不可用' }
  }

  const at = nowText()

  coupons = coupons.map((item) =>
    item.id === couponId
      ? { ...item, status: 'locked', order_id: orderId, locked_at: at }
      : item,
  )

  return { ok: true }
}

/** 支付核销：locked → used（按订单匹配，幂等）。 */
export function markMockCouponUsedOfOrder(orderId: number): void {
  const at = nowText()

  coupons = coupons.map((item) =>
    item.order_id === orderId && item.status === 'locked'
      ? { ...item, status: 'used', used_at: at }
      : item,
  )
}

/** 取消释放：locked → available，清空 order_id/locked_at（按订单匹配，幂等）。 */
export function releaseMockCouponOfOrder(orderId: number): void {
  coupons = coupons.map((item) =>
    item.order_id === orderId && item.status === 'locked'
      ? { ...item, status: 'available', order_id: null, locked_at: null }
      : item,
  )
}
