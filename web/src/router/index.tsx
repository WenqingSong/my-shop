import { Suspense, type ReactNode } from 'react'
import { createBrowserRouter, type RouteObject } from 'react-router'

import { RouteErrorPage } from '@/components/common/route-error-page'
import { RouteLoading } from '@/components/common/route-loading'
import { RootLayout } from '@/layouts/root-layout'
import { HomePage } from '@/pages/home-page'
import { NotFoundPage } from '@/pages/not-found-page'
import { ProductDetailPage, ProductsPage } from '@/router/lazy-pages'

function withRouteLoading(element: ReactNode) {
  return <Suspense fallback={<RouteLoading />}>{element}</Suspense>
}

/**
 * 路由表单独导出，测试可用 createMemoryRouter(routes) 复用同一份配置。
 * 页面级错误由 errorElement 承接，保留公共布局。
 */
export const routes: RouteObject[] = [
  {
    path: '/',
    element: <RootLayout />,
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, element: <HomePage /> },
      {
        path: 'products',
        element: withRouteLoading(<ProductsPage />),
        errorElement: <RouteErrorPage />,
      },
      {
        path: 'products/:id',
        element: withRouteLoading(<ProductDetailPage />),
        errorElement: <RouteErrorPage />,
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
