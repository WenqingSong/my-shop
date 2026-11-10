import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Heart } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'

import { FAVORITE_PAGE_SIZE, fetchFavorites, removeFavorite, type FavoriteListResult } from '@/api/favorites'
import { queryKeys } from '@/api/query-keys'
import type { FavoriteItem } from '@/api/types'
import { useAuthStatus } from '@/auth/use-auth'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { Pagination } from '@/components/common/pagination'
import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { describeFavoriteError } from '@/pages/account/account-errors'
import { ProductImage } from '@/components/product/product-image'

function unavailableText(reason: string): string {
  if (reason === 'product_deleted') {
    return '商品已删除，暂时无法查看'
  }

  return '商品已下架，暂时无法购买'
}

function FavoriteCard({
  item,
  onRemove,
  isRemoving,
}: {
  item: FavoriteItem
  onRemove: () => void
  isRemoving: boolean
}) {
  const displayName = item.product_name.trim() || '商品已删除'

  return (
    <article className="flex gap-4 rounded-xl border border-border bg-surface p-4 shadow-card">
      <ProductImage
        src={item.product_main_image}
        alt={displayName}
        className="size-24 shrink-0 rounded-lg border border-border"
      />

      <div className="flex min-w-0 flex-1 flex-col sm:flex-row sm:items-start sm:justify-between sm:gap-3">
        <div className="min-w-0 flex-1">
          {item.available ? (
            <Link
              to={`/products/${String(item.product_id)}`}
              aria-label={`查看商品：${displayName}`}
              className="line-clamp-2 text-sm font-medium leading-6 text-foreground hover:text-primary"
            >
              {displayName}
            </Link>
          ) : (
            <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">{displayName}</p>
          )}

          <div className="mt-1">
            {item.product_price !== null ? (
              <PriceDisplay value={item.product_price} size="md" />
            ) : (
              <span className="text-sm text-muted-foreground">价格不可用</span>
            )}
          </div>

          {item.available ? null : (
            <p className="mt-1 text-xs text-warning">{unavailableText(item.unavailable_reason)}</p>
          )}
          <p className="mt-1 text-xs text-muted-foreground">
            收藏于 {item.created_at ? item.created_at.slice(0, 10) : '—'}
          </p>
        </div>

        <Button
          variant="ghost"
          size="sm"
          onClick={onRemove}
          isLoading={isRemoving}
          aria-label={`取消收藏（${displayName}）`}
          className="mt-2 shrink-0 self-start text-danger hover:bg-danger-soft hover:text-danger sm:mt-0"
        >
          取消收藏
        </Button>
      </div>
    </article>
  )
}

export function FavoritesPage() {
  useDocumentTitle('我的收藏 | SurgeCart 商城')

  const { hasSession } = useAuthStatus()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [actionError, setActionError] = useState<string | null>(null)

  const favoritesQuery = useQuery({
    queryKey: queryKeys.favorites.list(page, FAVORITE_PAGE_SIZE),
    queryFn: ({ signal }) => fetchFavorites(page, FAVORITE_PAGE_SIZE, signal),
    enabled: hasSession,
    staleTime: 30_000,
  })

  const removeMutation = useMutation({
    mutationFn: (item: FavoriteItem) => removeFavorite(item.product_id),
    onMutate: async (item) => {
      setActionError(null)
      await queryClient.cancelQueries({ queryKey: queryKeys.favorites.all })
      const previous = queryClient.getQueriesData<FavoriteListResult>({
        queryKey: queryKeys.favorites.lists(),
      })

      queryClient.setQueriesData<FavoriteListResult>(
        { queryKey: queryKeys.favorites.lists() },
        (old) =>
          old
            ? {
                ...old,
                items: old.items.filter((entry) => entry.product_id !== item.product_id),
                total: Math.max(0, old.total - 1),
              }
            : old,
      )
      queryClient.setQueryData(queryKeys.favorites.check(item.product_id), { favorited: false })

      return { previous }
    },
    onError: (error, _item, context) => {
      context?.previous.forEach(([key, data]) => {
        queryClient.setQueryData(key, data)
      })
      setActionError(describeFavoriteError(error))
    },
    onSuccess: (_data, _item) => {
      // 删除当前页最后一条时回到上一页，避免停留在空页。
      if (favoritesQuery.data && favoritesQuery.data.items.length === 1 && page > 1) {
        setPage((current) => Math.max(1, current - 1))
      }
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.favorites.all })
    },
  })

  const data = favoritesQuery.data
  const totalPages = data ? Math.max(1, Math.ceil(data.total / FAVORITE_PAGE_SIZE)) : 1

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-xl font-semibold text-foreground">我的收藏</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          收藏状态以服务端为准，取消收藏后列表会立即更新。
        </p>
      </div>

      {actionError ? (
        <p role="alert" className="rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm text-danger">
          {actionError}
        </p>
      ) : null}

      {favoritesQuery.isPending ? <LoadingState label="正在加载收藏" /> : null}

      {favoritesQuery.isError ? (
        <ErrorState
          title="收藏加载失败"
          description="收藏列表暂时无法获取，请稍后重试。"
          onRetry={() => void favoritesQuery.refetch()}
        />
      ) : null}

      {data && data.total === 0 ? (
        <EmptyState
          icon={<Heart className="size-6" aria-hidden="true" />}
          title="还没有收藏商品"
          description="在商品详情页点击收藏，商品会出现在这里。"
          action={
            <Button asChild>
              <Link to="/products">去逛逛</Link>
            </Button>
          }
        />
      ) : null}

      {data && data.items.length > 0 ? (
        <>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            共 {data.total} 件收藏
          </p>
          <ul className="flex flex-col gap-4">
            {data.items.map((item) => (
              <li key={item.id}>
                <FavoriteCard
                  item={item}
                  onRemove={() => removeMutation.mutate(item)}
                  isRemoving={
                    removeMutation.isPending &&
                    removeMutation.variables?.product_id === item.product_id
                  }
                />
              </li>
            ))}
          </ul>
          <Pagination page={page} totalPages={totalPages} onPageChange={setPage} />
        </>
      ) : null}
    </div>
  )
}
