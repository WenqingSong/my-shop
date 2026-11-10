import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { addFavorite, fetchFavoriteStatus } from '@/api/favorites'
import { markMockProductOffShelf, seedMockFavorite } from '@/mocks/data/favorites'
import { server } from '@/mocks/server'
import { signInAsDemo } from '@/test/auth'
import { renderApp } from '@/test/render'

describe('我的收藏', () => {
  it('无收藏时展示空状态与去逛逛入口', async () => {
    await signInAsDemo()
    renderApp('/account/favorites')

    expect(await screen.findByText('还没有收藏商品')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '去逛逛' })).toHaveAttribute('href', '/products')
  })

  it('展示收藏、不可用商品提示并支持取消收藏', async () => {
    const user = userEvent.setup()
    await signInAsDemo()
    await addFavorite(1)
    await addFavorite(4)
    markMockProductOffShelf(3)
    seedMockFavorite(1, 3)

    renderApp('/account/favorites')

    expect(await screen.findByRole('heading', { name: '我的收藏' })).toBeInTheDocument()
    expect(await screen.findByText('共 3 件收藏')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: '查看商品：星海 X1 Pro 5G 手机 12GB+256GB' }),
    ).toHaveAttribute('href', '/products/1')
    expect(screen.getByText('商品已下架，暂时无法购买')).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: '取消收藏（星海 X1 Pro 5G 手机 12GB+256GB）' }),
    )

    await waitFor(() => {
      expect(
        screen.queryByRole('link', { name: '查看商品：星海 X1 Pro 5G 手机 12GB+256GB' }),
      ).not.toBeInTheDocument()
    })
    expect(await screen.findByText('共 2 件收藏')).toBeInTheDocument()
    await expect(fetchFavoriteStatus(1)).resolves.toEqual({ favorited: false })
  })

  it('加载失败展示错误状态与重试入口', async () => {
    await signInAsDemo()
    server.use(
      http.get('/api/favorites', () =>
        HttpResponse.json({ code: 1000, message: '服务器内部错误', data: null }, { status: 500 }),
      ),
    )

    renderApp('/account/favorites')

    expect(await screen.findByText('收藏加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })
})
