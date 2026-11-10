import { describe, expect, it } from 'vitest'

import {
  addCartItem,
  CART_QUANTITY_MAX,
  deleteCartItem,
  fetchCart,
  updateCartItemQuantity,
  updateCartItemSelected,
} from '@/api/cart'
import { loginRequest, registerRequest } from '@/api/auth'
import { ApiError } from '@/api/client'
import { setAuthTokens } from '@/auth/token-store'
import {
  markMockProductOffShelf,
  markMockSkuDeleted,
  markMockSkuDisabled,
  mockCartSkuId,
  seedMockCartItem,
  setMockSkuStock,
} from '@/mocks/data/cart'
import { signInAsDemo } from '@/test/auth'

async function catchError(run: () => Promise<unknown>): Promise<ApiError> {
  const error = await run().catch((reason: unknown) => reason)
  expect(error).toBeInstanceOf(ApiError)
  return error as ApiError
}

describe('购物车 API（MSW Mock，非真实后端）', () => {
  it('未登录访问返回 401', async () => {
    const error = await catchError(() => fetchCart())

    expect(error.status).toBe(401)
    expect(error.code).toBe('1002')
  })

  it('加购后返回服务端条目快照', async () => {
    await signInAsDemo()

    const skuId = mockCartSkuId(1, 0)
    const item = await addCartItem(skuId, 2)

    expect(item).toMatchObject({
      sku_id: skuId,
      quantity: 2,
      selected: true,
      available: true,
      unavailable_reason: '',
      insufficient: false,
    })
    expect(item.product_name).toContain('星海 X1 Pro')
    expect(item.sku_name).toBe('标准款')
    // 价格均为整数分，且未发生价格变化。
    expect(item.current_price).toBe(item.price_snapshot)
    expect(Number.isInteger(item.current_price)).toBe(true)

    const list = await fetchCart()
    expect(list.items).toHaveLength(1)
  })

  it('重复加购同一 SKU 累加数量，不产生第二条记录', async () => {
    await signInAsDemo()

    const skuId = mockCartSkuId(1, 0)
    await addCartItem(skuId, 1)
    const item = await addCartItem(skuId, 2)

    expect(item.quantity).toBe(3)
    expect((await fetchCart()).items).toHaveLength(1)
  })

  it('数量超出 1..999 时返回 400/8002', async () => {
    await signInAsDemo()
    const skuId = mockCartSkuId(1, 0)
    const item = await addCartItem(skuId, 1)

    expect((await catchError(() => updateCartItemQuantity(item.id, 0))).code).toBe('8002')
    expect(
      (await catchError(() => updateCartItemQuantity(item.id, CART_QUANTITY_MAX + 1))).code,
    ).toBe('8002')
    expect((await catchError(() => addCartItem(skuId, 0))).code).toBe('8002')
    expect((await updateCartItemQuantity(item.id, CART_QUANTITY_MAX)).quantity).toBe(999)
  })

  it('勾选、取消勾选与删除', async () => {
    await signInAsDemo()
    const item = await addCartItem(mockCartSkuId(1, 0), 1)

    expect((await updateCartItemSelected(item.id, false)).selected).toBe(false)
    expect((await fetchCart()).items[0]?.selected).toBe(false)

    await deleteCartItem(item.id)
    expect((await fetchCart()).items).toHaveLength(0)

    const error = await catchError(() => deleteCartItem(item.id))
    expect(error.status).toBe(404)
    expect(error.code).toBe('8001')
  })

  it('SKU 停售或商品下架时不可加购（409/8003）', async () => {
    await signInAsDemo()

    markMockSkuDisabled(mockCartSkuId(1, 0))
    expect((await catchError(() => addCartItem(mockCartSkuId(1, 0), 1))).code).toBe('8003')

    markMockProductOffShelf(2)
    expect((await catchError(() => addCartItem(mockCartSkuId(2, 0), 1))).code).toBe('8003')
  })

  it('不存在的 SKU 返回 404/5001', async () => {
    await signInAsDemo()

    const error = await catchError(() => addCartItem(999_999, 1))
    expect(error.status).toBe(404)
    expect(error.code).toBe('5001')
  })

  it('列表动态标识不可购买、库存不足与价格变化', async () => {
    await signInAsDemo()

    seedMockCartItem(1, mockCartSkuId(1, 0), 2)
    seedMockCartItem(1, mockCartSkuId(2, 0), 1)
    // 数量 2 > 库存 1，用于验证后端计算的 insufficient。
    seedMockCartItem(1, mockCartSkuId(3, 0), 2)

    markMockSkuDeleted(mockCartSkuId(1, 0))
    markMockSkuDisabled(mockCartSkuId(2, 0))
    setMockSkuStock(mockCartSkuId(3, 0), 1)

    const { items } = await fetchCart()

    expect(items.find((item) => item.sku_id === mockCartSkuId(1, 0))).toMatchObject({
      available: false,
      unavailable_reason: 'sku_deleted',
      product_id: null,
      current_price: null,
      stock: null,
    })
    expect(items.find((item) => item.sku_id === mockCartSkuId(2, 0))).toMatchObject({
      available: false,
      unavailable_reason: 'disabled',
    })
    expect(items.find((item) => item.sku_id === mockCartSkuId(3, 0))).toMatchObject({
      available: true,
      insufficient: true,
      stock: 1,
    })
  })

  it('不同账号的购物车互相隔离', async () => {
    await signInAsDemo()
    await addCartItem(mockCartSkuId(1, 0), 1)

    await registerRequest({ username: 'other', password: 'other12345' })
    setAuthTokens(await loginRequest({ username: 'other', password: 'other12345' }))
    expect((await fetchCart()).items).toHaveLength(0)

    // 切回 demo：服务端仍保存着 demo 自己的条目。
    setAuthTokens(await loginRequest({ username: 'demo', password: 'demo12345' }))
    expect((await fetchCart()).items).toHaveLength(1)
  })
})
