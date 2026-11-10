/**
 * 订单 Adapter：DTO → 视图模型、筛选参数拼装、状态语义。
 *
 * 金额全部以**整数分**参与计算与传输，仅在展示时转元；
 * 不使用浮点数做金额运算（行小计 = price × quantity，均为整数）。
 *
 * 状态枚举来自 `api/order/v1/order.go`，与 DB TINYINT 10~70 对应：
 * pending_payment(10) / paid(20) / shipped(30) / received(40) /
 * completed(50) / cancelled(60) / refunded(70)。
 */
import { formatCentsAsCny } from "@/utils/money";
import { formatServerDateTime, toRFC3339 } from "@/utils/datetime";
import type {
  AdminOrderListQuery,
  AdminOrderListItemDTO,
  OrderDTO,
  OrderItemDTO,
  OrderStatus
} from "../types/order";

type TagType = "success" | "info" | "warning" | "danger" | "primary";

interface OrderStatusMeta {
  label: string;
  tag: TagType;
  /** 是否为不可再流转的终态（仅用于展示提示，不作为操作依据） */
  terminal: boolean;
}

const ORDER_STATUS_META: Record<OrderStatus, OrderStatusMeta> = {
  pending_payment: { label: "待支付", tag: "warning", terminal: false },
  paid: { label: "已支付", tag: "primary", terminal: false },
  shipped: { label: "已发货", tag: "info", terminal: false },
  received: { label: "已收货", tag: "success", terminal: false },
  completed: { label: "已完成", tag: "success", terminal: true },
  cancelled: { label: "已取消", tag: "info", terminal: true },
  refunded: { label: "已退款", tag: "danger", terminal: true }
};

/** 状态筛选项（顺序与后端统计顺序一致） */
export const ORDER_STATUS_OPTIONS: Array<{
  value: OrderStatus;
  label: string;
}> = (
  [
    "pending_payment",
    "paid",
    "shipped",
    "received",
    "completed",
    "cancelled",
    "refunded"
  ] as OrderStatus[]
).map(value => ({ value, label: ORDER_STATUS_META[value].label }));

/** 状态文案；未知值不猜测语义 */
export function formatOrderStatus(status: string): string {
  return (
    ORDER_STATUS_META[status as OrderStatus]?.label ?? `未知状态（${status}）`
  );
}

/** 状态标签颜色；未知值用 danger 提示后端枚举变化 */
export function orderStatusTagType(status: string): TagType {
  return ORDER_STATUS_META[status as OrderStatus]?.tag ?? "danger";
}

/** 是否已进入终态（仅展示用） */
export function isTerminalOrderStatus(status: string): boolean {
  return ORDER_STATUS_META[status as OrderStatus]?.terminal ?? false;
}

const CANCEL_REASON_LABELS: Record<number, string> = {
  1: "用户取消",
  2: "超时未支付"
};

/** 取消原因文案 */
export function formatCancelReason(reason: number | null | undefined): string {
  if (reason === null || reason === undefined) return "—";
  return CANCEL_REASON_LABELS[reason] ?? `未知原因（${reason}）`;
}

/**
 * 后台是否允许发货。
 *
 * 后端只允许 paid → shipped，并且在事务内检查是否存在有效退款申请
 * （applying/pending_refund/refund_failed）；后者前端无法提前判断，
 * 以接口返回的 18005 为准。
 */
export function canShip(status: string): boolean {
  return status === "paid";
}

/** 订单项视图 */
export interface OrderItemView {
  id: number;
  skuId: number;
  productId: number;
  productName: string;
  skuName: string;
  image: string;
  priceCents: number;
  priceText: string;
  quantity: number;
  /** 行小计（整数分） */
  subtotalCents: number;
  subtotalText: string;
}

/** 订单项 DTO → 视图（使用下单时快照，不查当前商品） */
export function toOrderItemView(dto: OrderItemDTO): OrderItemView {
  const subtotalCents = dto.price * dto.quantity;
  return {
    id: dto.id,
    skuId: dto.sku_id,
    productId: dto.product_id,
    productName: dto.product_name,
    skuName: dto.sku_name,
    image: dto.product_main_image || "",
    priceCents: dto.price,
    priceText: formatCentsAsCny(dto.price),
    quantity: dto.quantity,
    subtotalCents,
    subtotalText: formatCentsAsCny(subtotalCents)
  };
}

/** 订单视图（详情页使用） */
export interface OrderView {
  id: number;
  orderNo: string;
  userId: number;
  status: OrderStatus;
  statusLabel: string;
  statusTagType: TagType;
  totalCents: number;
  totalText: string;
  originalCents: number;
  originalText: string;
  discountCents: number;
  discountText: string;
  recipientName: string;
  phone: string;
  addressText: string;
  createdText: string;
  paidText: string;
  shippedText: string;
  receivedText: string;
  completedText: string;
  cancelledText: string;
  refundedText: string;
  expireText: string;
  cancelReasonText: string;
  /** 商品总件数（Σ quantity） */
  itemCount: number;
  items: OrderItemView[];
}

/** 拼接收货地址；后端未脱敏时原样展示 */
export function formatAddress(parts: Array<string | null | undefined>): string {
  return parts
    .map(part => (part ?? "").trim())
    .filter(Boolean)
    .join(" ");
}

/** 订单 DTO → 视图 */
export function toOrderView(dto: OrderDTO): OrderView {
  const items = (dto.items ?? []).map(toOrderItemView);
  return {
    id: dto.id,
    orderNo: dto.order_no,
    userId: dto.user_id,
    status: dto.status,
    statusLabel: formatOrderStatus(dto.status),
    statusTagType: orderStatusTagType(dto.status),
    totalCents: dto.total_amount,
    totalText: formatCentsAsCny(dto.total_amount),
    originalCents: dto.original_amount,
    originalText: formatCentsAsCny(dto.original_amount),
    discountCents: dto.discount_amount,
    discountText: formatCentsAsCny(dto.discount_amount),
    recipientName: dto.recipient_name || "—",
    phone: dto.phone || "—",
    addressText:
      formatAddress([dto.province, dto.city, dto.district, dto.detail]) || "—",
    createdText: formatServerDateTime(dto.created_at),
    paidText: formatServerDateTime(dto.paid_at),
    shippedText: formatServerDateTime(dto.shipped_at),
    receivedText: formatServerDateTime(dto.received_at),
    completedText: formatServerDateTime(dto.completed_at),
    cancelledText: formatServerDateTime(dto.cancelled_at),
    refundedText: formatServerDateTime(dto.refunded_at),
    expireText: formatServerDateTime(dto.expire_at),
    cancelReasonText: formatCancelReason(dto.cancel_reason),
    itemCount: items.reduce((sum, item) => sum + item.quantity, 0),
    items
  };
}

/** 后台列表行视图 */
export interface OrderListRowView {
  id: number;
  orderNo: string;
  userId: number;
  status: OrderStatus;
  statusLabel: string;
  statusTagType: TagType;
  totalCents: number;
  totalText: string;
  createdText: string;
}

/** 列表 DTO → 视图 */
export function toOrderListRowView(
  dto: AdminOrderListItemDTO
): OrderListRowView {
  return {
    id: dto.id,
    orderNo: dto.order_no,
    userId: dto.user_id,
    status: dto.status,
    statusLabel: formatOrderStatus(dto.status),
    statusTagType: orderStatusTagType(dto.status),
    totalCents: dto.total_amount,
    totalText: formatCentsAsCny(dto.total_amount),
    createdText: formatServerDateTime(dto.created_at)
  };
}

/** 商品摘要（列表页用）：首件商品名 + 种类数，不替代详情快照 */
export function buildItemsSummary(items: OrderItemDTO[] | undefined): string {
  if (!items || items.length === 0) return "—";
  const first = items[0].product_name || items[0].sku_name || "未知商品";
  return items.length > 1 ? `${first} 等 ${items.length} 种商品` : first;
}

/** 商品总件数 */
export function sumItemQuantity(items: OrderItemDTO[] | undefined): number {
  if (!items) return 0;
  return items.reduce((sum, item) => sum + item.quantity, 0);
}

/** 列表筛选输入（页面状态） */
export interface OrderListInput {
  page: number;
  size: number;
  orderNo?: string;
  userId?: number | null;
  status?: OrderStatus | "";
  /** 下单时间范围（本地时间，左闭右开） */
  range?: [Date, Date] | null;
  sort?: "id" | "created_at";
  order?: "asc" | "desc";
}

/**
 * 页面筛选 → 后端查询参数。
 *
 * 时间范围转换为带本地时区偏移的 RFC3339；后端要求 start < end，
 * 非法/空的区间直接丢弃，不发送半截参数。
 */
export function toOrderListQuery(input: OrderListInput): AdminOrderListQuery {
  const query: AdminOrderListQuery = {
    page: input.page,
    size: input.size,
    sort: input.sort ?? "id",
    order: input.order ?? "desc"
  };

  const orderNo = input.orderNo?.trim();
  if (orderNo) query.order_no = orderNo;

  if (typeof input.userId === "number" && input.userId > 0) {
    query.user_id = input.userId;
  }
  if (input.status) query.status = input.status;

  const range = input.range;
  if (range && range.length === 2) {
    const [start, end] = range;
    if (
      start instanceof Date &&
      end instanceof Date &&
      Number.isFinite(start.getTime()) &&
      Number.isFinite(end.getTime()) &&
      start.getTime() < end.getTime()
    ) {
      query.start_time = toRFC3339(start);
      query.end_time = toRFC3339(end);
    }
  }

  return query;
}
