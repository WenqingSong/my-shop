import { Suspense, type ReactNode } from 'react'
import { createBrowserRouter, type RouteObject } from 'react-router'

import { GuestOnly } from '@/auth/guest-only'
import { RequireAuth } from '@/auth/require-auth'
import { RouteErrorPage } from '@/components/common/route-error-page'
import { RouteLoading } from '@/components/common/route-loading'
import { RootLayout } from '@/layouts/root-layout'
import { AccountLayout } from '@/pages/account/account-layout'
import { HomePage } from '@/pages/home-page'
import { NotFoundPage } from '@/pages/not-found-page'
import {
  AccountOverviewPage,
  AddressesPage,
  CartPage,
  FavoritesPage,
  LoginPage,
  ProductDetailPage,
  ProductsPage,
  ProfilePage,
  RegisterPage,
} from '@/router/lazy-pages'

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
      {
        // 购物车所有接口都要求登录（internal/cmd/routes_frontend.go 的 Auth 分组），
        // 因此路由层直接复用 RequireAuth：未登录跳 /login 并携带 /cart 作为回跳地址。
        path: 'cart',
        element: withRouteLoading(
          <RequireAuth>
            <CartPage />
          </RequireAuth>,
        ),
        errorElement: <RouteErrorPage />,
      },
      {
        path: 'login',
        element: withRouteLoading(
          <GuestOnly>
            <LoginPage />
          </GuestOnly>,
        ),
        errorElement: <RouteErrorPage />,
      },
      {
        path: 'register',
        element: withRouteLoading(
          <GuestOnly>
            <RegisterPage />
          </GuestOnly>,
        ),
        errorElement: <RouteErrorPage />,
      },
      {
        path: 'account',
        element: withRouteLoading(
          <RequireAuth>
            <AccountLayout />
          </RequireAuth>,
        ),
        errorElement: <RouteErrorPage />,
        children: [
          { index: true, element: withRouteLoading(<AccountOverviewPage />) },
          {
            path: 'profile',
            element: withRouteLoading(<ProfilePage />),
            errorElement: <RouteErrorPage />,
          },
          {
            path: 'addresses',
            element: withRouteLoading(<AddressesPage />),
            errorElement: <RouteErrorPage />,
          },
          {
            path: 'favorites',
            element: withRouteLoading(<FavoritesPage />),
            errorElement: <RouteErrorPage />,
          },
        ],
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
