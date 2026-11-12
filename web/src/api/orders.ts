import { authGetData, authPostData } from '@/api/auth-request'

/**
 * 订单 API（均需登录，作用于 Principal.UserID）。
 *
 * 事实来源：
 * - api/order/v1/order.go（Order / OrderItem / QuoteReq / QuoteRes / CreateReq）
 * - internal/cmd/routes_frontend.go（POST /orders、POST /orders/quote、GET /orders、GET /orders/:id）
 * - internal/logic/order/order.go（服务端定价、六槽位金额、单事务、幂等键）
 * - internal/logic/pricing/pricing.go（唯一计价入口与守恒公式）
 *
 * 金额口径（整数分）：
 * - `discount_amount` 只表示**优惠券优惠**，不包含会员折扣；
 * - `member_discount_amount` 是会员等级折扣，独立字段；
 * - `total_amount = original_amount - discount_amount - member_discount_amount + shipping_fee`
 *   （积分抵扣当前恒 0，后端没有 points_discount 订单字段）。
 *
 * 试算边界：`POST /orders/quote` 只读预览，**不检查库存、不占用优惠券、不落库**；
 * 金额也不是最终可信金额，创建订单时服务端会在事务内重新读取权威价格并复核。
 */

/** 订单状态枚举（与 api/order/v1/order.go 一致）。 */
export type OrderStatus =
  | 'pending_payment'
  | 'paid'
  | 'shipped'
  | 'received'
  | 'completed'
  | 'cancelled'
  | 'refunded'

/** 下单来源：cart=购物车勾选项，direct=单 SKU 直接购买。 */
export type OrderSource = 'cart' | 'direct'

/** 支付方式；未支付订单为空串。 */
export type PayType = '' | 'mock' | 'balance'

/** 订单项：商品/SKU/价格快照，自足。 */
export interface OrderItem {
  id: number
  sku_id: number
  /** SKU 编码快照（下单时的 sku_code）。 */
  sku_code: string
  product_id: number
  sku_name: string
  /** 规格文本快照（如「黑色,M」；无规格为空串）。 */
  spec_text: string
  product_name: string
  product_main_image: string
  /** 成交价（整数分，下单时 skus.price 快照）。 */
  price: number
  /** 原价快照（整数分，下单时 skus.original_price）。 */
  original_price: number
  quantity: number
}

/** 订单（创建响应与详情接口共用）。全部金额为整数分，由后端计算。 */
export interface Order {
  id: number
  order_no: string
  user_id: number
  status: OrderStatus
  /**
   * 应付金额：
   * `original_amount - discount_amount - member_discount_amount + shipping_fee`
   * （积分抵扣当前恒 0；后端 pricing.Quote 计算）。
   */
  total_amount: number
  /** 优惠前金额 = Σ 订单项 price × quantity（后端计算）。 */
  original_amount: number
  /** 优惠券优惠金额；无券为 0。**不包含**会员折扣。 */
  discount_amount: number
  /** 会员等级折扣金额；无会员权益为 0。 */
  member_discount_amount: number
  /** 运费（整数分）；未绑定运费模板的商品为 0。 */
  shipping_fee: number
  /** 支付方式：mock / balance；未支付为空串。 */
  pay_type: PayType
  user_coupon_id: number | null
  idempotency_key: string
  recipient_name: string
  phone: string
  province: string
  city: string
  district: string
  detail: string
  address_id: number | null
  expire_at: string | null
  cancel_reason: number | null
  paid_at: string | null
  shipped_at: string | null
  received_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  refunded_at: string | null
  created_at: string | null
  updated_at: string | null
  items: OrderItem[]
}

/**
 * POST /orders 请求。
 *
 * - source=cart：服务端读取本人**全部已勾选**购物车条目下单，忽略 sku_id/quantity；
 * - address_id 必须属于当前用户（否则 7001）；
 * - idempotency_key 必填：同键同内容返回既有订单，同键不同内容返回 9006；
 * - coupon_id 缺省/0 表示不使用优惠券。
 */
export interface CreateOrderPayload {
  source: OrderSource
  address_id: number
  idempotency_key: string
  sku_id?: number
  quantity?: number
  coupon_id?: number
}

/** POST /orders：创建订单；返回完整订单（含订单项、金额快照与状态）。 */
export function createOrder(payload: CreateOrderPayload): Promise<Order> {
  return authPostData<Order>('/orders', payload)
}

/**
 * POST /orders/quote 请求（api/order/v1/order.go QuoteReq）。
 *
 * - source=cart：按本人当前**全部已勾选**购物车条目试算，忽略 sku_id/quantity；
 * - source=direct：sku_code 优先、兼容 sku_id，quantity 必填；
 * - address_id 必填且必须属于本人（决定运费与地址可用性）；
 * - coupon_id 只读校验，不占用、不核销。
 */
export interface OrderQuotePayload {
  source: OrderSource
  address_id: number
  sku_code?: string
  sku_id?: number
  quantity?: number
  coupon_id?: number
}

/** 算价明细行（服务端权威价格）。 */
export interface OrderQuoteItem {
  sku_id: number
  sku_code: string
  product_id: number
  name: string
  spec_text: string
  price: number
  quantity: number
  /** 行金额 = price × quantity（整数分）。 */
  amount: number
}

/**
 * POST /orders/quote 响应 data（QuoteRes）。
 *
 * 六槽位守恒：
 * `payable_amount = original_amount - coupon_discount - member_discount - points_discount + shipping_fee`。
 * `points_discount` 当前恒 0；试算不检查库存、不锁定券、不落库。
 */
export interface OrderQuote {
  original_amount: number
  coupon_discount: number
  member_discount: number
  points_discount: number
  shipping_fee: number
  payable_amount: number
  items: OrderQuoteItem[]
}

/**
 * POST /orders/quote：订单算价预览（只读）。
 *
 * 失败可能是地址不可用（7001）、购物车无勾选（9004）、券不可用（19003/19007）
 * 或运费无法计算（21008），调用方必须展示错误而不是回退到客户端估算。
 */
export function quoteOrder(payload: OrderQuotePayload, signal?: AbortSignal): Promise<OrderQuote> {
  return authPostData<OrderQuote>('/orders/quote', payload, { signal })
}

/**
 * GET /orders 响应 data。
 *
 * 后端只返回本人全部订单（`internal/logic/order/order.go` 的 List：`WHERE user_id = ? ORDER BY id DESC`），
 * **没有分页字段（无 total/page/size）、没有状态筛选参数**。
 * 因此列表页的状态筛选与分页全部在浏览器本地完成，不能向后端发送 status/page/size
 * （即使发送也会被静默忽略，不会报错——更危险，容易被误认为"服务端已筛选"）。
 */
export interface OrderListResult {
  items: Order[]
}

/**
 * GET /orders：查询本人订单列表（无需参数）。
 *
 * 注意：
 * - 每条订单都内嵌 `items[]`（后端逐单查询订单项，属 N+1 查询），因此一次请求会带回全部明细；
 * - 后端不做超时懒取消（只有 GET /orders/:id 会），列表里的 `pending_payment` 可能已经
 *   超过 `expire_at`，展示时必须允许这种状态存在，不能自行判定为已取消。
 */
export function fetchOrders(signal?: AbortSignal): Promise<OrderListResult> {
  return authGetData<OrderListResult>('/orders', { signal })
}

/** GET /orders/:id：查询本人订单详情；不存在或非本人统一 404/9001。 */
export function fetchOrder(orderId: number, signal?: AbortSignal): Promise<Order> {
  return authGetData<Order>(`/orders/${String(orderId)}`, { signal })
}

/**
 * 订单操作接口（全部是 POST + 仅 path 参数 + 空请求体）。
 *
 * 事实来源：
 * - `api/order/v1/order.go:112-143`：PayReq / CancelReq / ReceiveReq 只有 `Id`（path, required），
 *   没有任何 body 字段；
 * - `internal/controller/order/order.go:48-72`：三个 handler 都只取 Principal.UserID；
 * - `internal/logic/order/order.go:447-595`：状态闸门与幂等语义。
 *
 * 三个接口都返回「更新后的完整 Order」，前端必须用它覆盖缓存，
 * 而不是按自己的假设去改本地状态。
 *
 * 统一发送 `{}` 作为请求体：后端不读取 body，但显式空对象可以避免
 * 「无 Content-Type 的空 body」在某些网关/代理下被改写为非法请求。
 */

/** POST /orders/:id/pay（Mock 支付）：仅待支付(10)可入；已支付重复调用幂等成功；其余状态 9002。 */
export function payOrder(orderId: number): Promise<Order> {
  return authPostData<Order>(`/orders/${String(orderId)}/pay`, {})
}

/** POST /orders/:id/cancel：仅待支付(10)可取消；同事务恢复库存 + 释放优惠券；其余状态 9002。 */
export function cancelOrder(orderId: number): Promise<Order> {
  return authPostData<Order>(`/orders/${String(orderId)}/cancel`, {})
}

/** POST /orders/:id/receive：仅已发货(30)可确认；同一事务 30→40→50，响应恒为 completed。 */
export function receiveOrder(orderId: number): Promise<Order> {
  return authPostData<Order>(`/orders/${String(orderId)}/receive`, {})
}
