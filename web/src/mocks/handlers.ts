import { delay, http, HttpResponse } from 'msw'

import type { Product } from '@/api/types'
import {
  createMockUser,
  findMockUserByAccessToken,
  findMockUserByUsername,
  revokeMockSession,
  rotateMockRefreshToken,
  startMockSession,
} from '@/mocks/data/auth'
import {
  addMockCartItem,
  deleteMockCartItem,
  getMockCartRequestDelay,
  listMockCart,
  updateMockCartQuantity,
  updateMockCartSelected,
} from '@/mocks/data/cart'
import {
  createMockAddress,
  deleteMockAddress,
  listMockAddresses,
  updateMockAddress,
  type MockAddressInput,
} from '@/mocks/data/addresses'
import {
  addMockFavorite,
  checkMockFavorite,
  listMockFavorites,
  removeMockFavorite,
} from '@/mocks/data/favorites'
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

const MOCK_USERNAME_PATTERN = /^[a-zA-Z0-9]{3,24}$/
const passwordEncoder = new TextEncoder()

function isValidMockUsername(value: string): boolean {
  return MOCK_USERNAME_PATTERN.test(value)
}

function isValidMockPassword(value: string): boolean {
  const bytes = passwordEncoder.encode(value).length
  return bytes >= 8 && bytes <= 24
}

function bearerToken(request: Request): string {
  const header = request.headers.get('authorization') ?? ''
  return header.startsWith('Bearer ') ? header.slice('Bearer '.length).trim() : ''
}

async function readJsonObject(request: Request): Promise<Record<string, unknown>> {
  try {
    const body: unknown = await request.json()
    return body && typeof body === 'object' ? (body as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

function readString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

const MOCK_PHONE_PATTERN = /^1[3-9]\d{9}$/

function normalizeText(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function runeLength(value: string): number {
  return Array.from(value).length
}

function isValidAddressText(value: string, maxLength: number): boolean {
  return value !== '' && runeLength(value) <= maxLength
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
  http.post(`${API}/register`, async ({ request }) => {
    const body = await readJsonObject(request)
    const username = readString(body.username)
    const password = readString(body.password)

    if (!isValidMockUsername(username) || !isValidMockPassword(password)) {
      return fail(400, 1001, '参数错误')
    }

    if (findMockUserByUsername(username)) {
      return fail(409, 2001, '用户名已存在')
    }

    return HttpResponse.json(ok(createMockUser(username, password)))
  }),

  http.post(`${API}/login`, async ({ request }) => {
    const body = await readJsonObject(request)
    const username = readString(body.username)
    const password = readString(body.password)

    if (!isValidMockUsername(username) || !isValidMockPassword(password)) {
      return fail(400, 1001, '参数错误')
    }

    const user = findMockUserByUsername(username)

    if (!user || user.password !== password) {
      return fail(401, 2002, '用户名或密码错误')
    }

    const session = startMockSession(username)

    if (!session) {
      return fail(401, 2002, '用户名或密码错误')
    }

    return HttpResponse.json(ok(session.tokens))
  }),

  http.post(`${API}/refresh`, async ({ request }) => {
    const body = await readJsonObject(request)
    const token = readString(body.refresh_token)
    const rotated = rotateMockRefreshToken(token)

    if (!rotated) {
      return fail(401, 2012, 'refresh token 无效')
    }

    return HttpResponse.json(ok(rotated.tokens))
  }),

  http.get(`${API}/me`, ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    return HttpResponse.json(ok(user))
  }),

  http.post(`${API}/logout`, ({ request }) => {
    const token = bearerToken(request)

    if (!revokeMockSession(token)) {
      return fail(401, 1002, '未授权')
    }

    return HttpResponse.json(ok(null))
  }),

  // 购物车：与 internal/cmd/routes_frontend.go 的 /cart 路由一一对应，全部需要登录。
  http.get(`${API}/cart`, ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    return HttpResponse.json(ok({ items: listMockCart(user.id) }))
  }),

  http.post(`${API}/cart/items`, async ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)
    const skuId = Number(body.sku_id)
    // 后端 quantity 可缺省（默认 1）；前端始终显式传整数。
    const quantity = body.quantity === undefined ? 1 : Number(body.quantity)

    if (!Number.isSafeInteger(quantity)) {
      return fail(400, 8002, '数量非法')
    }

    const result = addMockCartItem(user.id, skuId, quantity)

    return result.ok
      ? HttpResponse.json(ok(result.item))
      : fail(result.status, result.code, result.message)
  }),

  http.put(`${API}/cart/items/:id`, async ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)
    const quantity = Number(body.quantity)

    if (!Number.isSafeInteger(quantity)) {
      return fail(400, 8002, '数量非法')
    }

    // 仅 Mock：可注入响应延迟，用于验证连续修改数量时的请求竞争处理。
    const wait = getMockCartRequestDelay()

    if (wait > 0) {
      await delay(wait)
    }

    const result = updateMockCartQuantity(user.id, Number(params.id), quantity)

    return result.ok
      ? HttpResponse.json(ok(result.item))
      : fail(result.status, result.code, result.message)
  }),

  http.put(`${API}/cart/items/:id/selected`, async ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)

    if (typeof body.selected !== 'boolean') {
      return fail(400, 1001, '参数错误')
    }

    const result = updateMockCartSelected(user.id, Number(params.id), body.selected)

    return result.ok
      ? HttpResponse.json(ok(result.item))
      : fail(result.status, result.code, result.message)
  }),

  http.delete(`${API}/cart/items/:id`, ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const result = deleteMockCartItem(user.id, Number(params.id))

    return result.ok ? HttpResponse.json(ok({})) : fail(result.status, result.code, result.message)
  }),

  http.get(`${API}/addresses`, ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    return HttpResponse.json(ok({ items: listMockAddresses(user.id) }))
  }),

  http.post(`${API}/addresses`, async ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)
    const recipientName = normalizeText(body.recipient_name)
    const phone = normalizeText(body.phone)
    const province = normalizeText(body.province)
    const city = normalizeText(body.city)
    const district = normalizeText(body.district)
    const detail = normalizeText(body.detail)

    if (
      !isValidAddressText(recipientName, 32) ||
      !MOCK_PHONE_PATTERN.test(phone) ||
      !isValidAddressText(province, 32) ||
      !isValidAddressText(city, 32) ||
      !isValidAddressText(district, 32) ||
      !isValidAddressText(detail, 255)
    ) {
      return fail(400, 1001, '参数错误')
    }

    const address = createMockAddress(user.id, {
      recipient_name: recipientName,
      phone,
      province,
      city,
      district,
      detail,
      is_default: typeof body.is_default === 'boolean' ? body.is_default : undefined,
    })

    return HttpResponse.json(ok(address))
  }),

  http.get(`${API}/addresses/:id`, ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const address = listMockAddresses(user.id).find((item) => item.id === Number(params.id))

    return address ? HttpResponse.json(ok(address)) : fail(404, 7001, '地址不存在')
  }),

  http.put(`${API}/addresses/:id`, async ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)
    const patch: Partial<MockAddressInput> = {}

    const textFields = [
      ['recipient_name', 32],
      ['province', 32],
      ['city', 32],
      ['district', 32],
      ['detail', 255],
    ] as const

    for (const [field, maxLength] of textFields) {
      if (field in body) {
        const value = normalizeText(body[field])

        if (!isValidAddressText(value, maxLength)) {
          return fail(400, 1001, '参数错误')
        }

        patch[field] = value
      }
    }

    if ('phone' in body) {
      const phone = normalizeText(body.phone)

      if (!MOCK_PHONE_PATTERN.test(phone)) {
        return fail(400, 1001, '参数错误')
      }

      patch.phone = phone
    }

    if ('is_default' in body) {
      if (typeof body.is_default !== 'boolean') {
        return fail(400, 1001, '参数错误')
      }

      patch.is_default = body.is_default
    }

    const updated = updateMockAddress(user.id, Number(params.id), patch)

    return updated ? HttpResponse.json(ok(updated)) : fail(404, 7001, '地址不存在')
  }),

  http.delete(`${API}/addresses/:id`, ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    if (!deleteMockAddress(user.id, Number(params.id))) {
      return fail(404, 7001, '地址不存在')
    }

    return HttpResponse.json(ok({}))
  }),

  http.get(`${API}/favorites/check`, ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const rawProductId = new URL(request.url).searchParams.get('product_id')
    const productId = Number(rawProductId)

    if (!Number.isSafeInteger(productId) || productId <= 0) {
      return fail(400, 1001, '参数错误')
    }

    return HttpResponse.json(ok({ favorited: checkMockFavorite(user.id, productId) }))
  }),

  http.get(`${API}/favorites`, ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const url = new URL(request.url)
    const page = positiveInt(url.searchParams.get('page'), 1, 10_000)
    const size = positiveInt(url.searchParams.get('size'), 20, 100)

    return HttpResponse.json(ok(listMockFavorites(user.id, page, size)))
  }),

  http.post(`${API}/favorites`, async ({ request }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const body = await readJsonObject(request)
    const productId = Number(body.product_id)

    if (!Number.isSafeInteger(productId) || productId <= 0) {
      return fail(400, 1001, '参数错误')
    }

    const result = addMockFavorite(user.id, productId)

    if (!result.ok) {
      return fail(result.status, result.code, result.message)
    }

    return HttpResponse.json(ok(result.item))
  }),

  http.delete(`${API}/favorites/:product_id`, ({ request, params }) => {
    const user = findMockUserByAccessToken(bearerToken(request))

    if (!user) {
      return fail(401, 1002, '未授权')
    }

    const productId = Number(params.product_id)

    if (!Number.isSafeInteger(productId) || productId <= 0) {
      return fail(400, 1001, '参数错误')
    }

    removeMockFavorite(user.id, productId)
    return HttpResponse.json(ok({}))
  }),

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
