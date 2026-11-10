import { useQuery } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { fetchMyCoupons } from '@/api/coupons'
import { queryKeys } from '@/api/query-keys'
import { useAuthStatus } from '@/auth/use-auth'

/**
 * 我的优惠券（TanStack Query）。
 *
 * 数据来源唯一：`GET /my/coupons`（Auth 分组），未登录时不请求。
 *
 * 缓存策略：
 * - 只有一个列表 key：后端没有分页/状态筛选参数，筛选是纯本地派生；
 * - staleTime 较短：券状态会被订单流程改变（下单 locked、支付 used、取消 available），
 *   过长会让「我的优惠券」与结算页长期显示旧状态；
 * - 写操作（下单/支付/取消）通过失效 `queryKeys.coupons.all` 同步，
 *   不做乐观更新——券状态最终由服务端迁移结果决定。
 */
export const COUPONS_STALE_TIME = 15_000

function shouldRetry(failureCount: number, error: unknown): boolean {
  // 已拿到 HTTP 响应（4xx/5xx）说明请求到达服务端，业务错误重试不会改变结果。
  if (error instanceof ApiError && error.kind === 'http') {
    return false
  }

  return failureCount < 1
}

export function useCouponList() {
  const { hasSession } = useAuthStatus()

  return useQuery({
    queryKey: queryKeys.coupons.list(),
    queryFn: ({ signal }) => fetchMyCoupons(signal),
    enabled: hasSession,
    staleTime: COUPONS_STALE_TIME,
    retry: shouldRetry,
  })
}
