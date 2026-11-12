import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { queryKeys } from '@/api/query-keys'
import { clearAuthTokens } from '@/auth/token-store'
import { seedMockCoupon } from '@/mocks/data/coupons'
import { seedMockOrder } from '@/mocks/data/orders'
import { seedMockRefund } from '@/mocks/data/refunds'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

/**
 * 会话失效（refresh 失败 / 服务端撤销会话）时，账号私有缓存必须全部清理。
 *
 * 这是 W5 审查发现并修复的缺陷：修复前 `useAuthCacheReset` 只清理
 * auth/addresses/favorites/cart，订单、退款、优惠券缓存会残留到下一个登录用户。
 */
describe('会话失效后的账号私有缓存清理', () => {
  it('清空 Token 后，订单、退款、优惠券缓存全部被移除', async () => {
    const user = userEvent.setup()
    await signInAsDemo()

    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 209_900 })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying', amount: 209_900 })
    seedMockCoupon(DEMO_USER_ID, { name: '会话缓存测试券' })

    const { client } = renderApp('/account/orders')
    await screen.findByText(order.order_no)

    // 同一 SPA 会话内进入优惠券页，让 orders / refunds / coupons 三份缓存都存在。
    await user.click(
      within(screen.getByRole('navigation', { name: '个人中心导航' })).getByRole('link', {
        name: '我的优惠券',
      }),
    )
    expect(await screen.findByText('会话缓存测试券')).toBeInTheDocument()

    // 测试 QueryClient 的 gcTime=0，离开订单页后其缓存会被回收，
    // 因此这里只断言当前页面真实存在的缓存；完整清单由 user-cache.test.ts 覆盖。
    expect(client.getQueryData(queryKeys.coupons.list())).toBeDefined()

    // 模拟 refresh 失败：auth-request 在重试仍 401 后调用 clearAuthTokens()。
    act(() => {
      clearAuthTokens()
    })

    await waitFor(() => {
      expect(client.getQueryData(queryKeys.coupons.list())).toBeUndefined()
    })
    // 会话结束后被重定向到登录页。
    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
  })
})
