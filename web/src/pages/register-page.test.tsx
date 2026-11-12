import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { renderApp } from '@/test/render'

async function fillAndSubmit(username: string, password: string) {
  const user = userEvent.setup()

  await user.type(screen.getByLabelText('用户名'), username)
  await user.type(screen.getByLabelText('密码'), password)
  await user.click(screen.getByRole('button', { name: '注册' }))
}

describe('注册页', () => {
  it('空表单提交展示字段校验错误', async () => {
    const user = userEvent.setup()
    renderApp('/register')

    await screen.findByRole('heading', { level: 1, name: '注册' })
    await user.click(screen.getByRole('button', { name: '注册' }))

    expect(await screen.findByText('请输入用户名')).toBeInTheDocument()
    expect(screen.getByText('请输入密码')).toBeInTheDocument()
  })

  it('用户名格式不符合后端规则时前端拦截', async () => {
    renderApp('/register')

    await screen.findByRole('heading', { level: 1, name: '注册' })
    await fillAndSubmit('a_b', 'password123')

    expect(await screen.findByText('用户名需为 3-24 位字母或数字')).toBeInTheDocument()
  })

  it('重复用户名展示后端错误提示', async () => {
    renderApp('/register')

    await screen.findByRole('heading', { level: 1, name: '注册' })
    await fillAndSubmit('demo', 'password123')

    expect(await screen.findByRole('alert')).toHaveTextContent('用户名已存在，请更换后重试')
  })

  it('注册成功后回到登录页并预填用户名', async () => {
    const { router } = renderApp('/register')

    await screen.findByRole('heading', { level: 1, name: '注册' })
    await fillAndSubmit('newuser1', 'password123')

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/login')
    })

    expect(await screen.findByText('注册成功，请使用新账号登录。')).toBeInTheDocument()
    expect(screen.getByLabelText('用户名')).toHaveValue('newuser1')
  })
})
