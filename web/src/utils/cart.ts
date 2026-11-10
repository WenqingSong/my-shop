import type { CartItem } from '@/api/types'

/**
 * 购物车展示用的纯函数计算。
 *
 * 重要边界（与 W3-02 的分工）：
 * - 这里只做「展示口径」的整数分加减乘，用于商品小计与购物车预估金额；
 * - 它不是订单金额：不计算优惠、不计算运费、不判断成交价、不做库存最终裁决；
 * - 最终应付金额一律以 Go 后端订单试算结果为准。
 *
 * 所有金额入参/出参都是整数分，禁止经过浮点元换算。
 */

/**
 * 条目展示单价：只使用后端返回的当前 SKU 价。
 * SKU 被删除时 current_price 为 null，界面显示「价格不可用」；
 * price_snapshot 只用于「加购价」提示，不参与小计与预估金额。
 */
export function cartItemUnitPrice(item: CartItem): number | null {
  return item.current_price
}

/** 条目小计（整数分）；无可用单价时返回 null，界面显示「—」。 */
export function cartItemSubtotal(item: CartItem): number | null {
  const unitPrice = cartItemUnitPrice(item)

  if (unitPrice === null) {
    return null
  }

  return unitPrice * item.quantity
}

export interface CartSummary {
  /** 全部条目的商品件数（数量求和），Header 徽标使用。 */
  totalQuantity: number
  /** 全部条目数。 */
  itemCount: number
  /** 可购买条目（后端 available=true）。 */
  purchasableCount: number
  /** 已勾选且可购买的条目数。 */
  selectedCount: number
  /** 已勾选且可购买的商品件数。 */
  selectedQuantity: number
  /** 已勾选但不可购买的条目数（用于解释预估金额为何不含它们）。 */
  blockedSelectedCount: number
  /** 购物车预估金额（整数分）：仅汇总「已勾选 + available」条目。 */
  estimatedTotal: number
  /** 全部可购买条目是否都已勾选；无可用商品时为 false。 */
  allPurchasableSelected: boolean
}

/** 汇总购物车，用于金额展示与全选状态。所有金额均为整数分。 */
export function summarizeCart(items: CartItem[]): CartSummary {
  let totalQuantity = 0
  let purchasableCount = 0
  let selectedCount = 0
  let selectedQuantity = 0
  let blockedSelectedCount = 0
  let estimatedTotal = 0

  for (const item of items) {
    totalQuantity += item.quantity

    if (!item.available) {
      if (item.selected) {
        blockedSelectedCount += 1
      }
      continue
    }

    purchasableCount += 1

    if (!item.selected) {
      continue
    }

    selectedCount += 1
    selectedQuantity += item.quantity

    const subtotal = cartItemSubtotal(item)

    if (subtotal !== null) {
      estimatedTotal += subtotal
    }
  }

  return {
    totalQuantity,
    itemCount: items.length,
    purchasableCount,
    selectedCount,
    selectedQuantity,
    blockedSelectedCount,
    estimatedTotal,
    allPurchasableSelected: purchasableCount > 0 && selectedCount === purchasableCount,
  }
}

/** 不可购买原因的中文说明（与后端 unavailable_reason 枚举一一对应）。 */
export function describeUnavailableReason(reason: string): string {
  switch (reason) {
    case 'sku_deleted':
      return '该规格已删除，无法购买'
    case 'disabled':
      return '该规格已停售，无法购买'
    case 'off_shelf':
      return '商品已下架，无法购买'
    default:
      return '商品当前不可购买'
  }
}
