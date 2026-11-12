/**
 * 退款 API 模块。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET  /admin/refunds`               分页列表（refund:list）
 * - `GET  /admin/refunds/:id`           详情（refund:view）
 * - `GET  /admin/refunds/:id/audits`    审计记录（refund:view）
 * - `POST /admin/refunds/:id/review`    审核（refund:review）
 * - `POST /admin/refunds/:id/execute`   执行退款并确认结果（refund:execute）
 * - `POST /admin/refunds/:id/close`     关闭退款（refund:execute）
 *
 * 状态推进全部由后端事务完成（含订单 paid→refunded 与库存恢复），
 * 前端不提供修改退款状态或订单状态的入口。
 */
import { http } from "@/utils/http";
import type {
  AdminRefundListQuery,
  AdminRefundListResult,
  RefundAuditDTO,
  RefundDTO,
  RefundExecuteResult,
  RefundReviewResult
} from "../types/refund";

/** 后台退款分页列表 */
export function listRefunds(query: AdminRefundListQuery = {}) {
  return http.get<AdminRefundListResult, undefined>(
    "/admin/refunds",
    { params: query },
    { silent: true }
  );
}

/** 退款详情 */
export function getRefund(id: number) {
  return http.get<RefundDTO, undefined>(`/admin/refunds/${id}`, undefined, {
    silent: true
  });
}

/** 退款审计记录（按 id 升序，append-only） */
export function listRefundAudits(id: number) {
  return http.get<{ items: RefundAuditDTO[] }, undefined>(
    `/admin/refunds/${id}/audits`,
    undefined,
    { silent: true }
  );
}

/** 审核退款申请：approve → 待退款；reject → 审核拒绝 */
export function reviewRefund(
  id: number,
  payload: { result: RefundReviewResult; reason: string }
) {
  return http.post<RefundDTO, typeof payload>(
    `/admin/refunds/${id}/review`,
    { data: payload },
    { silent: true }
  );
}

/**
 * 执行退款并确认结果。
 *
 * `success`：待退款或退款失败 → 退款成功（同事务推进订单 + 恢复库存）；
 * `failure`：仅待退款 → 退款失败。
 */
export function executeRefund(
  id: number,
  payload: { result: RefundExecuteResult; reason: string }
) {
  return http.post<RefundDTO, typeof payload>(
    `/admin/refunds/${id}/execute`,
    { data: payload },
    { silent: true }
  );
}

/** 关闭退款（仅退款失败 → 已关闭，恢复履约） */
export function closeRefund(id: number, reason: string) {
  return http.post<RefundDTO, { reason: string }>(
    `/admin/refunds/${id}/close`,
    { data: { reason } },
    { silent: true }
  );
}
