import type { Address } from '@/api/types'
import type { Order, OrderItem } from '@/api/orders'
import { listMockAddresses } from '@/mocks/data/addresses'
import { listMockCart, removeMockCartItemsBySkuIds } from '@/mocks/data/cart'
import { mockProducts, mockSkusFor } from '@/mocks/data/catalog'

/**
 * MSW 订单状态（仅测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 行为对齐 internal/logic/order/order.go：
 * - source=cart：读取本人**全部已勾选**购物车条目，服务端按 mock 目录价格重新定价；
 * - 金额由 mock 服务端计算：original = Σ price×qty，discount=0，total=original；
 * - 地址必须属于当前用户（否则 7001）；
 * - 幂等：user_id + idempotency_key 唯一；同键同内容返回既有订单，同键不同内容 9006；
 * - 成功后删除已购（已勾选）购物车条目，未勾选条目保留；
 * - 无勾选条目 → 9004。
 *
 * 仅 Mock 提供的测试钩子（真实后端不存在）：
 * - setMockOrderRequestDelay：延迟响应，用于验证「提交中」与防重复点击；
 * - setMockOrderErrorOnce：下一次创建强制返回指定业务错误（构造库存不足/不可购买等）；
 * - getMockOrderCreateRequestCount / getMockOrderCreatePayloads：断言请求次数与幂等键。
 */

interface MockOrderRecord extends Order {
  request_hash: string
}

interface MockSkuInfo {
  skuId: number
  productId: number
  skuName: string
  productName: string
  mainImage: string
  price: number
  available: boolean
}

let orders: MockOrderRecord[] = []
let sequence = 0

let requestDelayMs = 0
let forcedError: { status: number; code: number; message: string } | null = null
let createRequestCount = 0
let createPayloads: Record<string, unknown>[] = []

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

function resolveSku(skuId: number): MockSkuInfo | undefined {
  for (const product of mockProducts) {
    const sku = mockSkusFor(product).find((item) => item.id === skuId)

    if (sku) {
      return {
        skuId,
        productId: product.id,
        skuName: sku.name,
        productName: product.name,
        mainImage: product.main_image,
        price: sku.price,
        available: product.status === 'on_shelf' && sku.status === 'enabled',
      }
    }
  }

  return undefined
}

function requestHash(addressId: number, lines: readonly { skuId: number; quantity: number }[]): string {
  const normalized = [...lines]
    .sort((a, b) => a.skuId - b.skuId)
    .map((line) => `${String(line.skuId)}:${String(line.quantity)}`)
    .join(',')

  return `cart|${String(addressId)}|0|${normalized}`
}

export function resetMockOrders(): void {
  orders = []
  sequence = 0
  requestDelayMs = 0
  forcedError = null
  createRequestCount = 0
  createPayloads = []
}

export function setMockOrderRequestDelay(ms: number): void {
  requestDelayMs = ms
}

export function getMockOrderRequestDelay(): number {
  return requestDelayMs
}

/** 测试专用：下一次创建订单强制失败（返回指定业务错误，不落库）。 */
export function setMockOrderErrorOnce(error: { status: number; code: number; message: string }): void {
  forcedError = error
}

export function getMockOrderCreateRequestCount(): number {
  return createRequestCount
}

export function getMockOrderCreatePayloads(): Record<string, unknown>[] {
  return [...createPayloads]
}

export interface MockCreateOrderInput {
  source?: unknown
  address_id?: unknown
  idempotency_key?: unknown
}

type MockOrderResult =
  | { ok: true; order: Order }
  | { ok: false; status: number; code: number; message: string }

export function createMockOrder(userId: number, input: MockCreateOrderInput): MockOrderResult {
  createRequestCount += 1
  createPayloads = [...createPayloads, { ...input }]

  if (forcedError) {
    const error = forcedError
    forcedError = null
    return { ok: false, ...error }
  }

  if (input.source !== 'cart') {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  const addressId = Number(input.address_id)
  const idempotencyKey = typeof input.idempotency_key === 'string' ? input.idempotency_key.trim() : ''

  if (!Number.isSafeInteger(addressId) || addressId <= 0 || idempotencyKey === '') {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  const selected = listMockCart(userId).filter((item) => item.selected)

  if (selected.length === 0) {
    return { ok: false, status: 400, code: 9004, message: '购物车无勾选商品' }
  }

  const resolved: { sku: MockSkuInfo; quantity: number }[] = []

  for (const item of selected) {
    const sku = resolveSku(item.sku_id)

    if (!sku || !sku.available) {
      return { ok: false, status: 409, code: 9003, message: '商品不可购买' }
    }

    resolved.push({ sku, quantity: item.quantity })
  }

  const address: Address | undefined = listMockAddresses(userId).find(
    (item) => item.id === addressId,
  )

  if (!address) {
    return { ok: false, status: 404, code: 7001, message: '地址不存在' }
  }

  const hash = requestHash(
    addressId,
    resolved.map((line) => ({ skuId: line.sku.skuId, quantity: line.quantity })),
  )
  const existing = orders.find(
    (order) => order.user_id === userId && order.idempotency_key === idempotencyKey,
  )

  if (existing) {
    if (existing.request_hash !== hash) {
      return { ok: false, status: 409, code: 9006, message: '幂等键冲突' }
    }

    return { ok: true, order: existing }
  }

  sequence += 1
  const createdAt = nowText()
  const originalAmount = resolved.reduce(
    (total, line) => total + line.sku.price * line.quantity,
    0,
  )

  const items: OrderItem[] = resolved.map((line, index) => ({
    id: sequence * 100 + index + 1,
    sku_id: line.sku.skuId,
    product_id: line.sku.productId,
    sku_name: line.sku.skuName,
    product_name: line.sku.productName,
    product_main_image: line.sku.mainImage,
    price: line.sku.price,
    quantity: line.quantity,
  }))

  // 与后端 generateOrderNo 一致：13 位毫秒时间戳 + 12 位随机 hex。
  const orderNo = `${Date.now()}${Math.random().toString(16).slice(2, 14).padEnd(12, '0')}`

  const order: MockOrderRecord = {
    id: sequence,
    order_no: orderNo,
    user_id: userId,
    status: 'pending_payment',
    total_amount: originalAmount,
    original_amount: originalAmount,
    discount_amount: 0,
    user_coupon_id: null,
    idempotency_key: idempotencyKey,
    request_hash: hash,
    recipient_name: address.recipient_name,
    phone: address.phone,
    province: address.province,
    city: address.city,
    district: address.district,
    detail: address.detail,
    address_id: address.id,
    expire_at: null,
    cancel_reason: null,
    paid_at: null,
    shipped_at: null,
    received_at: null,
    completed_at: null,
    cancelled_at: null,
    refunded_at: null,
    created_at: createdAt,
    updated_at: createdAt,
    items,
  }

  orders = [...orders, order]
  // 与真实后端一致：订单事务内删除已购（已勾选）条目。
  removeMockCartItemsBySkuIds(
    userId,
    resolved.map((line) => line.sku.skuId),
  )

  return { ok: true, order }
}

export function getMockOrder(userId: number, orderId: number): Order | undefined {
  return orders.find((order) => order.user_id === userId && order.id === orderId)
}
