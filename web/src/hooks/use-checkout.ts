import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef } from 'react'

import { createOrder, type Order } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import { createIdempotencyKey } from '@/utils/idempotency'

/**
 * 下单提交。
 *
 * 幂等策略（与后端 internal/logic/order/order.go 对齐）：
 * - 后端唯一键 uk_user_idempotency(user_id, idempotency_key) + request_hash 比对；
 * - 同一份下单内容（地址 + 商品行）在「未成功」前复用同一个 key：
 *   网络超时/失败后重试，若服务端其实已创建成功，会命中幂等键返回既有订单，不会重复下单；
 * - 内容一旦变化（换地址、购物车增减/改数量），signature 变化 → 生成新 key，
 *   避免「同键不同内容」触发 9006。
 *
 * 本阶段不传 coupon_id：后端有券能力但前端没有「订单可用券 / 优惠试算」接口，
 * 不能在前端自行计算优惠金额。
 */
export interface SubmitOrderVariables {
  addressId: number
  /** 由地址 + 已选商品行生成的指纹，用于判断幂等键能否复用。 */
  signature: string
}

export function useCreateOrder() {
  const queryClient = useQueryClient()
  const keyRef = useRef<{ signature: string; key: string } | null>(null)

  return useMutation<Order, Error, SubmitOrderVariables>({
    mutationFn: ({ addressId, signature }) => {
      const cached = keyRef.current
      const key = cached && cached.signature === signature ? cached.key : createIdempotencyKey()
      keyRef.current = { signature, key }

      return createOrder({
        source: 'cart',
        address_id: addressId,
        idempotency_key: key,
      })
    },
    onSuccess: (order) => {
      // 成功页先用创建响应即时渲染，再以后端详情为最终数据源。
      queryClient.setQueryData(queryKeys.orders.detail(order.id), order)
      // 后端在创建事务内删除「已勾选的已购条目」，未勾选条目保留：
      // 失效整份购物车缓存，让 Header 徽标与购物车页面与服务端一致。
      // 不阻塞跳转：成功页立刻展示，购物车在后台刷新。
      void queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
    },
  })
}
