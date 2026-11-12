import { HttpResponse, http } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { fetchCart, type CartListResult } from '@/api/cart'
import { queryKeys } from '@/api/query-keys'
import {
  getMockCartListRequestCount,
  getMockCartQuantityRequests,
  markMockProductOffShelf,
  markMockSkuDeleted,
  mockCartSkuId,
  seedMockCartItem,
  setMockCartRequestDelay,
  setMockSkuStock,
} from '@/mocks/data/cart'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const DEMO_USER_ID = 1
const PRODUCT_ONE = '星海 X1 Pro 5G 手机 12GB+256GB'
const PRODUCT_TWO = '星海 Note 12 手机 8GB+128GB'

function summaryPanel() {
  return screen.getByRole('complementary', { name: '购物车金额汇总' })
}

describe('购物车页面', () => {
  it('空购物车展示空状态与去挑选商品入口', async () => {
    await signInAsDemo()
    renderApp('/cart')

    expect(await screen.findByText('购物车还是空的')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去挑选商品' })).toHaveAttribute('href', '/products')
    // 空车不展示金额汇总与全选。
    expect(screen.queryByRole('complementary', { name: '购物车金额汇总' })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: '全选商品' })).not.toBeInTheDocument()
  })

  it('展示商品信息、单价、数量、小计与预估金额（整数分转人民币）', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    renderApp('/cart')

    const row = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })

    expect(within(row).getByText(`${PRODUCT_ONE}`)).toBeInTheDocument()
    expect(within(row).getByText('规格：标准款')).toBeInTheDocument()
    // 单价 ¥4,299.00
    expect(within(row).getByText('4,299')).toBeInTheDocument()
    // 小计 2 × ¥4,299.00 = ¥8,598.00
    expect(within(row).getByText('8,598')).toBeInTheDocument()
    // 汇总金额与已选件数
    expect(within(summaryPanel()).getByText('8,598')).toBeInTheDocument()
    expect(within(summaryPanel()).getByText('2 件')).toBeInTheDocument()
    // W3-02 起结算入口指向 /checkout，金额仍以服务端下单结果为准。
    expect(screen.getByRole('link', { name: '去结算' })).toHaveAttribute('href', '/checkout')
    expect(
      screen.getByText(/最终应付金额以提交订单后服务端返回为准/),
    ).toBeInTheDocument()
  })

  it('通过步进器修改数量并同步小计与服务端状态', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    renderApp('/cart')

    const row = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    // 数量 1 时单价与小计同为 ¥4,299.00
    expect(within(row).getAllByText('4,299')).toHaveLength(2)

    await user.click(within(row).getByRole('button', { name: `增加数量（${PRODUCT_ONE}）` }))

    await waitFor(() => {
      expect(within(summaryPanel()).getByText('8,598')).toBeInTheDocument()
    })
    expect(within(summaryPanel()).getByText('2 件')).toBeInTheDocument()

    await waitFor(async () => {
      const serverCart = await fetchCart()
      expect(serverCart.items[0]?.quantity).toBe(2)
    })
  })

  it('连续修改数量时串行发送，旧响应不会覆盖最新值', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    // 拉长首次响应时间，保证三次点击都落在同一个在途请求窗口内。
    setMockCartRequestDelay(300)

    const { client } = renderApp('/cart')
    const row = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    const increase = within(row).getByRole('button', { name: `增加数量（${PRODUCT_ONE}）` })

    await user.click(increase)
    await user.click(increase)
    await user.click(increase)

    await waitFor(
      async () => {
        const serverCart = await fetchCart()
        expect(serverCart.items[0]?.quantity).toBe(4)
      },
      { timeout: 5_000 },
    )

    // 请求被合并为「首个值 + 最终值」，且不存在并发乱序。
    const requests = getMockCartQuantityRequests()
    expect(requests.at(-1)).toBe(4)
    expect(requests.length).toBeLessThanOrEqual(2)
    expect(requests).toEqual([...requests].sort((a, b) => a - b))

    // 缓存最终值来自服务端响应，而不是较早返回的旧值。
    const cached = client.getQueryData<CartListResult>(queryKeys.cart.list())
    expect(cached?.items[0]?.quantity).toBe(4)
  })

  it('取消勾选后不把该商品计入预估金额', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    renderApp('/cart')

    const row = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    const checkbox = within(row).getByRole('checkbox', { name: `选择商品：${PRODUCT_ONE}` })
    expect(checkbox).toBeChecked()

    await user.click(checkbox)

    await waitFor(() => {
      expect(within(summaryPanel()).getByText('0 件')).toBeInTheDocument()
    })
    expect(within(summaryPanel()).getByText('0')).toBeInTheDocument()
    await waitFor(async () => {
      const serverCart = await fetchCart()
      expect(serverCart.items[0]?.selected).toBe(false)
    })
  })

  it('全选会勾选所有可购买商品，并汇总金额', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 1)
    renderApp('/cart')

    const firstRow = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    const secondRow = screen.getByRole('article', { name: `购物车商品：${PRODUCT_TWO}` })

    await user.click(within(firstRow).getByRole('checkbox', { name: `选择商品：${PRODUCT_ONE}` }))

    await waitFor(() => {
      expect(within(summaryPanel()).getByText('1 件')).toBeInTheDocument()
    })

    await user.click(screen.getByRole('checkbox', { name: '全选商品' }))

    await waitFor(() => {
      expect(within(firstRow).getByRole('checkbox')).toBeChecked()
      expect(within(secondRow).getByRole('checkbox')).toBeChecked()
    })
    // 4,299 + 1,999 = 6,298
    await waitFor(() => {
      expect(within(summaryPanel()).getByText('6,298')).toBeInTheDocument()
    })
    await waitFor(async () => {
      const serverCart = await fetchCart()
      expect(serverCart.items.every((item) => item.selected)).toBe(true)
    })
  })

  it('删除商品后列表与徽标同步更新', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 2)
    renderApp('/cart')

    const row = await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    expect(screen.getByRole('link', { name: '购物车，2 件商品' })).toBeInTheDocument()

    await user.click(within(row).getByRole('button', { name: `删除商品（${PRODUCT_ONE}）` }))

    expect(await screen.findByText('已将商品移出购物车')).toBeInTheDocument()
    expect(await screen.findByText('购物车还是空的')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '购物车，2 件商品' })).not.toBeInTheDocument()

    await waitFor(async () => {
      expect((await fetchCart()).items).toHaveLength(0)
    })
  })

  it('展示不可购买与库存不足状态，并禁用勾选与数量', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(2, 0), 2)
    markMockProductOffShelf(1)
    setMockSkuStock(mockCartSkuId(2, 0), 1)

    renderApp('/cart')

    const offShelfRow = await screen.findByRole('article', {
      name: `购物车商品：${PRODUCT_ONE}`,
    })
    expect(within(offShelfRow).getByText('商品已下架，无法购买')).toBeInTheDocument()
    expect(within(offShelfRow).getByRole('checkbox')).toBeDisabled()
    expect(
      within(offShelfRow).getByRole('button', { name: `增加数量（${PRODUCT_ONE}）` }),
    ).toBeDisabled()

    const lowStockRow = screen.getByRole('article', { name: `购物车商品：${PRODUCT_TWO}` })
    expect(within(lowStockRow).getByText('库存不足，当前库存 1 件')).toBeInTheDocument()
    // 预估金额不包含已下架商品：仅 1,999 × 2
    expect(within(summaryPanel()).getByText('3,998')).toBeInTheDocument()
    expect(within(summaryPanel()).getByText(/1 件不可购买/)).toBeInTheDocument()
    // 存在不可购买商品时不提供结算入口，避免必然失败的 9003 提交。
    expect(screen.queryByRole('link', { name: '去结算' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '去结算' })).toBeDisabled()
  })

  it('SKU 被删除时保留条目、展示价格不可用且不参与金额汇总', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(3, 0), 1)
    markMockSkuDeleted(mockCartSkuId(3, 0))

    renderApp('/cart')

    const row = await screen.findByRole('article', { name: '购物车商品：已删除的商品' })
    expect(within(row).getByText('该规格已删除，无法购买')).toBeInTheDocument()
    expect(within(row).getByText('价格不可用')).toBeInTheDocument()
    expect(within(summaryPanel()).getByText('0')).toBeInTheDocument()
  })

  it('加载失败展示错误状态并可重新加载', async () => {
    await signInAsDemo()
    // 查询自带一次自动重试（retry: failureCount < 1），因此前两次请求失败后进入错误态。
    let attempts = 0

    server.use(
      http.get('/api/cart', () => {
        attempts += 1

        if (attempts <= 2) {
          return HttpResponse.json(
            { code: 1000, message: '服务器内部错误', data: null },
            { status: 500 },
          )
        }

        return HttpResponse.json({ code: 0, message: 'OK', data: { items: [] } })
      }),
    )

    renderApp('/cart')

    // 查询带一次自动重试（默认 1s 退避），因此这里放宽等待时间。
    expect(
      await screen.findByText('购物车加载失败', undefined, { timeout: 3_000 }),
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '重新加载' }))
    expect(await screen.findByText('购物车还是空的')).toBeInTheDocument()
  })

  it('退出登录后清理购物车缓存与徽标，不残留上一账号数据', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    const { client, router } = renderApp('/cart')
    await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })
    expect(screen.getByRole('link', { name: '购物车，1 件商品' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/')
    })
    expect(client.getQueryData(queryKeys.cart.list())).toBeUndefined()
    expect(screen.queryByRole('link', { name: '购物车，1 件商品' })).not.toBeInTheDocument()
  })

  it('Header 与购物车页面共享同一次 GET /cart 请求', async () => {
    await signInAsDemo()
    seedMockCartItem(DEMO_USER_ID, mockCartSkuId(1, 0), 1)

    renderApp('/cart')
    await screen.findByRole('article', { name: `购物车商品：${PRODUCT_ONE}` })

    // Header 徽标与页面共用 queryKeys.cart.list()，只应产生一次列表请求。
    expect(getMockCartListRequestCount()).toBe(1)
    expect(screen.getByRole('link', { name: '购物车，1 件商品' })).toHaveAttribute('href', '/cart')
  })
})
