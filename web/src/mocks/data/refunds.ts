import type { Refund, RefundStatus } from '@/api/refunds'
import { REFUND_REASON_MAX_LENGTH } from '@/api/refunds'
import { getMockOrder } from '@/mocks/data/orders'

/**
 * MSW 退款状态（仅测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 行为对齐 `internal/logic/refund/refund.go`：
 * - 申请：订单必须属于当前用户且 `status=paid`；reason trim 后非空且 ≤255 字符；
 *   金额由「服务端」取 `order.total_amount`，不接受客户端金额；
 *   同一订单至多一条有效申请（applying/pending_refund/refund_failed），否则 18004；
 * - 列表：仅本人、按 id 倒序，支持 order_id 过滤（与后端一致，无分页无状态筛选）；
 * - 详情：不存在或非本人统一 18001；
 * - 撤销：仅 applying → revoked，其余 18002。
 *
 * 仅 Mock 提供的测试钩子（真实后端不存在）：
 * - `setMockRefundDelay`：延迟写接口响应，用于验证「提交中」与防重复提交；
 * - `getMockRefundWriteCount`：写接口（申请 + 撤销）请求计数；
 * - `seedMockRefund`：直接写入指定状态的退款记录，用于构造审核拒绝/退款成功等场景。
 */

let refunds: Refund[] = []
let sequence = 0
let writeDelayMs = 0
let writeRequestCount = 0

/** 有效退款状态（与后端 activeRefundStatuses 一致）。 */
const ACTIVE_STATUSES: readonly RefundStatus[] = ['applying', 'pending_refund', 'refund_failed']

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

function runeLength(value: string): number {
  return Array.from(value).length
}

function clone(refund: Refund): Refund {
  return { ...refund }
}

export function resetMockRefunds(): void {
  refunds = []
  sequence = 0
  writeDelayMs = 0
  writeRequestCount = 0
}

/** 测试专用：设置退款写接口（申请/撤销）的响应延迟。 */
export function setMockRefundDelay(ms: number): void {
  writeDelayMs = ms
}

export function getMockRefundDelay(): number {
  return writeDelayMs
}

/** 退款写接口（申请 + 撤销）请求数，用于断言重复提交只发出一次请求。 */
export function getMockRefundWriteCount(): number {
  return writeRequestCount
}

export type MockRefundResult =
  | { ok: true; refund: Refund }
  | { ok: false; status: number; code: number; message: string }

const REFUND_NOT_FOUND: MockRefundResult = {
  ok: false,
  status: 404,
  code: 18001,
  message: '退款申请不存在',
}

/**
 * 发起退款申请（对齐 `internal/logic/refund/refund.go:125-196`）。
 *
 * 注意：Mock 不建模库存与优惠券——真实后端在申请阶段也不会动它们。
 */
export function applyMockRefund(userId: number, orderId: number, reason: unknown): MockRefundResult {
  writeRequestCount += 1

  const order = getMockOrder(userId, orderId)

  // 订单不存在与不属于当前用户统一 404/18001（防枚举，与后端一致）。
  if (!order) {
    return REFUND_NOT_FOUND
  }

  const text = typeof reason === 'string' ? reason.trim() : ''

  if (text === '' || runeLength(text) > REFUND_REASON_MAX_LENGTH) {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  if (order.status !== 'paid') {
    return { ok: false, status: 409, code: 18003, message: '订单不可退款' }
  }

  if (refunds.some((item) => item.order_id === orderId && ACTIVE_STATUSES.includes(item.status))) {
    return { ok: false, status: 409, code: 18004, message: '退款申请已存在' }
  }

  sequence += 1
  const createdAt = nowText()

  const refund: Refund = {
    id: sequence,
    order_id: orderId,
    user_id: userId,
    status: 'applying',
    // 与后端一致：金额取订单快照，不接受客户端金额。
    amount: order.total_amount,
    reason: text,
    review_reason: '',
    reviewed_by: null,
    reviewed_at: null,
    execute_reason: '',
    executed_by: null,
    executed_at: null,
    created_at: createdAt,
    updated_at: createdAt,
  }

  refunds = [...refunds, refund]

  return { ok: true, refund: clone(refund) }
}

/** 本人退款列表：按 id 倒序，可选 order_id 过滤。 */
export function listMockRefunds(userId: number, orderId?: number): Refund[] {
  return refunds
    .filter((refund) => refund.user_id === userId)
    .filter((refund) => (orderId && orderId > 0 ? refund.order_id === orderId : true))
    .sort((a, b) => b.id - a.id)
    .map(clone)
}

/** 本人退款详情；不存在或非本人返回 undefined（handler 转 18001）。 */
export function getMockRefund(userId: number, refundId: number): Refund | undefined {
  const refund = refunds.find((item) => item.id === refundId && item.user_id === userId)
  return refund ? clone(refund) : undefined
}

/** 撤销退款申请（对齐 `internal/logic/refund/refund.go:199-255`）。 */
export function revokeMockRefund(userId: number, refundId: number): MockRefundResult {
  writeRequestCount += 1

  const refund = refunds.find((item) => item.id === refundId && item.user_id === userId)

  if (!refund) {
    return REFUND_NOT_FOUND
  }

  if (refund.status !== 'applying') {
    return { ok: false, status: 409, code: 18002, message: '退款状态迁移非法' }
  }

  const updated: Refund = {
    ...refund,
    status: 'revoked',
    updated_at: nowText(),
  }

  refunds = refunds.map((item) => (item.id === updated.id ? updated : item))

  return { ok: true, refund: clone(updated) }
}

export interface MockSeedRefundInput {
  status?: RefundStatus
  amount?: number
  reason?: string
  review_reason?: string
  reviewed_by?: number | null
  reviewed_at?: string | null
  execute_reason?: string
  executed_by?: number | null
  executed_at?: string | null
  created_at?: string
}

/**
 * 测试专用：直接写入一条指定状态的退款记录（真实后端不存在该入口）。
 *
 * 用于构造「待退款 / 退款失败 / 审核拒绝 / 已撤销 / 退款成功 / 已关闭」等展示场景，
 * 避免测试必须经过后台审核与执行接口（管理端不在本任务范围）。
 */
export function seedMockRefund(
  userId: number,
  orderId: number,
  input: MockSeedRefundInput = {},
): Refund {
  sequence += 1
  const createdAt = input.created_at ?? nowText()

  const refund: Refund = {
    id: sequence,
    order_id: orderId,
    user_id: userId,
    status: input.status ?? 'applying',
    amount: input.amount ?? 0,
    reason: input.reason ?? '测试退款原因',
    review_reason: input.review_reason ?? '',
    reviewed_by: input.reviewed_by ?? null,
    reviewed_at: input.reviewed_at ?? null,
    execute_reason: input.execute_reason ?? '',
    executed_by: input.executed_by ?? null,
    executed_at: input.executed_at ?? null,
    created_at: createdAt,
    updated_at: createdAt,
  }

  refunds = [...refunds, refund]

  return clone(refund)
}
