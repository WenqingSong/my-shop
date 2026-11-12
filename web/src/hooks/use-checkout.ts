import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef } from 'react'

import { ApiError } from '@/api/client'
import { createOrder, type Order } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import { createIdempotencyKey } from '@/utils/idempotency'

/**
 * 下单提交。
 *
 * 幂等策略（与后端 `internal/logic/order/order.go` 对齐）：
 * - 后端唯一键 `uk_user_idempotency(user_id, idempotency_key)` + `request_hash` 比对；
 * - 同一份下单内容（来源 + 地址 + **优惠券** + 商品行）在「未成功」前复用同一个 key：
 *   网络超时/失败后重试，若服务端其实已创建成功，会命中幂等键返回既有订单，不会重复下单；
 * - 内容一旦变化（换地址、购物车增减/改数量、**换券或取消用券**），signature 变化 → 生成新 key，
 *   避免「同键不同内容」触发 9006。
 *
 * 优惠券金额不在前端计算：只把用户选择的 `user_coupon_id` 传给后端，
 * 由 `POST /orders` 事务内的 `Coupon().GetDiscount` 校验并计算优惠。
 */
export interface SubmitOrderVariables {
  addressId: number
  /** 0 表示不使用优惠券；>0 为 `user_coupons.id`。 */
  couponId: number
  /** 由地址 + 优惠券 + 已选商品行生成的指纹，用于判断幂等键能否复用。 */
  signature: string
}

export function useCreateOrder() {
  const queryClient = useQueryClient()
  const keyRef = useRef<{ signature: string; key: string } | null>(null)

  return useMutation<Order, Error, SubmitOrderVariables>({
    mutationFn: ({ addressId, couponId, signature }) => {
      const cached = keyRef.current
      const key = cached && cached.signature === signature ? cached.key : createIdempotencyKey()
      keyRef.current = { signature, key }

      return createOrder({
        source: 'cart',
        address_id: addressId,
        idempotency_key: key,
        // 0/缺省都表示不使用优惠券；显式只在选中券时传，避免无意义字段。
        ...(couponId > 0 ? { coupon_id: couponId } : {}),
      })
    },
    onSuccess: (order) => {
      // 成功页先用创建响应即时渲染，再以后端详情为最终数据源。
      queryClient.setQueryData(queryKeys.orders.detail(order.id), order)
      // 订单列表缓存必须失效：W4-01 的订单中心是另一个已缓存的视图，
      // 不失效会让「刚下的单」在列表里缺失，直到下一次窗口聚焦/重新进入。
      void queryClient.invalidateQueries({ queryKey: queryKeys.orders.list() })
      // 后端在创建事务内删除「已勾选的已购条目」，未勾选条目保留：
      // 失效整份购物车缓存，让 Header 徽标与购物车页面与服务端一致。
      // 不阻塞跳转：成功页立刻展示，购物车在后台刷新。
      void queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
      // 用券下单后券在服务端是 locked：失效优惠券缓存，让「我的优惠券」与结算页回到真实状态。
      void queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all })
    },
    onError: (error) => {
      // 19007 = 券不可用（已使用/被占用/不在使用期/未达门槛）；
      // 19003 = 券不存在或不属于当前用户。两者都说明本地券缓存已过期。
      if (
        error instanceof ApiError &&
        error.kind === 'http' &&
        (error.code === '19003' || error.code === '19007' || error.code === '9006')
      ) {
        void queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all })
      }
    },
  })
}
