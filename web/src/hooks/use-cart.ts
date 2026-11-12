import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useCallback, useRef, useState } from 'react'

import {
  addCartItem,
  CART_QUANTITY_MAX,
  CART_QUANTITY_MIN,
  deleteCartItem,
  fetchCart,
  updateCartItemQuantity,
  updateCartItemSelected,
  type CartListResult,
} from '@/api/cart'
import { ApiError } from '@/api/client'
import { queryKeys } from '@/api/query-keys'
import type { CartItem } from '@/api/types'
import { useAuthStatus } from '@/auth/use-auth'
import { describeCartError } from '@/pages/cart-errors'
import { summarizeCart } from '@/utils/cart'

/**
 * 购物车服务端状态（TanStack Query）。
 *
 * 数据权威性：
 * - 唯一的服务端数据源是 GET /cart；Header 徽标与购物车页面共用
 *   `queryKeys.cart.list()`，不会为了计数额外请求；
 * - 这里不做「本地购物车」：所有写操作都落到后端，缓存只做乐观展示与回滚。
 *
 * 认证关系：
 * - 查询只在存在会话时启用，未登录不发请求；
 * - 登出 / 会话失效由 useAuthCacheReset 与 useLogout 统一 removeQueries，
 *   账号切换不会看到上一位用户的购物车。
 */
export const CART_STALE_TIME = 30_000

/**
 * 操作成功提示带时间戳：页面同时挂载数量/勾选/删除三类操作，
 * 只展示「最近一次操作」的提示，避免旧提示盖住新提示。
 */
export interface CartFeedback {
  text: string
  at: number
}

export function useCartQuery() {
  const { hasSession } = useAuthStatus()

  return useQuery({
    queryKey: queryKeys.cart.list(),
    queryFn: ({ signal }) => fetchCart(signal),
    enabled: hasSession,
    staleTime: CART_STALE_TIME,
    // 401 已由 authGetData 做过一次 refresh + 重试，仍失败说明会话不可恢复。
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.isUnauthorized) && failureCount < 1,
  })
}

/**
 * Header 购物车件数：直接复用购物车列表缓存（同 key、同 staleTime）。
 * 未登录或数据未就绪时返回 0，避免展示上一位用户的数字。
 */
export function useCartCount(): number {
  const { hasSession } = useAuthStatus()
  const { data } = useCartQuery()

  if (!hasSession || !data) {
    return 0
  }

  return summarizeCart(data.items).totalQuantity
}

function readCartData(queryClient: QueryClient): CartListResult | undefined {
  return queryClient.getQueryData<CartListResult>(queryKeys.cart.list())
}

/**
 * 仅当缓存已存在时写入。
 *
 * 登出会 removeQueries；如果此时仍有在途响应，直接 setQueryData 会把上一账号的
 * 条目重新写回缓存，因此这里显式拒绝「无缓存时创建缓存」。
 */
function patchCartData(
  queryClient: QueryClient,
  updater: (items: CartItem[]) => CartItem[],
): void {
  const current = readCartData(queryClient)

  if (!current) {
    return
  }

  queryClient.setQueryData<CartListResult>(queryKeys.cart.list(), {
    items: updater(current.items),
  })
}

/** 合并服务端响应到指定条目；只合并本次操作拥有的字段，避免覆盖并发的其它操作。 */
function applyCartItemFields(
  queryClient: QueryClient,
  itemId: number,
  fields: Partial<CartItem>,
): void {
  patchCartData(queryClient, (items) =>
    items.map((item) => (item.id === itemId ? { ...item, ...fields } : item)),
  )
}

export interface AddToCartVariables {
  skuId: number
  quantity: number
}

/**
 * 加入购物车：成功后失效整份购物车缓存。
 * POST /cart/items 是「累加」语义，客户端无法可靠地在本地推算最终数量，
 * 因此不做本地合并，直接以服务端返回的列表为准（Header 徽标同步更新）。
 */
export function useAddToCart() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ skuId, quantity }: AddToCartVariables) => addCartItem(skuId, quantity),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
    },
  })
}

interface QuantityQueueState {
  /** 用户期望的最终数量（可能连续变化）。 */
  desired: number
  /** 是否已有请求在途；同一条目始终串行发送。 */
  running: boolean
}

/**
 * 修改商品数量：按条目串行 + 合并连续操作。
 *
 * 竞争问题：用户快速点「+」时，多个 PUT 若并发发出，后端可能乱序处理，
 * 先发出的旧响应晚到会覆盖较新的界面状态。
 *
 * 处理方式（不引入额外状态库）：
 * 1. 每个条目维护一个 desired（最新目标值），同一时间最多一个在途请求；
 * 2. 请求返回后若 desired 已变化，则丢弃这次响应（不写缓存），立刻用最新目标再发一次；
 * 3. 只有「响应值 === 当前目标值」时才把它合并进缓存并结束本轮。
 *
 * 结果：连续点 5 次通常只产生 2 次请求（首次 + 最终值），且最终状态一定是最新值。
 */
export function useUpdateCartQuantity() {
  const queryClient = useQueryClient()
  const queues = useRef(new Map<number, QuantityQueueState>())
  const [pendingItemIds, setPendingItemIds] = useState<readonly number[]>([])
  const [error, setError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<CartFeedback | null>(null)

  const updatePending = useCallback((itemId: number, pending: boolean) => {
    setPendingItemIds((current) => {
      if (pending) {
        return current.includes(itemId) ? current : [...current, itemId]
      }

      return current.filter((id) => id !== itemId)
    })
  }, [])

  const drain = useCallback(
    async (itemId: number) => {
      const state = queues.current.get(itemId)

      if (!state || state.running) {
        return
      }

      state.running = true
      updatePending(itemId, true)

      try {
        for (;;) {
          const target = state.desired
          const item = await updateCartItemQuantity(itemId, target)

          if (state.desired === target) {
            // 服务端权威值：只合并数量，保留并发的勾选状态等其它字段。
            applyCartItemFields(queryClient, itemId, { quantity: item.quantity })
            setFeedback({ text: '数量已更新', at: Date.now() })
            break
          }
          // 目标已更新：丢弃这次旧响应，继续发送最新目标。
        }
      } catch (cause) {
        setError(describeCartError(cause))
        // 失败时以服务端为准，回滚乐观数量并刷新库存/可购状态。
        await queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
      } finally {
        state.running = false
        queues.current.delete(itemId)
        updatePending(itemId, false)
      }
    },
    [queryClient, updatePending],
  )

  const setQuantity = useCallback(
    (itemId: number, quantity: number) => {
      const target = Math.min(
        Math.max(Math.trunc(quantity), CART_QUANTITY_MIN),
        CART_QUANTITY_MAX,
      )
      const current = readCartData(queryClient)?.items.find((item) => item.id === itemId)

      if (!current) {
        return
      }

      setError(null)
      setFeedback(null)
      // 乐观更新：输入框与合计立即反映目标值，服务端确认后再校正。
      applyCartItemFields(queryClient, itemId, { quantity: target })

      const state = queues.current.get(itemId)

      if (state) {
        state.desired = target
      } else {
        queues.current.set(itemId, { desired: target, running: false })
      }

      void drain(itemId)
    },
    [drain, queryClient],
  )

  const isPending = useCallback(
    (itemId: number) => pendingItemIds.includes(itemId),
    [pendingItemIds],
  )

  return { setQuantity, isPending, error, feedback }
}

export interface SetAllSelectedVariables {
  items: CartItem[]
  selected: boolean
}

/**
 * 勾选 / 取消勾选。
 *
 * 后端只有单条接口（PUT /cart/items/:id/selected），「全选」只能并发 N 次单条请求：
 * - 乐观更新全部可购买条目；
 * - allSettled 后若存在失败，刷新列表回到服务端真实状态并提示，不静默假装成功。
 */
export function useCartSelection() {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<CartFeedback | null>(null)

  const toggleMutation = useMutation({
    mutationFn: ({ itemId, selected }: { itemId: number; selected: boolean }) =>
      updateCartItemSelected(itemId, selected),
    onMutate: ({ itemId, selected }) => {
      setError(null)
      setFeedback(null)
      const previous = readCartData(queryClient)
      applyCartItemFields(queryClient, itemId, { selected })
      return { previous }
    },
    onError: (cause, _variables, context) => {
      setError(describeCartError(cause))

      if (context?.previous) {
        queryClient.setQueryData(queryKeys.cart.list(), context.previous)
      }
    },
    onSuccess: (item) => {
      applyCartItemFields(queryClient, item.id, { selected: item.selected })
    },
  })

  const setAllMutation = useMutation({
    mutationFn: async ({ items, selected }: SetAllSelectedVariables) => {
      const targets = items.filter((item) => item.available && item.selected !== selected)
      const results = await Promise.allSettled(
        targets.map((item) => updateCartItemSelected(item.id, selected)),
      )

      return { failed: results.filter((result) => result.status === 'rejected').length }
    },
    onMutate: ({ selected }) => {
      setError(null)
      setFeedback(null)
      const previous = readCartData(queryClient)
      patchCartData(queryClient, (items) =>
        items.map((item) => (item.available ? { ...item, selected } : item)),
      )
      return { previous }
    },
    onError: (cause, _variables, context) => {
      setError(describeCartError(cause))

      if (context?.previous) {
        queryClient.setQueryData(queryKeys.cart.list(), context.previous)
      }
    },
    onSuccess: async ({ failed }, { selected }) => {
      if (failed > 0) {
        setError(`有 ${String(failed)} 件商品勾选失败，已刷新为服务端状态`)
        await queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
        return
      }

      setFeedback({
        text: selected ? '已全选可购买商品' : '已取消全选',
        at: Date.now(),
      })
    },
  })

  return {
    setSelected: (itemId: number, selected: boolean) =>
      toggleMutation.mutate({ itemId, selected }),
    setAllSelected: (items: CartItem[], selected: boolean) =>
      setAllMutation.mutate({ items, selected }),
    isSelectionPending: toggleMutation.isPending || setAllMutation.isPending,
    error,
    feedback,
  }
}

/**
 * 删除条目：乐观移除 + 失败回滚。
 * 后端 DELETE 成功返回空对象，不需要二次拉取列表。
 */
export function useRemoveCartItem() {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<CartFeedback | null>(null)

  const mutation = useMutation({
    mutationFn: (itemId: number) => deleteCartItem(itemId),
    onMutate: (itemId) => {
      setError(null)
      setFeedback(null)
      const previous = readCartData(queryClient)
      patchCartData(queryClient, (items) => items.filter((item) => item.id !== itemId))
      return { previous }
    },
    onSuccess: () => {
      setFeedback({ text: '已将商品移出购物车', at: Date.now() })
    },
    onError: (cause, _itemId, context) => {
      setError(describeCartError(cause))

      if (context?.previous) {
        queryClient.setQueryData(queryKeys.cart.list(), context.previous)
      }
    },
  })

  return {
    remove: (itemId: number) => mutation.mutate(itemId),
    isPending: mutation.isPending,
    pendingItemId: mutation.isPending ? mutation.variables : undefined,
    error,
    feedback,
  }
}
