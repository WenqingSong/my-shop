import { describe, expect, it } from 'vitest'

import { ApiError } from '@/api/client'
import { applyRefund, fetchRefund, fetchRefunds, revokeRefund } from '@/api/refunds'
import { seedMockOrder } from '@/mocks/data/orders'
import { seedMockRefund } from '@/mocks/data/refunds'
import { signInAsDemo } from '@/test/auth'

const DEMO_USER_ID = 1
const OTHER_USER_ID = 2

async function expectApiError(promise: Promise<unknown>, code: string, status: number) {
  const error = await promise.then(
    () => null,
    (reason: unknown) => reason,
  )

  expect(error).toBeInstanceOf(ApiError)
  const apiError = error as ApiError
  expect(apiError.code).toBe(code)
  expect(apiError.status).toBe(status)
}

describe('退款 API（MSW，非真实后端）', () => {
  it('未登录时四个退款接口均返回 401', async () => {
    await expectApiError(applyRefund(1, '不想要了'), '1002', 401)
    await expectApiError(fetchRefunds(), '1002', 401)
    await expectApiError(fetchRefund(1), '1002', 401)
    await expectApiError(revokeRefund(1), '1002', 401)
  })

  it('已支付未发货订单可以申请整单退款，金额由服务端按订单实付金额派生', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'paid',
      original_amount: 2_599,
      discount_amount: 500,
      total_amount: 2_099,
    })

    const refund = await applyRefund(order.id, '  不想要了  ')

    expect(refund.status).toBe('applying')
    expect(refund.order_id).toBe(order.id)
    expect(refund.user_id).toBe(DEMO_USER_ID)
    // 服务端派生：等于订单 total_amount，而不是 original_amount。
    expect(refund.amount).toBe(2_099)
    // 原因按后端 TrimSpace 语义保存。
    expect(refund.reason).toBe('不想要了')
    expect(refund.review_reason).toBe('')
    expect(refund.reviewed_at).toBeNull()
    expect(refund.executed_at).toBeNull()
    expect(refund.created_at).not.toBeNull()
  })

  it('待支付 / 已发货 / 已取消 / 已退款订单不能申请退款（18003）', async () => {
    await signInAsDemo()

    for (const status of ['pending_payment', 'shipped', 'cancelled', 'refunded'] as const) {
      const order = seedMockOrder(DEMO_USER_ID, { status })
      await expectApiError(applyRefund(order.id, '不想要了'), '18003', 409)
    }
  })

  it('退款原因为空或超过 255 字符时返回 1001', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await expectApiError(applyRefund(order.id, '   '), '1001', 400)
    await expectApiError(applyRefund(order.id, '退'.repeat(256)), '1001', 400)
  })

  it('同一订单已有有效申请时再次申请返回 18004', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await applyRefund(order.id, '第一次申请')
    await expectApiError(applyRefund(order.id, '重复申请'), '18004', 409)
  })

  it('已撤销的申请不阻止再次申请', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'revoked' })

    const refund = await applyRefund(order.id, '重新申请')
    expect(refund.status).toBe('applying')
  })

  it('他人订单与不存在的订单统一返回 18001（防枚举）', async () => {
    await signInAsDemo()
    const otherOrder = seedMockOrder(OTHER_USER_ID, { status: 'paid' })

    await expectApiError(applyRefund(otherOrder.id, '越权尝试'), '18001', 404)
    await expectApiError(applyRefund(99_999, '不存在'), '18001', 404)
  })

  it('列表只返回本人退款，按 id 倒序，并支持 order_id 过滤', async () => {
    await signInAsDemo()
    const firstOrder = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 1_000 })
    const secondOrder = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 2_000 })
    seedMockOrder(OTHER_USER_ID, { status: 'paid' })

    const first = await applyRefund(firstOrder.id, '第一单退款')
    const second = await applyRefund(secondOrder.id, '第二单退款')
    seedMockRefund(OTHER_USER_ID, 999, { status: 'applying' })

    const all = await fetchRefunds()
    expect(all.items.map((item) => item.id)).toEqual([second.id, first.id])
    expect(all.items.every((item) => item.user_id === DEMO_USER_ID)).toBe(true)

    const filtered = await fetchRefunds(secondOrder.id)
    expect(filtered.items).toHaveLength(1)
    expect(filtered.items[0]?.id).toBe(second.id)
  })

  it('详情：本人可读，他人与不存在统一 18001', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = await applyRefund(order.id, '详情测试')

    const detail = await fetchRefund(refund.id)
    expect(detail.id).toBe(refund.id)

    const otherRefund = seedMockRefund(OTHER_USER_ID, 999, { status: 'applying' })
    await expectApiError(fetchRefund(otherRefund.id), '18001', 404)
    await expectApiError(fetchRefund(99_999), '18001', 404)
  })

  it('撤销：仅申请中可撤销，成功后状态为 revoked 且订单不受影响', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = await applyRefund(order.id, '要撤销')

    const revoked = await revokeRefund(refund.id)
    expect(revoked.status).toBe('revoked')

    const detail = await fetchRefund(refund.id)
    expect(detail.status).toBe('revoked')
    // 撤销不改变订单状态：订单仍为 paid（由订单接口另行验证）。
  })

  it('撤销：非申请中状态返回 18002，他人退款返回 18001', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const pending = seedMockRefund(DEMO_USER_ID, order.id, { status: 'pending_refund' })

    await expectApiError(revokeRefund(pending.id), '18002', 409)

    const otherRefund = seedMockRefund(OTHER_USER_ID, 999, { status: 'applying' })
    await expectApiError(revokeRefund(otherRefund.id), '18001', 404)
  })
})
