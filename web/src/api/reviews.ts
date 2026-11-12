import { getData } from '@/api/envelope'
import type { ReviewListResult } from '@/api/types'

export const PRODUCT_REVIEW_PAGE_SIZE = 5

export interface ReviewListQuery {
  page?: number
  size?: number
}

/**
 * GET /products/:id/reviews（公开，仅 published，按 id 倒序）
 * 后端：api/review/v1/review.go ListReq / ListRes（含 avg_rating、count 实时聚合）。
 */
export function fetchProductReviews(
  productId: number,
  query: ReviewListQuery = {},
  signal?: AbortSignal,
): Promise<ReviewListResult> {
  return getData<ReviewListResult>(`/products/${String(productId)}/reviews`, {
    signal,
    query: {
      page: query.page,
      size: query.size ?? PRODUCT_REVIEW_PAGE_SIZE,
    },
  })
}
