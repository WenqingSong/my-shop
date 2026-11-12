import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { seedMockOrder } from '@/mocks/data/orders'
import { seedMockRefund } from '@/mocks/data/refunds'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

function ok<T>(data: T) {
  return { code: 0, message: 'OK', data }
}

describe('我的退款列表', () => {
  it('无退款时展示空状态与订单入口', async () => {
    await signInAsDemo()
    renderApp('/account/refunds')

    expect(await screen.findByText('还没有退款记录')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '查看我的订单' })).toHaveAttribute(
      'href',
      '/account/orders',
    )
  })

  it('展示退款编号、关联订单号、金额、原因、状态与申请时间', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'paid',
      total_amount: 209_900,
      original_amount: 259_900,
      discount_amount: 50_000,
    })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, {
      status: 'applying',
      amount: 209_900,
      reason: '下单后不想要了',
      created_at: '2026-11-10 18:00:00',
    })

    renderApp('/account/refunds')

    expect(await screen.findByRole('heading', { name: '我的退款' })).toBeInTheDocument()

    const card = (await screen.findByText(`#${String(refund.id)}`)).closest('article')
    expect(card).not.toBeNull()

    const scope = within(card as HTMLElement)
    expect(scope.getByText(order.order_no)).toBeInTheDocument()
    expect(scope.getByText('下单后不想要了')).toBeInTheDocument()
    expect(scope.getByText('申请时间 2026-11-10 18:00:00')).toBeInTheDocument()
    expect(scope.getByText('申请中')).toBeInTheDocument()
    expect(card).toHaveTextContent('¥2,099.00')
    // 明确声明状态不代表真实资金退回（Mock 与真实后端均为模拟支付）。
    expect(card).toHaveTextContent('不代表真实资金已经退回')
    expect(scope.getByRole('link', { name: `查看退款 ${String(refund.id)} 详情` })).toHaveAttribute(
      'href',
      `/account/refunds/${String(refund.id)}`,
    )
  })

  it('状态筛选是本地筛选，只保留所选状态的记录', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const paidOrder = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refundedOrder = seedMockOrder(DEMO_USER_ID, { status: 'refunded' })
    const applying = seedMockRefund(DEMO_USER_ID, paidOrder.id, { status: 'applying', reason: '申请中原因' })
    const succeeded = seedMockRefund(DEMO_USER_ID, refundedOrder.id, {
      status: 'refund_succeeded',
      reason: '已完成原因',
    })

    renderApp('/account/refunds')

    expect(await screen.findByText('申请中原因')).toBeInTheDocument()
    expect(screen.getByText('已完成原因')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /退款成功/ }))

    await waitFor(() => {
      expect(screen.queryByText('申请中原因')).not.toBeInTheDocument()
    })
    expect(screen.getByText('已完成原因')).toBeInTheDocument()
    expect(screen.getByText(`#${String(succeeded.id)}`)).toBeInTheDocument()
    expect(screen.queryByText(`#${String(applying.id)}`)).not.toBeInTheDocument()

    // 切回全部后两条都回来。
    await user.click(screen.getByRole('button', { name: /全部/ }))
    expect(await screen.findByText('申请中原因')).toBeInTheDocument()
  })

  it('加载失败展示错误态，可重新加载', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    let calls = 0
    server.use(
      http.get('/api/refunds', () => {
        calls += 1

        if (calls === 1) {
          return HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 })
        }

        return HttpResponse.json(ok({ items: [] }))
      }),
    )

    renderApp('/account/refunds')

    expect(await screen.findByText('退款记录加载失败')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '重新加载' }))
    expect(await screen.findByText('还没有退款记录')).toBeInTheDocument()
  })

  it('点击刷新重新请求服务端数据', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying', reason: '第一条' })

    let calls = 0
    server.use(
      http.get('/api/refunds', () => {
        calls += 1

        return HttpResponse.json(
          ok({
            items: [{ ...seedRefundPayload(order.id), id: calls === 1 ? 101 : 202, reason: calls === 1 ? '第一条' : '刷新后第二条' }],
          }),
        )
      }),
    )

    renderApp('/account/refunds')

    expect(await screen.findByText('第一条')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '刷新' }))

    expect(await screen.findByText('刷新后第二条')).toBeInTheDocument()
    expect(screen.queryByText('第一条')).not.toBeInTheDocument()
  })
})

/** 构造 GET /refunds 返回的字段完整退款对象（字段与后端 Refund 结构一致）。 */
function seedRefundPayload(orderId: number): Record<string, unknown> {
  return {
    id: 1,
    order_id: orderId,
    user_id: DEMO_USER_ID,
    status: 'applying',
    amount: 1_000,
    reason: '原因',
    review_reason: '',
    reviewed_by: null,
    reviewed_at: null,
    execute_reason: '',
    executed_by: null,
    executed_at: null,
    created_at: '2026-11-10 18:00:00',
    updated_at: '2026-11-10 18:00:00',
  }
}
