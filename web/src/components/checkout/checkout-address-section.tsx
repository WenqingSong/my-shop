import { MapPin, Plus } from 'lucide-react'
import { Link } from 'react-router'

import type { Address } from '@/api/types'
import { Button } from '@/components/ui/button'
import { cn } from '@/utils/cn'

export interface CheckoutAddressSectionProps {
  addresses: Address[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  selectedId: number | null
  onSelect: (addressId: number) => void
}

/**
 * 结算页收货地址选择。
 *
 * 复用现有 GET /addresses，不重复实现地址管理：
 * - 默认地址优先选中（选择逻辑在上层，本组件只负责渲染与回调）；
 * - 「管理收货地址 / 新增收货地址」跳转既有 /account/addresses 页面。
 */
export function CheckoutAddressSection({
  addresses,
  isLoading,
  isError,
  onRetry,
  selectedId,
  onSelect,
}: CheckoutAddressSectionProps) {
  return (
    <section aria-labelledby="checkout-address-heading" className="rounded-xl border border-border bg-surface p-5 shadow-card">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="checkout-address-heading" className="text-base font-semibold text-foreground">
          收货地址
        </h2>
        <Link
          to="/account/addresses"
          className="text-sm font-medium text-primary hover:text-primary-hover"
        >
          管理收货地址
        </Link>
      </div>

      {isLoading ? (
        <p className="mt-4 text-sm text-muted-foreground" aria-live="polite">
          正在加载收货地址…
        </p>
      ) : null}

      {isError ? (
        <div className="mt-4 flex flex-wrap items-center gap-3 rounded-lg border border-danger/20 bg-danger-soft px-4 py-3">
          <p role="alert" className="text-sm text-danger">
            收货地址加载失败，暂时无法提交订单。
          </p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            重新加载
          </Button>
        </div>
      ) : null}

      {!isLoading && !isError && addresses.length === 0 ? (
        <div className="mt-4 flex flex-col items-start gap-3 rounded-lg border border-dashed border-border-strong bg-gray-25 px-4 py-6">
          <span className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
            <MapPin className="size-4 text-muted-foreground" aria-hidden="true" />
            还没有收货地址
          </span>
          <p className="text-xs leading-5 text-muted-foreground">
            下单需要一个收货地址，创建后返回本页继续结算。
          </p>
          <Button asChild size="sm">
            <Link to="/account/addresses">
              <Plus aria-hidden="true" />
              新增收货地址
            </Link>
          </Button>
        </div>
      ) : null}

      {addresses.length > 0 ? (
        <fieldset className="mt-4">
          <legend className="sr-only">选择收货地址</legend>
          <div className="grid gap-2 sm:grid-cols-2">
            {addresses.map((address) => {
              const checked = address.id === selectedId

              return (
                <label
                  key={address.id}
                  className={cn(
                    'flex cursor-pointer gap-3 rounded-lg border px-4 py-3 transition-colors duration-150',
                    'has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/40 has-[:focus-visible]:ring-offset-2',
                    checked
                      ? 'border-primary bg-primary-soft'
                      : 'border-border-strong bg-surface hover:border-brand-300',
                  )}
                >
                  <input
                    type="radio"
                    name="checkout-address"
                    value={address.id}
                    checked={checked}
                    onChange={() => onSelect(address.id)}
                    className="mt-1 size-4 shrink-0 accent-primary"
                    aria-label={`使用收货地址：${address.recipient_name}`}
                  />
                  <span className="min-w-0">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-foreground">
                        {address.recipient_name}
                      </span>
                      <span className="text-sm text-muted-foreground">{address.phone}</span>
                      {address.is_default ? (
                        <span className="rounded-full bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
                          默认
                        </span>
                      ) : null}
                    </span>
                    <span className="mt-1 block text-xs leading-5 text-muted-foreground">
                      {address.province}
                      {address.city}
                      {address.district}
                      {address.detail}
                    </span>
                  </span>
                </label>
              )
            })}
          </div>
        </fieldset>
      ) : null}
    </section>
  )
}
