import { describe, expect, it, vi } from 'vitest'

import type { AuthTokenResult } from '@/api/auth'
import {
  clearAuthTokens,
  getAccessToken,
  getAuthSessionSnapshot,
  getAuthTokens,
  getRefreshToken,
  hasAuthSession,
  setAuthTokens,
  subscribeAuthSession,
} from '@/auth/token-store'

const tokenResult: AuthTokenResult = {
  access_token: 'access-token',
  refresh_token: 'refresh-token',
  token_type: 'Bearer',
  expires_in: 3600,
}

describe('token-store', () => {
  it('保存双 token 并可读取', () => {
    const before = Date.now()
    setAuthTokens(tokenResult)

    expect(getAccessToken()).toBe('access-token')
    expect(getRefreshToken()).toBe('refresh-token')
    expect(hasAuthSession()).toBe(true)
    expect(getAuthSessionSnapshot()).toBe(true)
    expect(getAuthTokens()?.expiresAt).toBeGreaterThanOrEqual(before + 3600 * 1000)
  })

  it('清空后回到未登录状态', () => {
    setAuthTokens(tokenResult)
    clearAuthTokens()

    expect(getAccessToken()).toBeNull()
    expect(getRefreshToken()).toBeNull()
    expect(hasAuthSession()).toBe(false)
    expect(getAuthSessionSnapshot()).toBe(false)
  })

  it('仅在有/无 Token 状态切换时通知订阅者', () => {
    const listener = vi.fn()
    const unsubscribe = subscribeAuthSession(listener)

    setAuthTokens(tokenResult)
    expect(listener).toHaveBeenCalledTimes(1)

    // 刷新会替换 token，但“是否存在会话”没变，不应触发额外渲染。
    setAuthTokens({ ...tokenResult, access_token: 'access-token-2' })
    expect(listener).toHaveBeenCalledTimes(1)

    clearAuthTokens()
    expect(listener).toHaveBeenCalledTimes(2)

    clearAuthTokens()
    expect(listener).toHaveBeenCalledTimes(2)

    unsubscribe()
  })
})
