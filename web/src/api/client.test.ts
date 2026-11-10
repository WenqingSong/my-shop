import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { ApiError, createApiClient } from '@/api/client'
import { server } from '@/mocks/server'

const BASE_URL = 'http://localhost/api'

function createTestClient(overrides: { timeoutMs?: number; token?: string } = {}) {
  return createApiClient({
    baseUrl: BASE_URL,
    timeoutMs: overrides.timeoutMs ?? 1_000,
    getToken: () => overrides.token,
  })
}

describe('apiClient', () => {
  it('发送 JSON 请求并解析 JSON 响应', async () => {
    server.use(
      http.post(`${BASE_URL}/__test__/echo`, async ({ request }) => {
        const body = await request.json()

        return HttpResponse.json({
          received: body,
          authorization: request.headers.get('authorization'),
          keyword: new URL(request.url).searchParams.get('keyword'),
        })
      }),
    )

    const client = createTestClient({ token: 'test-token' })
    const result = await client.post<{
      received: { sku: string }
      authorization: string
      keyword: string
    }>(
      '/__test__/echo',
      { sku: 'demo-sku' },
      { query: { keyword: '耳机', page: 1, tags: ['a', 'b'] } },
    )

    expect(result.received).toEqual({ sku: 'demo-sku' })
    expect(result.authorization).toBe('Bearer test-token')
    expect(result.keyword).toBe('耳机')
  })

  it('把非 2xx 响应转换为 ApiError', async () => {
    server.use(
      http.get(`${BASE_URL}/__test__/missing`, () =>
        HttpResponse.json({ message: '商品不存在', code: 'NOT_FOUND' }, { status: 404 }),
      ),
    )

    const error = await createTestClient()
      .get('/__test__/missing')
      .catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.kind).toBe('http')
    expect(apiError.status).toBe(404)
    expect(apiError.code).toBe('NOT_FOUND')
    expect(apiError.message).toBe('商品不存在')
    expect(apiError.isNotFound).toBe(true)
  })

  it('超时后抛出 timeout 类型的 ApiError', async () => {
    server.use(
      http.get(`${BASE_URL}/__test__/slow`, async () => {
        await new Promise((resolve) => setTimeout(resolve, 200))
        return HttpResponse.json({ ok: true })
      }),
    )

    const error = await createTestClient({ timeoutMs: 30 })
      .get('/__test__/slow')
      .catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).kind).toBe('timeout')
  })

  it('204 响应返回 undefined', async () => {
    server.use(
      http.delete(`${BASE_URL}/__test__/empty`, () => new HttpResponse(null, { status: 204 })),
    )

    await expect(createTestClient().delete('/__test__/empty')).resolves.toBeUndefined()
  })
})
