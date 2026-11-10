import { RefreshCw, Ticket } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router'

import type { UserCoupon } from '@/api/coupons'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { CouponCard } from '@/components/coupon/coupon-card'
import { Button } from '@/components/ui/button'
import { useCouponList } from '@/hooks/use-coupons'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { cn } from '@/utils/cn'
import {
  COUPON_STATUS_FILTERS,
  filterCouponsByStatus,
  type CouponStatusFilterId,
} from '@/utils/coupons'

/**
 * 我的优惠券（/account/coupons，RequireAuth 保护）。
 *
 * 数据来源：`GET /my/coupons`（仅本人，按 id 倒序，无分页）。
 *
 * 明确不做的事：
 * - 不提供领券中心：后端没有面向普通用户的公开模板列表接口，
 *   `/admin/coupons` 需要管理员权限，前端不得调用；
 * - 不新增 expired 状态：后端只有 available/locked/used；
 * - 不展示「适用范围」：后端券为平台通用券，没有商品/分类范围字段；
 * - 不计算优惠金额：只展示后端返回的 `discount_amount` 与门槛。
 */
export function CouponsPage() {
  useDocumentTitle('我的优惠券 | SurgeCart 商城')

  const [activeStatus, setActiveStatus] = useState<CouponStatusFilterId>('all')
  const couponsQuery = useCouponList()

  const coupons = useMemo<UserCoupon[]>(
    () => couponsQuery.data?.items ?? [],
    [couponsQuery.data],
  )

  const statusCounts = useMemo(() => {
    const counts = new Map<CouponStatusFilterId, number>()

    for (const filter of COUPON_STATUS_FILTERS) {
      counts.set(filter.id, filterCouponsByStatus(coupons, filter.id).length)
    }

    return counts
  }, [coupons])

  const filtered = useMemo(
    () => filterCouponsByStatus(coupons, activeStatus),
    [coupons, activeStatus],
  )

  const noCouponsAtAll = coupons.length === 0

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold text-foreground">我的优惠券</h2>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            优惠券数据来自服务端；状态筛选在浏览器本地完成，不是服务端筛选。
          </p>
        </div>

        <Button
          variant="outline"
          size="sm"
          onClick={() => void couponsQuery.refetch()}
          isLoading={couponsQuery.isFetching && !couponsQuery.isPending}
        >
          <RefreshCw aria-hidden="true" />
          刷新
        </Button>
      </div>

      <div
        role="group"
        aria-label="按优惠券状态筛选"
        className="-mx-1 flex gap-2 overflow-x-auto px-1 pb-1"
      >
        {COUPON_STATUS_FILTERS.map((filter) => {
          const isActive = filter.id === activeStatus
          const count = statusCounts.get(filter.id) ?? 0

          return (
            <button
              key={filter.id}
              type="button"
              onClick={() => setActiveStatus(filter.id)}
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

      {couponsQuery.isPending ? <LoadingState label="正在加载优惠券" /> : null}

      {couponsQuery.isError ? (
        <ErrorState
          title="优惠券加载失败"
          description="我的优惠券暂时无法获取，请稍后重试。"
          onRetry={() => void couponsQuery.refetch()}
        />
      ) : null}

      {couponsQuery.isSuccess && noCouponsAtAll ? (
        <EmptyState
          icon={<Ticket className="size-6" aria-hidden="true" />}
          title="还没有优惠券"
          description="本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口，无法在这里展示可领取的优惠券。"
          action={
            <Button asChild>
              <Link to="/products">去逛逛</Link>
            </Button>
          }
        />
      ) : null}

      {couponsQuery.isSuccess && !noCouponsAtAll && filtered.length === 0 ? (
        <EmptyState
          icon={<Ticket className="size-6" aria-hidden="true" />}
          title="该状态下暂无优惠券"
          description="切换到「全部」可以查看所有优惠券。"
          action={
            <Button variant="outline" onClick={() => setActiveStatus('all')}>
              查看全部优惠券
            </Button>
          }
        />
      ) : null}

      {filtered.length > 0 ? (
        <>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            共 {filtered.length} 张优惠券
          </p>

          <ul className="flex flex-col gap-4">
            {filtered.map((coupon) => (
              <li key={coupon.id}>
                <CouponCard coupon={coupon} />
              </li>
            ))}
          </ul>
        </>
      ) : null}

      <section
        aria-labelledby="coupon-notes-heading"
        className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
      >
        <h3 id="coupon-notes-heading" className="text-sm font-semibold text-foreground">
          使用说明
        </h3>
        <ul className="mt-2 flex list-disc flex-col gap-1.5 pl-5 text-xs leading-5 text-muted-foreground">
          <li>优惠券为平台通用固定金额满减券，后端没有商品或分类适用范围字段。</li>
          <li>状态只有「可使用 / 已锁定 / 已使用」三种，后端没有独立的「已过期」状态。</li>
          <li>下单成功但未支付时券为「已锁定」；支付后核销为「已使用」；取消订单后恢复「可使用」。</li>
          <li>退款成功不会返还优惠券，券保持「已使用」。</li>
          <li>是否满足使用条件与最终优惠金额，以结算页提交订单时服务端校验结果为准。</li>
        </ul>
      </section>
    </div>
  )
}
