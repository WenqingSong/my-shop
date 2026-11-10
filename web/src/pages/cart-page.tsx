import { ShoppingCart } from 'lucide-react'
import { Link } from 'react-router'

import { CartItemRow } from '@/components/cart/cart-item-row'
import { CartSummaryPanel } from '@/components/cart/cart-summary'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import {
  useCartQuery,
  useCartSelection,
  useRemoveCartItem,
  useUpdateCartQuantity,
  type CartFeedback,
} from '@/hooks/use-cart'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { summarizeCart } from '@/utils/cart'

/**
 * 购物车页面（/cart，路由层 RequireAuth 保护）。
 *
 * 数据流：
 * - GET /cart 是唯一数据源，Header 徽标共用同一份缓存；
 * - 勾选、数量、删除均为服务端持久化操作，失败会回滚并给出提示；
 * - 金额只做展示预估，不参与订单金额计算（W3-02 以后端试算为准）。
 */
export function CartPage() {
  useDocumentTitle('购物车 | SurgeCart 商城')

  const cartQuery = useCartQuery()
  const quantity = useUpdateCartQuantity()
  const selection = useCartSelection()
  const removal = useRemoveCartItem()

  const items = cartQuery.data?.items ?? []
  const summary = summarizeCart(items)
  const someSelected = summary.selectedCount > 0 && !summary.allPurchasableSelected
  const actionError = quantity.error ?? selection.error ?? removal.error
  // 三类操作各自产生提示，只展示最近一次操作的结果，避免旧提示盖住新提示。
  const actionFeedback = [removal.feedback, selection.feedback, quantity.feedback].reduce<
    CartFeedback | null
  >((latest, entry) => (!entry ? latest : !latest || entry.at >= latest.at ? entry : latest), null)

  return (
    <PageContainer className="py-8 lg:py-10">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            购物车
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            商品价格、库存与可购买状态均来自商城后端，结算时以订单试算为准。
          </p>
        </div>
        <Link
          to="/products"
          className="text-sm font-medium text-primary hover:text-primary-hover"
        >
          继续购物
        </Link>
      </header>

      {actionError ? (
        <p
          role="alert"
          className="mt-5 rounded-lg border border-danger/20 bg-danger-soft px-4 py-3 text-sm text-danger"
        >
          {actionError}
        </p>
      ) : null}

      {!actionError && actionFeedback ? (
        <p
          role="status"
          className="mt-5 rounded-lg border border-success/20 bg-success-soft px-4 py-3 text-sm text-success"
        >
          {actionFeedback.text}
        </p>
      ) : null}

      {cartQuery.isPending ? <LoadingState label="正在加载购物车" /> : null}

      {cartQuery.isError ? (
        <div className="mt-6">
          <ErrorState
            title="购物车加载失败"
            description="购物车暂时无法获取，请稍后重试。"
            onRetry={() => void cartQuery.refetch()}
          />
        </div>
      ) : null}

      {cartQuery.data && items.length === 0 ? (
        <div className="mt-6">
          <EmptyState
            icon={<ShoppingCart className="size-6" aria-hidden="true" />}
            title="购物车还是空的"
            description="从商品详情页选择规格并加入购物车，商品会出现在这里。"
            action={
              <Button asChild>
                <Link to="/products">去挑选商品</Link>
              </Button>
            }
          />
        </div>
      ) : null}

      {items.length > 0 ? (
        <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px] lg:items-start">
          <section aria-label="购物车商品列表" className="min-w-0">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-surface px-4 py-3 shadow-card">
              <label className="flex items-center gap-2 text-sm font-medium text-foreground">
                <input
                  type="checkbox"
                  checked={summary.allPurchasableSelected}
                  onChange={(event) => selection.setAllSelected(items, event.target.checked)}
                  ref={(element) => {
                    if (element) {
                      element.indeterminate = someSelected
                    }
                  }}
                  aria-label="全选商品"
                  className="size-4 accent-primary disabled:cursor-not-allowed disabled:opacity-50"
                />
                全选
              </label>
              <p className="text-sm text-muted-foreground" aria-live="polite">
                共 {summary.itemCount} 种商品，{summary.totalQuantity} 件；已选{' '}
                {summary.selectedQuantity} 件
              </p>
            </div>

            <ul className="mt-4 flex flex-col gap-4">
              {items.map((item) => (
                <li key={item.id}>
                  <CartItemRow
                    item={item}
                    onQuantityChange={(nextQuantity) => quantity.setQuantity(item.id, nextQuantity)}
                    onSelectedChange={(selected) => selection.setSelected(item.id, selected)}
                    onRemove={() => removal.remove(item.id)}
                    isQuantityPending={quantity.isPending(item.id)}
                    isRemoving={removal.pendingItemId === item.id}
                    selectionDisabled={selection.isSelectionPending}
                  />
                </li>
              ))}
            </ul>
          </section>

          <CartSummaryPanel summary={summary} />
        </div>
      ) : null}
    </PageContainer>
  )
}
