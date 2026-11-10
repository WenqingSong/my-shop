import type { Address } from '@/api/types'
import type { Order, OrderItem, OrderStatus } from '@/api/orders'
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
/** 订单操作（支付/取消/收货）的响应延迟，用于验证「请求中」与防重复点击。 */
let actionDelayMs = 0
let forcedError: { status: number; code: number; message: string } | null = null
let createRequestCount = 0
let createPayloads: Record<string, unknown>[] = []
/** 订单操作（支付/取消/收货）请求计数，用于断言「重复点击只发一次请求」。 */
let actionRequestCount = 0

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

/** 与后端 order.pay_timeout 默认值一致的支付超时（秒），仅用于 Mock 生成 expire_at。 */
const MOCK_PAY_TIMEOUT_SECONDS = 900

function timeTextAfter(seconds: number): string {
  return new Date(Date.now() + seconds * 1000).toISOString().slice(0, 19).replace('T', ' ')
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
  actionDelayMs = 0
  forcedError = null
  createRequestCount = 0
  createPayloads = []
  actionRequestCount = 0
}

export function setMockOrderRequestDelay(ms: number): void {
  requestDelayMs = ms
}

export function getMockOrderRequestDelay(): number {
  return requestDelayMs
}

/** 测试专用：设置订单操作（支付/取消/收货）的响应延迟。 */
export function setMockOrderActionDelay(ms: number): void {
  actionDelayMs = ms
}

export function getMockOrderActionDelay(): number {
  return actionDelayMs
}

/** 已发生的订单操作请求数（支付/取消/收货合计）。 */
export function getMockOrderActionRequestCount(): number {
  return actionRequestCount
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
    // 与真实后端一致：expire_at = 下单时间 + order.pay_timeout（默认 900s）。
    expire_at: timeTextAfter(MOCK_PAY_TIMEOUT_SECONDS),
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

/**
 * 本人订单列表（与后端 `GET /orders` 一致：仅本人、按 id 倒序、无分页无筛选）。
 * 返回副本，避免测试断言时被后续写入影响。
 */
export function listMockOrders(userId: number): Order[] {
  return orders
    .filter((order) => order.user_id === userId)
    .sort((a, b) => b.id - a.id)
    .map((order) => ({ ...order, items: [...order.items] }))
}

export interface MockSeedOrderInput {
  status?: OrderStatus
  created_at?: string
  expire_at?: string | null
  original_amount?: number
  discount_amount?: number
  total_amount?: number
  items?: OrderItem[]
  recipient_name?: string
  phone?: string
  province?: string
  city?: string
  district?: string
  detail?: string
  paid_at?: string | null
  shipped_at?: string | null
  received_at?: string | null
  completed_at?: string | null
  cancelled_at?: string | null
  refunded_at?: string | null
  cancel_reason?: number | null
  user_coupon_id?: number | null
}

const DEFAULT_SEED_ITEM: Omit<OrderItem, 'id'> = {
  sku_id: 9_001,
  product_id: 9_001,
  sku_name: '默认规格',
  product_name: '测试商品',
  product_main_image: '',
  price: 1_000,
  quantity: 1,
}

/**
 * 测试专用：直接写入一条指定状态的订单（真实后端不存在该入口）。
 *
 * 用于构造待支付/待发货/待收货/已完成/已取消/已退款等展示场景，
 * 避免测试必须走完整履约链路才能看到对应状态。
 */
export function seedMockOrder(userId: number, input: MockSeedOrderInput = {}): Order {
  sequence += 1
  const createdAt = input.created_at ?? nowText()
  const items =
    input.items ??
    [{ ...DEFAULT_SEED_ITEM, id: sequence * 100 + 1 }].map((item) => ({ ...item }))
  const originalAmount =
    input.original_amount ?? items.reduce((total, item) => total + item.price * item.quantity, 0)
  const discountAmount = input.discount_amount ?? 0

  const order: MockOrderRecord = {
    id: sequence,
    order_no: `${String(Date.now())}${Math.random().toString(16).slice(2, 14).padEnd(12, '0')}`,
    user_id: userId,
    status: input.status ?? 'pending_payment',
    total_amount: input.total_amount ?? originalAmount - discountAmount,
    original_amount: originalAmount,
    discount_amount: discountAmount,
    user_coupon_id: input.user_coupon_id ?? null,
    idempotency_key: `seed-${String(sequence)}`,
    request_hash: `seed-${String(sequence)}`,
    recipient_name: input.recipient_name ?? '张三',
    phone: input.phone ?? '13800138000',
    province: input.province ?? '广东省',
    city: input.city ?? '深圳市',
    district: input.district ?? '南山区',
    detail: input.detail ?? '科技园路 1 号',
    address_id: 1,
    expire_at:
      input.expire_at === undefined ? timeTextAfter(MOCK_PAY_TIMEOUT_SECONDS) : input.expire_at,
    cancel_reason: input.cancel_reason ?? null,
    paid_at: input.paid_at ?? null,
    shipped_at: input.shipped_at ?? null,
    received_at: input.received_at ?? null,
    completed_at: input.completed_at ?? null,
    cancelled_at: input.cancelled_at ?? null,
    refunded_at: input.refunded_at ?? null,
    created_at: createdAt,
    updated_at: createdAt,
    items,
  }

  orders = [...orders, order]

  return { ...order, items: [...order.items] }
}

/** 订单操作（支付/取消/收货）的 Mock 返回。 */
export type MockOrderActionResult =
  | { ok: true; order: Order }
  | { ok: false; status: number; code: number; message: string }

function findOwnedRecord(userId: number, orderId: number): MockOrderRecord | undefined {
  return orders.find((order) => order.user_id === userId && order.id === orderId)
}

function replaceOrder(next: MockOrderRecord): Order {
  orders = orders.map((order) => (order.id === next.id ? next : order))
  return { ...next, items: [...next.items] }
}

const NOT_FOUND: MockOrderActionResult = {
  ok: false,
  status: 404,
  code: 9001,
  message: '订单不存在',
}

const INVALID_TRANSITION: MockOrderActionResult = {
  ok: false,
  status: 409,
  code: 9002,
  message: '订单状态迁移非法',
}

/**
 * 模拟支付（对齐 internal/logic/order/order.go:447-516）：
 * - 仅 pending_payment → paid，写 paid_at；
 * - paid 重复支付幂等成功（返回既有订单，不改状态）；
 * - 其它状态 9002。
 */
export function payMockOrder(userId: number, orderId: number): MockOrderActionResult {
  actionRequestCount += 1
  const record = findOwnedRecord(userId, orderId)

  if (!record) {
    return NOT_FOUND
  }

  if (record.status === 'paid') {
    return { ok: true, order: { ...record, items: [...record.items] } }
  }

  if (record.status !== 'pending_payment') {
    return INVALID_TRANSITION
  }

  return {
    ok: true,
    order: replaceOrder({ ...record, status: 'paid', paid_at: nowText(), updated_at: nowText() }),
  }
}

/**
 * 取消订单（对齐 order.go:518-549, 729-761）：
 * - 仅 pending_payment → cancelled，cancel_reason=1（用户取消），写 cancelled_at；
 * - 同事务恢复库存、释放券 —— Mock 不建模库存与券，仅在文案层说明；
 * - 其它状态 9002。
 */
export function cancelMockOrder(userId: number, orderId: number): MockOrderActionResult {
  actionRequestCount += 1
  const record = findOwnedRecord(userId, orderId)

  if (!record) {
    return NOT_FOUND
  }

  if (record.status !== 'pending_payment') {
    return INVALID_TRANSITION
  }

  return {
    ok: true,
    order: replaceOrder({
      ...record,
      status: 'cancelled',
      cancel_reason: 1,
      cancelled_at: nowText(),
      updated_at: nowText(),
    }),
  }
}

/**
 * 确认收货（对齐 order.go:551-595）：
 * - 仅 shipped 可入；
 * - 后端在同一事务里 30→40→50，响应恒为 completed，这里同样直接落到 completed；
 * - 其它状态 9002。
 */
export function receiveMockOrder(userId: number, orderId: number): MockOrderActionResult {
  actionRequestCount += 1
  const record = findOwnedRecord(userId, orderId)

  if (!record) {
    return NOT_FOUND
  }

  if (record.status !== 'shipped') {
    return INVALID_TRANSITION
  }

  const at = nowText()

  return {
    ok: true,
    order: replaceOrder({
      ...record,
      status: 'completed',
      received_at: at,
      completed_at: at,
      updated_at: at,
    }),
  }
}
