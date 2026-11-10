/**
 * 订单 DTO（与 `api/order/v1/order.go` 字段一一对应）。
 *
 * 金额单位：**整数分**（int64）。页面展示统一在 adapter 中转换为元。
 * 时间：GoFrame `gtime.Time`，序列化为 `YYYY-MM-DD HH:mm:ss`，可空字段为 `null`。
 *
 * 注意：
 * - 后台订单接口只覆盖 `orders` 表（普通订单）；秒杀订单在 `flash_sale_orders`
 *   独立表，不在此 API 返回范围内。
 * - 后端订单结构**没有** `source`（下单来源）与物流公司/运单号字段，
 *   因此前端不展示这些内容。
 */
export type OrderStatus =
  | "pending_payment"
  | "paid"
  | "shipped"
  | "received"
  | "completed"
  | "cancelled"
  | "refunded";

/** 取消原因：1=用户取消，2=超时未支付 */
export type OrderCancelReason = 1 | 2;

/** 订单项（商品/SKU/价格快照，自足） */
export interface OrderItemDTO {
  id: number;
  sku_id: number;
  product_id: number;
  /** SKU 名称快照，不是当前 SKU 名称 */
  sku_name: string;
  /** 商品名快照 */
  product_name: string;
  /** 商品主图快照，可能为空串 */
  product_main_image: string;
  /** 成交价（整数分） */
  price: number;
  quantity: number;
}

/** 订单（详情接口返回，含明细） */
export interface OrderDTO {
  id: number;
  order_no: string;
  user_id: number;
  status: OrderStatus;
  /** 应付金额 = original_amount - discount_amount（整数分） */
  total_amount: number;
  /** 优惠前金额（整数分） */
  original_amount: number;
  /** 优惠金额（整数分） */
  discount_amount: number;
  user_coupon_id: number | null;
  idempotency_key: string;
  recipient_name: string;
  phone: string;
  province: string;
  city: string;
  district: string;
  detail: string;
  address_id: number | null;
  expire_at: string | null;
  cancel_reason: OrderCancelReason | null;
  paid_at: string | null;
  shipped_at: string | null;
  received_at: string | null;
  completed_at: string | null;
  cancelled_at: string | null;
  refunded_at: string | null;
  created_at: string | null;
  updated_at: string | null;
  items: OrderItemDTO[];
}

/** 后台订单列表项（轻量，不含明细与收货信息） */
export interface AdminOrderListItemDTO {
  id: number;
  order_no: string;
  user_id: number;
  status: OrderStatus;
  total_amount: number;
  created_at: string | null;
}

/** 后台订单列表查询参数 */
export interface AdminOrderListQuery {
  page?: number;
  size?: number;
  /** 订单号，**精确匹配** */
  order_no?: string;
  /** 用户 id，精确匹配（后端拒绝 <=0） */
  user_id?: number;
  status?: OrderStatus;
  /** 创建时间起点，RFC3339（含时区偏移），左闭 */
  start_time?: string;
  /** 创建时间终点，RFC3339（含时区偏移），右开 */
  end_time?: string;
  sort?: "id" | "created_at";
  order?: "asc" | "desc";
}

/** 后台订单列表响应 */
export interface AdminOrderListResult {
  items: AdminOrderListItemDTO[];
  total: number;
  page: number;
  size: number;
}
