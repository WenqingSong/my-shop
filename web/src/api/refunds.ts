import { authGetData, authPostData } from '@/api/auth-request'

/**
 * 退款 API（均需登录，作用于 Principal.UserID）。
 *
 * 事实来源（逐项对齐后端源码，未做任何推断扩展）：
 * - `api/refund/v1/refund.go`：Refund / ApplyReq / RevokeReq / DetailReq / ListReq；
 * - `internal/cmd/routes_frontend.go:98-103`：
 *   `POST /orders/:id/refund`、`GET /refunds`、`GET /refunds/:id`、`POST /refunds/:id/revoke`；
 * - `internal/logic/refund/refund.go`：状态机、金额派生、归属隔离、单一有效申请约束。
 *
 * 关键边界（不得在前端"补齐"）：
 * 1. 只支持「已支付未发货普通订单」的**整单全额**退款，没有商品级/部分退款；
 * 2. 退款金额由服务端从 `orders.total_amount` 派生（整数分），请求体不接受金额字段；
 * 3. 申请需要管理员审核，用户侧没有审核/执行接口；
 * 4. 没有真实资金退回：`refund_succeeded` 是项目内部业务状态，
 *    由管理员人工 Mock 确认结果，不存在支付网关、支付流水或到账回调；
 * 5. 用户侧没有退款审计记录接口（`/admin/refunds/:id/audits` 需 `refund:view`），
 *    因此前端不能构造状态时间轴。
 */

/**
 * 退款状态枚举（与 `api/refund/v1/refund.go:10-18` 一一对应）。
 *
 * DB 存 TINYINT 10/20/30/40/50/60/70，API 出参为字符串。
 */
export type RefundStatus =
  | 'applying'
  | 'pending_refund'
  | 'refund_failed'
  | 'review_rejected'
  | 'revoked'
  | 'refund_succeeded'
  | 'closed'

/**
 * 退款申请（详情/列表/写接口共用结构）。
 *
 * 金额为整数分，等于下单时的 `orders.total_amount` 快照，由服务端派生；
 * 前端只展示、不参与任何计算。
 */
export interface Refund {
  id: number
  /** 关联订单 id（后端为软引用，没有内嵌订单快照）。 */
  order_id: number
  /** 申请者（订单所有者）id。 */
  user_id: number
  status: RefundStatus
  /** 退款金额（整数分，= 订单 total_amount 快照）。 */
  amount: number
  /** 申请原因（用户填写，1-255 字符）。 */
  reason: string
  /** 审核原因（管理员审核同意/拒绝时填写；未审核为空串）。 */
  review_reason: string
  /** 审核管理员 id（可空；用户侧不展示）。 */
  reviewed_by: number | null
  /** 审核时间（可空）。 */
  reviewed_at: string | null
  /** 执行 / 关闭原因（管理员填写；未执行/未关闭为空串）。 */
  execute_reason: string
  /** 执行 / 关闭管理员 id（可空；用户侧不展示）。 */
  executed_by: number | null
  /** 执行 / 关闭时间（可空）。 */
  executed_at: string | null
  created_at: string | null
  updated_at: string | null
}

/** GET /refunds 响应 data；后端只返回本人记录，按 id 倒序，无 total/page/size。 */
export interface RefundListResult {
  items: Refund[]
}

/** 退款申请原因最大字符数（与后端 `maxReasonLen = 255`、DB VARCHAR(255) 对齐）。 */
export const REFUND_REASON_MAX_LENGTH = 255

/**
 * POST /orders/:id/refund：发起整单退款申请。
 *
 * 后端前置条件（`internal/logic/refund/refund.go:125-196`）：
 * - 订单属于当前用户（否则 18001，404，防枚举）；
 * - `orders.status === 'paid'`（否则 18003，409）；
 * - `reason` 去空格后非空且 ≤255 字符（否则 1001，400）；
 * - 同一订单至多一条有效申请（applying/pending_refund/refund_failed），
 *   重复申请由 `uk_active` 兜底返回 18004（409）。
 *
 * 请求体只有 `reason`，**没有金额字段**：金额由服务端从订单快照派生。
 * 成功返回创建后的完整 Refund（status 恒为 applying）。
 */
export function applyRefund(orderId: number, reason: string): Promise<Refund> {
  return authPostData<Refund>(`/orders/${String(orderId)}/refund`, { reason })
}

/**
 * GET /refunds：查询本人退款记录列表。
 *
 * 后端**没有分页、没有状态筛选**，只支持可选的 `order_id` 精确过滤
 * （`internal/logic/refund/refund.go:273-287`）。因此：
 * - 状态筛选与分页只能在浏览器本地完成；
 * - 传入 order_id 时由服务端过滤，属于真实的服务端过滤能力。
 */
export function fetchRefunds(orderId?: number, signal?: AbortSignal): Promise<RefundListResult> {
  return authGetData<RefundListResult>('/refunds', {
    query: orderId && orderId > 0 ? { order_id: orderId } : undefined,
    signal,
  })
}

/** GET /refunds/:id：查询本人退款详情；不存在或非本人统一 404/18001。 */
export function fetchRefund(refundId: number, signal?: AbortSignal): Promise<Refund> {
  return authGetData<Refund>(`/refunds/${String(refundId)}`, { signal })
}

/**
 * POST /refunds/:id/revoke：撤销本人退款申请。
 *
 * 后端只允许 `applying(10) → revoked(50)`（`internal/logic/refund/refund.go:199-255`）：
 * - 非本人 / 不存在 → 18001（404）；
 * - 其它状态 → 18002（409）；
 * - 不恢复库存、不改订单状态；撤销后可以再次申请。
 */
export function revokeRefund(refundId: number): Promise<Refund> {
  // 后端不读取 body，但显式空对象避免空 body 在代理层被改写（与订单操作一致）。
  return authPostData<Refund>(`/refunds/${String(refundId)}/revoke`, {})
}
