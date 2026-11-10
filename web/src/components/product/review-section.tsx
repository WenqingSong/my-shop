import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { UserRound } from 'lucide-react'
import { useState } from 'react'

import { queryKeys } from '@/api/query-keys'
import { fetchProductReviews, PRODUCT_REVIEW_PAGE_SIZE } from '@/api/reviews'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { Pagination } from '@/components/common/pagination'
import { RatingStars } from '@/components/product/rating-stars'

export interface ReviewSectionProps {
  productId: number
}

function formatDate(value: string | null): string {
  return value ? value.slice(0, 10) : ''
}

/**
 * 商品公开评价：独立接口 GET /products/:id/reviews，
 * 与商品详情解耦，评价加载失败不影响商品主信息展示。
 */
export function ReviewSection({ productId }: ReviewSectionProps) {
  const [page, setPage] = useState(1)

  const reviewsQuery = useQuery({
    queryKey: queryKeys.reviews.productList(productId, page, PRODUCT_REVIEW_PAGE_SIZE),
    queryFn: ({ signal }) => fetchProductReviews(productId, { page, size: PRODUCT_REVIEW_PAGE_SIZE }, signal),
    placeholderData: keepPreviousData,
  })

  const data = reviewsQuery.data
  const totalPages = data ? Math.max(1, Math.ceil(data.count / PRODUCT_REVIEW_PAGE_SIZE)) : 1

  return (
    <section aria-labelledby="review-heading" className="border-t border-border pt-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h2 id="review-heading" className="text-xl font-semibold text-foreground">
            商品评价
          </h2>
          {data && data.count > 0 ? (
            <p className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
              <RatingStars value={data.avg_rating} />
              <span className="font-medium text-foreground">{data.avg_rating.toFixed(1)}</span>
              <span>共 {data.count} 条已发布评价</span>
            </p>
          ) : null}
        </div>
      </div>

      <div className="mt-5">
        {reviewsQuery.isPending ? <LoadingState label="正在加载评价" /> : null}

        {reviewsQuery.isError ? (
          <ErrorState
            title="评价加载失败"
            description="商品评价暂时无法获取，可稍后重试。"
            onRetry={() => void reviewsQuery.refetch()}
            className="py-10"
          />
        ) : null}

        {data && data.count === 0 ? (
          <EmptyState title="暂无商品评价" description="该商品还没有已发布的评价。" />
        ) : null}

        {data && data.items.length > 0 ? (
          <>
            <ul className="divide-y divide-border">
              {data.items.map((review) => (
                <li key={review.id} className="flex gap-3 py-5">
                  <span className="grid size-9 shrink-0 place-items-center rounded-full bg-gray-100 text-gray-500">
                    <UserRound className="size-4" aria-hidden="true" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <span className="text-sm font-medium text-foreground">买家</span>
                      <RatingStars value={review.rating} size="sm" />
                      <time className="text-xs text-muted-foreground">{formatDate(review.created_at)}</time>
                    </div>
                    <p className="mt-2 whitespace-pre-line break-words text-sm leading-6 text-foreground">
                      {review.content}
                    </p>
                  </div>
                </li>
              ))}
            </ul>

            <Pagination
              page={page}
              totalPages={totalPages}
              onPageChange={setPage}
              className="mt-6"
            />
          </>
        ) : null}
      </div>
    </section>
  )
}
