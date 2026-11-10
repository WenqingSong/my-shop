import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    id: 11,
    sku_id: 9,
    product_id: 3,
    sku_name: '默认规格',
    product_name: '测试商品',
    product_main_image: '',
    price: 999,
    quantity: 2,
    ...overrides,
  }
}

describe('我的订单列表', () => {
  it('无订单时展示空状态与去逛逛入口', async () => {
    await signInAsDemo()
    renderApp('/account/orders')

    expect(await screen.findByText('还没有订单')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去逛逛' })).toHaveAttribute('href', '/products')
  })

  it('展示订单号、商品、SKU、数量、金额与详情入口', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, {
      status: 'paid',
      created_at: '2026-11-10 15:15:00',
      original_amount: 1_998,
      discount_amount: 0,
      total_amount: 1_998,
      items: [makeItem({ product_name: '云裳 通勤托特包', sku_name: '标准款', quantity: 2 })],
    })

    renderApp('/account/orders')

    expect(await screen.findByRole('heading', { name: '我的订单' })).toBeInTheDocument()

    const card = (await screen.findByText('云裳 通勤托特包')).closest('article')
    expect(card).not.toBeNull()

    const scope = within(card as HTMLElement)
    expect(scope.getByText(order.order_no)).toBeInTheDocument()
    expect(scope.getByText('下单时间 2026-11-10 15:15:00')).toBeInTheDocument()
    // 状态徽标展示后端状态原文（paid → 已支付），分组标签「待发货」只是本地筛选。
    expect(scope.getByText('已支付')).toBeInTheDocument()
    expect(scope.getByText('规格：标准款 · 数量 2')).toBeInTheDocument()
    expect(scope.getByText('共 2 件商品')).toBeInTheDocument()
    // PriceDisplay 把符号/整数/小数拆成多个 span，用 textContent 断言整体金额。
    expect(card).toHaveTextContent('¥19.98')

    expect(scope.getByRole('link', { name: `查看订单 ${order.order_no} 详情` })).toHaveAttribute(
      'href',
      `/account/orders/${String(order.id)}`,
    )
  })

  it('状态筛选为本地过滤：各类订单只出现在对应分组', async () => {
    const user = userEvent.setup()
    await signInAsDemo()

    seedMockOrder(DEMO_USER_ID, {
      status: 'pending_payment',
      items: [makeItem({ product_name: '待支付商品' })],
    })
    seedMockOrder(DEMO_USER_ID, {
      status: 'completed',
      items: [makeItem({ product_name: '已完成商品' })],
    })
    seedMockOrder(DEMO_USER_ID, {
      status: 'cancelled',
      items: [makeItem({ product_name: '已取消商品' })],
    })

    renderApp('/account/orders')

    expect(await screen.findByText('共 3 笔订单')).toBeInTheDocument()
    expect(screen.getByText('待支付商品')).toBeInTheDocument()
    expect(screen.getByText('已完成商品')).toBeInTheDocument()
    expect(screen.getByText('已取消商品')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^待支付/ }))

    expect(await screen.findByText('共 1 笔订单')).toBeInTheDocument()
    expect(screen.getByText('待支付商品')).toBeInTheDocument()
    expect(screen.queryByText('已完成商品')).not.toBeInTheDocument()
    expect(screen.queryByText('已取消商品')).not.toBeInTheDocument()

    // 已关闭 = cancelled + refunded，属于前端分组而非后端状态。
    await user.click(screen.getByRole('button', { name: /^已关闭/ }))

    expect(await screen.findByText('共 1 笔订单')).toBeInTheDocument()
    expect(screen.getByText('已取消商品')).toBeInTheDocument()
    expect(screen.queryByText('待支付商品')).not.toBeInTheDocument()
  })

  it('筛选后无订单时展示分组空状态并可回到全部', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    renderApp('/account/orders')
    expect(await screen.findByText('共 1 笔订单')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^已完成/ }))

    expect(await screen.findByText('该状态下暂无订单')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '查看全部订单' }))

    await waitFor(() => {
      expect(screen.getByText('共 1 笔订单')).toBeInTheDocument()
    })
  })

  it('加载失败展示错误状态与重试入口', async () => {
    await signInAsDemo()
    server.use(
      http.get('/api/orders', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
    )

    renderApp('/account/orders')

    expect(await screen.findByText('订单加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })

  it('未登录访问跳转登录页', async () => {
    renderApp('/account/orders')

    expect(await screen.findByRole('heading', { name: '登录' })).toBeInTheDocument()
  })
})
