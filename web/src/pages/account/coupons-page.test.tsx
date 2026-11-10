import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { queryKeys } from '@/api/query-keys'
import { seedMockCoupon } from '@/mocks/data/coupons'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1
const OTHER_USER_ID = 2

describe('我的优惠券', () => {
  it('无优惠券时展示空状态，并说明本期不提供领券中心', async () => {
    await signInAsDemo()
    renderApp('/account/coupons')

    expect(await screen.findByText('还没有优惠券')).toBeInTheDocument()
    expect(
      screen.getByText(/本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口/),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去逛逛' })).toHaveAttribute('href', '/products')
  })

  it('展示券名、减免金额、门槛、有效期、状态与关联订单', async () => {
    await signInAsDemo()
    const coupon = seedMockCoupon(DEMO_USER_ID, {
      name: '双十一满减券',
      threshold_amount: 200_000,
      discount_amount: 50_000,
      status: 'locked',
      order_id: 42,
      use_start_time: '2026-11-01 00:00:00',
      use_end_time: '2026-11-30 23:59:59',
      received_at: '2026-11-10 10:00:00',
      locked_at: '2026-11-10 11:00:00',
    })

    renderApp('/account/coupons')

    expect(await screen.findByRole('heading', { name: '我的优惠券' })).toBeInTheDocument()

    const card = (await screen.findByText('双十一满减券')).closest('article')
    expect(card).not.toBeNull()

    expect(card).toHaveTextContent('¥500.00')
    expect(card).toHaveTextContent('满 ¥2,000.00 可用')
    expect(card).toHaveTextContent('2026-11-01 00:00:00 至 2026-11-30 23:59:59')
    expect(card).toHaveTextContent('2026-11-10 10:00:00')
    expect(card).toHaveTextContent('2026-11-10 11:00:00')
    expect(document.querySelector('[data-coupon-status="locked"]')).toHaveTextContent('已锁定')
    expect(
      screen.getByRole('link', { name: `查看使用该优惠券的订单 #${String(coupon.order_id)}` }),
    ).toHaveAttribute('href', '/account/orders/42')
  })

  it.each([
    ['available', '可使用', '可在结算页选择使用'],
    ['locked', '已锁定', '支付后核销为「已使用」'],
    ['used', '已使用', '退款成功也不会返还优惠券'],
  ] as const)('状态 %s 映射为「%s」并给出对应说明', async (status, label, hint) => {
    await signInAsDemo()
    seedMockCoupon(DEMO_USER_ID, { name: `${label}的券`, status })

    renderApp('/account/coupons')

    // 先等待券数据渲染完成，再断言徽标（否则会与 loading 态竞争）。
    const card = (await screen.findByText(`${label}的券`)).closest('article')
    expect(card).not.toBeNull()
    // 说明文案在页面底部的「使用说明」中也出现同义句，因此限定在卡片内断言。
    expect(card).toHaveTextContent(label)
    expect(card).toHaveTextContent(hint)
    expect(document.querySelector(`[data-coupon-status="${status}"]`)).toHaveTextContent(label)
  })

  it('本地状态筛选只保留所选状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCoupon(DEMO_USER_ID, { name: '可用券', status: 'available' })
    seedMockCoupon(DEMO_USER_ID, { name: '锁定券', status: 'locked' })
    seedMockCoupon(DEMO_USER_ID, { name: '已用券', status: 'used' })

    renderApp('/account/coupons')

    expect(await screen.findByText('可用券')).toBeInTheDocument()
    expect(screen.getByText('锁定券')).toBeInTheDocument()
    expect(screen.getByText('已用券')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^已锁定/ }))

    await waitFor(() => {
      expect(screen.queryByText('可用券')).not.toBeInTheDocument()
    })
    expect(screen.getByText('锁定券')).toBeInTheDocument()
    expect(screen.queryByText('已用券')).not.toBeInTheDocument()
    expect(screen.getByText('共 1 张优惠券')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^全部/ }))
    expect(await screen.findByText('可用券')).toBeInTheDocument()
  })

  it('加载失败展示错误态并可重试', async () => {
    const user = userEvent.setup()
    await signInAsDemo()

    let calls = 0
    server.use(
      http.get('/api/my/coupons', () => {
        calls += 1

        if (calls === 1) {
          return HttpResponse.json(
            { code: 1000, message: '服务器内部错误', data: null },
            { status: 500 },
          )
        }

        return HttpResponse.json({ code: 0, message: 'OK', data: { items: [] } })
      }),
    )

    renderApp('/account/coupons')

    expect(await screen.findByText('优惠券加载失败')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    expect(await screen.findByText('还没有优惠券')).toBeInTheDocument()
  })

  it('点击刷新重新请求服务端数据', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCoupon(DEMO_USER_ID, { name: '初始券' })

    let calls = 0
    server.use(
      http.get('/api/my/coupons', () => {
        calls += 1

        return HttpResponse.json({
          code: 0,
          message: 'OK',
          data: {
            items: [
              {
                id: 1,
                template_id: 1,
                status: 'available',
                order_id: null,
                received_at: '2026-11-10 10:00:00',
                locked_at: null,
                used_at: null,
                name: calls === 1 ? '初始券' : '刷新后的券',
                threshold_amount: 0,
                discount_amount: 1_000,
                use_start_time: '2025-01-01 00:00:00',
                use_end_time: '2027-01-01 00:00:00',
              },
            ],
          },
        })
      }),
    )

    renderApp('/account/coupons')

    expect(await screen.findByText('初始券')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '刷新' }))
    expect(await screen.findByText('刷新后的券')).toBeInTheDocument()
  })

  it('账号隔离：只展示本人券', async () => {
    await signInAsDemo()
    seedMockCoupon(DEMO_USER_ID, { name: '我的券' })
    seedMockCoupon(OTHER_USER_ID, { name: '别人的券' })

    renderApp('/account/coupons')

    expect(await screen.findByText('我的券')).toBeInTheDocument()
    expect(screen.queryByText('别人的券')).not.toBeInTheDocument()
  })

  it('退出登录后清理优惠券缓存', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCoupon(DEMO_USER_ID, { name: '登录用户的券' })

    const { client } = renderApp('/account/coupons')
    expect(await screen.findByText('登录用户的券')).toBeInTheDocument()
    expect(client.getQueryData(queryKeys.coupons.list())).toBeDefined()

    await user.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(client.getQueryData(queryKeys.coupons.list())).toBeUndefined()
    })
  })
})
