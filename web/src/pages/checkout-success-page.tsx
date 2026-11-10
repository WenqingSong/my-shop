import { useQuery } from '@tanstack/react-query'
import { CheckCircle2 } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'

import { ApiError } from '@/api/client'
import { fetchOrder } from '@/api/orders'
import { queryKeys } from '@/api/query-keys'
import { PageContainer } from '@/components/common/page-container'
import { PagePlaceholder } from '@/components/common/page-placeholder'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import {
  describeOrderStatus,
  formatOrderAddress,
  formatOrderTime,
  orderTotalQuantity,
} from '@/utils/orders'

/**
 * 下单成功页（/checkout/success?order_id=...，RequireAuth 保护）。
 *
 * 数据来源：
 * - 创建订单成功后 hook 已把 POST /orders 的响应写入 orders.detail 缓存，页面可即时渲染；
 * - 同时仍会以 GET /orders/:id 重新拉取，确保展示的是服务端最终数据；
 * - 金额、状态、订单号全部直接展示后端字段，前端不做任何金额推导。
 *
 * 不包含支付入口与订单管理：支付/退款/订单列表属于后续阶段。
 */
export function CheckoutSuccessPage() {
  useDocumentTitle('订单提交成功 | SurgeCart 商城')

  const [searchParams] = useSearchParams()
  const rawOrderId = searchParams.get('order_id') ?? ''
  const orderId = Number(rawOrderId)
  const isValidOrderId = Number.isSafeInteger(orderId) && orderId > 0

  const orderQuery = useQuery({
    queryKey: queryKeys.orders.detail(isValidOrderId ? orderId : 0),
    queryFn: ({ signal }) => fetchOrder(orderId, signal),
    enabled: isValidOrderId,
    staleTime: 0,
    retry: (failureCount, error) => {
      if (error instanceof ApiError && (error.isNotFound || error.isUnauthorized)) {
        return false
      }

      return failureCount < 1
    },
  })

  const missing = (
    <PagePlaceholder
      eyebrow="订单"
      title="订单信息缺失"
      description="没有找到可展示的订单编号。可以返回商城继续浏览，或从购物车重新结算。"
      actions={
        <Button asChild>
          <Link to="/products">返回商城</Link>
        </Button>
      }
    />
  )

  if (!isValidOrderId) {
    return (
      <PageContainer className="py-16">
        <div className="flex min-h-[40vh] items-center justify-center">{missing}</div>
      </PageContainer>
    )
  }

  const order = orderQuery.data

  return (
    <PageContainer className="py-8 lg:py-10">
      {orderQuery.isPending ? <LoadingState label="正在加载订单结果" /> : null}

      {orderQuery.isError ? (
        orderQuery.error instanceof ApiError && orderQuery.error.isNotFound ? (
          <div className="flex min-h-[40vh] items-center justify-center">
            <PagePlaceholder
              eyebrow="404"
              title="订单不存在或无权查看"
              description="该订单可能不属于当前账号，或链接已失效。"
              actions={
                <Button asChild>
                  <Link to="/products">返回商城</Link>
                </Button>
              }
            />
          </div>
        ) : (
          <ErrorState
            title="订单结果加载失败"
            description="订单已经提交，但详情暂时无法获取，请稍后重试。"
            onRetry={() => void orderQuery.refetch()}
          />
        )
      ) : null}

      {order ? (
        <div className="mx-auto max-w-3xl">
          <div className="flex items-start gap-3">
            <span className="grid size-11 shrink-0 place-items-center rounded-full bg-success-soft text-success">
              <CheckCircle2 className="size-6" aria-hidden="true" />
            </span>
            <div>
              <h1 className="text-2xl font-semibold tracking-tight text-foreground">
                订单提交成功
              </h1>
              <p className="mt-1 text-sm text-muted-foreground">
                订单已创建，库存已由服务端扣减；应付金额与订单状态以下方服务端数据为准。
              </p>
            </div>
          </div>

          <section
            aria-labelledby="order-summary-heading"
            className="mt-6 rounded-xl border border-border bg-surface p-5 shadow-card"
          >
            <h2 id="order-summary-heading" className="text-base font-semibold text-foreground">
              订单信息
            </h2>
            <dl className="mt-4 grid gap-3 text-sm sm:grid-cols-2">
              <div className="flex items-center justify-between gap-3 sm:justify-start">
                <dt className="w-20 shrink-0 text-muted-foreground">订单编号</dt>
                <dd className="break-all font-medium tabular-nums text-foreground">
                  {order.order_no}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-3 sm:justify-start">
                <dt className="w-20 shrink-0 text-muted-foreground">订单状态</dt>
                <dd>
                  <span className="rounded-full bg-warning-soft px-2 py-0.5 text-xs font-medium text-warning">
                    {describeOrderStatus(order.status)}
                  </span>
                </dd>
              </div>
              <div className="flex items-center justify-between gap-3 sm:justify-start">
                <dt className="w-20 shrink-0 text-muted-foreground">商品件数</dt>
                <dd className="tabular-nums text-foreground">{orderTotalQuantity(order)} 件</dd>
              </div>
              <div className="flex items-center justify-between gap-3 sm:justify-start">
                <dt className="w-20 shrink-0 text-muted-foreground">下单时间</dt>
                <dd className="tabular-nums text-foreground">{formatOrderTime(order.created_at)}</dd>
              </div>
            </dl>

            <div className="mt-4 flex items-baseline justify-between gap-3 border-t border-border pt-4">
              <span className="text-sm font-medium text-foreground">应付金额</span>
              <PriceDisplay value={order.total_amount} size="lg" />
            </div>
            <p className="mt-1 text-right text-xs text-muted-foreground">
              由服务端订单事务计算并返回
            </p>
          </section>

          <section
            aria-labelledby="order-address-heading"
            className="mt-5 rounded-xl border border-border bg-surface p-5 shadow-card"
          >
            <h2 id="order-address-heading" className="text-base font-semibold text-foreground">
              收货信息
            </h2>
            <p className="mt-3 text-sm font-medium text-foreground">
              {order.recipient_name}
              <span className="ml-2 font-normal text-muted-foreground">{order.phone}</span>
            </p>
            <p className="mt-1 text-sm leading-6 text-muted-foreground">
              {formatOrderAddress(order)}
            </p>
          </section>

          <section
            aria-labelledby="order-items-heading"
            className="mt-5 rounded-xl border border-border bg-surface p-5 shadow-card"
          >
            <h2 id="order-items-heading" className="text-base font-semibold text-foreground">
              商品清单
            </h2>
            <ul className="mt-4 divide-y divide-border">
              {order.items.map((item) => (
                <li key={item.id} className="flex gap-4 py-3 first:pt-0 last:pb-0">
                  <ProductImage
                    src={item.product_main_image}
                    alt={item.product_name}
                    className="size-14 shrink-0 rounded-lg border border-border"
                  />
                  <div className="min-w-0 flex-1">
                    <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">
                      {item.product_name}
                    </p>
                    <p className="text-xs text-muted-foreground">
                      规格：{item.sku_name} · 数量 {item.quantity}
                    </p>
                  </div>
                  <div className="shrink-0 text-right">
                    <p className="text-xs text-muted-foreground">成交单价</p>
                    <PriceDisplay value={item.price} size="sm" tone="neutral" />
                  </div>
                </li>
              ))}
            </ul>
          </section>

          <p className="mt-4 text-xs leading-5 text-muted-foreground">
            支付与订单管理将在后续阶段开放；本页仅展示本次创建结果。
          </p>

          <div className="mt-6 flex flex-wrap gap-3">
            <Button asChild size="lg">
              <Link to="/products">继续购物</Link>
            </Button>
            <Button asChild variant="outline" size="lg">
              <Link to="/cart">查看购物车</Link>
            </Button>
          </div>
        </div>
      ) : null}
    </PageContainer>
  )
}
