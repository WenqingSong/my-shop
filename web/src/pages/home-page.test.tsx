import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { renderApp } from '@/test/render'

describe('商城首页', () => {
  it('渲染品牌首屏与分类入口', async () => {
    renderApp('/')

    expect(screen.getByRole('heading', { level: 1, name: /把好物/ })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '数码' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '家居生活' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '手机' })).toHaveAttribute('href', '/products?category_id=11')
  })

  it('展示最新上架商品并可进入详情', async () => {
    renderApp('/')

    expect(
      await screen.findByRole('link', { name: '查看商品：云裳 女士通勤托特包' }),
    ).toHaveAttribute('href', '/products/26')
  })

  it('未配置推荐位 code 时不渲染推荐区块', async () => {
    renderApp('/')

    await screen.findByRole('heading', { name: '数码' })

    expect(screen.queryByRole('heading', { name: '编辑推荐' })).not.toBeInTheDocument()
  })

  it('展示轮播图标题并链接到对应分类', async () => {
    renderApp('/')

    expect(await screen.findByRole('link', { name: /新品首发/ })).toHaveAttribute(
      'href',
      '/products?category_id=11',
    )
  })
})
