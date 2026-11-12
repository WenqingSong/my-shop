import { describe, expect, it } from 'vitest'

import { ApiError } from '@/api/client'
import {
  addFavorite,
  fetchFavoriteStatus,
  fetchFavorites,
  removeFavorite,
} from '@/api/favorites'
import { markMockProductOffShelf, seedMockFavorite } from '@/mocks/data/favorites'
import { signInAsDemo } from '@/test/auth'

describe('商品收藏 API', () => {
  it('未登录访问返回 401', async () => {
    await expect(fetchFavorites(1, 10)).rejects.toMatchObject({ status: 401 })
  })

  it('完成收藏、列表、状态查询与取消收藏', async () => {
    await signInAsDemo()

    expect((await fetchFavorites(1, 10)).total).toBe(0)
    expect((await fetchFavoriteStatus(1)).favorited).toBe(false)

    const added = await addFavorite(1)
    expect(added.product_id).toBe(1)
    expect(added.available).toBe(true)

    const list = await fetchFavorites(1, 10)
    expect(list.total).toBe(1)
    expect(list.items[0]?.product_name).toContain('星海 X1 Pro')
    expect((await fetchFavoriteStatus(1)).favorited).toBe(true)

    await removeFavorite(1)
    expect((await fetchFavorites(1, 10)).total).toBe(0)
    expect((await fetchFavoriteStatus(1)).favorited).toBe(false)

    // 取消未收藏商品也是幂等成功。
    await expect(removeFavorite(1)).resolves.toEqual({})
  })

  it('重复收藏幂等，不产生第二条记录', async () => {
    await signInAsDemo()

    await addFavorite(1)
    await addFavorite(1)

    expect((await fetchFavorites(1, 10)).total).toBe(1)
  })

  it('已下架商品不可收藏，返回 409/11001', async () => {
    await signInAsDemo()
    markMockProductOffShelf(3)

    const error = await addFavorite(3).catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(409)
    expect((error as ApiError).code).toBe('11001')
  })

  it('不存在的商品不可收藏，返回 404/4001', async () => {
    await signInAsDemo()

    const error = await addFavorite(999_999).catch((reason: unknown) => reason)

    expect((error as ApiError).status).toBe(404)
    expect((error as ApiError).code).toBe('4001')
  })

  it('收藏列表保留不可用商品并标识原因', async () => {
    await signInAsDemo()
    markMockProductOffShelf(3)
    seedMockFavorite(1, 3)
    seedMockFavorite(1, 999_999)

    const list = await fetchFavorites(1, 10)

    expect(list.total).toBe(2)
    expect(list.items.find((item) => item.product_id === 3)).toMatchObject({
      available: false,
      unavailable_reason: 'off_shelf',
    })
    expect(list.items.find((item) => item.product_id === 999_999)).toMatchObject({
      available: false,
      unavailable_reason: 'product_deleted',
      product_price: null,
    })
  })
})
