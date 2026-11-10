import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { renderApp } from '@/test/render'

describe('App', () => {
  it('首页渲染公共布局与主导航', () => {
    renderApp('/')

    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('contentinfo')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: /把好物/ })).toBeInTheDocument()

    const navigation = screen.getByRole('navigation', { name: '主导航' })
    expect(navigation).toHaveTextContent('首页')
    expect(navigation).toHaveTextContent('全部商品')
  })

  it('商品列表路由按需加载并可访问', async () => {
    renderApp('/products')

    expect(await screen.findByRole('heading', { level: 1, name: '全部商品' })).toBeInTheDocument()
    expect(screen.getByRole('banner')).toBeInTheDocument()
  })

  it('未知路由展示 404 页面并保留公共布局', () => {
    renderApp('/not-exist')

    expect(screen.getByRole('heading', { level: 1, name: '页面不存在' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回首页' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('banner')).toBeInTheDocument()
  })
})
