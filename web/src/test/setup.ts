import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'

import { resetRefreshStateForTests } from '@/auth/refresh-session'
import { clearAuthTokens } from '@/auth/token-store'
import { resetMockAddresses } from '@/mocks/data/addresses'
import { resetMockAuthState } from '@/mocks/data/auth'
import { resetMockCart } from '@/mocks/data/cart'
import { resetMockFavorites } from '@/mocks/data/favorites'
import { server } from '@/mocks/server'

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' })

  // jsdom 不提供浏览器式的相对 URL 解析，而 Node fetch 要求绝对 URL。
  // 浏览器里 apiClient 传相对路径是正确行为，测试环境在这里补齐 origin，
  // 保证 MSW 能按 /api/* 路径匹配请求（不修改生产代码）。
  const fetchWithOrigin = globalThis.fetch
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const resolved = typeof input === 'string' ? new URL(input, window.location.origin) : input
    return fetchWithOrigin(resolved, init)
  }) as typeof fetch
})

afterEach(() => {
  cleanup()
  server.resetHandlers()
  // 认证状态是模块级内存，必须逐用例清理，避免登录态泄漏到下一个用例。
  clearAuthTokens()
  resetRefreshStateForTests()
  resetMockAuthState()
  resetMockAddresses()
  resetMockFavorites()
  resetMockCart()
})

afterAll(() => {
  server.close()
})
