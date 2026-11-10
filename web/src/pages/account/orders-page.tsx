import { Package, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router'

import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { Pagination } from '@/components/common/pagination'
import { OrderCard } from '@/components/order/order-card'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderList } from '@/hooks/use-orders'
import { cn } from '@/utils/cn'
import {
  ORDER_STATUS_GROUPS,
  filterOrdersByGroup,
  type OrderStatusGroupId,
} from '@/utils/orders'

/**
 * 每页展示条数。
 *
 * 这是**本地分页**：后端 `GET /orders` 既没有 page/size 参数也没有 total 字段，
 * 一次请求就把本人全部订单（含明细）返回。这里只控制渲染数量，
 * 不会减少网络请求，也不能被描述为"服务端分页"。
 */
const LOCAL_PAGE_SIZE = 10

export function OrdersPage() {
  useDocumentTitle('我的订单 | SurgeCart 商城')

  const [activeGroup, setActiveGroup] = useState<OrderStatusGroupId>('all')
  const [page, setPage] = useState(1)

  const ordersQuery = useOrderList()
  // 稳定引用：useMemo 的依赖是 orders，避免每次渲染都生成新数组导致重复计算。
  const orders = useMemo(() => ordersQuery.data?.items ?? [], [ordersQuery.data])

  // 每个分组在本地的订单数：仅用于筛选标签上的计数，不代表服务端统计。
  const groupCounts = useMemo(() => {
    const counts = new Map<OrderStatusGroupId, number>()

    for (const group of ORDER_STATUS_GROUPS) {
      counts.set(group.id, filterOrdersByGroup(orders, group.id).length)
    }

    return counts
  }, [orders])

  const filtered = useMemo(
    () => filterOrdersByGroup(orders, activeGroup),
    [orders, activeGroup],
  )

  const totalPages = Math.max(1, Math.ceil(filtered.length / LOCAL_PAGE_SIZE))
  // 过滤后条目变少时把页码收敛到有效范围，避免停留在空页。
  const currentPage = Math.min(page, totalPages)
  const pageItems = filtered.slice(
    (currentPage - 1) * LOCAL_PAGE_SIZE,
    currentPage * LOCAL_PAGE_SIZE,
  )

  const handleSelectGroup = (groupId: OrderStatusGroupId) => {
    setActiveGroup(groupId)
    setPage(1)
  }

  const noOrdersAtAll = orders.length === 0

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold text-foreground">我的订单</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            订单数据来自服务端；状态筛选与分页在浏览器本地完成，不是服务端筛选/分页。
          </p>
        </div>

        <Button
          variant="outline"
          size="sm"
          onClick={() => void ordersQuery.refetch()}
          isLoading={ordersQuery.isFetching && !ordersQuery.isPending}
        >
          <RefreshCw aria-hidden="true" />
          刷新
        </Button>
      </div>

      <div
        role="group"
        aria-label="按订单状态筛选"
        className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1"
      >
        {ORDER_STATUS_GROUPS.map((group) => {
          const isActive = group.id === activeGroup
          const count = groupCounts.get(group.id) ?? 0

          return (
            <button
              key={group.id}
              type="button"
              onClick={() => handleSelectGroup(group.id)}
              aria-pressed={isActive}
              className={cn(
                'inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-sm font-medium transition-colors duration-150',
                isActive
                  ? 'border-primary bg-primary-soft text-primary'
                  : 'border-border bg-surface text-muted-foreground hover:border-brand-300 hover:text-foreground',
              )}
            >
              {group.label}
              <span className="tabular-nums text-xs opacity-70">{count}</span>
            </button>
          )
        })}
      </div>

      {ordersQuery.isPending ? <LoadingState label="正在加载订单" /> : null}

      {ordersQuery.isError ? (
        <ErrorState
          title="订单加载失败"
          description="订单列表暂时无法获取，请稍后重试。"
          onRetry={() => void ordersQuery.refetch()}
        />
      ) : null}

      {ordersQuery.isSuccess && noOrdersAtAll ? (
        <EmptyState
          icon={<Package className="size-6" aria-hidden="true" />}
          title="还没有订单"
          description="下单后可以在这里查看订单状态、商品明细与收货信息。"
          action={
            <Button asChild>
              <Link to="/products">去逛逛</Link>
            </Button>
          }
        />
      ) : null}

      {ordersQuery.isSuccess && !noOrdersAtAll && filtered.length === 0 ? (
        <EmptyState
          icon={<Package className="size-6" aria-hidden="true" />}
          title="该状态下暂无订单"
          description="切换到「全部」可以查看所有订单。"
          action={
            <Button variant="outline" onClick={() => handleSelectGroup('all')}>
              查看全部订单
            </Button>
          }
        />
      ) : null}

      {pageItems.length > 0 ? (
        <>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            共 {filtered.length} 笔订单
            {filtered.length > LOCAL_PAGE_SIZE
              ? ` · 本地分页，第 ${currentPage}/${totalPages} 页`
              : ''}
          </p>

          <ul className="flex flex-col gap-4">
            {pageItems.map((order) => (
              <li key={order.id}>
                <OrderCard order={order} />
              </li>
            ))}
          </ul>

          <Pagination page={currentPage} totalPages={totalPages} onPageChange={setPage} />
        </>
      ) : null}
    </div>
  )
}
