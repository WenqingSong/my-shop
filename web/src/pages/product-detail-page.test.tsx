import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { renderApp } from '@/test/render'

describe('商品详情页', () => {
  it('展示商品名、价格、规格与禁用的购物车入口', async () => {
    renderApp('/products/1')

    expect(
      await screen.findByRole('heading', { level: 1, name: '星海 X1 Pro 5G 手机 12GB+256GB' }),
    ).toBeInTheDocument()

    const pricePanel = screen.getByText('当前价格').closest('div')
    expect(pricePanel).not.toBeNull()
    expect(within(pricePanel as HTMLElement).getByText('4,299')).toBeInTheDocument()

    expect(screen.getByText('已选规格：标准款')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '加入购物车' })).toBeDisabled()
    expect(screen.getByText('库存状态')).toBeInTheDocument()
  })

  it('切换 SKU 后价格与已选规格同步更新', async () => {
    const user = userEvent.setup()
    renderApp('/products/1')

    await screen.findByRole('heading', { level: 1, name: '星海 X1 Pro 5G 手机 12GB+256GB' })

    const pricePanel = screen.getByText('当前价格').closest('div') as HTMLElement
    await user.click(screen.getByRole('button', { name: /升级款/ }))

    expect(screen.getByText('已选规格：升级款')).toBeInTheDocument()
    expect(within(pricePanel).getByText('4,349')).toBeInTheDocument()
    expect(within(pricePanel).queryByText('4,299')).not.toBeInTheDocument()
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
})
