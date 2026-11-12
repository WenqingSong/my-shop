import { HttpResponse, http } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { fetchCart } from '@/api/cart'
import { fetchFavoriteStatus } from '@/api/favorites'
import { queryKeys } from '@/api/query-keys'
import { mockProductDetail } from '@/mocks/data/catalog'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

const PRODUCT_NAME = '星海 X1 Pro 5G 手机 12GB+256GB'

describe('商品详情页', () => {
  it('展示商品名、价格、规格与加购入口（未登录时引导登录）', async () => {
    renderApp('/products/1')

    expect(
      await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME }),
    ).toBeInTheDocument()

    const pricePanel = screen.getByText('商品起售价').closest('div')
    expect(pricePanel).not.toBeNull()
    expect(within(pricePanel as HTMLElement).getByText('4,299')).toBeInTheDocument()

    // 后端要求 sku_id 必填，页面不再默认选中规格。
    expect(screen.getByText('请选择商品规格')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '登录后加入购物车' })).toBeInTheDocument()
    expect(screen.getByText('库存状态')).toBeInTheDocument()
  })

  it('切换 SKU 后价格与已选规格同步更新', async () => {
    const user = userEvent.setup()
    renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })

    await user.click(screen.getByRole('button', { name: /标准款/ }))
    expect(screen.getByText('已选规格：标准款')).toBeInTheDocument()

    const pricePanel = screen.getByText('当前价格').closest('div') as HTMLElement
    await user.click(screen.getByRole('button', { name: /升级款/ }))

    expect(screen.getByText('已选规格：升级款')).toBeInTheDocument()
    expect(within(pricePanel).getByText('4,349')).toBeInTheDocument()
    expect(within(pricePanel).queryByText('4,299')).not.toBeInTheDocument()
  })

  it('未选择规格时加购给出必选校验提示，且不发起请求', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })
    const addButton = screen.getByRole('button', { name: '加入购物车' })
    await waitFor(() => expect(addButton).toBeEnabled())

    await user.click(addButton)

    expect(await screen.findByRole('alert')).toHaveTextContent('请先选择商品规格')
    expect(screen.queryByText(/已加入购物车/)).not.toBeInTheDocument()
    expect((await fetchCart()).items).toHaveLength(0)
  })

  it('选择规格与数量后加入购物车，Header 徽标同步更新', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })
    const addButton = screen.getByRole('button', { name: '加入购物车' })
    await waitFor(() => expect(addButton).toBeEnabled())

    await user.click(screen.getByRole('button', { name: /标准款/ }))
    await user.click(screen.getByRole('button', { name: `增加数量（${PRODUCT_NAME}）` }))
    await user.click(addButton)

    expect(await screen.findByText(/已加入购物车（该规格共 2 件）/)).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: '购物车，2 件商品' })).toBeInTheDocument()

    const cart = await fetchCart()
    expect(cart.items).toHaveLength(1)
    expect(cart.items[0]).toMatchObject({ sku_id: 11, quantity: 2 })
  })

  it('无规格商品没有库存数据时明确展示库存未知，而不是 0 库存', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })
    expect(screen.getByText('选择规格后可查看该规格的库存信息。')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /标准款/ }))

    expect(
      screen.getByText('该规格暂无可用的库存数据，库存以下单时服务端校验为准'),
    ).toBeInTheDocument()
    expect(screen.queryByText('该规格当前无货')).not.toBeInTheDocument()
    expect(screen.getByText(/该规格暂时没有可用的库存数据/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加入购物车' })).toBeEnabled()
  })

  it('按规格组选择组合，映射真实 SKU、价格、划线价与库存', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    renderApp('/products/11')

    await screen.findByRole('heading', { level: 1, name: '简行 男士纯棉衬衫' })

    expect(screen.getByRole('group', { name: '商品规格' })).toBeInTheDocument()
    expect(screen.getByText('颜色')).toBeInTheDocument()
    expect(screen.getByText('尺码')).toBeInTheDocument()

    // 只选部分规格时不产生 SKU 选择，也不显示猜测的价格。
    await user.click(screen.getByRole('button', { name: '黑色' }))
    expect(screen.getByText('请选择商品规格')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'M' }))

    expect(screen.getByText('已选规格：黑色,M')).toBeInTheDocument()
    expect(screen.getByText('当前规格库存 5 件')).toBeInTheDocument()

    const pricePanel = screen.getByText('当前价格').closest('div') as HTMLElement
    expect(within(pricePanel).getByText('169')).toBeInTheDocument()
    // 划线价：原价 199.00
    expect(within(pricePanel).getByText('199')).toBeInTheDocument()

    // SKU 图优先于商品主图（商品主图为 product-2，黑色 M 的规格图为 product-1）。
    const hero = screen.getByRole('img', { name: '简行 男士纯棉衬衫' }) as HTMLImageElement
    expect(hero.src).toContain('/mock/product-1.svg')

    // 同一规格组的另一个值：该组合库存为 0。
    await user.click(screen.getByRole('button', { name: 'L' }))

    expect(await screen.findByText('该规格当前无货')).toBeInTheDocument()
    expect(screen.getByText('当前规格暂无库存，暂时无法加入购物车。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加入购物车' })).toBeDisabled()

    // 换回有货组合可恢复加购。
    await user.click(screen.getByRole('button', { name: '白色' }))
    await user.click(screen.getByRole('button', { name: 'M' }))

    expect(await screen.findByText('当前规格库存 12 件')).toBeInTheDocument()
    expect(screen.getByText('已选规格：白色,M')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加入购物车' })).toBeEnabled()
  })

  it('规格组合未命中 productValue 时明确提示不可售', async () => {
    const user = userEvent.setup()
    const detail = mockProductDetail(11)

    if (!detail) {
      throw new Error('Mock 商品 11 不存在')
    }

    const partialProductValue = { ...detail.productValue }
    delete partialProductValue['白色,L']

    server.use(
      http.get('/api/products/11', () =>
        HttpResponse.json({
          code: 0,
          message: 'OK',
          data: { ...detail, productValue: partialProductValue },
        }),
      ),
    )

    renderApp('/products/11')

    await screen.findByRole('heading', { level: 1, name: '简行 男士纯棉衬衫' })
    await user.click(screen.getByRole('button', { name: '白色' }))
    await user.click(screen.getByRole('button', { name: 'L' }))

    expect(await screen.findByText('该规格组合暂不可售，请更换规格。')).toBeInTheDocument()
    expect(screen.getByText('请选择商品规格')).toBeInTheDocument()
  })

  it('商品没有可用 SKU 时不能加入购物车', async () => {
    const detail = mockProductDetail(1)

    server.use(
      http.get('/api/products/1', () =>
        HttpResponse.json({ code: 0, message: 'OK', data: { ...detail, skus: [] } }),
      ),
    )

    await signInAsDemo()
    renderApp('/products/1')

    expect(await screen.findByText('该商品暂未配置可售规格')).toBeInTheDocument()
    expect(screen.getByText('该商品暂未配置可售规格，无法加入购物车。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加入购物车' })).toBeDisabled()
  })

  it('展示评价汇总并支持翻页', async () => {
    const user = userEvent.setup()
    renderApp('/products/1')

    expect(await screen.findByText('共 7 条已发布评价')).toBeInTheDocument()
    // 评价按 id 倒序：第一页是最新的 5 条。
    expect(screen.getByText('客服回复及时，发票开具顺利。')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '第 2 页' }))

    expect(await screen.findByText('包装完好，物流很快，手机手感不错。')).toBeInTheDocument()
  })

  it('商品不存在或已下架时展示 404 状态', async () => {
    renderApp('/products/999999')

    expect(
      await screen.findByRole('heading', { level: 1, name: '商品不存在或已下架' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回商品列表' })).toHaveAttribute('href', '/products')
  })

  it('非法商品 id 不发起请求，直接展示 404 状态', async () => {
    renderApp('/products/not-a-number')

    expect(
      await screen.findByRole('heading', { level: 1, name: '商品不存在或已下架' }),
    ).toBeInTheDocument()
  })

  it('未登录时加购入口跳转登录，登录后回到商品详情', async () => {
    const user = userEvent.setup()
    const { router } = renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })

    await user.click(screen.getByRole('button', { name: '登录后加入购物车' }))

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')

    await user.type(screen.getByLabelText('用户名'), 'demo')
    await user.type(screen.getByLabelText('密码'), 'demo12345')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/products/1')
    })
    expect(await screen.findByRole('button', { name: '加入购物车' })).toBeInTheDocument()
  })

  it('未登录时收藏入口跳转登录，登录后回到商品详情', async () => {
    const user = userEvent.setup()
    const { router } = renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })
    await user.click(screen.getByRole('button', { name: '登录后收藏' }))

    expect(await screen.findByRole('heading', { level: 1, name: '登录' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')

    await user.type(screen.getByLabelText('用户名'), 'demo')
    await user.type(screen.getByLabelText('密码'), 'demo12345')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/products/1')
    })
    expect(await screen.findByRole('button', { name: '收藏' })).toBeInTheDocument()
  })

  it('收藏与取消收藏同步服务端状态与 Query 缓存', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    const { client } = renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: PRODUCT_NAME })

    await user.click(await screen.findByRole('button', { name: '收藏' }))
    expect(await screen.findByRole('button', { name: '已收藏' })).toBeInTheDocument()
    expect(client.getQueryData(queryKeys.favorites.check(1))).toEqual({ favorited: true })
    await waitFor(async () => {
      await expect(fetchFavoriteStatus(1)).resolves.toEqual({ favorited: true })
    })

    await user.click(screen.getByRole('button', { name: '已收藏' }))
    expect(await screen.findByRole('button', { name: '收藏' })).toBeInTheDocument()
    expect(client.getQueryData(queryKeys.favorites.check(1))).toEqual({ favorited: false })
    await waitFor(async () => {
      await expect(fetchFavoriteStatus(1)).resolves.toEqual({ favorited: false })
    })
  })
})
