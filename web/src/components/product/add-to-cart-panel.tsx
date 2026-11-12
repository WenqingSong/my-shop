import { CheckCircle2, ShoppingCart } from 'lucide-react'
import { useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'

import { CART_QUANTITY_MAX } from '@/api/cart'
import type { ProductDetail, Sku } from '@/api/types'
import { useAuthStatus } from '@/auth/use-auth'
import { QuantityStepper } from '@/components/cart/quantity-stepper'
import { Button } from '@/components/ui/button'
import { useAddToCart } from '@/hooks/use-cart'
import { describeCartError } from '@/pages/cart-errors'

export interface AddToCartPanelProps {
  product: ProductDetail
  selectedSku: Sku | null
  /** 当前规格库存；null 表示后端没有提供该 SKU 的库存数据（未知，不是 0）。 */
  stock: number | null
}

/**
 * 商品详情「加入购物车」区域。
 *
 * 事实来源：
 * - POST /cart/items { sku_id, quantity }（api/cart/v1/cart.go AddReq）；
 * - 后端强制 sku_id 必填（缺失/≤0 → 400/1001），因此前端先做「必须选择规格」校验，
 *   不发送只有商品 id 的请求；
 * - 商品没有可用 SKU（product.skus 为空）时不提供加购操作；
 * - 库存来自商品详情 productValue（按 sku_id），只有明确为 0 时才禁止加购；
 *   库存未知时不虚构数量，也不假装无货，仅提示以下单校验为准。
 *
 * 未登录：按钮直接引导登录并携带当前商品详情地址，登录后回到本页。
 */
export function AddToCartPanel({ product, selectedSku, stock }: AddToCartPanelProps) {
  const { status, refetch } = useAuthStatus()
  const location = useLocation()
  const navigate = useNavigate()
  const addToCart = useAddToCart()
  const [quantity, setQuantity] = useState(1)
  const [skuError, setSkuError] = useState<string | null>(null)
  // 最近一次成功加购的 SKU：切换规格后旧成功提示不再展示。
  const [lastAddedSkuId, setLastAddedSkuId] = useState<number | null>(null)

  const hasSkus = product.skus.length > 0
  const currentSkuId = selectedSku?.id ?? null
  const addedQuantity = addToCart.data?.quantity ?? null
  const outOfStock = stock !== null && stock <= 0
  const maxQuantity =
    stock !== null && stock > 0 ? Math.min(stock, CART_QUANTITY_MAX) : CART_QUANTITY_MAX

  // 切换规格时重置数量与校验提示（渲染期调整 state，避免额外 effect）。
  const [syncedSkuId, setSyncedSkuId] = useState<number | null>(currentSkuId)

  if (currentSkuId !== syncedSkuId) {
    setSyncedSkuId(currentSkuId)
    setQuantity(1)
    setSkuError(null)
  }

  const handleAdd = () => {
    if (!selectedSku) {
      setSkuError('请先选择商品规格')
      return
    }

    if (outOfStock) {
      return
    }

    const sku = selectedSku
    setSkuError(null)
    addToCart.mutate(
      { skuId: sku.id, quantity },
      {
        onSuccess: () => {
          setLastAddedSkuId(sku.id)
          setQuantity(1)
        },
      },
    )
  }

  if (status === 'unauthenticated') {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <Button
          size="lg"
          onClick={() =>
            navigate('/login', {
              state: { from: `${location.pathname}${location.search}` },
            })
          }
        >
          <ShoppingCart aria-hidden="true" />
          登录后加入购物车
        </Button>
        <p className="text-xs leading-5 text-muted-foreground">登录后购物车会保存到你的账户。</p>
      </div>
    )
  }

  if (status === 'error') {
    return (
      <Button variant="outline" size="lg" onClick={() => void refetch()}>
        <ShoppingCart aria-hidden="true" />
        账户异常，点击重试
      </Button>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">数量</span>
          <QuantityStepper
            value={quantity}
            max={maxQuantity}
            disabled={!hasSkus || outOfStock || status === 'loading' || addToCart.isPending}
            label={product.name}
            onChange={setQuantity}
          />
        </div>

        <Button
          size="lg"
          onClick={handleAdd}
          isLoading={addToCart.isPending}
          disabled={!hasSkus || outOfStock || status === 'loading'}
          aria-describedby={
            !hasSkus ? 'cart-sku-unavailable' : outOfStock ? 'cart-stock-unavailable' : undefined
          }
        >
          <ShoppingCart aria-hidden="true" />
          加入购物车
        </Button>
      </div>

      {!hasSkus ? (
        <p id="cart-sku-unavailable" className="text-xs leading-5 text-warning">
          该商品暂未配置可售规格，无法加入购物车。
        </p>
      ) : null}

      {outOfStock ? (
        <p id="cart-stock-unavailable" className="text-xs leading-5 text-warning">
          当前规格暂无库存，暂时无法加入购物车。
        </p>
      ) : null}

      {hasSkus && selectedSku && stock === null ? (
        <p className="text-xs leading-5 text-muted-foreground">
          该规格暂时没有可用的库存数据，是否可购买以服务端下单校验为准。
        </p>
      ) : null}

      {status === 'loading' ? (
        <p className="text-xs leading-5 text-muted-foreground" aria-live="polite">
          正在确认登录状态…
        </p>
      ) : null}

      {skuError ? (
        <p role="alert" className="text-xs leading-5 text-danger">
          {skuError}
        </p>
      ) : null}

      {addToCart.isError ? (
        <p role="alert" className="text-xs leading-5 text-danger">
          {describeCartError(addToCart.error)}
        </p>
      ) : null}

      {addToCart.isSuccess && lastAddedSkuId === currentSkuId ? (
        <div
          role="status"
          className="flex flex-wrap items-center gap-2 rounded-lg border border-success/20 bg-success-soft px-4 py-3 text-sm text-success"
        >
          <CheckCircle2 className="size-4 shrink-0" aria-hidden="true" />
          <span>
            已加入购物车
            {addedQuantity !== null ? `（该规格共 ${String(addedQuantity)} 件）` : ''}
          </span>
          <Link to="/cart" className="font-medium underline underline-offset-2 hover:text-success">
            去购物车
          </Link>
        </div>
      ) : null}
    </div>
  )
}
