import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { AuthTokenResult } from '@/api/auth'
import { authGetData } from '@/api/auth-request'
import { getAuthTokens, hasAuthSession, setAuthTokens } from '@/auth/token-store'
import { server } from '@/mocks/server'

const staleTokens: AuthTokenResult = {
  access_token: 'stale-access',
  refresh_token: 'stale-refresh',
  token_type: 'Bearer',
  expires_in: 3600,
}

const freshTokens: AuthTokenResult = {
  access_token: 'fresh-access',
  refresh_token: 'fresh-refresh',
  token_type: 'Bearer',
  expires_in: 3600,
}

const USER = { id: 1, username: 'demo' }

function unauthorized() {
  return HttpResponse.json({ code: 1002, message: '未授权', data: null }, { status: 401 })
}

function ok<T>(data: T) {
  return HttpResponse.json({ code: 0, message: 'OK', data })
}

describe('authGetData 的 401 处理', () => {
  it('access token 失效时刷新一次并重试原请求', async () => {
    let meCalls = 0
    let refreshCalls = 0

    server.use(
      http.get('/api/me', ({ request }) => {
        meCalls += 1
        return request.headers.get('authorization') === `Bearer ${freshTokens.access_token}`
          ? ok(USER)
          : unauthorized()
      }),
      http.post('/api/refresh', async () => {
        refreshCalls += 1
        return ok(freshTokens)
      }),
    )

    setAuthTokens(staleTokens)

    await expect(authGetData('/me')).resolves.toEqual(USER)
    expect(meCalls).toBe(2)
    expect(refreshCalls).toBe(1)
    expect(getAuthTokens()?.accessToken).toBe('fresh-access')
    expect(getAuthTokens()?.refreshToken).toBe('fresh-refresh')
  })

  it('并发 401 共享同一个刷新请求，避免 refresh reuse', async () => {
    let refreshCalls = 0

    server.use(
      http.get('/api/me', ({ request }) =>
        request.headers.get('authorization') === `Bearer ${freshTokens.access_token}`
          ? ok(USER)
          : unauthorized(),
      ),
      http.post('/api/refresh', async () => {
        refreshCalls += 1
        await delay(30)
        return ok(freshTokens)
      }),
    )

    setAuthTokens(staleTokens)

    const results = await Promise.all([authGetData('/me'), authGetData('/me')])

    expect(results).toEqual([USER, USER])
    expect(refreshCalls).toBe(1)
  })

  it('刷新失败时清空 Token 并抛出 401', async () => {
    server.use(
      http.get('/api/me', () => unauthorized()),
      http.post('/api/refresh', () =>
        HttpResponse.json({ code: 2012, message: 'refresh token 无效', data: null }, { status: 401 }),
      ),
    )

    setAuthTokens(staleTokens)

    await expect(authGetData('/me')).rejects.toMatchObject({ status: 401 })
    expect(hasAuthSession()).toBe(false)
    expect(getAuthTokens()).toBeNull()
  })

  it('非 401 错误不触发刷新', async () => {
    let refreshCalls = 0

    server.use(
      http.get('/api/me', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
      http.post('/api/refresh', () => {
        refreshCalls += 1
        return ok(freshTokens)
      }),
    )

    setAuthTokens(staleTokens)

    await expect(authGetData('/me')).rejects.toMatchObject({ status: 500 })
    expect(refreshCalls).toBe(0)
    expect(hasAuthSession()).toBe(true)
  })
})
