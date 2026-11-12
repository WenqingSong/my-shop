import type { Sku, SkuProductValue } from '@/api/types'

/**
 * SKU 规格与库存的纯函数工具。
 *
 * 事实来源：`api/product/v1/product.go`（attr / productValue）与 `api/sku/v1/sku.go`（spec_values）。
 * 库存只存在于 productValue 中，`skus[]` 没有 stock 字段：
 * - productValue 命中且 stock 为数字 → 使用该数字（后端对无库存记录返回 0）；
 * - productValue 没有对应条目（例如无规格商品返回空对象）→ 库存**未知**，不能当成 0。
 */

/** `productValue` 的组合键：按规格组顺序用英文逗号连接规格值文本。 */
export function buildProductValueKey(values: readonly (string | null)[]): string | null {
  if (values.length === 0 || values.some((value) => value === null)) {
    return null
  }

  return values.join(',')
}

/** 按 sku_id 建立库存映射；只收录真实返回的数字库存。 */
export function buildSkuStockMap(
  productValue: Record<string, SkuProductValue> | null | undefined,
): Map<number, number> {
  const map = new Map<number, number>()

  for (const entry of Object.values(productValue ?? {})) {
    if (Number.isFinite(entry.stock)) {
      map.set(entry.sku_id, entry.stock)
    }
  }

  return map
}

/** SKU 库存：数字表示后端返回的可售库存；null 表示没有可用库存数据（未知）。 */
export function getSkuStock(stockMap: ReadonlyMap<number, number>, skuId: number): number | null {
  return stockMap.get(skuId) ?? null
}

/** SKU 规格展示文本：规格值按后端顺序连接；无规格回退到 SKU 名称。 */
export function describeSkuSpec(sku: Sku): string {
  const text = sku.spec_values
    .map((value) => value.value.trim())
    .filter((value) => value !== '')
    .join(',')

  return text !== '' ? text : sku.name
}

/** SKU 规格图；空串表示回退商品主图。 */
export function skuImageUrl(sku: Sku | null | undefined): string {
  return sku?.image_url.trim() ?? ''
}

/** 是否展示划线价：原价必须大于现价才有意义。 */
export function hasOriginalPrice(price: number, originalPrice: number): boolean {
  return originalPrice > price
}

/** 当前规格库存文案（未知时明确说明未知，不虚构数量）。 */
export function describeSkuStock(stock: number | null): string {
  if (stock === null) {
    return '该规格暂无可用的库存数据，库存以下单时服务端校验为准'
  }

  if (stock <= 0) {
    return '该规格当前无货'
  }

  return `当前规格库存 ${String(stock)} 件`
}
