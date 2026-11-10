import type { CartItem } from '@/api/types'
import { mockProducts, mockSkusFor } from '@/mocks/data/catalog'

/**
 * MSW 购物车状态（仅测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 行为对齐 internal/logic/cart/cart.go：
 * - 同一用户同一 SKU 至多一条，重复加购累加数量；
 * - 数量必须为 1..999（8002），累加后超上限整体拒绝；
 * - SKU 不存在 → 5001/404；SKU 禁用或商品下架 → 8003/409；
 * - 条目不存在或非本人 → 8001/404（不泄露他人数据）；
 * - available / unavailable_reason / stock / insufficient / price_changed 在读取时动态计算。
 *
 * 仅 Mock 提供的测试钩子（真实后端不存在）：
 * - setMockCartRequestDelay：延迟数量修改响应，用于验证请求竞争；
 * - getMockCartQuantityRequests：记录每次数量修改请求携带的数量；
 * - markMockSkuDisabled / markMockSkuDeleted / markMockProductOffShelf / setMockSkuStock：
 *   构造不可购买与库存不足场景（真实后端通过 admin 接口改变数据）。
 */

const MAX_QUANTITY = 999
const DEFAULT_STOCK = 100

interface MockCartRecord {
  id: number
  userId: number
  skuId: number
  quantity: number
  priceSnapshot: number
  selected: boolean
  createdAt: string
}

interface ResolvedSku {
  productId: number
  productName: string
  productMainImage: string
  skuName: string
  price: number
  productOnShelf: boolean
  skuEnabled: boolean
}

let records: MockCartRecord[] = []
let sequence = 0

const offShelfProductIds = new Set<number>()
const disabledSkuIds = new Set<number>()
const deletedSkuIds = new Set<number>()
const stockOverrides = new Map<number, number>()

let requestDelayMs = 0
let quantityRequests: number[] = []
let listRequests = 0

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

function resolveSku(skuId: number): ResolvedSku | undefined {
  for (const product of mockProducts) {
    const sku = mockSkusFor(product).find((item) => item.id === skuId)

    if (sku) {
      return {
        productId: product.id,
        productName: product.name,
        productMainImage: product.main_image,
        skuName: sku.name,
        price: sku.price,
        productOnShelf: product.status === 'on_shelf' && !offShelfProductIds.has(product.id),
        skuEnabled: sku.status === 'enabled' && !disabledSkuIds.has(sku.id),
      }
    }
  }

  return undefined
}

function toCartItem(record: MockCartRecord): CartItem {
  const sku = deletedSkuIds.has(record.skuId) ? undefined : resolveSku(record.skuId)

  if (!sku) {
    return {
      id: record.id,
      sku_id: record.skuId,
      quantity: record.quantity,
      selected: record.selected,
      price_snapshot: record.priceSnapshot,
      product_id: null,
      product_name: '',
      product_main_image: '',
      sku_name: '',
      current_price: null,
      price_changed: false,
      available: false,
      unavailable_reason: 'sku_deleted',
      stock: null,
      insufficient: false,
      created_at: record.createdAt,
      updated_at: record.createdAt,
    }
  }

  const available = sku.skuEnabled && sku.productOnShelf
  const unavailableReason = !sku.skuEnabled ? 'disabled' : sku.productOnShelf ? '' : 'off_shelf'
  const stock = stockOverrides.get(record.skuId) ?? DEFAULT_STOCK

  return {
    id: record.id,
    sku_id: record.skuId,
    quantity: record.quantity,
    selected: record.selected,
    price_snapshot: record.priceSnapshot,
    product_id: sku.productId,
    product_name: sku.productName,
    product_main_image: sku.productMainImage,
    sku_name: sku.skuName,
    current_price: sku.price,
    price_changed: sku.price !== record.priceSnapshot,
    available,
    unavailable_reason: unavailableReason,
    stock,
    insufficient: available && record.quantity > stock,
    created_at: record.createdAt,
    updated_at: record.createdAt,
  }
}

function findOwnedRecord(userId: number, itemId: number): MockCartRecord | undefined {
  return records.find((record) => record.userId === userId && record.id === itemId)
}

export function resetMockCart(): void {
  records = []
  sequence = 0
  offShelfProductIds.clear()
  disabledSkuIds.clear()
  deletedSkuIds.clear()
  stockOverrides.clear()
  requestDelayMs = 0
  quantityRequests = []
  listRequests = 0
}

/** 测试专用：GET /cart 被调用的次数，用于验证 Header 与页面共享同一份缓存。 */
export function getMockCartListRequestCount(): number {
  return listRequests
}

/** 测试专用：数量修改请求延迟（毫秒），用于构造「旧响应晚到」场景。 */
export function setMockCartRequestDelay(ms: number): void {
  requestDelayMs = ms
}

export function getMockCartRequestDelay(): number {
  return requestDelayMs
}

/** 测试专用：本次用例中每次数量修改请求携带的数量（按发送顺序）。 */
export function getMockCartQuantityRequests(): number[] {
  return [...quantityRequests]
}

/** 测试专用：SKU 停售。 */
export function markMockSkuDisabled(skuId: number): void {
  disabledSkuIds.add(skuId)
}

/** 测试专用：SKU 被删除（条目保留为悬空引用）。 */
export function markMockSkuDeleted(skuId: number): void {
  deletedSkuIds.add(skuId)
}

/** 测试专用：商品下架。 */
export function markMockProductOffShelf(productId: number): void {
  offShelfProductIds.add(productId)
}

/** 测试专用：覆盖库存。 */
export function setMockSkuStock(skuId: number, stock: number): void {
  stockOverrides.set(skuId, stock)
}

/** 测试专用：商品第 index 个 SKU 的 id（与 catalog mock 的生成规则一致）。 */
export function mockCartSkuId(productId: number, index = 0): number {
  return productId * 10 + index + 1
}

export function listMockCart(userId: number): CartItem[] {
  listRequests += 1

  return records
    .filter((record) => record.userId === userId)
    .sort((a, b) => a.id - b.id)
    .map(toCartItem)
}

/** 测试专用：预置购物车条目（不校验可购性，用于构造不可购买场景）。 */
export function seedMockCartItem(
  userId: number,
  skuId: number,
  quantity = 1,
  selected = true,
): CartItem {
  const existing = records.find(
    (record) => record.userId === userId && record.skuId === skuId,
  )

  if (existing) {
    existing.quantity = quantity
    existing.selected = selected
    return toCartItem(existing)
  }

  sequence += 1
  const record: MockCartRecord = {
    id: sequence,
    userId,
    skuId,
    quantity,
    priceSnapshot: resolveSku(skuId)?.price ?? 0,
    selected,
    createdAt: nowText(),
  }
  records = [...records, record]

  return toCartItem(record)
}

export function addMockCartItem(
  userId: number,
  skuId: number,
  quantity: number,
):
  | { ok: true; item: CartItem }
  | { ok: false; status: number; code: number; message: string } {
  if (!Number.isSafeInteger(skuId) || skuId <= 0) {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  if (!Number.isSafeInteger(quantity) || quantity < 1 || quantity > MAX_QUANTITY) {
    return { ok: false, status: 400, code: 8002, message: '数量非法' }
  }

  const sku = deletedSkuIds.has(skuId) ? undefined : resolveSku(skuId)

  if (!sku) {
    return { ok: false, status: 404, code: 5001, message: 'SKU 不存在' }
  }

  if (!sku.skuEnabled || !sku.productOnShelf) {
    return { ok: false, status: 409, code: 8003, message: '商品不可加购' }
  }

  const existing = records.find(
    (record) => record.userId === userId && record.skuId === skuId,
  )

  if (existing) {
    if (existing.quantity + quantity > MAX_QUANTITY) {
      return { ok: false, status: 400, code: 8002, message: '数量非法' }
    }

    existing.quantity += quantity
    return { ok: true, item: toCartItem(existing) }
  }

  sequence += 1
  const record: MockCartRecord = {
    id: sequence,
    userId,
    skuId,
    quantity,
    priceSnapshot: sku.price,
    selected: true,
    createdAt: nowText(),
  }
  records = [...records, record]

  return { ok: true, item: toCartItem(record) }
}

export function updateMockCartQuantity(
  userId: number,
  itemId: number,
  quantity: number,
):
  | { ok: true; item: CartItem }
  | { ok: false; status: number; code: number; message: string } {
  quantityRequests = [...quantityRequests, quantity]

  if (!Number.isSafeInteger(quantity) || quantity < 1 || quantity > MAX_QUANTITY) {
    return { ok: false, status: 400, code: 8002, message: '数量非法' }
  }

  const record = findOwnedRecord(userId, itemId)

  if (!record) {
    return { ok: false, status: 404, code: 8001, message: '购物车条目不存在' }
  }

  record.quantity = quantity

  return { ok: true, item: toCartItem(record) }
}

export function updateMockCartSelected(
  userId: number,
  itemId: number,
  selected: boolean,
):
  | { ok: true; item: CartItem }
  | { ok: false; status: number; code: number; message: string } {
  const record = findOwnedRecord(userId, itemId)

  if (!record) {
    return { ok: false, status: 404, code: 8001, message: '购物车条目不存在' }
  }

  record.selected = selected

  return { ok: true, item: toCartItem(record) }
}

export function deleteMockCartItem(
  userId: number,
  itemId: number,
): { ok: true } | { ok: false; status: number; code: number; message: string } {
  const record = findOwnedRecord(userId, itemId)

  if (!record) {
    return { ok: false, status: 404, code: 8001, message: '购物车条目不存在' }
  }

  records = records.filter((item) => item !== record)

  return { ok: true }
}
