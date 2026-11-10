import { ReceiptText, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router'

import type { Refund } from '@/api/refunds'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { Pagination } from '@/components/common/pagination'
import { RefundCard } from '@/components/refund/refund-card'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { useOrderList } from '@/hooks/use-orders'
import { useRefundList } from '@/hooks/use-refunds'
import { cn } from '@/utils/cn'
import {
  REFUND_STATUS_FILTERS,
  filterRefundsByStatus,
  type RefundStatusFilterId,
} from '@/utils/refunds'

/**
 * 每页展示条数。
 *
 * 这是**本地分页**：后端 `GET /refunds` 既没有 page/size 参数也没有 total 字段，
 * 一次请求返回本人全部退款记录（按 id 倒序）。这里只控制渲染数量。
 */
const LOCAL_PAGE_SIZE = 10

export function RefundsPage() {
  useDocumentTitle('我的退款 | SurgeCart 商城')

  const [activeStatus, setActiveStatus] = useState<RefundStatusFilterId>('all')
  const [page, setPage] = useState(1)

  const refundsQuery = useRefundList()
  const ordersQuery = useOrderList()

  // 稳定引用：useMemo 依赖 data，避免每次渲染生成新数组。
  const refunds = useMemo<Refund[]>(() => refundsQuery.data?.items ?? [], [refundsQuery.data])

  /**
   * 订单 id → 订单号映射。
   *
   * 退款结构只有 order_id，没有订单号；为了让「关联订单编号」可读，
   * 用一次 `GET /orders` 的既有缓存做本地映射（不额外引入接口）。
   * 订单列表不可用时不影响退款列表展示，卡片退化为展示订单 id。
   */
  const orderNoById = useMemo(() => {
    const map = new Map<number, string>()

    for (const order of ordersQuery.data?.items ?? []) {
      map.set(order.id, order.order_no)
    }

    return map
  }, [ordersQuery.data])

  const statusCounts = useMemo(() => {
    const counts = new Map<RefundStatusFilterId, number>()

    for (const filter of REFUND_STATUS_FILTERS) {
      counts.set(filter.id, filterRefundsByStatus(refunds, filter.id).length)
    }

    return counts
  }, [refunds])

  const filtered = useMemo(
    () => filterRefundsByStatus(refunds, activeStatus),
    [refunds, activeStatus],
  )

  const totalPages = Math.max(1, Math.ceil(filtered.length / LOCAL_PAGE_SIZE))
  // 筛选后条目变少时收敛页码，避免停留在空页。
  const currentPage = Math.min(page, totalPages)
  const pageItems = filtered.slice(
    (currentPage - 1) * LOCAL_PAGE_SIZE,
    currentPage * LOCAL_PAGE_SIZE,
  )

  const handleSelectStatus = (status: RefundStatusFilterId) => {
    setActiveStatus(status)
    setPage(1)
  }

  const noRefundsAtAll = refunds.length === 0

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold text-foreground">我的退款</h2>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            退款记录来自服务端；状态筛选与分页在浏览器本地完成，不是服务端筛选/分页。
          </p>
        </div>

        <Button
          variant="outline"
          size="sm"
          onClick={() => void refundsQuery.refetch()}
          isLoading={refundsQuery.isFetching && !refundsQuery.isPending}
        >
          <RefreshCw aria-hidden="true" />
          刷新
        </Button>
      </div>

      <div
        role="group"
        aria-label="按退款状态筛选"
        className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1"
      >
        {REFUND_STATUS_FILTERS.map((filter) => {
          const isActive = filter.id === activeStatus
          const count = statusCounts.get(filter.id) ?? 0

          return (
            <button
              key={filter.id}
              type="button"
              onClick={() => handleSelectStatus(filter.id)}
              aria-pressed={isActive}
              className={cn(
                'inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-sm font-medium transition-colors duration-150',
                isActive
                  ? 'border-primary bg-primary-soft text-primary'
                  : 'border-border bg-surface text-muted-foreground hover:border-brand-300 hover:text-foreground',
              )}
            >
              {filter.label}
              <span className="tabular-nums text-xs opacity-70">{count}</span>
            </button>
          )
        })}
      </div>

      {refundsQuery.isPending ? <LoadingState label="正在加载退款记录" /> : null}

      {refundsQuery.isError ? (
        <ErrorState
          title="退款记录加载失败"
          description="退款记录暂时无法获取，请稍后重试。"
          onRetry={() => void refundsQuery.refetch()}
        />
      ) : null}

      {refundsQuery.isSuccess && noRefundsAtAll ? (
        <EmptyState
          icon={<ReceiptText className="size-6" aria-hidden="true" />}
          title="还没有退款记录"
          description="已支付且未发货的订单可以在订单详情页发起整单退款申请。"
          action={
            <Button asChild>
              <Link to="/account/orders">查看我的订单</Link>
            </Button>
          }
        />
      ) : null}

      {refundsQuery.isSuccess && !noRefundsAtAll && filtered.length === 0 ? (
        <EmptyState
          icon={<ReceiptText className="size-6" aria-hidden="true" />}
          title="该状态下暂无退款记录"
          description="切换到「全部」可以查看所有退款记录。"
          action={
            <Button variant="outline" onClick={() => handleSelectStatus('all')}>
              查看全部退款
            </Button>
          }
        />
      ) : null}

      {pageItems.length > 0 ? (
        <>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            共 {filtered.length} 条退款记录
            {filtered.length > LOCAL_PAGE_SIZE
              ? ` · 本地分页，第 ${currentPage}/${totalPages} 页`
              : ''}
          </p>

          <ul className="flex flex-col gap-4">
            {pageItems.map((refund) => (
              <li key={refund.id}>
                <RefundCard refund={refund} orderNo={orderNoById.get(refund.order_id)} />
              </li>
            ))}
          </ul>

          <Pagination page={currentPage} totalPages={totalPages} onPageChange={setPage} />
        </>
      ) : null}

      <p className="text-xs leading-5 text-muted-foreground">
        退款仅支持「已支付未发货」订单的整单退款。退款状态反映项目内部业务处理进度，
        不代表真实资金已经退回；项目使用模拟支付，没有第三方支付渠道与资金流水。
      </p>
    </div>
  )
}
