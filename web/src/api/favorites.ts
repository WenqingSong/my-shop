import { authDeleteData, authGetData, authPostData } from '@/api/auth-request'
import type { FavoriteItem } from '@/api/types'

/**
 * 商品收藏 API（均需登录；这是收藏，不是点赞 /likes）。
 *
 * 事实来源：
 * - api/favorite/v1/favorite.go（FavoriteItem / AddReq / ListReq / CheckReq）
 * - internal/cmd/routes_frontend.go（POST/GET /favorites、DELETE /favorites/:product_id、GET /favorites/check）
 * - internal/logic/favorite/favorite.go（实时联查商品、11001 不可收藏、取消幂等）
 */

export const FAVORITE_PAGE_SIZE = 10

export interface FavoriteListResult {
  items: FavoriteItem[]
  total: number
  page: number
  size: number
}

/** GET /favorites：按收藏时间倒序分页。 */
export function fetchFavorites(
  page: number,
  size: number,
  signal?: AbortSignal,
): Promise<FavoriteListResult> {
  return authGetData<FavoriteListResult>('/favorites', {
    query: { page, size },
    signal,
  })
}

/** GET /favorites/check?product_id=：商品详情展示收藏态。 */
export function fetchFavoriteStatus(
  productId: number,
  signal?: AbortSignal,
): Promise<{ favorited: boolean }> {
  return authGetData<{ favorited: boolean }>('/favorites/check', {
    query: { product_id: productId },
    signal,
  })
}

/** POST /favorites：重复收藏幂等；商品不在售返回 409/11001。 */
export function addFavorite(productId: number): Promise<FavoriteItem> {
  return authPostData<FavoriteItem>('/favorites', { product_id: productId })
}

/** DELETE /favorites/:product_id：未收藏也幂等成功。 */
export function removeFavorite(productId: number): Promise<null> {
  return authDeleteData<null>(`/favorites/${String(productId)}`)
}
