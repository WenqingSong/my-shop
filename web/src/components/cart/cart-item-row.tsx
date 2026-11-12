import { Trash2 } from 'lucide-react'
import { Link } from 'react-router'

import { CART_QUANTITY_MAX } from '@/api/cart'
import type { CartItem } from '@/api/types'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductImage } from '@/components/product/product-image'
import { QuantityStepper } from '@/components/cart/quantity-stepper'
import { Button } from '@/components/ui/button'
import { cartItemSubtotal, cartItemUnitPrice, describeUnavailableReason } from '@/utils/cart'
import { formatPrice } from '@/utils/price'

export interface CartItemRowProps {
  item: CartItem
  onQuantityChange: (quantity: number) => void
  onSelectedChange: (selected: boolean) => void
  onRemove: () => void
  isQuantityPending: boolean
  isRemoving: boolean
  selectionDisabled?: boolean
}

/**
 * 单条购物车商品。
 *
 * 可购买性、库存与价格变化全部来自后端字段，组件不自行推断：
 * - available=false → 勾选与数量禁用，展示 unavailable_reason 对应文案；
 * - insufficient=true → 提示库存不足（仍允许修改数量与删除）；
 * - price_changed=true → 提示加购快照价与当前价不同，小计始终按当前价展示。
 */
export function CartItemRow({
  item,
  onQuantityChange,
  onSelectedChange,
  onRemove,
  isQuantityPending,
  isRemoving,
  selectionDisabled = false,
}: CartItemRowProps) {
  const displayName = item.product_name.trim() || '已删除的商品'
  const unitPrice = cartItemUnitPrice(item)
  const subtotal = cartItemSubtotal(item)
  const selectable = item.available && !selectionDisabled

  return (
    <article
      aria-label={`购物车商品：${displayName}`}
      className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
    >
      <div className="flex gap-3 sm:gap-4">
        <input
          type="checkbox"
          checked={item.selected}
          disabled={!selectable}
          onChange={(event) => onSelectedChange(event.target.checked)}
          aria-label={`选择商品：${displayName}`}
          className="mt-1 size-4 shrink-0 accent-primary disabled:cursor-not-allowed disabled:opacity-50"
        />

        <ProductImage
          src={item.product_main_image}
          alt={displayName}
          className="size-20 shrink-0 rounded-lg border border-border sm:size-24"
        />

        <div className="min-w-0 flex-1">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
            <div className="min-w-0">
              {item.available && item.product_id !== null ? (
                <Link
                  to={`/products/${String(item.product_id)}`}
                  className="line-clamp-2 text-sm font-medium leading-6 text-foreground hover:text-primary"
                >
                  {displayName}
                </Link>
              ) : (
                <p className="line-clamp-2 text-sm font-medium leading-6 text-foreground">
                  {displayName}
                </p>
              )}

              <p className="mt-1 text-xs text-muted-foreground">
                规格：{item.sku_name.trim() || '—'}
              </p>

              {!item.available ? (
                <p className="mt-1 text-xs font-medium text-warning">
                  {describeUnavailableReason(item.unavailable_reason)}
                </p>
              ) : null}

              {item.insufficient ? (
                <p className="mt-1 text-xs font-medium text-warning">
                  库存不足，当前库存 {item.stock ?? 0} 件
                </p>
              ) : null}

              {item.price_changed ? (
                <p className="mt-1 text-xs text-muted-foreground">
                  价格已变动，加购价 {formatPrice(item.price_snapshot)}
                </p>
              ) : null}
            </div>

            <div className="shrink-0 sm:text-right">
              <p className="text-xs text-muted-foreground">单价</p>
              {unitPrice !== null ? (
                <PriceDisplay value={unitPrice} size="sm" />
              ) : (
                <p className="text-sm text-muted-foreground">价格不可用</p>
              )}
            </div>
          </div>

          <div className="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-3">
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">数量</span>
              <QuantityStepper
                value={item.quantity}
                max={CART_QUANTITY_MAX}
                disabled={!item.available || isRemoving}
                label={displayName}
                onChange={onQuantityChange}
              />
              {isQuantityPending ? (
                <span className="text-xs text-muted-foreground">保存中</span>
              ) : null}
            </div>

            <div className="text-right">
              <p className="text-xs text-muted-foreground">小计</p>
              {subtotal !== null ? (
                <PriceDisplay value={subtotal} size="md" />
              ) : (
                <p className="text-sm text-muted-foreground">—</p>
              )}
            </div>

            <Button
              variant="ghost"
              size="sm"
              onClick={onRemove}
              isLoading={isRemoving}
              aria-label={`删除商品（${displayName}）`}
              className="text-danger hover:bg-danger-soft hover:text-danger"
            >
              <Trash2 aria-hidden="true" />
              删除
            </Button>
          </div>
        </div>
      </div>
    </article>
  )
}
