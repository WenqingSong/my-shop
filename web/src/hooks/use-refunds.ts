import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { ApiError } from '@/api/client'
import { queryKeys } from '@/api/query-keys'
import {
  applyRefund,
  fetchRefund,
  fetchRefunds,
  revokeRefund,
  type Refund,
} from '@/api/refunds'
import { useAuthStatus } from '@/auth/use-auth'

/**
 * 退款服务端状态（TanStack Query）。
 *
 * 数据来源唯一：`GET /refunds`、`GET /refunds/:id`，全部位于后端 Auth 分组
 * （`internal/cmd/routes_frontend.go:101-103`），未登录时不发起查询。
 *
 * 缓存策略：
 * - 列表 key 不带筛选参数（后端没有分页/状态筛选）；订单详情页使用 `list(orderId)`
 *   对应后端真实支持的 `?order_id=` 服务端过滤；
 * - 不启用乐观更新：退款状态由管理员审核/执行推进，前端猜测没有意义；
 * - 写接口返回的是完整 Refund，成功后写入详情缓存并失效列表；
 * - 失败时（HTTP 业务错误）本地缓存必然已过期，同时失效详情与列表。
 */
export const REFUNDS_STALE_TIME = 15_000

function shouldRetry(failureCount: number, error: unknown): boolean {
  // 已拿到 HTTP 响应说明请求到达了服务端：4xx 业务错误重试不会改变结果。
  if (error instanceof ApiError && error.kind === 'http') {
    return false
  }

  return failureCount < 1
}

/**
 * 本人退款记录列表。
 *
 * @param orderId 传入时由后端按订单过滤（真实服务端能力）；不传返回全部本人退款。
 */
export function useRefundList(orderId?: number) {
  const { hasSession } = useAuthStatus()
  const scopedOrderId = orderId && orderId > 0 ? orderId : undefined

  return useQuery({
    queryKey: queryKeys.refunds.list(scopedOrderId),
    queryFn: ({ signal }) => fetchRefunds(scopedOrderId, signal),
    enabled: hasSession,
    staleTime: REFUNDS_STALE_TIME,
    retry: shouldRetry,
  })
}

/** 本人退款详情；不存在或非本人由后端返回 404/18001。 */
export function useRefundDetail(refundId: number) {
  const { hasSession } = useAuthStatus()
  const validId = Number.isSafeInteger(refundId) && refundId > 0

  return useQuery({
    queryKey: queryKeys.refunds.detail(validId ? refundId : 0),
    queryFn: ({ signal }) => fetchRefund(refundId, signal),
    enabled: hasSession && validId,
    staleTime: REFUNDS_STALE_TIME,
    retry: shouldRetry,
  })
}

/**
 * 发起整单退款申请。
 *
 * 成功（服务端返回 status=applying 的完整 Refund）：
 * - 写入详情缓存，跳转详情页时无需再请求一次；
 * - 失效**所有**退款列表：全量列表与「按订单过滤」列表都会变化；
 * - 不失效订单缓存：申请阶段订单仍是 `paid`，后端不改订单状态；
 *   订单列表上的退款徽标来自退款列表，已一并失效。
 *
 * 失败：18003（订单状态已变化）/18004（已有有效申请）都意味着本地视图过期，
 * 因此同时失效该订单的退款列表、退款全量列表与订单缓存，让页面回到服务端真实状态。
 *
 * 并发保护：`mutation.isPending` 驱动提交按钮禁用，避免重复提交。
 */
export function useApplyRefund(orderId: number) {
  const queryClient = useQueryClient()

  return useMutation<Refund, Error, string>({
    mutationFn: (reason) => applyRefund(orderId, reason),
    retry: 0,
    onSuccess: (refund) => {
      queryClient.setQueryData(queryKeys.refunds.detail(refund.id), refund)
      void queryClient.invalidateQueries({ queryKey: queryKeys.refunds.lists() })
    },
    onError: async (error) => {
      if (!(error instanceof ApiError) || error.kind !== 'http') {
        return
      }

      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.refunds.lists() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.orders.detail(orderId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.orders.list() }),
      ])
    },
  })
}

/**
 * 撤销退款申请。
 *
 * 成功（服务端返回 status=revoked 的完整 Refund）：
 * - 写入详情缓存；
 * - 失效全部退款列表（订单详情页与订单列表的退款徽标都依赖它）。
 *
 * 失败：18002 表示状态已被管理员推进（例如已审核通过），
 * 因此失效详情与列表，让页面立刻回到服务端真实状态。
 */
export function useRevokeRefund() {
  const queryClient = useQueryClient()

  return useMutation<Refund, Error, number>({
    mutationFn: (refundId) => revokeRefund(refundId),
    retry: 0,
    onSuccess: (refund) => {
      queryClient.setQueryData(queryKeys.refunds.detail(refund.id), refund)
      void queryClient.invalidateQueries({ queryKey: queryKeys.refunds.lists() })
    },
    onError: async (error, refundId) => {
      if (!(error instanceof ApiError) || error.kind !== 'http') {
        return
      }

      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.refunds.detail(refundId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.refunds.lists() }),
      ])
    },
  })
}
