import type { Order, OrderStatus } from '@/api/orders'

/**
 * 订单展示工具。
 *
 * 注意：这里不计算任何金额，只做状态文案与地址拼接；
 * 订单金额一律直接展示后端返回的整数字段（total_amount / original_amount / discount_amount）。
 */

export const ORDER_STATUS_LABELS: Record<OrderStatus, string> = {
  pending_payment: '待支付',
  paid: '已支付',
  shipped: '已发货',
  received: '已收货',
  completed: '已完成',
  cancelled: '已取消',
  refunded: '已退款',
}

export function describeOrderStatus(status: OrderStatus | string): string {
  return ORDER_STATUS_LABELS[status as OrderStatus] ?? status
}

/** 收货地址快照拼接（省市区 + 详细地址）。 */
export function formatOrderAddress(order: Order): string {
  return `${order.province}${order.city}${order.district}${order.detail}`
}

/** 订单商品总件数（仅用于展示，不参与金额计算）。 */
export function orderTotalQuantity(order: Order): number {
  return order.items.reduce((total, item) => total + item.quantity, 0)
}

/** 格式化后端返回的时间字符串（YYYY-MM-DD HH:mm:ss）为可读文本。 */
export function formatOrderTime(value: string | null): string {
  return value ? value.slice(0, 19) : '—'
}
