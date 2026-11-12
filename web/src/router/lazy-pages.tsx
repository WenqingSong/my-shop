import { lazy } from 'react'

/**
 * 路由级代码拆分：商品列表、详情、登录、注册与个人中心子页面各自独立 chunk。
 * 首页与个人中心布局保持同步加载，避免首屏/切换页面多一次网络往返。
 */
export const ProductsPage = lazy(() =>
  import('@/pages/products-page').then((module) => ({ default: module.ProductsPage })),
)

export const ProductDetailPage = lazy(() =>
  import('@/pages/product-detail-page').then((module) => ({ default: module.ProductDetailPage })),
)

export const CartPage = lazy(() =>
  import('@/pages/cart-page').then((module) => ({ default: module.CartPage })),
)

export const CheckoutPage = lazy(() =>
  import('@/pages/checkout-page').then((module) => ({ default: module.CheckoutPage })),
)

export const CheckoutSuccessPage = lazy(() =>
  import('@/pages/checkout-success-page').then((module) => ({
    default: module.CheckoutSuccessPage,
  })),
)

export const LoginPage = lazy(() =>
  import('@/pages/login-page').then((module) => ({ default: module.LoginPage })),
)

export const RegisterPage = lazy(() =>
  import('@/pages/register-page').then((module) => ({ default: module.RegisterPage })),
)

export const AccountOverviewPage = lazy(() =>
  import('@/pages/account/overview-page').then((module) => ({
    default: module.AccountOverviewPage,
  })),
)

export const ProfilePage = lazy(() =>
  import('@/pages/account/profile-page').then((module) => ({ default: module.ProfilePage })),
)

export const AddressesPage = lazy(() =>
  import('@/pages/account/addresses-page').then((module) => ({ default: module.AddressesPage })),
)

export const FavoritesPage = lazy(() =>
  import('@/pages/account/favorites-page').then((module) => ({ default: module.FavoritesPage })),
)

export const OrdersPage = lazy(() =>
  import('@/pages/account/orders-page').then((module) => ({ default: module.OrdersPage })),
)

export const OrderDetailPage = lazy(() =>
  import('@/pages/account/order-detail-page').then((module) => ({
    default: module.OrderDetailPage,
  })),
)

export const RefundsPage = lazy(() =>
  import('@/pages/account/refunds-page').then((module) => ({ default: module.RefundsPage })),
)

export const RefundDetailPage = lazy(() =>
  import('@/pages/account/refund-detail-page').then((module) => ({
    default: module.RefundDetailPage,
  })),
)

export const CouponsPage = lazy(() =>
  import('@/pages/account/coupons-page').then((module) => ({ default: module.CouponsPage })),
)
