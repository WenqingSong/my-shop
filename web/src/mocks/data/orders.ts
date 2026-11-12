import type { Address } from '@/api/types'
import type {
  Order,
  OrderItem,
  OrderQuote,
  OrderQuoteItem,
  OrderStatus,
} from '@/api/orders'
import { listMockAddresses } from '@/mocks/data/addresses'
import { listMockCart, removeMockCartItemsBySkuIds } from '@/mocks/data/cart'
import { mockProducts, mockSkusFor } from '@/mocks/data/catalog'
import {
  evaluateMockCoupon,
  getMockCoupon,
  lockMockCoupon,
  releaseMockCouponOfOrder,
  markMockCouponUsedOfOrder,
} from '@/mocks/data/coupons'

/**
 * MSW 订单状态（仅测试 / 本地 Mock，不参与生产逻辑）。
 *
 * 行为对齐 internal/logic/order/order.go：
 * - source=cart：读取本人**全部已勾选**购物车条目，服务端按 mock 目录价格重新定价；
 * - 金额由 mock 服务端按六槽位计算（与 pricing.Quote 同公式）：
 *   original − 券 − 会员 − 积分(恒 0) + 运费 = payable；
 * - 地址必须属于当前用户（否则 7001）；
 * - 幂等：user_id + idempotency_key 唯一；同键同内容返回既有订单，同键不同内容 9006；
 * - 成功后删除已购（已勾选）购物车条目，未勾选条目保留；
 * - 无勾选条目 → 9004；
 * - `POST /orders/quote` 只读：不校验库存、不锁定券、不写订单、不清理购物车。
 *
 * 仅 Mock 提供的测试钩子（真实后端不存在）：
 * - setMockOrderRequestDelay：延迟响应，用于验证「提交中」与防重复点击；
 * - setMockOrderErrorOnce：下一次创建强制返回指定业务错误（构造库存不足/不可购买等）；
 * - setMockQuoteErrorOnce：下一次试算强制返回指定业务错误；
 * - setMockPricingBaseline：模拟会员折扣率与运费（真实值来自会员等级与运费模板）；
 * - getMockOrderCreateRequestCount / getMockOrderCreatePayloads：断言请求次数与幂等键；
 * - getMockQuoteRequestCount / getMockQuotePayloads：断言试算请求次数与入参。
 */

interface MockOrderRecord extends Order {
  request_hash: string
}

interface MockSkuInfo {
  skuId: number
  skuCode: string
  productId: number
  skuName: string
  specText: string
  productName: string
  mainImage: string
  imageUrl: string
  price: number
  originalPrice: number
  available: boolean
}

interface MockAmounts {
  originalAmount: number
  couponDiscount: number
  memberDiscount: number
  pointsDiscount: number
  shippingFee: number
  payableAmount: number
}

let orders: MockOrderRecord[] = []
let sequence = 0

let requestDelayMs = 0
/** 订单操作（支付/取消/收货）的响应延迟，用于验证「请求中」与防重复点击。 */
let actionDelayMs = 0
/** 试算响应延迟，用于验证「试算中不展示旧金额」。 */
let quoteDelayMs = 0
let forcedError: { status: number; code: number; message: string } | null = null
let forcedQuoteError: { status: number; code: number; message: string } | null = null
/** 会员折扣率（万分比）：10000 = 不打折，默认无会员权益。 */
let memberDiscountRateBp = 10_000
/** Mock 运费（整数分）：真实值由运费模板与地区决定。 */
let shippingFee = 0
let createRequestCount = 0
let createPayloads: Record<string, unknown>[] = []
/** 订单操作（支付/取消/收货）请求计数，用于断言「重复点击只发一次请求」。 */
let actionRequestCount = 0
let quoteRequestCount = 0
let quotePayloads: Record<string, unknown>[] = []

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
        skuCode: sku.sku_code,
        productId: product.id,
        skuName: sku.name,
        specText: sku.spec_values
          .map((value) => value.value)
          .filter((value) => value !== '')
          .join(','),
        productName: product.name,
        mainImage: product.main_image,
        imageUrl: sku.image_url,
        price: sku.price,
        originalPrice: sku.original_price,
        available: product.status === 'on_shelf' && sku.status === 'enabled',
      }
    }
  }

  return undefined
}

/** 按 sku_code 定位 SKU（direct 试算优先使用 sku_code）。 */
function resolveSkuByCode(skuCode: string): MockSkuInfo | undefined {
  for (const product of mockProducts) {
    const sku = mockSkusFor(product).find((item) => item.sku_code === skuCode)

    if (sku) {
      return resolveSku(sku.id)
    }
  }

  return undefined
}

/**
 * 会员折扣（对齐 internal/logic/order/order.go resolveMemberDiscount）：
 * `floor(original × (10000 - rate_bp) / 10000)`，并夹紧到 `original - couponDiscount`。
 * 无等级权益（rate_bp = 10000）时为 0。
 */
function mockMemberDiscount(originalAmount: number, couponDiscount: number): number {
  if (originalAmount <= 0 || memberDiscountRateBp <= 0 || memberDiscountRateBp >= 10_000) {
    return 0
  }

  const remaining = originalAmount - couponDiscount

  if (remaining <= 0) {
    return 0
  }

  const raw = Math.floor((originalAmount * (10_000 - memberDiscountRateBp)) / 10_000)
  return Math.max(0, Math.min(raw, remaining))
}

/**
 * 请求指纹（对齐后端 `requestHash`）：source|address_id|coupon_id|sku:qty...
 *
 * coupon_id 参与指纹，保证「换券 / 取消用券」不会与旧请求共用同一个幂等键内容。
 */
function requestHash(
  addressId: number,
  couponId: number,
  lines: readonly { skuId: number; quantity: number }[],
): string {
  const normalized = [...lines]
    .sort((a, b) => a.skuId - b.skuId)
    .map((line) => `${String(line.skuId)}:${String(line.quantity)}`)
    .join(',')

  return `cart|${String(addressId)}|${String(couponId)}|${normalized}`
}

export function resetMockOrders(): void {
  orders = []
  sequence = 0
  requestDelayMs = 0
  actionDelayMs = 0
  quoteDelayMs = 0
  forcedError = null
  forcedQuoteError = null
  memberDiscountRateBp = 10_000
  shippingFee = 0
  createRequestCount = 0
  createPayloads = []
  actionRequestCount = 0
  quoteRequestCount = 0
  quotePayloads = []
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

/** 测试专用：设置试算响应延迟（验证试算中不展示旧金额）。 */
export function setMockQuoteRequestDelay(ms: number): void {
  quoteDelayMs = ms
}

export function getMockQuoteRequestDelay(): number {
  return quoteDelayMs
}

/** 已发生的订单操作请求数（支付/取消/收货合计）。 */
export function getMockOrderActionRequestCount(): number {
  return actionRequestCount
}

/** 测试专用：下一次创建订单强制失败（返回指定业务错误，不落库）。 */
export function setMockOrderErrorOnce(error: { status: number; code: number; message: string }): void {
  forcedError = error
}

/** 测试专用：下一次试算强制失败。 */
export function setMockQuoteErrorOnce(error: {
  status: number
  code: number
  message: string
}): void {
  forcedQuoteError = error
}

/**
 * 测试专用：模拟会员折扣率与整单运费。
 * 真实后端分别由会员等级权益与运费模板/地区规则产生，Mock 无法完整建模。
 */
export function setMockPricingBaseline(input: {
  memberDiscountRateBp?: number
  shippingFee?: number
}): void {
  if (input.memberDiscountRateBp !== undefined) {
    memberDiscountRateBp = input.memberDiscountRateBp
  }

  if (input.shippingFee !== undefined) {
    shippingFee = input.shippingFee
  }
}

export function getMockOrderCreateRequestCount(): number {
  return createRequestCount
}

export function getMockOrderCreatePayloads(): Record<string, unknown>[] {
  return [...createPayloads]
}

/** 试算请求次数（用于断言「不选地址不发请求」等）。 */
export function getMockQuoteRequestCount(): number {
  return quoteRequestCount
}

export function getMockQuotePayloads(): Record<string, unknown>[] {
  return [...quotePayloads]
}

export interface MockCreateOrderInput {
  source?: unknown
  address_id?: unknown
  idempotency_key?: unknown
  /** 0/缺省表示不使用优惠券（与后端一致）。 */
  coupon_id?: unknown
}

export interface MockQuoteInput {
  source?: unknown
  address_id?: unknown
  sku_code?: unknown
  sku_id?: unknown
  quantity?: unknown
  coupon_id?: unknown
}

type MockOrderResult =
  | { ok: true; order: Order }
  | { ok: false; status: number; code: number; message: string }

type MockQuoteResult =
  | { ok: true; quote: OrderQuote }
  | { ok: false; status: number; code: number; message: string }

type MockFailure = { ok: false; status: number; code: number; message: string }

/** 读取本人已勾选购物车条目并按 Mock 目录重新定价（与后端 buildLines 同口径）。 */
function resolveSelectedLines(
  userId: number,
): { ok: true; lines: { sku: MockSkuInfo; quantity: number }[] } | MockFailure {
  const selected = listMockCart(userId).filter((item) => item.selected)

  if (selected.length === 0) {
    return { ok: false, status: 400, code: 9004, message: '购物车无勾选商品' }
  }

  const lines: { sku: MockSkuInfo; quantity: number }[] = []

  for (const item of selected) {
    const sku = resolveSku(item.sku_id)

    if (!sku || !sku.available) {
      return { ok: false, status: 409, code: 9003, message: '商品不可购买' }
    }

    lines.push({ sku, quantity: item.quantity })
  }

  return { ok: true, lines }
}

/** 六槽位金额（与 internal/logic/pricing 同公式，积分恒 0）。 */
function computeMockAmounts(
  userId: number,
  lines: readonly { sku: MockSkuInfo; quantity: number }[],
  couponId: number,
): { ok: true; amounts: MockAmounts } | MockFailure {
  const originalAmount = lines.reduce(
    (total, line) => total + line.sku.price * line.quantity,
    0,
  )

  // 用券校验与优惠计算（对齐后端 GetDiscount：归属/有效期/门槛）。
  let couponDiscount = 0

  if (couponId > 0) {
    const evaluated = evaluateMockCoupon(userId, couponId, originalAmount)

    if (!evaluated.ok) {
      return evaluated
    }

    couponDiscount = evaluated.discountAmount
  }

  const memberDiscount = mockMemberDiscount(originalAmount, couponDiscount)
  const pointsDiscount = 0
  const payableAmount =
    originalAmount - couponDiscount - memberDiscount - pointsDiscount + shippingFee

  return {
    ok: true,
    amounts: {
      originalAmount,
      couponDiscount,
      memberDiscount,
      pointsDiscount,
      shippingFee,
      payableAmount,
    },
  }
}

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

  // coupon_id：缺省/0 表示不使用；与后端一致，负数属于参数错误。
  const couponId = input.coupon_id === undefined ? 0 : Number(input.coupon_id)

  if (!Number.isSafeInteger(couponId) || couponId < 0) {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  const linesResult = resolveSelectedLines(userId)

  if (!linesResult.ok) {
    return linesResult
  }

  const resolved = linesResult.lines

  const address: Address | undefined = listMockAddresses(userId).find(
    (item) => item.id === addressId,
  )

  if (!address) {
    return { ok: false, status: 404, code: 7001, message: '地址不存在' }
  }

  const amountsResult = computeMockAmounts(userId, resolved, couponId)

  if (!amountsResult.ok) {
    return amountsResult
  }

  const amounts = amountsResult.amounts

  const hash = requestHash(
    addressId,
    couponId,
    resolved.map((line) => ({ skuId: line.sku.skuId, quantity: line.quantity })),
  )
  const existing = orders.find(
    (order) => order.user_id === userId && order.idempotency_key === idempotencyKey,
  )

  if (existing) {
    if (existing.request_hash !== hash) {
      return { ok: false, status: 409, code: 9006, message: '幂等键冲突' }
    }

    // 幂等重试：券可能已被本次下单锁定，直接返回既有订单（后端同语义）。
    return { ok: true, order: existing }
  }

  // 幂等检查之后再判定券状态：重复请求命中既有订单时不应因「券已锁定」被拒。
  if (couponId > 0) {
    const coupon = getMockCoupon(userId, couponId)

    if (!coupon) {
      return { ok: false, status: 404, code: 19003, message: '优惠券不存在' }
    }

    if (coupon.status !== 'available') {
      return { ok: false, status: 409, code: 19007, message: '优惠券不可用' }
    }
  }

  sequence += 1
  const createdAt = nowText()

  const items: OrderItem[] = resolved.map((line, index) => ({
    id: sequence * 100 + index + 1,
    sku_id: line.sku.skuId,
    sku_code: line.sku.skuCode,
    product_id: line.sku.productId,
    sku_name: line.sku.skuName,
    spec_text: line.sku.specText,
    product_name: line.sku.productName,
    product_main_image: line.sku.mainImage,
    price: line.sku.price,
    original_price: line.sku.originalPrice,
    quantity: line.quantity,
  }))

  // 与后端 generateOrderNo 一致：13 位毫秒时间戳 + 12 位随机 hex。
  const orderNo = `${Date.now()}${Math.random().toString(16).slice(2, 14).padEnd(12, '0')}`

  const order: MockOrderRecord = {
    id: sequence,
    order_no: orderNo,
    user_id: userId,
    status: 'pending_payment',
    total_amount: amounts.payableAmount,
    original_amount: amounts.originalAmount,
    discount_amount: amounts.couponDiscount,
    member_discount_amount: amounts.memberDiscount,
    shipping_fee: amounts.shippingFee,
    pay_type: '',
    user_coupon_id: couponId > 0 ? couponId : null,
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

  // 与真实后端同一事务：下单即预占券（available → locked）。
  if (couponId > 0) {
    lockMockCoupon(userId, couponId, order.id)
  }

  // 与真实后端一致：订单事务内删除已购（已勾选）条目。
  removeMockCartItemsBySkuIds(
    userId,
    resolved.map((line) => line.sku.skuId),
  )

  return { ok: true, order }
}

/**
 * 订单试算（对齐 internal/logic/order/order.go Quote）：
 * - 只读：不写订单、不扣库存、不锁定券、不清理购物车；
 * - 校验来源/地址归属/购物车勾选/券可用性（只读）；
 * - items 使用服务端（Mock 目录）权威价格。
 */
export function quoteMockOrder(userId: number, input: MockQuoteInput): MockQuoteResult {
  quoteRequestCount += 1
  quotePayloads = [...quotePayloads, { ...input }]

  if (forcedQuoteError) {
    const error = forcedQuoteError
    forcedQuoteError = null
    return { ok: false, ...error }
  }

  const source = input.source

  if (source !== 'cart' && source !== 'direct') {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  const addressId = Number(input.address_id)
  const couponId = input.coupon_id === undefined ? 0 : Number(input.coupon_id)

  if (!Number.isSafeInteger(addressId) || addressId <= 0) {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  if (!Number.isSafeInteger(couponId) || couponId < 0) {
    return { ok: false, status: 400, code: 1001, message: '参数错误' }
  }

  const address = listMockAddresses(userId).find((item) => item.id === addressId)

  if (!address) {
    return { ok: false, status: 404, code: 7001, message: '地址不存在' }
  }

  let lines: { sku: MockSkuInfo; quantity: number }[]

  if (source === 'direct') {
    const skuCode = typeof input.sku_code === 'string' ? input.sku_code.trim() : ''
    const skuId = Number(input.sku_id ?? 0)
    const quantity = Number(input.quantity)

    if (!Number.isSafeInteger(quantity) || quantity < 1 || quantity > 999) {
      return { ok: false, status: 400, code: 1001, message: '参数错误' }
    }

    const sku =
      skuCode !== ''
        ? resolveSkuByCode(skuCode)
        : Number.isSafeInteger(skuId) && skuId > 0
          ? resolveSku(skuId)
          : undefined

    if (!sku || !sku.available) {
      return { ok: false, status: 409, code: 9003, message: '商品不可购买' }
    }

    lines = [{ sku, quantity }]
  } else {
    const linesResult = resolveSelectedLines(userId)

    if (!linesResult.ok) {
      return linesResult
    }

    lines = linesResult.lines
  }

  const amountsResult = computeMockAmounts(userId, lines, couponId)

  if (!amountsResult.ok) {
    return amountsResult
  }

  const { amounts } = amountsResult
  const items: OrderQuoteItem[] = lines.map((line) => ({
    sku_id: line.sku.skuId,
    sku_code: line.sku.skuCode,
    product_id: line.sku.productId,
    name: line.sku.skuName,
    spec_text: line.sku.specText,
    price: line.sku.price,
    quantity: line.quantity,
    amount: line.sku.price * line.quantity,
  }))

  return {
    ok: true,
    quote: {
      original_amount: amounts.originalAmount,
      coupon_discount: amounts.couponDiscount,
      member_discount: amounts.memberDiscount,
      points_discount: amounts.pointsDiscount,
      shipping_fee: amounts.shippingFee,
      payable_amount: amounts.payableAmount,
      items,
    },
  }
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
  member_discount_amount?: number
  shipping_fee?: number
  pay_type?: Order['pay_type']
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
  sku_code: 'SKU-SEED-0001',
  product_id: 9_001,
  sku_name: '默认规格',
  spec_text: '',
  product_name: '测试商品',
  product_main_image: '',
  price: 1_000,
  original_price: 1_000,
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
  const memberDiscountAmount = input.member_discount_amount ?? 0
  const fee = input.shipping_fee ?? 0

  const order: MockOrderRecord = {
    id: sequence,
    order_no: `${String(Date.now())}${Math.random().toString(16).slice(2, 14).padEnd(12, '0')}`,
    user_id: userId,
    status: input.status ?? 'pending_payment',
    total_amount:
      input.total_amount ?? originalAmount - discountAmount - memberDiscountAmount + fee,
    original_amount: originalAmount,
    discount_amount: discountAmount,
    member_discount_amount: memberDiscountAmount,
    shipping_fee: fee,
    pay_type: input.pay_type ?? '',
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

  // 与真实后端一致：支付事务内核销券（locked → used）。
  markMockCouponUsedOfOrder(record.id)

  return {
    ok: true,
    order: replaceOrder({
      ...record,
      status: 'paid',
      // 与真实后端一致：pay_type 缺省按 mock 落库（余额支付为 balance）。
      pay_type: 'mock',
      paid_at: nowText(),
      updated_at: nowText(),
    }),
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

  // 与真实后端一致：取消事务内释放券（locked → available）。
  releaseMockCouponOfOrder(record.id)

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
