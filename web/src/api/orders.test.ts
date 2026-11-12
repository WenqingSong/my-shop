import { describe, expect, it } from 'vitest'

import { fetchCart } from '@/api/cart'
import { ApiError } from '@/api/client'
import {
  cancelOrder,
  createOrder,
  fetchOrder,
  fetchOrders,
  payOrder,
  quoteOrder,
  receiveOrder,
} from '@/api/orders'
import { createMockAddress } from '@/mocks/data/addresses'
import { mockCartSkuId, seedMockCartItem } from '@/mocks/data/cart'
import { getMockCoupon, seedMockCoupon } from '@/mocks/data/coupons'
import {
  getMockOrderCreatePayloads,
  getMockQuotePayloads,
  seedMockOrder,
  setMockPricingBaseline,
  setMockQuoteErrorOnce,
} from '@/mocks/data/orders'
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
    expect(order.member_discount_amount).toBe(0)
    expect(order.shipping_fee).toBe(0)
    expect(order.pay_type).toBe('')
    expect(order.status).toBe('pending_payment')
    // 订单号格式与后端一致：13 位毫秒时间戳 + 12 位随机 hex。
    expect(order.order_no).toMatch(/^\d{13}[0-9a-f]{12}$/)
    expect(order.items).toHaveLength(2)
    expect(order.items[0]).toMatchObject({
      sku_id: 11,
      sku_code: 'SKU0101',
      spec_text: '',
      original_price: 429_900,
    })
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

describe('订单试算 API（POST /orders/quote，MSW Mock）', () => {
  it('未登录访问返回 401', async () => {
    const error = await catchError(() => quoteOrder({ source: 'cart', address_id: 1 }))

    expect(error.status).toBe(401)
    expect(error.code).toBe('1002')
  })

  it('购物车试算返回六槽位金额与明细，且不产生任何副作用', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    // 会员 95 折 + 8 元运费（真实后端分别来自会员权益与运费模板）。
    setMockPricingBaseline({ memberDiscountRateBp: 9_500, shippingFee: 800 })

    const quote = await quoteOrder({ source: 'cart', address_id: address.id })

    expect(quote.original_amount).toBe(859_800)
    expect(quote.coupon_discount).toBe(0)
    // floor(859800 × 500 / 10000) = 42990
    expect(quote.member_discount).toBe(42_990)
    expect(quote.points_discount).toBe(0)
    expect(quote.shipping_fee).toBe(800)
    expect(quote.payable_amount).toBe(817_610)
    expect(quote.items).toEqual([
      {
        sku_id: 11,
        sku_code: 'SKU0101',
        product_id: 1,
        name: '标准款',
        spec_text: '',
        price: 429_900,
        quantity: 2,
        amount: 859_800,
      },
    ])
    expect(getMockQuotePayloads().at(-1)).toMatchObject({
      source: 'cart',
      address_id: address.id,
    })

    // 试算只读：不扣库存、不锁券、不建单、不清购物车。
    expect((await fetchCart()).items).toHaveLength(1)
    expect((await fetchOrders()).items).toHaveLength(0)
  })

  it('优惠券只参与试算，不改变券状态', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    const coupon = seedMockCoupon(DEMO_USER_ID, {
      name: '试算券',
      threshold_amount: 0,
      discount_amount: 50_000,
    })

    const quote = await quoteOrder({
      source: 'cart',
      address_id: address.id,
      coupon_id: coupon.id,
    })

    expect(quote.original_amount).toBe(429_900)
    expect(quote.coupon_discount).toBe(50_000)
    expect(quote.payable_amount).toBe(379_900)

    // 券仍是 available（试算不占用、不核销）。
    expect(getMockCoupon(DEMO_USER_ID, coupon.id)?.status).toBe('available')
  })

  it('直接购买按 sku_code + 数量试算', async () => {
    await signInAsDemo()
    const address = seedAddress()

    const quote = await quoteOrder({
      source: 'direct',
      address_id: address.id,
      sku_code: 'SKU0101',
      quantity: 3,
    })

    expect(quote.original_amount).toBe(1_289_700)
    expect(quote.payable_amount).toBe(1_289_700)
    expect(quote.items[0]).toMatchObject({ sku_id: 11, quantity: 3, amount: 1_289_700 })
  })

  it('地址不属于本人返回 7001，无勾选商品返回 9004', async () => {
    await signInAsDemo()
    const address = seedAddress()

    const addressError = await catchError(() =>
      quoteOrder({ source: 'cart', address_id: 999_999 }),
    )
    expect(addressError.status).toBe(404)
    expect(addressError.code).toBe('7001')

    const emptyError = await catchError(() =>
      quoteOrder({ source: 'cart', address_id: address.id }),
    )
    expect(emptyError.status).toBe(400)
    expect(emptyError.code).toBe('9004')
  })

  it('服务端试算失败时抛出业务错误', async () => {
    await signInAsDemo()
    const address = seedAddress()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    setMockQuoteErrorOnce({ status: 500, code: 1000, message: '服务器内部错误' })

    const error = await catchError(() => quoteOrder({ source: 'cart', address_id: address.id }))

    expect(error.status).toBe(500)
    expect(error.code).toBe('1000')
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

describe('订单操作 API（MSW Mock）', () => {
  it('未登录时支付/取消/收货均返回 401', async () => {
    for (const run of [
      () => payOrder(1),
      () => cancelOrder(1),
      () => receiveOrder(1),
    ]) {
      const error = await catchError(run)
      expect(error.status).toBe(401)
      expect(error.code).toBe('1002')
    }
  })

  it('待支付订单可模拟支付，重复支付幂等且不改变 paid_at', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    const paid = await payOrder(order.id)

    expect(paid.status).toBe('paid')
    expect(paid.paid_at).toBeTruthy()

    const replay = await payOrder(order.id)

    expect(replay.status).toBe('paid')
    expect(replay.paid_at).toBe(paid.paid_at)
  })

  it('已取消订单不能支付（9002）', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'cancelled' })

    const error = await catchError(() => payOrder(order.id))

    expect(error.status).toBe(409)
    expect(error.code).toBe('9002')
  })

  it('待支付订单可取消，写入用户取消原因', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'pending_payment' })

    const cancelled = await cancelOrder(order.id)

    expect(cancelled.status).toBe('cancelled')
    expect(cancelled.cancel_reason).toBe(1)
    expect(cancelled.cancelled_at).toBeTruthy()
  })

  it('已支付订单不能取消（9002）', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    const error = await catchError(() => cancelOrder(order.id))

    expect(error.code).toBe('9002')
  })

  it('已发货订单确认收货后直接完成', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'shipped' })

    const completed = await receiveOrder(order.id)

    expect(completed.status).toBe('completed')
    expect(completed.completed_at).toBeTruthy()
  })

  it('未发货订单不能确认收货（9002）', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    const error = await catchError(() => receiveOrder(order.id))

    expect(error.code).toBe('9002')
  })

  it('操作他人的订单返回 9001', async () => {
    await signInAsDemo()
    const otherOrder = seedMockOrder(DEMO_USER_ID + 1, { status: 'pending_payment' })

    const error = await catchError(() => payOrder(otherOrder.id))

    expect(error.status).toBe(404)
    expect(error.code).toBe('9001')
  })
})
