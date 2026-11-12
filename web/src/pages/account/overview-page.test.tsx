import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { addFavorite } from '@/api/favorites'
import { loginRequest } from '@/api/auth'
import { queryKeys } from '@/api/query-keys'
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

describe('个人中心与路由保护', () => {
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
    const summary = await screen.findByRole('region', { name: '当前登录用户' })
    expect(within(summary).getByText('demo')).toBeInTheDocument()
    expect(within(summary).getByText('1')).toBeInTheDocument()

    await user.click(within(summary).getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })
    expect(await screen.findByRole('link', { name: '登录 / 注册' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '账户：demo' })).not.toBeInTheDocument()
  })

  it('个人资料页只展示后端真实字段，不提供伪造的修改表单', async () => {
    await seedDemoSession()
    renderApp('/account/profile')

    expect(await screen.findByRole('heading', { name: '个人资料' })).toBeInTheDocument()
    const profile = await screen.findByRole('region', { name: '账户信息' })
    expect(within(profile).getByText('demo')).toBeInTheDocument()
    expect(screen.getByText('资料修改暂未开放')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
  })

  it('个人中心导航可进入收货地址与收藏页面', async () => {
    const user = userEvent.setup()
    await seedDemoSession()
    const { router } = renderApp('/account')

    await screen.findByRole('region', { name: '当前登录用户' })
    const nav = screen.getByRole('navigation', { name: '个人中心导航' })

    await user.click(within(nav).getByRole('link', { name: '收货地址' }))
    expect(await screen.findByRole('heading', { name: '收货地址' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/account/addresses')

    await user.click(within(nav).getByRole('link', { name: '我的收藏' }))
    expect(await screen.findByRole('heading', { name: '我的收藏' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/account/favorites')
  })

  it('退出登录后移除收藏等用户私有缓存', async () => {
    const user = userEvent.setup()
    await seedDemoSession()
    await addFavorite(1)

    const { client, router } = renderApp('/account/favorites')
    expect(
      await screen.findByRole('link', { name: '查看商品：星海 X1 Pro 5G 手机 12GB+256GB' }),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })
    expect(client.getQueryData(queryKeys.favorites.list(1, 10))).toBeUndefined()
    expect(client.getQueryData(queryKeys.auth.me())).toBeUndefined()
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
