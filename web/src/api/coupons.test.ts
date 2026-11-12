import { describe, expect, it } from 'vitest'

import { ApiError } from '@/api/client'
import { applyRefund } from '@/api/refunds'
import { revokeRefund } from '@/api/refunds'
import { createOrder } from '@/api/orders'
import { fetchMyCoupons } from '@/api/coupons'
import { expireMockCoupon, seedMockCoupon } from '@/mocks/data/coupons'
import { createMockAddress } from '@/mocks/data/addresses'
import { mockCartSkuId, seedMockCartItem } from '@/mocks/data/cart'
import {
  cancelMockOrder,
  getMockOrder,
  payMockOrder,
} from '@/mocks/data/orders'
import { signInAsDemo } from '@/test/auth'

const DEMO_USER_ID = 1
const OTHER_USER_ID = 2

/** 与 mock 目录一致：sku price = 4,299.00 元。 */
const SKU_PRICE = 429_900

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

function seedOneItem() {
  seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
}

describe('优惠券 API（MSW，非真实后端）', () => {
  it('未登录访问我的优惠券返回 401', async () => {
    await expectApiError(fetchMyCoupons(), '1002', 401)
  })

  it('我的优惠券只返回本人券，按 id 倒序', async () => {
    await signInAsDemo()
    const first = seedMockCoupon(DEMO_USER_ID, { name: '第一张', discount_amount: 1_000 })
    const second = seedMockCoupon(DEMO_USER_ID, { name: '第二张', discount_amount: 2_000 })
    seedMockCoupon(OTHER_USER_ID, { name: '别人的券', discount_amount: 9_000 })

    const result = await fetchMyCoupons()

    expect(result.items.map((item) => item.id)).toEqual([second.id, first.id])
    expect(result.items.every((item) => item.name !== '别人的券')).toBe(true)
    // 状态枚举只有后端真实存在的三种。
    expect(result.items.every((item) => ['available', 'locked', 'used'].includes(item.status))).toBe(
      true,
    )
  })

  it('用券下单：服务端计算优惠金额，券变为 locked 并绑定订单', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const coupon = seedMockCoupon(DEMO_USER_ID, {
      name: '满减券',
      threshold_amount: 0,
      discount_amount: 50_000,
    })

    const order = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'coupon-order-1',
      coupon_id: coupon.id,
    })

    expect(order.original_amount).toBe(SKU_PRICE)
    expect(order.discount_amount).toBe(50_000)
    expect(order.total_amount).toBe(SKU_PRICE - 50_000)
    expect(order.user_coupon_id).toBe(coupon.id)

    const [after] = (await fetchMyCoupons()).items
    expect(after?.status).toBe('locked')
    expect(after?.order_id).toBe(order.id)
    expect(after?.locked_at).not.toBeNull()
  })

  it('不使用优惠券时 user_coupon_id 为 null，金额无优惠', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()

    const order = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'no-coupon-order',
    })

    expect(order.original_amount).toBe(SKU_PRICE)
    expect(order.discount_amount).toBe(0)
    expect(order.total_amount).toBe(SKU_PRICE)
    expect(order.user_coupon_id).toBeNull()
  })

  it('优惠金额不超过商品金额（INV-C06：min(减免, 原价)）', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const coupon = seedMockCoupon(DEMO_USER_ID, {
      threshold_amount: 0,
      discount_amount: SKU_PRICE + 100_000,
    })

    const order = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'cap-coupon-order',
      coupon_id: coupon.id,
    })

    expect(order.discount_amount).toBe(SKU_PRICE)
    expect(order.total_amount).toBe(0)
  })

  it('未达门槛、已使用、已过期、他人券都会被服务端拒绝（19007/19003）', async () => {
    await signInAsDemo()
    seedAddress()

    const tooHigh = seedMockCoupon(DEMO_USER_ID, { threshold_amount: SKU_PRICE + 1 })
    seedOneItem()
    await expectApiError(
      createOrder({
        source: 'cart',
        address_id: 1,
        idempotency_key: 'threshold-fail',
        coupon_id: tooHigh.id,
      }),
      '19007',
      409,
    )

    const used = seedMockCoupon(DEMO_USER_ID, { status: 'used' })
    await expectApiError(
      createOrder({
        source: 'cart',
        address_id: 1,
        idempotency_key: 'used-fail',
        coupon_id: used.id,
      }),
      '19007',
      409,
    )

    const expired = seedMockCoupon(DEMO_USER_ID)
    expireMockCoupon(expired.id)
    await expectApiError(
      createOrder({
        source: 'cart',
        address_id: 1,
        idempotency_key: 'expired-fail',
        coupon_id: expired.id,
      }),
      '19007',
      409,
    )

    const others = seedMockCoupon(OTHER_USER_ID)
    await expectApiError(
      createOrder({
        source: 'cart',
        address_id: 1,
        idempotency_key: 'others-fail',
        coupon_id: others.id,
      }),
      '19003',
      404,
    )
  })

  it('同一幂等键 + 同一内容（含同一张券）重试返回既有订单，不重复锁券', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const coupon = seedMockCoupon(DEMO_USER_ID, { discount_amount: 10_000 })

    const payload = {
      source: 'cart' as const,
      address_id: 1,
      idempotency_key: 'retry-same-coupon',
      coupon_id: coupon.id,
    }

    const first = await createOrder(payload)

    // 下单成功后购物车条目被清空（与真实后端一致），重试前重新加购同样的商品，
    // 才能复现「同键同内容」的幂等命中路径。
    seedOneItem()
    const second = await createOrder(payload)

    expect(second.id).toBe(first.id)
    const [after] = (await fetchMyCoupons()).items
    expect(after?.status).toBe('locked')
    expect(after?.order_id).toBe(first.id)
  })

  it('同一幂等键 + 不同券返回 9006（优惠券参与请求指纹）', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const firstCoupon = seedMockCoupon(DEMO_USER_ID, { discount_amount: 10_000 })
    const secondCoupon = seedMockCoupon(DEMO_USER_ID, { discount_amount: 20_000 })

    await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'same-key-different-coupon',
      coupon_id: firstCoupon.id,
    })

    // 换券后必须使用新幂等键；这里刻意复用旧键验证后端会拒绝。
    seedOneItem()
    await expectApiError(
      createOrder({
        source: 'cart',
        address_id: 1,
        idempotency_key: 'same-key-different-coupon',
        coupon_id: secondCoupon.id,
      }),
      '9006',
      409,
    )
  })

  it('支付后券核销为 used；取消订单后券恢复 available', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const coupon = seedMockCoupon(DEMO_USER_ID, { discount_amount: 10_000 })

    const order = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'pay-coupon-order',
      coupon_id: coupon.id,
    })

    payMockOrder(DEMO_USER_ID, order.id)
    const [afterPay] = (await fetchMyCoupons()).items
    expect(afterPay?.status).toBe('used')
    expect(afterPay?.used_at).not.toBeNull()

    // 第二单：下单后取消，券应释放。
    seedOneItem()
    const coupon2 = seedMockCoupon(DEMO_USER_ID, { discount_amount: 5_000 })
    const order2 = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'cancel-coupon-order',
      coupon_id: coupon2.id,
    })

    cancelMockOrder(DEMO_USER_ID, order2.id)
    const items = (await fetchMyCoupons()).items
    const released = items.find((item) => item.id === coupon2.id)
    expect(released?.status).toBe('available')
    expect(released?.order_id).toBeNull()
    expect(released?.locked_at).toBeNull()

    // 已核销的券不受其它订单取消影响。
    const stillUsed = items.find((item) => item.id === coupon.id)
    expect(stillUsed?.status).toBe('used')
  })

  it('退款申请与撤销不会返还优惠券（券保持 used）', async () => {
    await signInAsDemo()
    seedAddress()
    seedOneItem()
    const coupon = seedMockCoupon(DEMO_USER_ID, { discount_amount: 10_000 })

    const order = await createOrder({
      source: 'cart',
      address_id: 1,
      idempotency_key: 'refund-coupon-order',
      coupon_id: coupon.id,
    })
    payMockOrder(DEMO_USER_ID, order.id)

    const refund = await applyRefund(order.id, '不想要了')
    let [afterApply] = (await fetchMyCoupons()).items
    expect(afterApply?.status).toBe('used')

    await revokeRefund(refund.id)
    ;[afterApply] = (await fetchMyCoupons()).items
    expect(afterApply?.status).toBe('used')
    expect(afterApply?.order_id).toBe(order.id)

    // 退款申请不影响订单上的券快照。
    expect(getMockOrder(DEMO_USER_ID, order.id)?.user_coupon_id).toBe(coupon.id)
  })

})
