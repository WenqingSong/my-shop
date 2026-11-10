import { Heart, Loader2 } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router'

import { useAuthStatus } from '@/auth/use-auth'
import { Button } from '@/components/ui/button'
import { useFavoriteStatus, useToggleFavorite } from '@/hooks/use-favorites'
import { describeFavoriteError } from '@/pages/account/account-errors'
import { cn } from '@/utils/cn'

/**
 * 商品详情收藏按钮。
 * 未登录时不请求收藏态，点击跳登录并携带来源；登录成功后回到商品详情。
 */
export function FavoriteButton({ productId }: { productId: number }) {
  const { status, refetch } = useAuthStatus()
  const location = useLocation()
  const navigate = useNavigate()
  const favoriteQuery = useFavoriteStatus(productId)
  const toggleFavorite = useToggleFavorite()

  if (status === 'unauthenticated') {
    return (
      <Button
        variant="outline"
        size="lg"
        onClick={() =>
          navigate('/login', {
            state: { from: `${location.pathname}${location.search}` },
          })
        }
      >
        <Heart aria-hidden="true" />
        登录后收藏
      </Button>
    )
  }

  if (status === 'loading' || favoriteQuery.isPending) {
    return (
      <Button variant="outline" size="lg" disabled>
        <Loader2 className="animate-spin" aria-hidden="true" />
        收藏状态加载中
      </Button>
    )
  }

  if (status === 'error') {
    return (
      <Button variant="outline" size="lg" onClick={() => void refetch()}>
        <Heart aria-hidden="true" />
        账户异常，点击重试
      </Button>
    )
  }

  if (favoriteQuery.isError) {
    return (
      <div className="flex flex-col gap-1">
        <Button variant="outline" size="lg" onClick={() => void favoriteQuery.refetch()}>
          <Heart aria-hidden="true" />
          收藏状态加载失败，重试
        </Button>
      </div>
    )
  }

  const favorited = favoriteQuery.data?.favorited ?? false

  return (
    <div className="flex flex-col gap-1">
      <Button
        variant={favorited ? 'secondary' : 'outline'}
        size="lg"
        aria-pressed={favorited}
        isLoading={toggleFavorite.isPending}
        onClick={() => toggleFavorite.mutate({ productId, favorite: !favorited })}
      >
        <Heart
          aria-hidden="true"
          className={cn(favorited && 'fill-current')}
        />
        {favorited ? '已收藏' : '收藏'}
      </Button>
      {toggleFavorite.error ? (
        <p role="alert" className="text-xs text-danger">
          {describeFavoriteError(toggleFavorite.error)}
        </p>
      ) : null}
    </div>
  )
}
