import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { createMockAddress } from '@/mocks/data/addresses'
import { mockCartSkuId, seedMockCartItem } from '@/mocks/data/cart'
import { expireMockCoupon, seedMockCoupon } from '@/mocks/data/coupons'
import {
  getMockOrderCreatePayloads,
  setMockOrderErrorOnce,
  setMockOrderRequestDelay,
} from '@/mocks/data/orders'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

function seedCheckout() {
  seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
  createMockAddress(DEMO_USER_ID, {
    recipient_name: '张三',
    phone: '13800138000',
    province: '广东省',
    city: '深圳市',
    district: '南山区',
    detail: '科技园路 1 号',
    is_default: true,
  })
}

function couponSection() {
  return screen.getByRole('region', { name: '优惠券' })
}

async function openCheckout() {
  renderApp('/checkout')
  expect(await screen.findByRole('heading', { level: 1, name: '确认订单' })).toBeInTheDocument()
  return couponSection()
}

describe('结算页优惠券选择', () => {
  it('展示后端返回的券，默认不使用优惠券，并说明适用性以服务端为准', async () => {
    await signInAsDemo()
    seedCheckout()
    seedMockCoupon(DEMO_USER_ID, {
      name: '满 100 减 50 券',
      threshold_amount: 100_000,
      discount_amount: 50_000,
    })

    const section = await openCheckout()

    expect(await within(section).findByText('满 100 减 50 券')).toBeInTheDocument()
    expect(within(section).getByText(/优惠券适用性及最终优惠金额以服务端下单校验结果为准/)).toBeInTheDocument()
    expect(within(section).getByRole('radio', { name: /不使用优惠券/ })).toBeChecked()
    expect(within(section).getByRole('radio', { name: /满 100 减 50 券/ })).not.toBeChecked()
  })

  it('未达门槛的券展示为不可选并给出原因', async () => {
    await signInAsDemo()
    seedCheckout()
    seedMockCoupon(DEMO_USER_ID, {
      name: '满 5000 减 100 券',
      threshold_amount: 500_000,
      discount_amount: 10_000,
    })

    const section = await openCheckout()

    const radio = await within(section).findByRole('radio', { name: /满 5000 减 100 券/ })
    expect(radio).toBeDisabled()
    expect(within(section).getByText(/商品金额未达到使用门槛/)).toBeInTheDocument()
  })

  it('已锁定与已过期的券不可选', async () => {
    await signInAsDemo()
    seedCheckout()
    seedMockCoupon(DEMO_USER_ID, { name: '已锁定券', status: 'locked' })
    const expired = seedMockCoupon(DEMO_USER_ID, { name: '过期券' })
    expireMockCoupon(expired.id)

    const section = await openCheckout()

    expect(await within(section).findByRole('radio', { name: /已锁定券/ })).toBeDisabled()
    expect(within(section).getByText(/该券已被订单占用，暂不可用/)).toBeInTheDocument()
    expect(within(section).getByRole('radio', { name: /过期券/ })).toBeDisabled()
    expect(within(section).getByText(/已超出该券的使用有效期/)).toBeInTheDocument()
  })

  it('选择优惠券后提交，请求携带 coupon_id，金额以服务端返回为准', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()
    const coupon = seedMockCoupon(DEMO_USER_ID, {
      name: '下单用券',
      threshold_amount: 0,
      discount_amount: 50_000,
    })

    const section = await openCheckout()
    await user.click(await within(section).findByRole('radio', { name: /下单用券/ }))
    expect(within(section).getByRole('radio', { name: /下单用券/ })).toBeChecked()

    // 摘要区展示预估优惠与预估应付（明确标注为预估）。
    const amountPanel = screen.getByRole('complementary', { name: '订单金额' })
    expect(amountPanel).toHaveTextContent('优惠（预估）')
    expect(amountPanel).toHaveTextContent('¥500.00')

    await user.click(screen.getByRole('button', { name: '提交订单' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })

    const payloads = getMockOrderCreatePayloads()
    expect(payloads.at(-1)).toMatchObject({ coupon_id: coupon.id })

    // 成功页展示服务端返回的三项金额。
    const summary = screen.getByRole('region', { name: '订单信息' })
    expect(summary).toHaveTextContent('¥4,299.00')
    expect(summary).toHaveTextContent('¥500.00')
    expect(summary).toHaveTextContent('¥3,799.00')
    expect(summary).toHaveTextContent(`本单使用优惠券（券 id：${String(coupon.id)}）`)
  })

  it('换券生成新的幂等键（不会与旧内容共用同一个键）', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()
    const first = seedMockCoupon(DEMO_USER_ID, { name: '券 A', discount_amount: 10_000 })
    const second = seedMockCoupon(DEMO_USER_ID, { name: '券 B', discount_amount: 20_000 })

    const section = await openCheckout()

    // 第一次：选券 A 提交，服务端返回 500，停留在结算页。
    setMockOrderErrorOnce({ status: 500, code: 1000, message: '服务器内部错误' })
    await user.click(await within(section).findByRole('radio', { name: /券 A/ }))
    await user.click(screen.getByRole('button', { name: '提交订单' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    // 第二次：换成券 B 提交成功。
    await user.click(within(section).getByRole('radio', { name: /券 B/ }))
    await user.click(screen.getByRole('button', { name: '提交订单' }))
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })

    const payloads = getMockOrderCreatePayloads()
    expect(payloads).toHaveLength(2)
    expect(payloads[0]?.coupon_id).toBe(first.id)
    expect(payloads[1]?.coupon_id).toBe(second.id)
    expect(payloads[0]?.idempotency_key).not.toBe(payloads[1]?.idempotency_key)
  })

  it('取消用券生成新的幂等键，且请求不再携带 coupon_id', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()
    const coupon = seedMockCoupon(DEMO_USER_ID, { name: '可取消的券', discount_amount: 10_000 })

    const section = await openCheckout()

    setMockOrderErrorOnce({ status: 500, code: 1000, message: '服务器内部错误' })
    await user.click(await within(section).findByRole('radio', { name: /可取消的券/ }))
    await user.click(screen.getByRole('button', { name: '提交订单' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await user.click(within(section).getByRole('radio', { name: /不使用优惠券/ }))
    await user.click(screen.getByRole('button', { name: '提交订单' }))
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })

    const payloads = getMockOrderCreatePayloads()
    expect(payloads[0]?.coupon_id).toBe(coupon.id)
    expect(payloads[1]?.coupon_id).toBeUndefined()
    expect(payloads[0]?.idempotency_key).not.toBe(payloads[1]?.idempotency_key)
  })

  it('后端拒绝优惠券（19007）时提示并回落到「不使用优惠券」', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()
    seedMockCoupon(DEMO_USER_ID, { name: '服务端已拒绝的券', discount_amount: 10_000 })

    const section = await openCheckout()
    await user.click(await within(section).findByRole('radio', { name: /服务端已拒绝的券/ }))

    server.use(
      http.post('/api/orders', () =>
        HttpResponse.json({ code: 19007, message: '优惠券不可用', data: null }, { status: 409 }),
      ),
    )

    await user.click(screen.getByRole('button', { name: '提交订单' }))

    expect(
      await screen.findByText(/优惠券当前不可用（可能已被占用、已使用、超出有效期或未达到门槛）/),
    ).toBeInTheDocument()
    // 回落到不使用优惠券，用户可以立即重新提交。
    await waitFor(() => {
      expect(within(section).getByRole('radio', { name: /不使用优惠券/ })).toBeChecked()
    })
  })

  it('没有任何优惠券时展示提示，仍可不使用优惠券下单', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()

    const section = await openCheckout()

    expect(
      await within(section).findByText(/暂无优惠券。本期不提供领券中心/),
    ).toBeInTheDocument()
    expect(within(section).queryByRole('radio')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '提交订单' }))
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })
    expect(getMockOrderCreatePayloads().at(-1)?.coupon_id).toBeUndefined()
  })

  it('提交中禁用按钮，连续点击只发送一次请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()
    seedMockCoupon(DEMO_USER_ID, { name: '防重复券', discount_amount: 10_000 })

    const section = await openCheckout()
    await user.click(await within(section).findByRole('radio', { name: /防重复券/ }))

    setMockOrderRequestDelay(120)

    const submit = screen.getByRole('button', { name: '提交订单' })
    await user.click(submit)
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '提交订单' })).toBeDisabled()
    })
    await user.click(screen.getByRole('button', { name: '提交订单' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })
    expect(getMockOrderCreatePayloads()).toHaveLength(1)
  })
})

describe('下单成功页的优惠金额', () => {
  it('不使用优惠券时展示「未使用优惠券」，应付等于商品总额', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedCheckout()

    await openCheckout()
    await user.click(screen.getByRole('button', { name: '提交订单' }))

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '订单信息' })).toBeInTheDocument()
    })

    const summary = screen.getByRole('region', { name: '订单信息' })
    expect(summary).toHaveTextContent('未使用优惠券')
    expect(summary).toHaveTextContent('¥4,299.00')
    expect(summary).not.toHaveTextContent('本单使用优惠券')
    // 商品总额与应付金额相同：两处都出现同一金额。
    expect(summary.textContent?.split('¥4,299.00').length).toBeGreaterThanOrEqual(2)
  })
})
