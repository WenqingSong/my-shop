import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import {
  cancelMockOrder,
  getMockOrderActionRequestCount,
  seedMockOrder,
  setMockOrderActionDelay,
} from '@/mocks/data/orders'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

async function openDetail(orderId: number) {
  renderApp(`/account/orders/${String(orderId)}`)
  await screen.findByRole('heading', { name: '订单详情' })
}

describe('订单操作入口（按后端状态闸门展示）', () => {
  it('待支付订单展示「模拟支付」与「取消订单」，不展示确认收货', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    await openDetail(order.id)

    expect(screen.getByRole('button', { name: '模拟支付' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消订单' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '确认收货' })).not.toBeInTheDocument()
  })

  it('已支付订单不展示模拟支付与取消入口', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openDetail(order.id)

    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消订单' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '确认收货' })).not.toBeInTheDocument()
    expect(screen.getByText(/订单已支付，等待商家发货/)).toBeInTheDocument()
  })

  it('已取消与已退款订单不展示任何操作入口', async () => {
    await signInAsDemo()
    const cancelled = seedMockOrder(DEMO_USER_ID, { status: 'cancelled' })
    const refunded = seedMockOrder(DEMO_USER_ID, { status: 'refunded' })

    const { unmount } = renderApp(`/account/orders/${String(cancelled.id)}`)
    await screen.findByRole('heading', { name: '订单详情' })
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消订单' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '确认收货' })).not.toBeInTheDocument()
    unmount()

    renderApp(`/account/orders/${String(refunded.id)}`)
    await screen.findByRole('heading', { name: '订单详情' })
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
  })
})

describe('模拟支付', () => {
  it('确认框明确说明是模拟支付、不产生真实扣款', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment', total_amount: 3_998 })

    await openDetail(order.id)
    await user.click(screen.getByRole('button', { name: '模拟支付' }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('heading', { name: '模拟支付确认' })).toBeInTheDocument()
    expect(within(dialog).getByText(/当前为项目模拟支付，不会产生真实资金扣款/)).toBeInTheDocument()
    expect(within(dialog).getByText(/不对接任何第三方支付渠道/)).toBeInTheDocument()
    // 不得出现真实支付品牌
    expect(dialog.textContent).not.toMatch(/支付宝|微信支付|银联/)
    expect(within(dialog).getByText(order.order_no)).toBeInTheDocument()
  })

  it('确认后调用真实接口，状态更新为已支付并提示成功', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    await openDetail(order.id)
    await user.click(screen.getByRole('button', { name: '模拟支付' }))

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认模拟支付' }))

    expect(
      await screen.findByText('模拟支付已完成，订单状态已更新为「已支付」。'),
    ).toBeInTheDocument()
    expect(document.querySelector('[data-status="paid"]')).toHaveTextContent('已支付')
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
    expect(screen.getByText(/支付时间/)).toBeInTheDocument()
  })

  it('请求在途时禁用确认按钮，重复点击只发出一次请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    setMockOrderActionDelay(120)
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    await openDetail(order.id)
    await user.click(screen.getByRole('button', { name: '模拟支付' }))

    const dialog = await screen.findByRole('dialog')
    const confirm = within(dialog).getByRole('button', { name: '确认模拟支付' })
    await user.click(confirm)

    await waitFor(() => {
      expect(within(dialog).getByRole('button', { name: '确认模拟支付' })).toBeDisabled()
    })
    expect(within(dialog).getByRole('button', { name: '确认模拟支付' })).toHaveAttribute(
      'aria-busy',
      'true',
    )

    await waitFor(() => {
      expect(screen.getByText(/模拟支付已完成/)).toBeInTheDocument()
    })
    expect(getMockOrderActionRequestCount()).toBe(1)
  })

  it('后端返回 9002 时展示业务错误，并刷新为服务端最新状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    // 先让页面加载出「待支付」，再模拟「服务端已被超时取消」的真实竞态。
    await openDetail(order.id)
    expect(document.querySelector('[data-status="pending_payment"]')).toHaveTextContent('待支付')

    cancelMockOrder(DEMO_USER_ID, order.id)
    server.use(
      http.post('/api/orders/:id/pay', () =>
        HttpResponse.json(
          { code: 9002, message: '订单状态迁移非法', data: null },
          { status: 409 },
        ),
      ),
    )

    await user.click(screen.getByRole('button', { name: '模拟支付' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认模拟支付' }))

    expect(
      await screen.findByText(/订单状态已变化（可能已超时自动取消），已为你刷新最新状态/),
    ).toBeInTheDocument()

    await waitFor(() => {
      expect(document.querySelector('[data-status="cancelled"]')).toHaveTextContent('已取消')
    })
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
  })

  it('网络异常时提示可重试，且不改动订单状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    server.use(http.post('/api/orders/:id/pay', () => HttpResponse.error()))

    await openDetail(order.id)
    await user.click(screen.getByRole('button', { name: '模拟支付' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认模拟支付' }))

    expect(await screen.findByText(/网络异常，请检查网络后重试/)).toBeInTheDocument()
    expect(document.querySelector('[data-status="pending_payment"]')).toHaveTextContent('待支付')
  })
})

describe('取消订单', () => {
  it('取消成功后状态为已取消，并展示用户取消原因', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    await openDetail(order.id)
    await user.click(screen.getByRole('button', { name: '取消订单' }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('heading', { name: '取消订单确认' })).toBeInTheDocument()
    expect(within(dialog).getByText(/服务端会在同一事务内恢复该订单占用的库存/)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: '确认取消订单' }))

    expect(await screen.findByText(/订单已取消，服务端已在同一事务内恢复库存/)).toBeInTheDocument()
    expect(document.querySelector('[data-status="cancelled"]')).toHaveTextContent('已取消')
    expect(screen.getByText(/取消原因：用户主动取消/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
  })
})

describe('确认收货', () => {
  it('已发货订单确认收货后直接完成', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'shipped' })

    await openDetail(order.id)
    expect(screen.queryByRole('button', { name: '模拟支付' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消订单' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '确认收货' }))

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认收货' }))

    expect(await screen.findByText('已确认收货，订单已完成。')).toBeInTheDocument()
    expect(document.querySelector('[data-status="completed"]')).toHaveTextContent('已完成')
    expect(screen.queryByRole('button', { name: '确认收货' })).not.toBeInTheDocument()
    expect(screen.getByText(/订单已完成，无需继续操作/)).toBeInTheDocument()
  })

  it('待发货订单不展示确认收货入口', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openDetail(order.id)

    expect(screen.queryByRole('button', { name: '确认收货' })).not.toBeInTheDocument()
  })
})
