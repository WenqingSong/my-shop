import { authGetData, authPostData } from '@/api/auth-request'

/**
 * 订单 API（均需登录，作用于 Principal.UserID）。
 *
 * 事实来源：
 * - api/order/v1/order.go（Order / OrderItem / CreateReq / DetailReq）
 * - internal/cmd/routes_frontend.go（POST /orders、GET /orders、GET /orders/:id）
 * - internal/logic/order/order.go（服务端定价、单事务、幂等键）
 *
 * 重要：后端**没有独立的订单试算接口**。唯一能计算正式金额的入口是 POST /orders，
 * 它会在同一事务内创建订单、写订单项、扣库存并清理已购购物车条目。
 * 因此前端不得用创建接口做「试算」，购物车金额只能作为非权威预估展示。
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

/** 下单来源（本项目只使用 cart；direct 用于直接购买，W3-02 不接入）。 */
export type OrderSource = 'cart' | 'direct'

/** 订单项：商品/SKU/价格快照，自足。 */
export interface OrderItem {
  id: number
  sku_id: number
  product_id: number
  sku_name: string
  product_name: string
  product_main_image: string
  /** 成交价（整数分，下单时 skus.price 快照）。 */
  price: number
  quantity: number
}

/** 订单（创建响应与详情接口共用）。全部金额为整数分，由后端计算。 */
export interface Order {
  id: number
  order_no: string
  user_id: number
  status: OrderStatus
  /** 应付金额 = original_amount - discount_amount（后端计算）。 */
  total_amount: number
  /** 优惠前金额 = Σ 订单项 price × quantity（后端计算）。 */
  original_amount: number
  discount_amount: number
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
 * - coupon_id 缺省/0 表示不使用优惠券（本阶段不接入优惠券选择）。
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
