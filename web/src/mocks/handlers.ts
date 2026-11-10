import { http, HttpResponse } from 'msw'

import type { Product } from '@/api/types'
import {
  mockBanners,
  mockCategories,
  mockProductDetail,
  mockProducts,
  mockRecommendation,
  mockReviews,
} from '@/mocks/data/catalog'

/**
 * MSW 请求处理器（仅用于测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 响应结构与后端 internal/middleware/response.go 保持一致：
 * 成功 {code:0,message:"OK",data:...}；失败 {code,message,data:null} + 对应 HTTP 状态。
 *
 * 仅 Mock 支持的测试钩子（真实后端不存在）：
 * - GET /api/products?keyword=__error__ 固定返回 500，用于验证列表错误态。
 */

const API = '/api'

/** Mock 轮播图：保持后端 /storage/banners/* 的 URL 形态，避免测试依赖本地静态目录。 */
const MOCK_BANNER_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 270"><rect width="480" height="270" fill="#bfd6fe"/><rect x="60" y="180" width="360" height="24" rx="12" fill="#6094fa"/></svg>'

function ok<T>(data: T) {
  return { code: 0, message: 'OK', data }
}

function fail(status: number, code: number, message: string) {
  return HttpResponse.json({ code, message, data: null }, { status })
}

function positiveInt(value: string | null, fallback: number, max: number): number {
  if (!value || !/^\d+$/.test(value)) {
    return fallback
  }

  const parsed = Number(value)
  return Math.min(Math.max(parsed, 1), max)
}

type SortField = 'id' | 'price' | 'created_at' | 'updated_at'

function normalizeSort(value: string | null): SortField {
  return value === 'id' || value === 'price' || value === 'created_at' || value === 'updated_at'
    ? value
    : 'created_at'
}

function compareProducts(a: Product, b: Product, field: SortField): number {
  if (field === 'price') {
    return a.price - b.price
  }

  if (field === 'id') {
    return a.id - b.id
  }

  return String(a[field]).localeCompare(String(b[field]))
}

export const handlers = [
  http.get(`${API}/categories`, () => HttpResponse.json(ok({ items: mockCategories }))),

  http.get(`${API}/banners`, () => HttpResponse.json(ok({ items: mockBanners }))),

  http.get('/storage/banners/:file', () =>
    new HttpResponse(MOCK_BANNER_SVG, { headers: { 'Content-Type': 'image/svg+xml' } }),
  ),

  http.get(`${API}/products`, ({ request }) => {
    const url = new URL(request.url)
    const keyword = (url.searchParams.get('keyword') ?? '').trim().toLowerCase()

    if (keyword === '__error__') {
      return fail(500, 1000, '服务器内部错误')
    }

    const page = positiveInt(url.searchParams.get('page'), 1, 10_000)
    const size = positiveInt(url.searchParams.get('size'), 20, 100)
    const rawCategoryId = url.searchParams.get('category_id')
    const categoryId = rawCategoryId ? Number(rawCategoryId) : undefined
    const sortField = normalizeSort(url.searchParams.get('sort'))
    const direction = url.searchParams.get('order') === 'asc' ? 1 : -1

    const filtered = mockProducts.filter((product) => {
      const matchesCategory = categoryId === undefined || product.category_id === categoryId
      const matchesKeyword =
        keyword === '' ||
        product.name.toLowerCase().includes(keyword) ||
        product.brand.toLowerCase().includes(keyword)
      return matchesCategory && matchesKeyword
    })

    const sorted = [...filtered].sort((a, b) => {
      const result = compareProducts(a, b, sortField)
      // 与后端一致：排序值相同时按 id 倒序兜底，保证稳定顺序。
      return result === 0 ? b.id - a.id : result * direction
    })

    const start = (page - 1) * size

    return HttpResponse.json(
      ok({
        items: sorted.slice(start, start + size),
        total: sorted.length,
        page,
        size,
      }),
    )
  }),

  http.get(`${API}/products/:id/reviews`, ({ request, params }) => {
    const url = new URL(request.url)
    const productId = Number(params.id)
    const page = positiveInt(url.searchParams.get('page'), 1, 10_000)
    const size = positiveInt(url.searchParams.get('size'), 20, 100)

    const filtered = mockReviews
      .filter((review) => review.product_id === productId)
      .sort((a, b) => b.id - a.id)
    const start = (page - 1) * size
    const ratingSum = filtered.reduce((total, review) => total + review.rating, 0)
    const count = filtered.length

    return HttpResponse.json(
      ok({
        items: filtered.slice(start, start + size),
        page,
        size,
        total: count,
        avg_rating: count > 0 ? Math.round((ratingSum / count) * 10) / 10 : 0,
        count,
      }),
    )
  }),

  http.get(`${API}/products/:id`, ({ params }) => {
    const productId = Number(params.id)
    const detail = Number.isSafeInteger(productId) ? mockProductDetail(productId) : undefined

    if (!detail) {
      return fail(404, 4001, '商品不存在')
    }

    return HttpResponse.json(ok(detail))
  }),

  http.get(`${API}/recommendations/:code`, ({ params }) => {
    if (params.code !== mockRecommendation.code) {
      return HttpResponse.json(ok({ code: '', name: '', items: [] }))
    }

    return HttpResponse.json(ok(mockRecommendation))
  }),
]

/** 便于测试断言 Mock 数据的规模，避免用例里再写死数字。 */
export const mockCatalogSize = mockProducts.length
