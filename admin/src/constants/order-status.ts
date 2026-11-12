/**
 * 订单状态整数枚举 → 中文名称映射。
 *
 * 依据（仓库内真实定义，非推测）：
 * - `internal/logic/order/order.go` 状态机常量：
 *   statusPendingPayment=10 / statusPaid=20 / statusShipped=30 /
 *   statusReceived=40 / statusCompleted=50 / statusCancelled=60 / statusRefunded=70
 * - `internal/logic/dashboard/dashboard.go`：
 *   `allOrderStatuses = {10,20,30,40,50,60,70}`；
 *   金额与销量口径仅统计 `{20,30,40,50}`（已支付/已发货/已收货/已完成），
 *   排除待支付(10)、已取消(60)、已退款(70)。
 *
 * 注意：这里的整数状态是**后台聚合接口**（`/admin/dashboard/orders/status`）的口径，
 * 与前台普通订单 API 的字符串状态不是同一套表示，不要混用。
 */

/** 订单状态整数码 */
export const ORDER_STATUS = {
  PENDING_PAYMENT: 10,
  PAID: 20,
  SHIPPED: 30,
  RECEIVED: 40,
  COMPLETED: 50,
  CANCELLED: 60,
  REFUNDED: 70
} as const;

/** 订单状态整数码 → 中文名称 */
export const ORDER_STATUS_LABELS: Record<number, string> = {
  [ORDER_STATUS.PENDING_PAYMENT]: "待支付",
  [ORDER_STATUS.PAID]: "已支付",
  [ORDER_STATUS.SHIPPED]: "已发货",
  [ORDER_STATUS.RECEIVED]: "已收货",
  [ORDER_STATUS.COMPLETED]: "已完成",
  [ORDER_STATUS.CANCELLED]: "已取消",
  [ORDER_STATUS.REFUNDED]: "已退款"
};

/**
 * 取得状态展示名。
 *
 * 遇到映射表中不存在的状态码时**不猜测语义**，原样返回 `状态 <code>`，
 * 便于发现后端新增状态后及时补充映射。
 */
export function formatOrderStatus(status: number): string {
  return ORDER_STATUS_LABELS[status] ?? `状态 ${status}`;
}

/** 是否为已知状态码 */
export function isKnownOrderStatus(status: number): boolean {
  return status in ORDER_STATUS_LABELS;
}
