import { describe, expect, it } from 'vitest'

import type { CartItem } from '@/api/types'
import { cartItemSubtotal, cartItemUnitPrice, describeUnavailableReason, summarizeCart } from '@/utils/cart'

function makeItem(overrides: Partial<CartItem> = {}): CartItem {
  return {
    id: 1,
    sku_id: 11,
    quantity: 1,
    selected: true,
    price_snapshot: 1000,
    product_id: 1,
    product_name: '测试商品',
    product_main_image: '/mock/product-1.svg',
    sku_name: '标准款',
    current_price: 1000,
    price_changed: false,
    available: true,
    unavailable_reason: '',
    stock: 100,
    insufficient: false,
    created_at: '2026-11-10 10:00:00',
    updated_at: '2026-11-10 10:00:00',
    ...overrides,
  }
}

describe('购物车展示计算', () => {
  it('小计与预估金额使用整数分相加', () => {
    const items = [
      makeItem({ id: 1, quantity: 2, current_price: 129_900 }),
      makeItem({ id: 2, quantity: 1, current_price: 19_900 }),
    ]

    expect(cartItemSubtotal(items[0]!)).toBe(259_800)

    const summary = summarizeCart(items)
    expect(summary.estimatedTotal).toBe(279_700)
    expect(summary.totalQuantity).toBe(3)
    expect(summary.selectedQuantity).toBe(3)
    expect(summary.selectedCount).toBe(2)
    expect(summary.allPurchasableSelected).toBe(true)
  })

  it('未勾选与不可购买条目不计入预估金额', () => {
    const items = [
      makeItem({ id: 1, quantity: 1, current_price: 10_000, selected: true }),
      makeItem({ id: 2, quantity: 1, current_price: 20_000, selected: false }),
      makeItem({
        id: 3,
        quantity: 1,
        current_price: 30_000,
        available: false,
        unavailable_reason: 'off_shelf',
      }),
    ]

    const summary = summarizeCart(items)

    expect(summary.estimatedTotal).toBe(10_000)
    expect(summary.selectedCount).toBe(1)
    expect(summary.purchasableCount).toBe(2)
    expect(summary.blockedSelectedCount).toBe(1)
    expect(summary.allPurchasableSelected).toBe(false)
  })

  it('SKU 被删除时没有可用单价与小计', () => {
    const item = makeItem({
      current_price: null,
      available: false,
      unavailable_reason: 'sku_deleted',
      product_id: null,
      stock: null,
    })

    expect(cartItemUnitPrice(item)).toBeNull()
    expect(cartItemSubtotal(item)).toBeNull()
    expect(summarizeCart([item]).estimatedTotal).toBe(0)
  })

  it('无可用商品时全选状态为 false', () => {
    expect(summarizeCart([]).allPurchasableSelected).toBe(false)
    expect(
      summarizeCart([makeItem({ available: false, unavailable_reason: 'disabled' })])
        .allPurchasableSelected,
    ).toBe(false)
  })

  it('不可购买原因映射为中文文案', () => {
    expect(describeUnavailableReason('sku_deleted')).toContain('已删除')
    expect(describeUnavailableReason('disabled')).toContain('停售')
    expect(describeUnavailableReason('off_shelf')).toContain('下架')
    expect(describeUnavailableReason('unknown')).toContain('不可购买')
  })
})
