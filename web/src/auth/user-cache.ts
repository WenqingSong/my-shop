import type { QueryClient } from '@tanstack/react-query'

import { queryKeys } from '@/api/query-keys'

/**
 * 账号私有 Query Key 清单。
 *
 * 这些 key 都不含 user id，缓存一旦保留，下一位登录用户会在 refetch 完成前
 * 看到上一位用户的数据（订单、退款、优惠券、地址、收藏、购物车都属于此类）。
 *
 * **新增任何用户私有数据时，必须把它的 `all` key 加到这里。**
 */
const USER_SCOPED_KEYS = [
  queryKeys.auth.all,
  queryKeys.addresses.all,
  queryKeys.favorites.all,
  queryKeys.cart.all,
  queryKeys.orders.all,
  queryKeys.refunds.all,
  queryKeys.coupons.all,
] as const

/**
 * 清空全部账号私有缓存。
 *
 * 两个调用点必须共用这一份清单，否则会出现「主动登出清得干净、
 * 会话失效（refresh 失败）却残留」的不一致：
 * - `useLogout`：用户主动退出；
 * - `useAuthCacheReset`：有会话 → 无会话的转变（refresh 失败、服务端撤销会话）。
 */
export function clearUserScopedQueries(queryClient: QueryClient): void {
  for (const key of USER_SCOPED_KEYS) {
    queryClient.removeQueries({ queryKey: key })
  }
}
