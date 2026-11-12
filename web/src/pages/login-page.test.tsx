import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { renderApp } from '@/test/render'

async function fillAndSubmit(username: string, password: string) {
  const user = userEvent.setup()

  await user.type(screen.getByLabelText('用户名'), username)
  await user.type(screen.getByLabelText('密码'), password)
  await user.click(screen.getByRole('button', { name: '登录' }))
}

describe('登录页', () => {
  it('未登录时 Header 展示登录入口', async () => {
    renderApp('/')

    expect(screen.getByRole('link', { name: '登录 / 注册' })).toHaveAttribute('href', '/login')
    expect(screen.getByRole('heading', { level: 1, name: /把好物/ })).toBeInTheDocument()
  })

  it('空表单提交展示字段校验错误', async () => {
    const user = userEvent.setup()
    renderApp('/login')

    await screen.findByRole('heading', { level: 1, name: '登录' })
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('请输入用户名')).toBeInTheDocument()
    expect(screen.getByText('请输入密码')).toBeInTheDocument()
  })

  it('密码错误展示后端错误提示', async () => {
    renderApp('/login')

    await screen.findByRole('heading', { level: 1, name: '登录' })
    await fillAndSubmit('demo', 'wrongpass')

    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或密码错误')
  })

  it('登录成功跳转首页，Header 切换为已登录状态', async () => {
    const { router } = renderApp('/login')

    await screen.findByRole('heading', { level: 1, name: '登录' })
    await fillAndSubmit('demo', 'demo12345')

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })
    expect(await screen.findByRole('link', { name: '账户：demo' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '退出登录' })).toBeInTheDocument()
    // 安全约束：Token 只存内存，不落 localStorage / sessionStorage。
    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
  })

  it('未登录访问受保护页面后登录，会返回原目标页面', async () => {
    const { router } = renderApp('/account')

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')

    await fillAndSubmit('demo', 'demo12345')

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/account')
    })
    expect(await screen.findByRole('heading', { level: 1, name: '账户' })).toBeInTheDocument()
  })

  it('密码可见性可以切换', async () => {
    const user = userEvent.setup()
    renderApp('/login')

    await screen.findByRole('heading', { level: 1, name: '登录' })
    const password = screen.getByLabelText('密码')

    expect(password).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('button', { name: '显示密码' }))
    expect(password).toHaveAttribute('type', 'text')
    await user.click(screen.getByRole('button', { name: '隐藏密码' }))
    expect(password).toHaveAttribute('type', 'password')
  })
})
