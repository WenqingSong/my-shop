import { render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'

import { createQueryClient } from '@/api/query-client'
import { AppProviders } from '@/providers/app-providers'
import { routes } from '@/router'

function renderApp(initialRoute = '/') {
  const router = createMemoryRouter(routes, { initialEntries: [initialRoute] })

  return render(
    <AppProviders client={createQueryClient()}>
      <RouterProvider router={router} />
    </AppProviders>,
  )
}

describe('App', () => {
  it('渲染首页与公共布局', () => {
    renderApp()

    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('contentinfo')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: /把好物/ })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument()
    expect(screen.getByText('W0 工程基线')).toBeInTheDocument()
  })

  it('首页展示设计基线与组件基线区块', () => {
    renderApp()

    expect(screen.getByRole('heading', { name: '设计基线' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '组件与交互状态' })).toBeInTheDocument()
    expect(screen.getAllByText('1,299').length).toBeGreaterThan(0)
    expect(screen.getAllByText('.00').length).toBeGreaterThan(0)
  })

  it('未知路由展示 404 页面并保留公共布局', () => {
    renderApp('/not-exist')

    expect(screen.getByRole('heading', { level: 1, name: '页面不存在' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回首页' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('banner')).toBeInTheDocument()
  })
})
