import type { OrderStatus } from '@/api/orders'
import { cn } from '@/utils/cn'
import { describeOrderStatus, describeOrderStatusTone, type OrderStatusTone } from '@/utils/orders'

const toneClass: Record<OrderStatusTone, string> = {
  warning: 'bg-warning-soft text-warning',
  primary: 'bg-primary-soft text-primary',
  success: 'bg-success-soft text-success',
  neutral: 'bg-gray-100 text-gray-600',
  danger: 'bg-danger-soft text-danger',
}

export interface OrderStatusBadgeProps {
  status: OrderStatus | string
  className?: string
}

/**
 * 订单状态徽标。
 *
 * 只做「状态枚举 → 中文文案 + 颜色」的映射，不推断状态含义；
 * 文案来源 utils/orders.ts 的 ORDER_STATUS_LABELS（与后端 7 态一一对应）。
 */
export function OrderStatusBadge({ status, className }: OrderStatusBadgeProps) {
  return (
    <span
      // data-status 保留后端原始枚举，便于测试与样式钩子区分「状态徽标」和
      // 进度组件里同名的步骤文案（例如「待支付」同时是状态与第一个节点）。
      data-status={status}
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium',
        toneClass[describeOrderStatusTone(status)],
        className,
      )}
    >
      {describeOrderStatus(status)}
    </span>
  )
}
