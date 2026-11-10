import { describe, expect, it } from 'vitest'

import { fetchCart } from '@/api/cart'
import { ApiError } from '@/api/client'
import { createOrder, fetchOrder, fetchOrders } from '@/api/orders'
import { createMockAddress } from '@/mocks/data/addresses'
import { mockCartSkuId, seedMockCartItem } from '@/mocks/data/cart'
import { getMockOrderCreatePayloads, seedMockOrder } from '@/mocks/data/orders'
import { signInAsDemo } from '@/test/auth'

const DEMO_USER_ID = 1
const KEY = 'w302-api-test-key-00000000000001'

async function catchError(run: () => Promise<unknown>): Promise<ApiError> {
  const error = await run().catch((reason: unknown) => reason)
  expect(error).toBeInstanceOf(ApiError)
  return error as ApiError
}

function seedAddress() {
  return createMockAddress(DEMO_USER_ID, {
    recipient_name: '张三',
    phone: '13800138000',
    province: '广东省',
    city: '深圳市',
    district: '南山区',
    detail: '科技园路 1 号',
    is_default: true,
  })
}

describe('订单 API（MSW Mock，非真实后端）', () => {
  it('未登录访问返回 401', async () => {
    const error = await catchError(() =>
      createOrder({ source: 'cart', address_id: 1, idempotency_key: KEY }),
    )

    expect(error.status).toBe(401)
    expect(error.code).toBe('1002')
  })

  it('购物车下单由服务端计算金额，并清理已购条目', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 1)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(3, 0), 1, false)

    const order = await createOrder({
      source: 'cart',
      address_id: address.id,
      idempotency_key: KEY,
    })

    // 4,299 × 2 + 1,999 = 10,597（整数分）
    expect(order.total_amount).toBe(1_059_700)
    expect(order.original_amount).toBe(1_059_700)
    expect(order.discount_amount).toBe(0)
    expect(order.status).toBe('pending_payment')
    // 订单号格式与后端一致：13 位毫秒时间戳 + 12 位随机 hex。
    expect(order.order_no).toMatch(/^\d{13}[0-9a-f]{12}$/)
    expect(order.items).toHaveLength(2)
    expect(order.recipient_name).toBe('张三')
    expect(order.address_id).toBe(address.id)

    // 已勾选条目被服务端删除，未勾选条目保留。
    const cart = await fetchCart()
    expect(cart.items).toHaveLength(1)
    expect(cart.items[0]?.sku_id).toBe(mockCartSkuId(3, 0))

    const payload = getMockOrderCreatePayloads()[0]
    expect(payload).toMatchObject({ source: 'cart', address_id: address.id })
  })

  it('同键同内容返回同一订单，同键不同内容返回 9006', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    const first = await createOrder({
      source: 'cart',
      address_id: address.id,
      idempotency_key: KEY,
    })

    // 真实后端在创建成功时已删除已购条目；这里重新放入相同内容，模拟同内容重放。
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    const replay = await createOrder({
      source: 'cart',
      address_id: address.id,
      idempotency_key: KEY,
    })

    expect(replay.id).toBe(first.id)

    // 购物车内容变化后复用同一幂等键 → 同键不同内容。
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 1)
    const error = await catchError(() =>
      createOrder({ source: 'cart', address_id: address.id, idempotency_key: KEY }),
    )

    expect(error.status).toBe(409)
    expect(error.code).toBe('9006')
  })

  it('成功下单后购物车已清空，直接重试同键会先命中 9004（后端行为）', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    await createOrder({ source: 'cart', address_id: address.id, idempotency_key: KEY })

    // 后端在幂等判定前先读取购物车：条目已被清理 → 9004，而不是返回既有订单。
    const error = await catchError(() =>
      createOrder({ source: 'cart', address_id: address.id, idempotency_key: KEY }),
    )

    expect(error.status).toBe(400)
    expect(error.code).toBe('9004')
  })

  it('无勾选商品返回 9004，地址不属于本人返回 7001', async () => {
    await signInAsDemo()
    const address = seedAddress()

    const emptyError = await catchError(() =>
      createOrder({ source: 'cart', address_id: address.id, idempotency_key: KEY }),
    )
    expect(emptyError.code).toBe('9004')

    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    const addressError = await catchError(() =>
      createOrder({ source: 'cart', address_id: 999_999, idempotency_key: KEY }),
    )
    expect(addressError.status).toBe(404)
    expect(addressError.code).toBe('7001')
  })

  it('订单详情仅本人可见，不存在返回 9001', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    const order = await createOrder({
      source: 'cart',
      address_id: address.id,
      idempotency_key: KEY,
    })

    await expect(fetchOrder(order.id)).resolves.toMatchObject({
      id: order.id,
      order_no: order.order_no,
    })

    const error = await catchError(() => fetchOrder(999_999))
    expect(error.status).toBe(404)
    expect(error.code).toBe('9001')
  })
})

describe('订单列表 API（GET /orders，MSW Mock）', () => {
  it('未登录访问返回 401', async () => {
    const error = await catchError(() => fetchOrders())

    expect(error.status).toBe(401)
    expect(error.code).toBe('1002')
  })

  it('仅返回本人订单，按 id 倒序，且每条都带商品明细', async () => {
    await signInAsDemo()

    const first = seedMockOrder(DEMO_USER_ID, { status: 'completed' })
    const second = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })
    // 其他用户的订单不得出现在当前用户列表里（服务端 WHERE user_id = ?）。
    seedMockOrder(DEMO_USER_ID + 1, { status: 'paid' })

    const result = await fetchOrders()

    expect(result.items.map((order) => order.id)).toEqual([second.id, first.id])
    expect(result.items.every((order) => order.user_id === DEMO_USER_ID)).toBe(true)
    expect(result.items[0]?.items).toHaveLength(1)
    expect(result.items[0]?.items[0]?.sku_name).toBe('默认规格')
  })

  it('空列表返回空数组，不是 null', async () => {
    await signInAsDemo()

    const result = await fetchOrders()

    expect(result.items).toEqual([])
  })
})
