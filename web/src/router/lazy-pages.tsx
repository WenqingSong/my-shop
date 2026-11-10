import { lazy } from 'react'

/**
 * 路由级代码拆分：商品列表、详情、登录、注册、账户各自独立 chunk。
 * 首页保持同步加载，避免首屏多一次网络往返。
 */
export const ProductsPage = lazy(() =>
  import('@/pages/products-page').then((module) => ({ default: module.ProductsPage })),
)

export const ProductDetailPage = lazy(() =>
  import('@/pages/product-detail-page').then((module) => ({ default: module.ProductDetailPage })),
)

export const LoginPage = lazy(() =>
  import('@/pages/login-page').then((module) => ({ default: module.LoginPage })),
)

export const RegisterPage = lazy(() =>
  import('@/pages/register-page').then((module) => ({ default: module.RegisterPage })),
)

export const AccountPage = lazy(() =>
  import('@/pages/account-page').then((module) => ({ default: module.AccountPage })),
)
