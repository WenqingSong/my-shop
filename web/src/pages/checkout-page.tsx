import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ShoppingCart } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { fetchAddresses } from '@/api/addresses'
import { ApiError } from '@/api/client'
import type { UserCoupon } from '@/api/coupons'
import { queryKeys } from '@/api/query-keys'
import type { CartItem } from '@/api/types'
import type { QuoteState } from '@/components/checkout/checkout-summary'
import { CheckoutAddressSection } from '@/components/checkout/checkout-address-section'
import { CheckoutCouponSection } from '@/components/checkout/checkout-coupon-section'
import { CheckoutItemList } from '@/components/checkout/checkout-item-list'
import { CheckoutSummary } from '@/components/checkout/checkout-summary'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import { useCreateOrder } from '@/hooks/use-checkout'
import { useCartQuery } from '@/hooks/use-cart'
import { useCouponList } from '@/hooks/use-coupons'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderQuote } from '@/hooks/use-order-quote'
import {
  describeOrderError,
  describeQuoteError,
  isCouponRejected,
  isIdempotencyConflict,
  isUnknownResultError,
} from '@/pages/checkout-errors'
import { summarizeCart } from '@/utils/cart'
import { evaluateCouponForCheckout } from '@/utils/coupons'
import { buildOrderSignature } from '@/utils/idempotency'

/**
 * 结算页（/checkout，RequireAuth 保护）。
 *
 * 金额链路（R2 起）：
 * 1. 未选地址 / 无有效勾选商品 → 不发起试算；
 * 2. 地址、优惠券、已勾选商品（含数量与当前单价）变化 → Query Key 变化 → 重新 `POST /orders/quote`；
 * 3. 试算进行中不展示上一份金额，失败时展示错误与重试入口，绝不退回客户端计算；
 * 4. 只有试算成功且不是刷新中才允许提交；
 * 5. 提交成功后最终金额使用 `POST /orders` 返回的订单快照（成功页展示）。
 *
 * 试算语义：只读预览，不检查库存、不锁定优惠券、不创建订单。
 * 下单来源固定 source=cart：服务端按本人**全部已勾选**购物车条目下单。
 */
export function CheckoutPage() {
  useDocumentTitle('确认订单 | SurgeCart 商城')

  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const cartQuery = useCartQuery()
  const createOrder = useCreateOrder()
  const couponsQuery = useCouponList()
  const [chosenAddressId, setChosenAddressId] = useState<number | null>(null)
  const [selectedCouponId, setSelectedCouponId] = useState<number | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const submittingRef = useRef(false)

  const addressesQuery = useQuery({
    queryKey: queryKeys.addresses.list(),
    queryFn: ({ signal }) => fetchAddresses(signal),
    staleTime: 30_000,
  })

  // 稳定引用：结算页同时依赖购物车、地址、优惠券三份服务端数据，
  // 这里把派生数组与金额汇总 memo 化，避免每次渲染都产生新对象
  // （也避免 React Compiler 因「依赖可能被修改」跳过优化）。
  const items = useMemo<CartItem[]>(() => cartQuery.data?.items ?? [], [cartQuery.data])
  const selectedItems = useMemo(() => items.filter((item) => item.selected), [items])
  const unselectedCount = items.length - selectedItems.length
  const summary = useMemo(() => summarizeCart(selectedItems), [selectedItems])
  const blockedItems = selectedItems.filter((item) => !item.available)
  const insufficientItems = selectedItems.filter((item) => item.available && item.insufficient)
  const blocked = blockedItems.length > 0

  const addresses = addressesQuery.data?.items ?? []
  // 默认地址优先；用户显式选择后以选择为准。
  const selectedAddress =
    addresses.find((address) => address.id === chosenAddressId) ??
    addresses.find((address) => address.is_default) ??
    addresses[0] ??
    null

  /**
   * 当前选择的优惠券（派生，不额外存一份副本）。
   *
   * 只有「用户选中的券仍然存在且按前端估算可选」时才真正生效：
   * 券列表刷新后如果该券被别人占用 / 已过期 / 不再满足门槛，
   * 这里会自动回落到「不使用优惠券」，避免提交一个必然失败的请求。
   * 前端的可选性判断只用于减少无效请求，权威判定仍在服务端试算与下单事务中。
   */
  const coupons = useMemo<readonly UserCoupon[]>(
    () => couponsQuery.data?.items ?? [],
    [couponsQuery.data],
  )
  const estimatedGoodsAmount = summary.estimatedTotal

  const selectedCoupon = useMemo(() => {
    if (selectedCouponId === null) {
      return null
    }

    const coupon = coupons.find((item) => item.id === selectedCouponId)

    if (!coupon) {
      return null
    }

    const evaluation = evaluateCouponForCheckout(coupon, estimatedGoodsAmount)

    return evaluation.selectable ? { coupon, evaluation } : null
  }, [coupons, selectedCouponId, estimatedGoodsAmount])

  const couponId = selectedCoupon?.coupon.id ?? 0

  // 不可购买条目没有有效试算语义：不请求、直接由页面提示。
  const quotableItems = blocked ? [] : selectedItems
  const quoteEnabled = selectedAddress !== null && selectedItems.length > 0 && !blocked

  const quoteQuery = useOrderQuote({
    addressId: selectedAddress?.id ?? null,
    couponId,
    items: quotableItems,
  })

  // 只接受「当前 key + 当前前置条件」下的成功结果：
  // - 新输入（地址/券/商品行）下 isSuccess 为 false，不会复用旧金额；
  // - 前置条件失效（如商品变为不可购买）时即使缓存仍有数据也不展示。
  const quote = quoteEnabled && quoteQuery.isSuccess ? quoteQuery.data : null
  const quoteState: QuoteState = quote
    ? 'success'
    : quoteEnabled && quoteQuery.isError
      ? 'error'
      : quoteEnabled
        ? 'pending'
        : 'idle'

  const quoteIdleReason = blocked
    ? '已勾选商品中存在不可购买商品，暂时无法试算金额。'
    : selectedItems.length === 0
      ? '没有勾选的商品，无法试算金额。'
      : selectedAddress === null
        ? '请先选择收货地址，试算需要地址信息计算运费。'
        : '等待试算条件满足。'

  const canSubmit =
    selectedItems.length > 0 &&
    !blocked &&
    selectedAddress !== null &&
    quote !== null &&
    !quoteQuery.isFetching &&
    !createOrder.isPending

  const handleRetryQuote = () => {
    if (quoteQuery.error instanceof ApiError && quoteQuery.error.code === '7001') {
      // 地址可能已被删除：同时刷新地址簿，避免用户只能在失效地址上反复重试。
      void queryClient.invalidateQueries({ queryKey: queryKeys.addresses.all })
    }

    void quoteQuery.refetch()
  }

  const handleClearCoupon = () => {
    setSelectedCouponId(null)
    // 券可能已过期/被占用：刷新券列表让选择区回到服务端真实状态。
    void queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all })
  }

  const handleSubmit = () => {
    if (!canSubmit || !selectedAddress || !quote || submittingRef.current) {
      return
    }

    // 同步锁：防止 isPending 尚未重渲染时的连续点击。
    submittingRef.current = true
    setSubmitError(null)

    // 优惠券 id 必须参与内容指纹：换券 / 取消用券都会生成新的幂等键，
    // 与后端 requestHash（source|address|coupon|sku:qty）保持一致。
    const signature = buildOrderSignature(
      selectedAddress.id,
      selectedItems.map((item) => ({ sku_id: item.sku_id, quantity: item.quantity })),
      couponId,
    )

    createOrder.mutate(
      { addressId: selectedAddress.id, couponId, signature },
      {
        onSuccess: (order) => {
          // 试算结果不再具有时效性：成功下单后移除全部试算缓存。
          queryClient.removeQueries({ queryKey: queryKeys.orders.quotes() })
          // replace：避免用户回退到结算页再次提交。
          navigate(`/checkout/success?order_id=${String(order.id)}`, { replace: true })
        },
        onError: (error) => {
          setSubmitError(describeOrderError(error))

          if (isIdempotencyConflict(error) || isUnknownResultError(error)) {
            // 9006：同键订单内容与当前不一致，刷新购物车回到真实状态。
            // 超时/网络异常：订单可能已创建（购物车条目已被服务端删除），
            // 刷新购物车能帮助用户判断，同时避免在旧缓存上重复提交。
            void queryClient.invalidateQueries({ queryKey: queryKeys.cart.all })
            void quoteQuery.refetch()
          }

          if (isCouponRejected(error)) {
            // 19007/19003：券在服务端已不可用。刷新券列表，回落到不使用优惠券，
            // 让用户可以立即取消选择后重新提交（新幂等键）。
            void queryClient.invalidateQueries({ queryKey: queryKeys.coupons.all })
            setSelectedCouponId(null)
          }
        },
        onSettled: () => {
          submittingRef.current = false
        },
      },
    )
  }

  return (
    <PageContainer className="py-8 lg:py-10">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            确认订单
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            核对收货地址与商品后提交，金额以服务端试算与下单返回为准。
          </p>
        </div>
        <Link to="/cart" className="text-sm font-medium text-primary hover:text-primary-hover">
          返回购物车
        </Link>
      </header>

      {submitError ? (
        <p
          role="alert"
          className="mt-5 rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm leading-6 text-danger"
        >
          {submitError}
        </p>
      ) : null}

      {cartQuery.isPending ? <LoadingState label="正在加载结算商品" /> : null}

      {cartQuery.isError ? (
        <div className="mt-6">
          <ErrorState
            title="结算信息加载失败"
            description="购物车暂时无法获取，请稍后重试。"
            onRetry={() => void cartQuery.refetch()}
          />
        </div>
      ) : null}

      {cartQuery.data && selectedItems.length === 0 ? (
        <div className="mt-6">
          <EmptyState
            icon={<ShoppingCart className="size-6" aria-hidden="true" />}
            title="没有勾选的商品"
            description="结算只包含购物车中已勾选的商品，请返回购物车勾选后再来结算。"
            action={
              <Button asChild>
                <Link to="/cart">去购物车勾选</Link>
              </Button>
            }
          />
        </div>
      ) : null}

      {selectedItems.length > 0 ? (
        <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-start">
          <div className="flex min-w-0 flex-col gap-5">
            <CheckoutAddressSection
              addresses={addresses}
              isLoading={addressesQuery.isPending}
              isError={addressesQuery.isError}
              onRetry={() => void addressesQuery.refetch()}
              selectedId={selectedAddress?.id ?? null}
              onSelect={setChosenAddressId}
            />

            {blocked ? (
              <p
                role="alert"
                className="rounded-lg border border-warning/30 bg-warning-soft px-4 py-3 text-sm leading-6 text-warning"
              >
                以下商品当前不可购买，无法提交订单：
                {blockedItems.map((item) => item.product_name.trim() || '已删除的商品').join('、')}
                。请返回购物车取消勾选或删除后重试。
              </p>
            ) : null}

            {insufficientItems.length > 0 ? (
              <p className="rounded-lg border border-warning/30 bg-warning-soft px-4 py-3 text-sm leading-6 text-warning">
                部分商品库存不足，提交时将由服务端校验；若库存已耗尽会拒绝下单且不会扣减其他商品库存。
              </p>
            ) : null}

            <CheckoutItemList
              items={selectedItems}
              quoteItems={quote?.items}
              quoteState={quoteState}
            />

            {unselectedCount > 0 ? (
              <p className="text-xs leading-5 text-muted-foreground">
                购物车中还有 {unselectedCount} 种未勾选商品，不会参与本次下单。
              </p>
            ) : null}

            <CheckoutCouponSection
              coupons={coupons}
              isLoading={couponsQuery.isPending}
              isError={couponsQuery.isError}
              onRetry={() => void couponsQuery.refetch()}
              selectedCouponId={selectedCoupon?.coupon.id ?? null}
              onSelect={setSelectedCouponId}
              estimatedGoodsAmount={quote?.original_amount ?? estimatedGoodsAmount}
            />
          </div>

          <CheckoutSummary
            quantity={summary.selectedQuantity}
            quote={quote}
            quoteState={quoteState}
            quoteErrorMessage={
              quoteQuery.error && quoteState === 'error'
                ? describeQuoteError(quoteQuery.error)
                : undefined
            }
            quoteIdleReason={quoteIdleReason}
            onRetryQuote={handleRetryQuote}
            couponSelected={selectedCoupon !== null}
            onClearCoupon={handleClearCoupon}
            blocked={blocked}
            disabled={selectedAddress === null}
            isSubmitting={createOrder.isPending}
            canSubmit={canSubmit}
            onSubmit={handleSubmit}
          />
        </div>
      ) : null}
    </PageContainer>
  )
}
