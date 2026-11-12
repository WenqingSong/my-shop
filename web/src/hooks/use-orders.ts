import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { cancelOrder, fetchOrder, fetchOrders, payOrder, receiveOrder } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import type { Order } from '@/api/orders'
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

/** 订单可执行的操作（与后端状态闸门一一对应）。 */
export type OrderActionKind = 'pay' | 'cancel' | 'receive'

const ACTION_REQUEST: Record<OrderActionKind, (orderId: number) => Promise<Order>> = {
  pay: payOrder,
  cancel: cancelOrder,
  receive: receiveOrder,
}

/**
 * 订单操作（模拟支付 / 取消 / 确认收货）。
 *
 * 缓存策略（以服务端为准，不做乐观更新）：
 * - 成功：后端返回的是**更新后的完整订单**，直接写入详情缓存（避免再发一次 GET），
 *   并失效列表缓存，使列表状态、分组计数、个人中心入口在下次进入时与服务端一致；
 * - 失败：如果是 HTTP 业务错误（例如 9002 状态已迁移/已超时取消），
 *   说明本地缓存已经过期，必须同时失效详情与列表，让页面立刻回到真实状态；
 * - 不做乐观更新：交易状态不能靠前端猜测，失败回滚反而会闪回错误状态。
 *
 * 并发保护：`mutation.isPending` 同时驱动所有按钮的 disabled/loading，
 * 用户快速连点不会发出第二个请求（mutation 本身也不会并发执行同一实例）。
 */
export function useOrderAction(orderId: number) {
  const queryClient = useQueryClient()

  return useMutation<Order, Error, OrderActionKind>({
    mutationFn: (action) => ACTION_REQUEST[action](orderId),
    retry: 0,
    onSuccess: (order, action) => {
      queryClient.setQueryData(queryKeys.orders.detail(order.id), order)
      void queryClient.invalidateQueries({ queryKey: queryKeys.orders.list() })

      // 优惠券联动（W4-04）：
      // - 支付成功：后端在同一事务内把 locked 券核销为 used；
      // - 取消成功：后端在同一事务内把 locked 券释放为 available；
      // - 确认收货不影响券状态，无需额外请求。
      if (action === 'pay' || action === 'cancel') {
        void queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all })
      }
    },
    onError: async (error, action) => {
      if (!(error instanceof ApiError) || error.kind !== 'http') {
        return
      }

      const tasks = [
        queryClient.invalidateQueries({ queryKey: queryKeys.orders.detail(orderId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.orders.list() }),
      ]

      // 业务错误说明本地缓存已过期：支付/取消失败时券状态也可能已变化（例如已被其它请求占用/释放）。
      if (action === 'pay' || action === 'cancel') {
        tasks.push(queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all }))
      }

      await Promise.all(tasks)
    },
  })
}
