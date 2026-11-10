import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { RefundStatus } from '@/api/refunds'
import { seedMockOrder } from '@/mocks/data/orders'
import {
  getMockRefundWriteCount,
  seedMockRefund,
  setMockRefundDelay,
} from '@/mocks/data/refunds'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

describe('退款详情', () => {
  it('展示退款编号、状态、关联订单号、金额、申请原因与申请时间', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 209_900 })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, {
      status: 'applying',
      amount: 209_900,
      reason: '商品不合适，申请退款',
      created_at: '2026-11-10 18:00:00',
    })

    renderApp(`/account/refunds/${String(refund.id)}`)

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(screen.getByText(`退款编号 #${String(refund.id)}`)).toBeInTheDocument()
    // 状态同时出现在徽标与「当前状态」字段中，用 data 属性精确定位徽标。
    expect(document.querySelector('[data-refund-status="applying"]')).toHaveTextContent('申请中')
    expect(screen.getByText('商品不合适，申请退款')).toBeInTheDocument()
    expect(screen.getByText('申请时间')).toBeInTheDocument()
    // 申请时间与「最近更新」在 seed 数据里相同，两条都应展示。
    expect(screen.getAllByText('2026-11-10 18:00:00')).toHaveLength(2)
    // 金额来自后端 amount（整数分），页面不做任何换算。
    // PriceDisplay 把金额拆成多个 span 渲染，因此对整行做 textContent 断言。
    expect(screen.getByText('退款金额').closest('div')).toHaveTextContent('¥2,099.00')

    // 关联订单号需要再查一次订单详情；加载完成后展示订单号并可跳转。
    const orderLink = await screen.findByRole('link', { name: order.order_no })
    expect(orderLink).toHaveAttribute('href', `/account/orders/${String(order.id)}`)

    // 资金真实性声明必须存在，且不得出现真实渠道或到账承诺。
    expect(screen.getByText(/不代表真实资金已经退回/)).toBeInTheDocument()
    expect(screen.getByText(/不展示退款渠道、支付流水号或预计到账时间/)).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/支付宝|微信支付|银联|原路退回/)
  })

  it('已审核/已处理时展示后端实际写入的审核与处理信息', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, {
      status: 'review_rejected',
      reason: '申请退款',
      review_reason: '商品已发货，不支持仅退款',
      reviewed_at: '2026-11-10 19:00:00',
    })

    renderApp(`/account/refunds/${String(refund.id)}`)

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '审核信息' })).toBeInTheDocument()
    expect(screen.getByText('商品已发货，不支持仅退款')).toBeInTheDocument()
    expect(screen.getByText('2026-11-10 19:00:00')).toBeInTheDocument()
    // 未执行时不应出现「处理信息」区块。
    expect(screen.queryByRole('heading', { name: '处理信息' })).not.toBeInTheDocument()
    // 审核拒绝属于终态，不提供撤销入口。
    expect(screen.queryByRole('button', { name: '撤销退款申请' })).not.toBeInTheDocument()
  })

  it('退款成功状态展示执行时间与说明，但明确不承诺资金到账', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'refunded', refunded_at: '2026-11-10 20:00:00' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, {
      status: 'refund_succeeded',
      execute_reason: '人工确认退款完成',
      executed_at: '2026-11-10 20:00:00',
    })

    renderApp(`/account/refunds/${String(refund.id)}`)

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(document.querySelector('[data-refund-status="refund_succeeded"]')).toHaveTextContent(
      '退款成功',
    )
    expect(screen.getByText('人工确认退款完成')).toBeInTheDocument()
    expect(screen.getByText(/已在系统内处理完成/)).toBeInTheDocument()
    // 允许出现「不会承诺预计到账时间」这类否定说明，但不得出现到账结论。
    expect(document.body.textContent).not.toMatch(/已到账|已退回银行卡|资金已退回/)
  })

  it.each<[RefundStatus, string]>([
    ['applying', '申请中'],
    ['pending_refund', '待退款'],
    ['refund_failed', '退款失败'],
    ['review_rejected', '审核拒绝'],
    ['revoked', '已撤销'],
    ['refund_succeeded', '退款成功'],
    ['closed', '已关闭'],
  ])('状态 %s 映射为「%s」', async (status, label) => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status })

    renderApp(`/account/refunds/${String(refund.id)}`)

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(document.querySelector(`[data-refund-status="${status}"]`)).toHaveTextContent(label)
  })

  it('退款不存在或非本人时展示 404 占位', async () => {
    await signInAsDemo()
    renderApp('/account/refunds/99999')

    expect(await screen.findByText('退款不存在或无权查看')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回退款列表' })).toHaveAttribute(
      'href',
      '/account/refunds',
    )
  })

  it('非法退款编号不发起请求，直接展示占位', async () => {
    await signInAsDemo()
    renderApp('/account/refunds/not-a-number')

    expect(await screen.findByText('退款信息缺失')).toBeInTheDocument()
  })

  it('网络异常展示错误态，可重试', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying' })

    let calls = 0
    server.use(
      http.get('/api/refunds/:id', () => {
        calls += 1

        // 网络错误会按 hook 的 retry 策略重试一次，因此前两次都必须失败，
        // 页面才会稳定进入错误态；「重新加载」是第 3 次请求。
        if (calls <= 2) {
          return HttpResponse.error()
        }

        return HttpResponse.json({
          code: 0,
          message: 'OK',
          data: {
            id: refund.id,
            order_id: order.id,
            user_id: DEMO_USER_ID,
            status: 'applying',
            amount: 0,
            reason: '重试后的数据',
            review_reason: '',
            reviewed_by: null,
            reviewed_at: null,
            execute_reason: '',
            executed_by: null,
            executed_at: null,
            created_at: '2026-11-10 18:00:00',
            updated_at: '2026-11-10 18:00:00',
          },
        })
      }),
    )

    renderApp(`/account/refunds/${String(refund.id)}`)

    // 网络错误会重试一次（retryDelay 默认 1s），这里放宽等待时间。
    expect(await screen.findByText('退款详情加载失败', undefined, { timeout: 3_000 })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))

    expect(await screen.findByText('重试后的数据')).toBeInTheDocument()
  })
})

describe('退款撤销（详情页）', () => {
  it('申请中且撤销成功时状态变为已撤销，并展示成功反馈', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying' })

    renderApp(`/account/refunds/${String(refund.id)}`)

    await screen.findByRole('heading', { name: '退款详情' })
    await user.click(screen.getByRole('button', { name: '撤销退款申请' }))

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('撤销后本次退款申请将变为「已撤销」')
    await user.click(screen.getByRole('button', { name: '确认撤销' }))

    expect(
      await screen.findByText(/退款申请已撤销，订单保持已支付状态/),
    ).toBeInTheDocument()
    await waitFor(() => {
      expect(document.querySelector('[data-refund-status="revoked"]')).toHaveTextContent('已撤销')
    })
    expect(screen.queryByRole('button', { name: '撤销退款申请' })).not.toBeInTheDocument()
  })

  it('待退款状态不提供撤销入口', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status: 'pending_refund' })

    renderApp(`/account/refunds/${String(refund.id)}`)

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '撤销退款申请' })).not.toBeInTheDocument()
    expect(screen.getByText(/商家已审核通过，正在处理退款，此阶段不可撤销/)).toBeInTheDocument()
  })

  it('撤销失败（18002）展示业务错误并刷新为服务端真实状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying' })

    renderApp(`/account/refunds/${String(refund.id)}`)
    await screen.findByRole('heading', { name: '退款详情' })

    // 模拟管理员已经在另一处把申请推进到待退款。
    server.use(
      http.post('/api/refunds/:id/revoke', () =>
        HttpResponse.json({ code: 18002, message: '退款状态迁移非法', data: null }, { status: 409 }),
      ),
      http.get('/api/refunds/:id', () =>
        HttpResponse.json({
          code: 0,
          message: 'OK',
          data: {
            id: refund.id,
            order_id: order.id,
            user_id: DEMO_USER_ID,
            status: 'pending_refund',
            amount: 0,
            reason: refund.reason,
            review_reason: '审核通过',
            reviewed_by: 1,
            reviewed_at: '2026-11-10 19:00:00',
            execute_reason: '',
            executed_by: null,
            executed_at: null,
            created_at: refund.created_at,
            updated_at: '2026-11-10 19:00:00',
          },
        }),
      ),
    )

    await user.click(screen.getByRole('button', { name: '撤销退款申请' }))
    await user.click(await screen.findByRole('button', { name: '确认撤销' }))

    expect(
      await screen.findByText(/退款状态已变化（可能已审核通过），无法撤销，已为你刷新最新状态/),
    ).toBeInTheDocument()
    await waitFor(() => {
      expect(document.querySelector('[data-refund-status="pending_refund"]')).toHaveTextContent(
        '待退款',
      )
    })
  })

  it('撤销请求在途时禁用确认按钮，重复点击只发出一次请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    const refund = seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying' })

    setMockRefundDelay(120)
    renderApp(`/account/refunds/${String(refund.id)}`)
    await screen.findByRole('heading', { name: '退款详情' })

    await user.click(screen.getByRole('button', { name: '撤销退款申请' }))
    const confirm = await screen.findByRole('button', { name: '确认撤销' })
    await user.click(confirm)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '确认撤销' })).toBeDisabled()
    })

    await waitFor(() => {
      expect(screen.getByText(/退款申请已撤销/)).toBeInTheDocument()
    })
    expect(getMockRefundWriteCount()).toBe(1)
  })
})
