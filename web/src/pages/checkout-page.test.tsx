import { HttpResponse, http } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { fetchCart } from '@/api/cart'
import { createMockAddress } from '@/mocks/data/addresses'
import {
  markMockProductOffShelf,
  mockCartSkuId,
  seedMockCartItem,
} from '@/mocks/data/cart'
import {
  createMockOrder,
  getMockOrderCreatePayloads,
  getMockOrderCreateRequestCount,
  setMockOrderErrorOnce,
  setMockOrderRequestDelay,
} from '@/mocks/data/orders'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1
const PRODUCT_ONE = '星海 X1 Pro 5G 手机 12GB+256GB'
const PRODUCT_TWO = '星海 Note 12 手机 8GB+128GB'

function seedDefaultAddress() {
  return createMockAddress(DEMO_USER_ID, {
    recipient_name: '张三',
    phone: '13800138000',
    province: '广东省',
    city: '深圳市',
    district: '南山区',
    detail: '科技园路 1 号 101 室',
    is_default: true,
  })
}

function orderInfoPanel() {
  return screen.getByRole('region', { name: '订单信息' })
}

describe('结算页', () => {
  it('未登录访问跳转登录，登录后回到结算页', async () => {
    const user = userEvent.setup()
    const { router } = renderApp('/checkout')

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')

    await user.type(screen.getByLabelText('用户名'), 'demo')
    await user.type(screen.getByLabelText('密码'), 'demo12345')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/checkout')
    })
    // 新账号没有购物车数据：展示「无勾选商品」空状态。
    expect(await screen.findByText('没有勾选的商品')).toBeInTheDocument()
  })

  it('没有勾选商品时展示空状态且不提供提交入口', async () => {
    await signInAsDemo()
    // 有商品但未勾选：结算只针对已勾选条目。
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1, false)

    renderApp('/checkout')

    expect(await screen.findByText('没有勾选的商品')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去购物车勾选' })).toHaveAttribute('href', '/cart')
    expect(screen.queryByRole('button', { name: '提交订单' })).not.toBeInTheDocument()
  })

  it('展示商品清单与预估金额，并明确标注不是后端试算', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 1)
    seedDefaultAddress()

    renderApp('/checkout')

    expect(await screen.findByRole('heading', { level: 1, name: '确认订单' })).toBeInTheDocument()

    const itemList = screen.getByRole('region', { name: '商品清单' })
    expect(within(itemList).getByText(PRODUCT_ONE)).toBeInTheDocument()
    expect(within(itemList).getByText(PRODUCT_TWO)).toBeInTheDocument()
    expect(within(itemList).getAllByText('规格：标准款')).toHaveLength(2)
    // 2 × ¥4,299.00 与 1 × ¥1,999.00（数量为 1 时单价与小计相同）
    expect(within(itemList).getByText('4,299')).toBeInTheDocument()
    expect(within(itemList).getAllByText('1,999')).toHaveLength(2)

    const amountPanel = screen.getByRole('complementary', { name: '订单金额' })
    expect(within(amountPanel).getByText('3 件')).toBeInTheDocument()
    // 4,299 × 2 + 1,999 = 10,597
    expect(within(amountPanel).getAllByText('10,597').length).toBeGreaterThanOrEqual(1)
    expect(
      within(amountPanel).getByText(/不是后端订单试算结果/),
    ).toBeInTheDocument()
    // 运费与优惠均未接入，不展示虚构金额。
    expect(within(amountPanel).getAllByText('暂未接入')).toHaveLength(2)

    // 默认地址自动选中
    expect(await screen.findByRole('radio', { name: '使用收货地址：张三' })).toBeChecked()
    expect(screen.getByRole('button', { name: '提交订单' })).toBeEnabled()
  })

  it('默认地址优先，支持切换收货地址', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    createMockAddress(DEMO_USER_ID, {
      recipient_name: '张三',
      phone: '13800138000',
      province: '广东省',
      city: '深圳市',
      district: '南山区',
      detail: '科技园路 1 号',
    })
    createMockAddress(DEMO_USER_ID, {
      recipient_name: '李四',
      phone: '13900139000',
      province: '上海市',
      city: '上海市',
      district: '浦东新区',
      detail: '世纪大道 2 号',
      is_default: true,
    })

    renderApp('/checkout')

    expect(await screen.findByRole('radio', { name: '使用收货地址：李四' })).toBeChecked()
    expect(screen.getByRole('radio', { name: '使用收货地址：张三' })).not.toBeChecked()

    await user.click(screen.getByRole('radio', { name: '使用收货地址：张三' }))

    expect(screen.getByRole('radio', { name: '使用收货地址：张三' })).toBeChecked()
    expect(screen.getByRole('radio', { name: '使用收货地址：李四' })).not.toBeChecked()
  })

  it('没有收货地址时提示新增并禁止提交', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    renderApp('/checkout')

    expect(await screen.findByText('还没有收货地址')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /新增收货地址/ })).toHaveAttribute(
      'href',
      '/account/addresses',
    )
    expect(screen.getByText('请选择收货地址后再提交订单。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '提交订单' })).toBeDisabled()
  })

  it('已勾选商品不可购买时阻止提交且不发起请求', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    seedDefaultAddress()
    markMockProductOffShelf(1)

    renderApp('/checkout')

    expect(
      await screen.findByText(/以下商品当前不可购买，无法提交订单/),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '提交订单' })).toBeDisabled()
    expect(getMockOrderCreateRequestCount()).toBe(0)
  })

  it('正常创建订单后进入成功页，并按后端快照清理已购条目', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const address = seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 1)
    // 未勾选条目：下单后应保留在购物车。
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(3, 0), 1, false)

    const { router } = renderApp('/checkout')
    await screen.findByRole('heading', { level: 1, name: '确认订单' })

    await user.click(screen.getByRole('button', { name: '提交订单' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/checkout/success')
    })
    expect(router.state.location.search).toContain('order_id=')

    expect(await screen.findByRole('heading', { level: 1, name: '订单提交成功' })).toBeInTheDocument()

    const payloads = getMockOrderCreatePayloads()
    expect(payloads).toHaveLength(1)
    expect(payloads[0]).toMatchObject({ source: 'cart', address_id: address.id })
    expect(String(payloads[0]?.idempotency_key)).toHaveLength(32)

    const info = orderInfoPanel()
    expect(within(info).getByText(/^\d{13}[0-9a-f]{12}$/)).toBeInTheDocument()
    expect(within(info).getByText('待支付')).toBeInTheDocument()
    // 金额来自后端返回：4,299 × 2 + 1,999 = 10,597
    expect(within(info).getByText('10,597')).toBeInTheDocument()
    expect(within(info).getByText('由服务端订单事务计算并返回')).toBeInTheDocument()

    // 收货快照
    expect(screen.getByText('张三')).toBeInTheDocument()
    expect(screen.getByText(/广东省深圳市南山区科技园路 1 号 101 室/)).toBeInTheDocument()

    // 已勾选条目被服务端删除，未勾选条目保留 → 徽标只统计剩余 1 件
    await waitFor(async () => {
      const cart = await fetchCart()
      expect(cart.items).toHaveLength(1)
      expect(cart.items[0]?.sku_id).toBe(mockCartSkuId(3, 0))
    })
    expect(await screen.findByRole('link', { name: '购物车，1 件商品' })).toBeInTheDocument()
  })

  it('提交中禁用按钮，连续点击只发送一次请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    setMockOrderRequestDelay(300)

    const { router } = renderApp('/checkout')
    await screen.findByRole('heading', { level: 1, name: '确认订单' })

    const submit = screen.getByRole('button', { name: '提交订单' })
    await user.click(submit)
    await user.click(submit)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '提交订单' })).toBeDisabled()
    })

    await waitFor(
      () => {
        expect(router.state.location.pathname).toBe('/checkout/success')
      },
      { timeout: 5_000 },
    )
    expect(getMockOrderCreateRequestCount()).toBe(1)
  })

  it('服务端库存不足时展示业务错误并保持购物车不变', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    setMockOrderErrorOnce({ status: 409, code: 6001, message: '库存不足' })

    const { router } = renderApp('/checkout')
    await screen.findByRole('heading', { level: 1, name: '确认订单' })

    await user.click(screen.getByRole('button', { name: '提交订单' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '库存不足，请返回购物车调整数量后重试',
    )
    expect(router.state.location.pathname).toBe('/checkout')
    expect((await fetchCart()).items).toHaveLength(1)
  })

  it('幂等键冲突（9006）提示刷新结算页', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    setMockOrderErrorOnce({ status: 409, code: 9006, message: '幂等键冲突' })

    renderApp('/checkout')
    await screen.findByRole('heading', { level: 1, name: '确认订单' })

    await user.click(screen.getByRole('button', { name: '提交订单' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('订单内容已变化，请刷新结算页后重新提交')
  })

  it('网络异常后重试复用同一幂等键，不会重复下单', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    let firstKey: string | null = null
    server.use(
      http.post('/api/orders', async ({ request }) => {
        const body = (await request.json()) as { idempotency_key?: string }
        firstKey = body.idempotency_key ?? null
        return HttpResponse.error()
      }),
    )

    const { router } = renderApp('/checkout')
    await screen.findByRole('heading', { level: 1, name: '确认订单' })

    await user.click(screen.getByRole('button', { name: '提交订单' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/不会重复创建订单/)
    expect(firstKey).toHaveLength(32)

    // 恢复默认 Mock handler，模拟用户点击重试。
    server.resetHandlers()
    await user.click(screen.getByRole('button', { name: '提交订单' }))

    await waitFor(
      () => {
        expect(router.state.location.pathname).toBe('/checkout/success')
      },
      { timeout: 5_000 },
    )

    const payloads = getMockOrderCreatePayloads()
    expect(payloads).toHaveLength(1)
    expect(payloads[0]?.idempotency_key).toBe(firstKey)
    // 只落一单
    expect(getMockOrderCreateRequestCount()).toBe(1)
  })
})

describe('下单成功页', () => {
  it('缺少或非法订单号时展示订单信息缺失', async () => {
    await signInAsDemo()
    renderApp('/checkout/success')

    expect(
      await screen.findByRole('heading', { level: 1, name: '订单信息缺失' }),
    ).toBeInTheDocument()
  })

  it('订单不存在或非本人时展示 404 状态', async () => {
    await signInAsDemo()
    renderApp('/checkout/success?order_id=999999')

    expect(
      await screen.findByRole('heading', { level: 1, name: '订单不存在或无权查看' }),
    ).toBeInTheDocument()
  })

  it('直接访问时以后端订单详情渲染金额与状态', async () => {
    await signInAsDemo()
    const address = seedDefaultAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    const result = createMockOrder(DEMO_USER_ID, {
      source: 'cart',
      address_id: address.id,
      idempotency_key: 'w302-direct-visit-key',
    })
    expect(result.ok).toBe(true)

    if (!result.ok) {
      return
    }

    renderApp(`/checkout/success?order_id=${String(result.order.id)}`)

    expect(
      await screen.findByRole('heading', { level: 1, name: '订单提交成功' }),
    ).toBeInTheDocument()
    const info = orderInfoPanel()
    expect(within(info).getByText(result.order.order_no)).toBeInTheDocument()
    expect(within(info).getByText('待支付')).toBeInTheDocument()
    expect(within(info).getByText('4,299')).toBeInTheDocument()
    expect(screen.getByText(/支付与订单管理将在后续阶段开放/)).toBeInTheDocument()
  })
})
