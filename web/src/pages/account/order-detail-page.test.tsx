import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { OrderItem } from '@/api/orders'
import { seedMockOrder } from '@/mocks/data/orders'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

function makeItem(overrides: Partial<OrderItem> = {}): OrderItem {
  return {
    id: 21,
    sku_id: 9,
    product_id: 3,
    sku_name: '标准款',
    product_name: '云裳 通勤托特包',
    product_main_image: '',
    price: 45_900,
    quantity: 2,
    ...overrides,
  }
}

describe('订单详情', () => {
  it('展示订单号、状态、商品、金额、收货快照与时间', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'shipped',
      created_at: '2026-11-10 15:15:00',
      expire_at: '2026-11-10 15:30:00',
      paid_at: '2026-11-10 15:16:00',
      shipped_at: '2026-11-10 18:00:00',
      original_amount: 96_800,
      discount_amount: 8_000,
      total_amount: 88_800,
      recipient_name: '张三',
      phone: '13800138000',
      province: '广东省',
      city: '深圳市',
      district: '南山区',
      detail: '科技园路 1 号',
      items: [makeItem()],
    })

    renderApp(`/account/orders/${String(order.id)}`)

    expect(await screen.findByRole('heading', { name: '订单详情' })).toBeInTheDocument()
    expect(screen.getByText(order.order_no)).toBeInTheDocument()
    expect(screen.getByText('已发货')).toBeInTheDocument()

    // 商品明细
    expect(screen.getByText('云裳 通勤托特包')).toBeInTheDocument()
    expect(screen.getByText('规格：标准款')).toBeInTheDocument()
    expect(screen.getByText('数量：2')).toBeInTheDocument()

    const itemsSection = screen.getByRole('region', { name: '商品明细' })
    expect(itemsSection).toHaveTextContent('¥459.00')

    // 金额三项全部来自后端快照，前端不做运算
    const amountSection = screen.getByRole('region', { name: '金额' })
    expect(amountSection).toHaveTextContent('商品总金额')
    expect(amountSection).toHaveTextContent('¥968.00')
    expect(amountSection).toHaveTextContent('优惠金额')
    expect(amountSection).toHaveTextContent('¥80.00')
    expect(amountSection).toHaveTextContent('应付金额')
    expect(amountSection).toHaveTextContent('¥888.00')

    // 收货快照
    const addressSection = screen.getByRole('region', { name: '收货信息' })
    expect(addressSection).toHaveTextContent('张三')
    expect(addressSection).toHaveTextContent('13800138000')
    expect(addressSection).toHaveTextContent('广东省深圳市南山区科技园路 1 号')

    // 时间信息：只展示非 null 字段
    const timeSection = screen.getByRole('region', { name: '时间信息' })
    expect(timeSection).toHaveTextContent('下单时间')
    expect(timeSection).toHaveTextContent('支付时间')
    expect(timeSection).toHaveTextContent('发货时间')
    expect(timeSection).not.toHaveTextContent('收货时间')
    expect(timeSection).not.toHaveTextContent('退款时间')
  })

  it('收货信息使用订单快照，不受地址簿影响', async () => {
    await signInAsDemo()
    // 地址簿为空，但订单快照里仍有收货人：证明展示的是订单快照而不是当前地址。
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'paid',
      recipient_name: '李四',
      phone: '13900139000',
      detail: '深南大道 2 号',
    })

    renderApp(`/account/orders/${String(order.id)}`)

    const addressSection = await screen.findByRole('region', { name: '收货信息' })
    expect(addressSection).toHaveTextContent('李四')
    expect(addressSection).toHaveTextContent('13900139000')
    expect(addressSection).toHaveTextContent('深南大道 2 号')
  })

  it('待支付订单展示服务端支付截止时间，且不提供前端倒计时', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'pending_payment',
      expire_at: '2026-11-10 15:30:00',
    })

    renderApp(`/account/orders/${String(order.id)}`)

    expect(await screen.findByText('订单详情')).toBeInTheDocument()
    expect(document.querySelector('[data-status="pending_payment"]')).toHaveTextContent('待支付')
    expect(screen.getByText(/支付截止时间（服务端返回）/)).toBeInTheDocument()
    expect(screen.getByText('2026-11-10 15:30:00')).toBeInTheDocument()
    // 不实现前端倒计时：页面明确说明倒计时缺失与后端自动取消的关系。
    expect(screen.getByText(/页面不提供前端倒计时与取消操作/)).toBeInTheDocument()
    // W4-02 起提供模拟支付与取消入口（具体行为由 order-actions.test.tsx 覆盖）。
    expect(screen.getByRole('button', { name: '模拟支付' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消订单' })).toBeInTheDocument()
  })

  it('expire_at 缺失时不展示支付截止时间', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment', expire_at: null })

    renderApp(`/account/orders/${String(order.id)}`)

    expect(await screen.findByText('订单详情')).toBeInTheDocument()
    expect(document.querySelector('[data-status="pending_payment"]')).toHaveTextContent('待支付')
    expect(screen.queryByText(/支付截止时间/)).not.toBeInTheDocument()
  })

  it('已取消订单展示终止状态与取消原因', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'cancelled',
      cancel_reason: 2,
      cancelled_at: '2026-11-10 15:31:00',
    })

    renderApp(`/account/orders/${String(order.id)}`)

    expect(await screen.findByText('订单已取消，交易终止')).toBeInTheDocument()
    expect(screen.getByText(/取消原因：超时未支付自动取消/)).toBeInTheDocument()
    expect(screen.getByText('已取消')).toBeInTheDocument()
  })

  it('已退款订单展示退款终止提示', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'refunded',
      paid_at: '2026-11-10 15:16:00',
      refunded_at: '2026-11-10 16:00:00',
    })

    renderApp(`/account/orders/${String(order.id)}`)

    expect(await screen.findByText('订单已退款，交易终止')).toBeInTheDocument()
    expect(screen.getByText('已退款')).toBeInTheDocument()
  })

  it('订单不存在或无权查看时展示 404 占位而不是空壳页面', async () => {
    await signInAsDemo()

    renderApp('/account/orders/999999')

    expect(await screen.findByText('订单不存在或无权查看')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '订单详情' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回订单列表' })).toHaveAttribute(
      'href',
      '/account/orders',
    )
  })

  it('加载失败展示错误状态与重试入口', async () => {
    await signInAsDemo()
    server.use(
      http.get('/api/orders/:id', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
    )

    renderApp('/account/orders/1')

    expect(await screen.findByText('订单详情加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })

  it('订单号非法时展示信息缺失占位，不发请求', async () => {
    await signInAsDemo()

    renderApp('/account/orders/not-a-number')

    expect(await screen.findByText('订单信息缺失')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回订单列表' })).toHaveAttribute(
      'href',
      '/account/orders',
    )
  })

  it('未登录访问跳转登录页', async () => {
    renderApp('/account/orders/1')

    expect(await screen.findByRole('heading', { name: '登录' })).toBeInTheDocument()
  })

  it('订单进度由当前状态推导并明确标注', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    renderApp(`/account/orders/${String(order.id)}`)

    const progressSection = await screen.findByRole('region', { name: '订单进度' })
    expect(progressSection).toHaveTextContent('待支付')
    expect(progressSection).toHaveTextContent('待发货')
    expect(progressSection).toHaveTextContent('待收货')
    expect(progressSection).toHaveTextContent('已完成')
    expect(within(progressSection).getByText(/不是服务端返回的历史事件记录/)).toBeInTheDocument()
  })
})
