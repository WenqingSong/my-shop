import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { loginRequest } from '@/api/auth'
import { getAuthTokens, setAuthTokens } from '@/auth/token-store'
import { server } from '@/mocks/server'
import { renderApp } from '@/test/render'

function ok<T>(data: T) {
  return HttpResponse.json({ code: 0, message: 'OK', data })
}

function unauthorized() {
  return HttpResponse.json({ code: 1002, message: '未授权', data: null }, { status: 401 })
}

async function seedDemoSession() {
  const tokens = await loginRequest({ username: 'demo', password: 'demo12345' })
  setAuthTokens(tokens)
  return tokens
}

async function loginAsDemo() {
  const user = userEvent.setup()

  await user.type(screen.getByLabelText('用户名'), 'demo')
  await user.type(screen.getByLabelText('密码'), 'demo12345')
  await user.click(screen.getByRole('button', { name: '登录' }))
}

describe('账户页与路由保护', () => {
  it('未登录访问账户页会跳转到登录页', async () => {
    const { router } = renderApp('/account')

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('登录后展示当前用户，退出后恢复未登录状态', async () => {
    const user = userEvent.setup()
    const { router } = renderApp('/login')

    await screen.findByRole('heading', { level: 1, name: '登录' })
    await loginAsDemo()

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })

    await user.click(screen.getByRole('link', { name: '账户：demo' }))

    expect(await screen.findByRole('heading', { level: 1, name: '账户' })).toBeInTheDocument()
    const summary = screen.getByRole('region', { name: '当前登录用户' })
    expect(within(summary).getByText('demo')).toBeInTheDocument()
    expect(within(summary).getByText('1')).toBeInTheDocument()

    await user.click(within(summary).getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })
    expect(await screen.findByRole('link', { name: '登录 / 注册' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '账户：demo' })).not.toBeInTheDocument()
  })

  it('Header、路由守卫与账户页共享同一个 /me 请求', async () => {
    await seedDemoSession()
    let meCalls = 0

    server.use(
      http.get('/api/me', () => {
        meCalls += 1
        return ok({ id: 1, username: 'demo' })
      }),
    )

    renderApp('/account')

    expect(await screen.findByRole('heading', { level: 1, name: '账户' })).toBeInTheDocument()
    expect(meCalls).toBe(1)
  })

  it('access token 失效时自动 refresh 并继续加载账户', async () => {
    await seedDemoSession()

    server.use(
      http.get('/api/me', ({ request }) =>
        request.headers.get('authorization') === 'Bearer refreshed-access'
          ? ok({ id: 1, username: 'demo' })
          : unauthorized(),
      ),
      http.post('/api/refresh', () =>
        ok({
          access_token: 'refreshed-access',
          refresh_token: 'refreshed-refresh',
          token_type: 'Bearer',
          expires_in: 3600,
        }),
      ),
    )

    renderApp('/account')

    expect(await screen.findByRole('heading', { level: 1, name: '账户' })).toBeInTheDocument()
    expect(getAuthTokens()?.accessToken).toBe('refreshed-access')
  })

  it('refresh 也失效时清空会话并跳转登录页', async () => {
    await seedDemoSession()

    server.use(
      http.get('/api/me', () => unauthorized()),
      http.post('/api/refresh', () =>
        HttpResponse.json({ code: 2012, message: 'refresh token 无效', data: null }, { status: 401 }),
      ),
    )

    const { router } = renderApp('/account')

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(getAuthTokens()).toBeNull()
  })
})
