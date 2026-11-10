import { useQuery } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { fetchOrder, fetchOrders } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import { useAuthStatus } from '@/auth/use-auth'

/**
 * 订单服务端状态（TanStack Query）。
 *
 * 数据来源唯一：`GET /orders` 与 `GET /orders/:id`，全部需要登录
 * （internal/cmd/routes_frontend.go 的 Auth 分组），未登录时查询不启用。
 *
 * 缓存策略：
 * - 列表 key 不含参数（后端没有筛选/分页参数），筛选与分页是纯本地派生；
 * - staleTime 取较短值：订单状态会被后端异步改变（超时扫描器每 60s 取消过期订单），
 *   过长的 staleTime 会让「待支付」长时间停留在过期状态；
 * - 401 已由 authGetData 做过一次 refresh + 重试，仍失败说明会话不可恢复，不再重试；
 * - 404 是业务结果（不存在/非本人），重试没有意义。
 */
export const ORDERS_STALE_TIME = 15_000

function shouldRetry(failureCount: number, error: unknown): boolean {
  // 已经拿到 HTTP 响应（4xx/5xx）说明请求到达了服务端：业务错误重试不会改变结果，
  // 还会让错误态延迟出现。只对网络中断/超时这类瞬时故障重试一次。
  if (error instanceof ApiError && error.kind === 'http') {
    return false
  }

  return failureCount < 1
}

/** 本人订单列表（后端返回全部订单，按 id 倒序）。 */
export function useOrderList() {
  const { hasSession } = useAuthStatus()

  return useQuery({
    queryKey: queryKeys.orders.list(),
    queryFn: ({ signal }) => fetchOrders(signal),
    enabled: hasSession,
    staleTime: ORDERS_STALE_TIME,
    retry: shouldRetry,
  })
}

/** 本人订单详情；不存在或非本人由后端返回 404/9001。 */
export function useOrderDetail(orderId: number) {
  const { hasSession } = useAuthStatus()
  const validId = Number.isSafeInteger(orderId) && orderId > 0

  return useQuery({
    queryKey: queryKeys.orders.detail(validId ? orderId : 0),
    queryFn: ({ signal }) => fetchOrder(orderId, signal),
    enabled: hasSession && validId,
    staleTime: ORDERS_STALE_TIME,
    retry: shouldRetry,
  })
}
