import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { fetchBanners } from '@/api/banners'
import { fetchCategoryTree } from '@/api/categories'
import { ApiError } from '@/api/client'
import { fetchProductDetail, fetchProductList } from '@/api/products'
import { fetchProductReviews } from '@/api/reviews'
import { server } from '@/mocks/server'

describe('商品浏览 API 模块', () => {
  it('解包统一响应并透传分页与筛选结果', async () => {
    const result = await fetchProductList({ page: 1, size: 12, keyword: '星海', sort: 'price', order: 'asc' })

    expect(result.total).toBe(4)
    expect(result.items.map((item) => item.id)).toEqual([20, 5, 2, 1])
    expect(result.page).toBe(1)
    expect(result.size).toBe(12)
  })

  it('分类 id 精确匹配，空关键词不参与过滤', async () => {
    const result = await fetchProductList({ page: 1, size: 12, categoryId: 12, keyword: '   ' })

    expect(result.total).toBe(3)
    expect(result.items.every((item) => item.category_id === 12)).toBe(true)
  })

  it('keyword 发送前去除首尾空白', async () => {
    let captured: URL | null = null

    server.use(
      http.get('/api/products', ({ request }) => {
        captured = new URL(request.url)
        return HttpResponse.json({ code: 0, message: 'OK', data: { items: [], total: 0, page: 1, size: 12 } })
      }),
    )

    await fetchProductList({ keyword: '  星海  ', page: 1, size: 12 })

    expect(captured).not.toBeNull()
    expect((captured as unknown as URL).searchParams.get('keyword')).toBe('星海')
  })

  it('商品详情包含 enabled SKU 列表', async () => {
    const detail = await fetchProductDetail(1)

    expect(detail.name).toContain('星海 X1 Pro')
    expect(detail.skus).toHaveLength(2)
    expect(detail.skus.every((sku) => sku.status === 'enabled')).toBe(true)
  })

  it('详情 404 转换为带业务码的 ApiError', async () => {
    const error = await fetchProductDetail(999_999).catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(404)
    expect((error as ApiError).code).toBe('4001')
    expect((error as ApiError).isNotFound).toBe(true)
  })

  it('分类树与轮播图结构符合后端字段', async () => {
    const categories = await fetchCategoryTree()
    const banners = await fetchBanners()

    expect(categories.items[0]?.children?.[0]?.name).toBe('手机')
    expect(banners.items[0]).toMatchObject({ title: '新品首发', sort: 1 })
  })

  it('评价接口返回分页、平均分与总数', async () => {
    const reviews = await fetchProductReviews(1, { page: 2, size: 5 })

    expect(reviews.count).toBe(7)
    expect(reviews.avg_rating).toBe(4.3)
    expect(reviews.items).toHaveLength(2)
    expect(reviews.items.every((item) => item.product_id === 1)).toBe(true)
  })
})
