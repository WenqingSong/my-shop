import { getData } from '@/api/envelope'
import type { ProductDetail, ProductListResult } from '@/api/types'

/** 后端白名单：internal/logic/product/product.go sortColumns。 */
export type ProductSort = 'id' | 'price' | 'created_at' | 'updated_at'
export type SortOrder = 'asc' | 'desc'

export interface ProductListQuery {
  page?: number
  size?: number
  categoryId?: number
  keyword?: string
  sort?: ProductSort
  order?: SortOrder
}

/** 商品列表每页数量：桌面 4 列 × 3 行，移动端 2 列 × 6 行。 */
export const PRODUCT_PAGE_SIZE = 12

/** 首页商品区块取数上限（与列表页分页解耦）。 */
export const HOME_PRODUCT_LIMIT = 10

/**
 * GET /products（公开，仅 on_shelf）
 * 后端：api/product/v1/product.go ListReq / ListRes。
 */
export function fetchProductList(
  query: ProductListQuery = {},
  signal?: AbortSignal,
): Promise<ProductListResult> {
  const keyword = query.keyword?.trim()

  return getData<ProductListResult>('/products', {
    signal,
    query: {
      page: query.page,
      size: query.size,
      category_id: query.categoryId,
      // 空关键词不发送，避免后端对空串执行无意义 LIKE。
      keyword: keyword ? keyword : undefined,
      sort: query.sort,
      order: query.order,
    },
  })
}

/**
 * GET /products/:id（公开，仅 on_shelf；后端同步自增 view_count）
 * 后端：api/product/v1/product.go DetailReq / DetailRes（内嵌 enabled SKU）。
 */
export function fetchProductDetail(productId: number, signal?: AbortSignal): Promise<ProductDetail> {
  return getData<ProductDetail>(`/products/${String(productId)}`, { signal })
}
