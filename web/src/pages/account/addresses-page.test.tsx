import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/mocks/server'
import { renderApp } from '@/test/render'
import { signInAsDemo } from '@/test/auth'

async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
  const buttons = screen.getAllByRole('button', { name: '新增地址' })
  await user.click(buttons[0] as HTMLElement)
  return screen.getByRole('dialog')
}

async function fillAddress(
  user: ReturnType<typeof userEvent.setup>,
  values: {
    name: string
    phone: string
    detail: string
    province?: string
    city?: string
    district?: string
    isDefault?: boolean
  },
) {
  await user.type(screen.getByLabelText('收货人姓名'), values.name)
  await user.type(screen.getByLabelText('手机号'), values.phone)
  await user.type(screen.getByLabelText('省份'), values.province ?? '广东省')
  await user.type(screen.getByLabelText('城市'), values.city ?? '深圳市')
  await user.type(screen.getByLabelText('区/县'), values.district ?? '南山区')
  await user.type(screen.getByLabelText('详细地址'), values.detail)

  if (values.isDefault) {
    await user.click(screen.getByLabelText('设为默认地址'))
  }
}

describe('收货地址管理', () => {
  it('空状态展示新增入口，创建首条地址自动默认', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/account/addresses')

    expect(await screen.findByText('还没有收货地址')).toBeInTheDocument()

    const dialog = await openCreateDialog(user)
    expect(within(dialog).getByRole('heading', { name: '新增收货地址' })).toBeInTheDocument()

    await fillAddress(user, { name: '张三', phone: '13800138000', detail: '科技园路 1 号' })
    await user.click(within(dialog).getByRole('button', { name: '保存地址' }))

    expect(await screen.findByText('收货地址已新增')).toBeInTheDocument()
    const card = await screen.findByRole('article', { name: '收货地址：张三' })
    expect(within(card).getByText('默认')).toBeInTheDocument()
    expect(card).toHaveTextContent('广东省深圳市南山区')
  })

  it('新增时校验手机号格式', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/account/addresses')

    await screen.findByText('还没有收货地址')
    const dialog = await openCreateDialog(user)

    await user.type(within(dialog).getByLabelText('收货人姓名'), '张三')
    await user.type(within(dialog).getByLabelText('手机号'), '12345')
    await user.type(within(dialog).getByLabelText('省份'), '广东省')
    await user.type(within(dialog).getByLabelText('城市'), '深圳市')
    await user.type(within(dialog).getByLabelText('区/县'), '南山区')
    await user.type(within(dialog).getByLabelText('详细地址'), '科技园路 1 号')
    await user.click(within(dialog).getByRole('button', { name: '保存地址' }))

    expect(await within(dialog).findByText('请输入正确的 11 位手机号')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: '收货地址：张三' })).not.toBeInTheDocument()
  })

  it('支持新增第二条、设置默认、编辑与删除', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/account/addresses')

    await screen.findByText('还没有收货地址')

    // 第一条：张三（自动默认）
    let dialog = await openCreateDialog(user)
    await fillAddress(user, { name: '张三', phone: '13800138000', detail: '科技园路 1 号' })
    await user.click(within(dialog).getByRole('button', { name: '保存地址' }))
    await screen.findByRole('article', { name: '收货地址：张三' })

    // 第二条：李四（显式默认）
    dialog = await openCreateDialog(user)
    await fillAddress(user, {
      name: '李四',
      phone: '13900139000',
      detail: '科技园路 2 号',
      isDefault: true,
    })
    await user.click(within(dialog).getByRole('button', { name: '保存地址' }))

    const liSiCard = await screen.findByRole('article', { name: '收货地址：李四' })
    expect(within(liSiCard).getByText('默认')).toBeInTheDocument()

    // 把张三设为默认
    await user.click(screen.getByRole('button', { name: '设为默认地址（张三）' }))

    expect(await screen.findByText('已设为默认地址')).toBeInTheDocument()
    await waitFor(() => {
      expect(within(screen.getByRole('article', { name: '收货地址：张三' })).getByText('默认')).toBeInTheDocument()
    })

    // 编辑张三的详细地址
    await user.click(screen.getByRole('button', { name: '编辑地址（张三）' }))
    const editDialog = screen.getByRole('dialog')
    const detailInput = within(editDialog).getByLabelText('详细地址')
    await user.clear(detailInput)
    await user.type(detailInput, '科技园路 3 号 303 室')
    await user.click(within(editDialog).getByRole('button', { name: '保存地址' }))

    expect(await screen.findByText('收货地址已更新')).toBeInTheDocument()
    expect(screen.getByRole('article', { name: '收货地址：张三' })).toHaveTextContent(
      '科技园路 3 号 303 室',
    )

    // 删除李四
    await user.click(screen.getByRole('button', { name: '删除地址（李四）' }))
    const deleteDialog = screen.getByRole('dialog')
    await user.click(within(deleteDialog).getByRole('button', { name: '删除' }))

    expect(await screen.findByText('收货地址已删除')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('article', { name: '收货地址：李四' })).not.toBeInTheDocument()
    })
  })

  it('加载失败展示错误状态与重试入口', async () => {
    await signInAsDemo()
    server.use(
      http.get('/api/addresses', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
    )

    renderApp('/account/addresses')

    expect(await screen.findByText('收货地址加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })
})
