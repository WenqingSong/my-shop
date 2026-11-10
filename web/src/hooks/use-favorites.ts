import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { addFavorite, fetchFavoriteStatus, removeFavorite } from '@/api/favorites'
import { queryKeys } from '@/api/query-keys'
import { useAuthStatus } from '@/auth/use-auth'

/** 商品详情收藏态：仅登录后请求，避免匿名用户产生 401。 */
export function useFavoriteStatus(productId: number) {
  const { hasSession } = useAuthStatus()

  return useQuery({
    queryKey: queryKeys.favorites.check(productId),
    queryFn: ({ signal }) => fetchFavoriteStatus(productId, signal),
    enabled: hasSession && Number.isSafeInteger(productId) && productId > 0,
    staleTime: 30_000,
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.isUnauthorized) && failureCount < 1,
  })
}

export interface ToggleFavoriteVariables {
  productId: number
  /** true 表示本次操作要收藏，false 表示取消收藏。 */
  favorite: boolean
}

/**
 * 收藏/取消收藏：
 * - 先更新收藏态缓存（乐观更新），再发请求；
 * - 失败回滚；
 * - 成功后使收藏列表失效，保证账户页与详情页状态一致。
 */
export function useToggleFavorite() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ productId, favorite }: ToggleFavoriteVariables) =>
      favorite ? addFavorite(productId) : removeFavorite(productId),
    onMutate: async ({ productId, favorite }) => {
      const key = queryKeys.favorites.check(productId)
      await queryClient.cancelQueries({ queryKey: key })
      const previous = queryClient.getQueryData<{ favorited: boolean }>(key)
      queryClient.setQueryData(key, { favorited: favorite })
      return { key, previous }
    },
    onError: (_error, _variables, context) => {
      if (!context) {
        return
      }

      if (context.previous) {
        queryClient.setQueryData(context.key, context.previous)
      } else {
        queryClient.removeQueries({ queryKey: context.key })
      }
    },
    onSettled: async (_data, _error, variables) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.favorites.check(variables.productId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.favorites.lists() }),
      ])
    },
  })
}
