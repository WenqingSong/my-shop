import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/mocks/server'
import { renderApp } from '@/test/render'

describe('商品列表页', () => {
  it('展示商品总数、第一个商品与分页信息', async () => {
    renderApp('/products')

    expect(await screen.findByRole('heading', { level: 1, name: '全部商品' })).toBeInTheDocument()
    expect(
      await screen.findByRole('link', { name: '查看商品：云裳 女士通勤托特包' }),
    ).toBeInTheDocument()
    expect(screen.getByText('第 1 / 3 页')).toBeInTheDocument()
  })

  it('Header 搜索框提交后按关键词过滤', async () => {
    const user = userEvent.setup()
    renderApp('/products')

    await screen.findByRole('heading', { level: 1, name: '全部商品' })

    const searchInput = screen.getAllByRole('searchbox', { name: '搜索关键词' })[0]
    expect(searchInput).toBeDefined()

    await user.type(searchInput as HTMLElement, '星海')
    await user.click(screen.getAllByRole('button', { name: '搜索' })[0] as HTMLElement)

    expect(
      await screen.findByRole('heading', { level: 1, name: '“星海”的搜索结果' }),
    ).toBeInTheDocument()
    expect(await screen.findByText('第 1 / 1 页')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: /查看商品：星海/ })).toHaveLength(4)
  })

  it('分页切换会加载下一页商品', async () => {
    const user = userEvent.setup()
    renderApp('/products')

    await screen.findByRole('link', { name: '查看商品：云裳 女士通勤托特包' })

    await user.click(screen.getByRole('button', { name: '第 2 页' }))

    expect(
      await screen.findByRole('link', { name: '查看商品：云裳 女士针织开衫' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: '查看商品：云裳 女士通勤托特包' }),
    ).not.toBeInTheDocument()
  })

  it('分类筛选只展示该分类商品', async () => {
    renderApp('/products?category_id=12')

    expect(await screen.findByRole('heading', { level: 1, name: '耳机音箱' })).toBeInTheDocument()
    expect(
      await screen.findByRole('link', { name: '查看商品：云听 Air 3 主动降噪耳机' }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /查看商品：云裳/ })).not.toBeInTheDocument()
  })

  it('无匹配结果时展示空状态并支持清除筛选', async () => {
    const user = userEvent.setup()
    renderApp('/products?q=%E4%B8%8D%E5%AD%98%E5%9C%A8%E7%9A%84%E5%95%86%E5%93%81')

    expect(await screen.findByText('没有找到符合条件的商品')).toBeInTheDocument()

    const emptyState = screen.getByText('没有找到符合条件的商品').closest('div')
    expect(emptyState).not.toBeNull()
    await user.click(within(emptyState as HTMLElement).getByRole('button', { name: '清除筛选' }))

    expect(await screen.findByRole('heading', { level: 1, name: '全部商品' })).toBeInTheDocument()
  })

  it('接口失败时展示错误状态与重试入口', async () => {
    server.use(
      http.get('/api/products', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
    )

    renderApp('/products')

    expect(await screen.findByText('商品加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })

  it('价格升序排序使用后端 sort/order 参数', async () => {
    renderApp('/products?sort=price&order=asc')

    const cards = await screen.findAllByRole('link', { name: /查看商品：/ })
    expect(cards[0]).toHaveAccessibleName('查看商品：暖居 布艺收纳箱 45L')
  })
})
