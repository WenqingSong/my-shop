import { lazy } from 'react'

/**
 * 路由级代码拆分：商品列表与详情各自成为独立 chunk。
 * 首页保持同步加载，避免首屏多一次网络往返。
 */
export const ProductsPage = lazy(() =>
  import('@/pages/products-page').then((module) => ({ default: module.ProductsPage })),
)

export const ProductDetailPage = lazy(() =>
  import('@/pages/product-detail-page').then((module) => ({ default: module.ProductDetailPage })),
)
