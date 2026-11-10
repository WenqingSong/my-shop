import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { queryKeys } from '@/api/query-keys'
import { seedMockOrder } from '@/mocks/data/orders'
import { getMockRefundWriteCount, seedMockRefund, setMockRefundDelay } from '@/mocks/data/refunds'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1

async function openOrderDetail(orderId: number) {
  const result = renderApp(`/account/orders/${String(orderId)}`)
  await screen.findByRole('heading', { name: '订单详情' })
  return result
}

describe('退款申请入口（按后端状态闸门展示）', () => {
  it('已支付未发货订单展示「申请退款」入口', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openOrderDetail(order.id)

    expect(await screen.findByRole('button', { name: '申请退款' })).toBeInTheDocument()
    expect(screen.getByText(/订单已支付且未发货，可以申请整单退款/)).toBeInTheDocument()
  })

  it.each(['pending_payment', 'shipped', 'cancelled', 'refunded'] as const)(
    '%s 订单不展示申请退款入口',
    async (status) => {
      await signInAsDemo()
      const order = seedMockOrder(DEMO_USER_ID, { status })

      await openOrderDetail(order.id)

      // 等退款区块加载完成（否则可能因 pending 而误判）。
      expect(
        await screen.findByText(/该订单没有退款申请。/, undefined, { timeout: 3_000 }),
      ).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: '申请退款' })).not.toBeInTheDocument()
    },
  )

  it('已存在有效退款申请时不展示入口，并说明不可重复申请', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'pending_refund' })

    await openOrderDetail(order.id)

    expect(await screen.findByText(/该订单存在处理中的退款申请（待退款）/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '申请退款' })).not.toBeInTheDocument()
  })

  it('已撤销的退款不阻止再次申请', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'revoked' })

    await openOrderDetail(order.id)

    expect(await screen.findByRole('button', { name: '申请退款' })).toBeInTheDocument()
    // 订单详情同时列出该订单的历史退款记录。
    expect(screen.getByText('已撤销')).toBeInTheDocument()
  })
})

describe('退款申请表单', () => {
  it('确认框说明整单退款、需审核且不代表资金退回', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 209_900 })

    await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('heading', { name: '申请整单退款' })).toBeInTheDocument()
    expect(dialog).toHaveTextContent('退款金额由服务端按订单实付金额计算，无法修改')
    expect(dialog).toHaveTextContent('申请成功不代表退款完成')
    expect(dialog).toHaveTextContent('也不代表真实资金已经退回')
    expect(dialog).toHaveTextContent(order.order_no)
    // 金额来自订单 total_amount，且没有可编辑的金额输入框。
    expect(within(dialog).getByText('订单实付金额').closest('div')).toHaveTextContent('¥2,099.00')
    expect(within(dialog).queryByLabelText(/金额/)).not.toBeInTheDocument()
    // 后端没有图片上传接口，表单不提供凭证上传。
    expect(within(dialog).queryByLabelText(/凭证|上传/)).not.toBeInTheDocument()
    expect(dialog.textContent).not.toMatch(/支付宝|微信支付|银联/)
  })

  it('原因为空时阻止提交并提示', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    expect(await screen.findByText('请填写退款原因')).toBeInTheDocument()
    expect(getMockRefundWriteCount()).toBe(0)
  })

  it('原因超过 255 字符时阻止提交', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    const dialog = await screen.findByRole('dialog')
    // paste 比逐字符 type 快得多，避免 256 次输入触发测试超时。
    await user.click(within(dialog).getByLabelText('退款原因'))
    await user.paste('退'.repeat(256))
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    expect(await screen.findByText('退款原因不能超过 255 个字符')).toBeInTheDocument()
    expect(getMockRefundWriteCount()).toBe(0)
  })

  it('提交成功跳转退款详情，状态为申请中', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid', total_amount: 209_900 })

    const { router } = await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('退款原因'), '下单后不想要了')
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/account/refunds/1')
    })

    expect(await screen.findByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    expect(document.querySelector('[data-refund-status="applying"]')).toHaveTextContent('申请中')
    expect(screen.getByText('下单后不想要了')).toBeInTheDocument()
    expect(screen.getByText('退款金额').closest('div')).toHaveTextContent('¥2,099.00')
  })

  it('请求在途时禁用提交，重复点击只发出一次请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    setMockRefundDelay(120)
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('退款原因'), '重复提交测试')
    const submit = within(dialog).getByRole('button', { name: '提交退款申请' })
    await user.click(submit)

    await waitFor(() => {
      expect(within(dialog).getByRole('button', { name: '提交退款申请' })).toBeDisabled()
    })

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '退款详情' })).toBeInTheDocument()
    })
    expect(getMockRefundWriteCount()).toBe(1)
  })

  it('后端返回 18003 时展示业务错误并刷新为服务端状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    const { router } = await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    server.use(
      http.post('/api/orders/:id/refund', () =>
        HttpResponse.json({ code: 18003, message: '订单不可退款', data: null }, { status: 409 }),
      ),
    )

    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('退款原因'), '抢在发货后提交')
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    expect(
      await screen.findByText(/只有「已支付且未发货」的订单可以申请退款，已为你刷新最新状态/),
    ).toBeInTheDocument()
    // 失败不跳转，仍停留在订单详情。
    expect(router.state.location.pathname).toBe(`/account/orders/${String(order.id)}`)
  })

  it('后端返回 18004 时提示查看现有退款记录', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    server.use(
      http.post('/api/orders/:id/refund', () =>
        HttpResponse.json({ code: 18004, message: '退款申请已存在', data: null }, { status: 409 }),
      ),
    )

    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('退款原因'), '并发重复申请')
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    expect(
      await screen.findByText(/该订单已有处理中的退款申请，请直接查看现有退款记录/),
    ).toBeInTheDocument()
  })

  it('网络异常时提示网络问题且不跳转', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })

    const { router } = await openOrderDetail(order.id)
    await user.click(await screen.findByRole('button', { name: '申请退款' }))

    server.use(http.post('/api/orders/:id/refund', () => HttpResponse.error()))

    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('退款原因'), '网络异常测试')
    await user.click(within(dialog).getByRole('button', { name: '提交退款申请' }))

    expect(await screen.findByText('网络异常，请检查网络后重试')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe(`/account/orders/${String(order.id)}`)
  })
})

describe('订单列表与退款的联动', () => {
  it('订单卡片展示该订单最新的退款状态', async () => {
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'pending_refund' })

    renderApp('/account/orders')

    const card = (await screen.findByText(order.order_no)).closest('article')
    expect(card).not.toBeNull()
    expect(card).toHaveTextContent('退款')
    expect(card).toHaveTextContent('待退款')
    // 订单本身仍是已支付（退款申请不改变订单状态）。
    expect(card).toHaveTextContent('已支付')
  })

  it('退出登录后清理退款缓存，避免下一个账号看到上一位用户的数据', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const order = seedMockOrder(DEMO_USER_ID, { status: 'paid' })
    seedMockRefund(DEMO_USER_ID, order.id, { status: 'applying', reason: '上一位用户的退款' })

    const { client } = renderApp('/account/refunds')
    expect(await screen.findByText('上一位用户的退款')).toBeInTheDocument()
    expect(client.getQueryData(queryKeys.refunds.list())).toBeDefined()

    await user.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(client.getQueryData(queryKeys.refunds.list())).toBeUndefined()
    })
    expect(client.getQueryData(queryKeys.orders.list())).toBeUndefined()
  })
})
