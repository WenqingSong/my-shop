import type { Order, OrderStatus } from '@/api/orders'

/**
 * 订单展示工具。
 *
 * 事实来源（逐项对齐后端源码，未做任何推断扩展）：
 * - 状态枚举：api/order/v1/order.go 的 Status* 常量（7 态）；
 * - 取消原因：api/order/v1/order.go 的 CancelReason* 常量（1=用户、2=超时）；
 * - 金额：orders 表三列快照 original_amount / discount_amount / total_amount；
 * - 时间字段：created_at / paid_at / shipped_at / received_at / completed_at /
 *   cancelled_at / refunded_at，未发生的为 null。
 *
 * 本模块只做「文案、分组、进度推导、格式化」，**不计算任何金额**：
 * 订单金额一律直接展示后端返回的整数字段，因此这里没有 sum/discount 之类的函数。
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

/**
 * 状态徽标色调（只影响展示，不改变状态语义）。
 * 与 DESIGN_SYSTEM.md 的 success / warning / danger / primary / neutral 语义一致。
 */
export type OrderStatusTone = 'warning' | 'primary' | 'success' | 'neutral' | 'danger'

export function describeOrderStatusTone(status: OrderStatus | string): OrderStatusTone {
  switch (status) {
    case 'pending_payment':
      return 'warning'
    case 'paid':
    case 'shipped':
      return 'primary'
    case 'received':
    case 'completed':
      return 'success'
    case 'refunded':
      return 'danger'
    case 'cancelled':
    default:
      return 'neutral'
  }
}

/**
 * 列表页状态分组（**纯前端分组**，不是后端状态）。
 *
 * 后端只有 7 个状态，没有「订单状态分类」字段，也没有状态筛选接口。
 * 分组映射：
 * - 待支付 = pending_payment
 * - 待发货 = paid（已付款、等商家发货）
 * - 待收货 = shipped（已发货、等用户确认收货）
 * - 已完成 = completed；`received` 也归入此组，因为后端 Receive 在同一事务里
 *   30→40→50，`received` 对外基本不可观测（见 internal/logic/order/order.go）；
 * - 已关闭 = cancelled + refunded（两个终态，都不可再继续履约）
 */
export type OrderStatusGroupId =
  | 'all'
  | 'pending_payment'
  | 'pending_shipment'
  | 'pending_receipt'
  | 'completed'
  | 'closed'

export interface OrderStatusGroup {
  id: OrderStatusGroupId
  label: string
  /** 该分组包含的后端状态；`all` 为空数组，表示不过滤。 */
  statuses: readonly OrderStatus[]
}

export const ORDER_STATUS_GROUPS: readonly OrderStatusGroup[] = [
  { id: 'all', label: '全部', statuses: [] },
  { id: 'pending_payment', label: '待支付', statuses: ['pending_payment'] },
  { id: 'pending_shipment', label: '待发货', statuses: ['paid'] },
  { id: 'pending_receipt', label: '待收货', statuses: ['shipped'] },
  { id: 'completed', label: '已完成', statuses: ['received', 'completed'] },
  { id: 'closed', label: '已关闭', statuses: ['cancelled', 'refunded'] },
]

/** 按分组过滤订单（本地过滤，不产生网络请求）。 */
export function filterOrdersByGroup(
  orders: readonly Order[],
  groupId: OrderStatusGroupId,
): Order[] {
  const group = ORDER_STATUS_GROUPS.find((item) => item.id === groupId)

  if (!group || group.statuses.length === 0) {
    return [...orders]
  }

  return orders.filter((order) => group.statuses.includes(order.status))
}

/** 取消原因文案（api/order/v1/order.go：1=用户取消、2=超时未支付自动取消）。 */
export function describeCancelReason(reason: number | null): string {
  if (reason === 1) {
    return '用户主动取消'
  }

  if (reason === 2) {
    return '超时未支付自动取消'
  }

  return '—'
}

/**
 * 订单生命周期进度。
 *
 * 重要边界：后端**没有状态历史/事件流接口**，这里展示的是「由当前状态推导」的进度，
 * 不是真实事件记录。页面必须显式标注这一点，不得把推导结果包装成"订单轨迹"。
 */
export type OrderProgressState = 'done' | 'current' | 'todo'

export interface OrderProgressStep {
  key: 'payment' | 'shipment' | 'receipt' | 'completion'
  label: string
  state: OrderProgressState
}

export interface OrderProgress {
  steps: OrderProgressStep[]
  /** 终止态（已取消/已退款）：流程不会继续推进。 */
  halted: boolean
  /** 终止态说明；未终止为 null。 */
  haltedLabel: string | null
}

const PROGRESS_STEPS = [
  { key: 'payment', label: '待支付' },
  { key: 'shipment', label: '待发货' },
  { key: 'receipt', label: '待收货' },
  { key: 'completion', label: '已完成' },
] as const

function buildSteps(states: readonly OrderProgressState[]): OrderProgressStep[] {
  return PROGRESS_STEPS.map((step, index) => ({
    key: step.key,
    label: step.label,
    state: states[index] ?? 'todo',
  }))
}

export function buildOrderProgress(status: OrderStatus | string): OrderProgress {
  switch (status) {
    case 'pending_payment':
      return { steps: buildSteps(['current', 'todo', 'todo', 'todo']), halted: false, haltedLabel: null }
    case 'paid':
      return { steps: buildSteps(['done', 'current', 'todo', 'todo']), halted: false, haltedLabel: null }
    case 'shipped':
      return { steps: buildSteps(['done', 'done', 'current', 'todo']), halted: false, haltedLabel: null }
    case 'received':
    case 'completed':
      return { steps: buildSteps(['done', 'done', 'done', 'done']), halted: false, haltedLabel: null }
    case 'refunded':
      // 退款只能从「已支付未发货」发起：支付已完成，履约在发货前终止。
      return {
        steps: buildSteps(['done', 'current', 'todo', 'todo']),
        halted: true,
        haltedLabel: '订单已退款，交易终止',
      }
    case 'cancelled':
    default:
      return {
        steps: buildSteps(['todo', 'todo', 'todo', 'todo']),
        halted: true,
        haltedLabel: '订单已取消，交易终止',
      }
  }
}

/** 收货地址快照拼接（省市区 + 详细地址）。 */
export function formatOrderAddress(order: Order): string {
  return `${order.province}${order.city}${order.district}${order.detail}`
}

/** 订单商品总件数（仅用于展示，不参与金额计算）。 */
export function orderTotalQuantity(order: Order): number {
  return order.items.reduce((total, item) => total + item.quantity, 0)
}

/**
 * 格式化后端返回的时间字符串（`YYYY-MM-DD HH:mm:ss`）为可读文本。
 *
 * 只截断、不转换时区、不补默认值：后端返回什么就展示什么。
 * 详见 W4-01 开发知识文档「时区」一节——后端同一响应内的 created_at 与 expire_at
 * 采用了不同的时区表示，前端不做任何隐式偏移，避免把数据问题伪装成正确时间。
 */
export function formatOrderTime(value: string | null): string {
  return value ? value.slice(0, 19) : '—'
}
