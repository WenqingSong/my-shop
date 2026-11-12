import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { queryKeys } from '@/api/query-keys'
import { clearUserScopedQueries } from '@/auth/user-cache'

/**
 * 账号私有缓存清理清单的回归测试。
 *
 * W5 审查发现：修复前 `useAuthCacheReset` 只清理 4 类 key（auth/addresses/favorites/cart），
 * 订单、退款、优惠券在「会话失效」路径上会残留。现在两处调用共用同一份清单。
 */
describe('clearUserScopedQueries', () => {
  it('清理全部账号私有 Query Key（含订单/退款/优惠券）', () => {
    const client = new QueryClient()

    const keys = [
      queryKeys.auth.me(),
      queryKeys.addresses.list(),
      queryKeys.favorites.list(1, 20),
      queryKeys.cart.list(),
      queryKeys.orders.list(),
      queryKeys.orders.detail(1),
      queryKeys.refunds.list(),
      queryKeys.refunds.list(1),
      queryKeys.refunds.detail(1),
      queryKeys.coupons.list(),
    ] as const

    for (const key of keys) {
      client.setQueryData(key, { marker: key })
    }

    for (const key of keys) {
      expect(client.getQueryData(key)).toBeDefined()
    }

    clearUserScopedQueries(client)

    for (const key of keys) {
      expect(client.getQueryData(key)).toBeUndefined()
    }
  })

  it('不影响公开数据缓存（商品/分类/轮播）', () => {
    const client = new QueryClient()

    client.setQueryData(queryKeys.products.list({ page: 1, size: 20 }), { items: [] })
    client.setQueryData(queryKeys.categories.tree(), { items: [] })
    client.setQueryData(queryKeys.banners.list(), { items: [] })

    clearUserScopedQueries(client)

    expect(client.getQueryData(queryKeys.products.list({ page: 1, size: 20 }))).toBeDefined()
    expect(client.getQueryData(queryKeys.categories.tree())).toBeDefined()
    expect(client.getQueryData(queryKeys.banners.list())).toBeDefined()
  })
})
