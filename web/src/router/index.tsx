import { createBrowserRouter, type RouteObject } from 'react-router'

import { RootLayout } from '@/layouts/root-layout'
import { HomePage } from '@/pages/home-page'
import { NotFoundPage } from '@/pages/not-found-page'

/**
 * 路由表单独导出，测试可用 createMemoryRouter(routes) 复用同一份配置。
 * 业务路由（商品、购物车、订单）在 W1–W5 按契约逐步加入。
 */
export const routes: RouteObject[] = [
  {
    path: '/',
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
