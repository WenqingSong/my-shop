import { describe, expect, it } from 'vitest'

import { fetchCurrentUser, logoutRequest } from '@/api/account'
import { loginRequest, refreshTokens, registerRequest } from '@/api/auth'
import { ApiError } from '@/api/client'
import { hasAuthSession, setAuthTokens } from '@/auth/token-store'

describe('认证 API', () => {
  it('登录返回双 token，携带 access token 可获取当前用户', async () => {
    const tokens = await loginRequest({ username: 'demo', password: 'demo12345' })

    expect(tokens.token_type).toBe('Bearer')
    expect(tokens.expires_in).toBe(3600)
    expect(tokens.access_token).not.toBe('')
    expect(tokens.refresh_token).not.toBe('')

    setAuthTokens(tokens)

    await expect(fetchCurrentUser()).resolves.toEqual({ id: 1, username: 'demo' })
  })

  it('错误密码返回 2002，且不创建会话', async () => {
    const error = await loginRequest({ username: 'demo', password: 'wrongpass' }).catch(
      (reason: unknown) => reason,
    )

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(401)
    expect((error as ApiError).code).toBe('2002')
    expect(hasAuthSession()).toBe(false)
  })

  it('注册重复用户名返回 2001，新用户返回 id/username', async () => {
    const duplicate = await registerRequest({ username: 'demo', password: 'password123' }).catch(
      (reason: unknown) => reason,
    )

    expect(duplicate).toBeInstanceOf(ApiError)
    expect((duplicate as ApiError).code).toBe('2001')

    await expect(
      registerRequest({ username: 'newuser1', password: 'password123' }),
    ).resolves.toEqual({ id: 2, username: 'newuser1' })
  })

  it('注册参数不符合后端规则时返回 1001', async () => {
    const error = await registerRequest({ username: 'a_b', password: 'password123' }).catch(
      (reason: unknown) => reason,
    )

    expect((error as ApiError).code).toBe('1001')
  })

  it('退出登录撤销服务端会话，之后 /me 返回 401', async () => {
    const tokens = await loginRequest({ username: 'demo', password: 'demo12345' })
    setAuthTokens(tokens)

    await expect(logoutRequest()).resolves.toBeNull()
    await expect(fetchCurrentUser()).rejects.toMatchObject({ status: 401 })
    // 刷新失败后本地 Token 必须被清空，避免 UI 仍显示已登录。
    expect(hasAuthSession()).toBe(false)
  })

  it('未携带 access token 时 /me 返回 401', async () => {
    await expect(fetchCurrentUser()).rejects.toMatchObject({ status: 401 })
  })

  it('refresh 轮换后旧 refresh token 立即失效', async () => {
    const first = await loginRequest({ username: 'demo', password: 'demo12345' })
    const second = await refreshTokens(first.refresh_token)

    expect(second.access_token).not.toBe(first.access_token)
    expect(second.refresh_token).not.toBe(first.refresh_token)

    await expect(refreshTokens(first.refresh_token)).rejects.toMatchObject({
      status: 401,
      code: '2012',
    })
  })
})
