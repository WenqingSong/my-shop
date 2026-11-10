import { http } from "msw";
import {
  mockDb,
  type MockRefund,
  type MockRefundAudit,
  type MockRefundStatus
} from "./mock-db";
import {
  fail,
  isAuthorized,
  ok,
  queryInt,
  toInt,
  unauthorized
} from "./shared";

/**
 * 退款 Mock（对齐 `internal/logic/refund/refund.go`）。
 *
 * 状态机：
 * - review approve：applying(10) → pending_refund(20)
 * - review reject：applying(10) → review_rejected(40)
 * - execute success：pending_refund(20) / refund_failed(30) → refund_succeeded(60)，
 *   同事务把订单 paid→refunded 并恢复库存；已成功再次调用幂等返回
 * - execute failure：pending_refund(20) → refund_failed(30)
 * - close：refund_failed(30) → closed(70)
 *
 * 审计记录 append-only，before/after 为 DB TINYINT 状态码。
 */
const REFUND_STATUS_CODE: Record<MockRefundStatus, number> = {
  applying: 10,
  pending_refund: 20,
  refund_failed: 30,
  review_rejected: 40,
  revoked: 50,
  refund_succeeded: 60,
  closed: 70
};

const REFUND_STATUSES = Object.keys(REFUND_STATUS_CODE);

const MAX_REASON_LENGTH = 255;

function findRefund(id: number): MockRefund | null {
  return mockDb.refunds.find(refund => refund.id === id) ?? null;
}

function findOrder(id: number) {
  return mockDb.orders.find(order => order.id === id) ?? null;
}

function appendAudit(
  refundId: number,
  action: MockRefundAudit["action"],
  beforeStatus: number,
  afterStatus: number,
  reason: string,
  operator: { type: 1 | 2; id: number; username: string } = {
    type: 2,
    id: 1,
    username: "admin"
  }
) {
  const audit: MockRefundAudit = {
    id: mockDb.nextRefundAuditId++,
    refund_id: refundId,
    action,
    operator_type: operator.type,
    operator_id: operator.id,
    operator_username: operator.username,
    before_status: beforeStatus,
    after_status: afterStatus,
    reason,
    created_at: mockDb.now()
  };
  mockDb.refundAudits.push(audit);
}

function validateReason(reason: unknown): string | null {
  const text = String(reason ?? "").trim();
  if (!text) return null;
  if ([...text].length > MAX_REASON_LENGTH) return null;
  return text;
}

/** 退款成功时恢复库存（模拟后端 IncreaseInTx） */
function restoreInventory(orderId: number) {
  const items = mockDb.orderItems.filter(item => item.order_id === orderId);
  for (const item of items) {
    const record = mockDb.inventories.get(item.sku_id);
    const before = record?.quantity ?? 0;
    const after = before + item.quantity;
    mockDb.inventories.set(item.sku_id, {
      sku_id: item.sku_id,
      quantity: after,
      created_at: record?.created_at ?? mockDb.now(),
      updated_at: mockDb.now()
    });
    mockDb.logs.push({
      id: mockDb.nextLogId++,
      sku_id: item.sku_id,
      change_type: "increase",
      change_qty: item.quantity,
      before_qty: before,
      after_qty: after,
      operator_admin_id: null,
      reason: "",
      created_at: mockDb.now()
    });
  }
}

export const refundHandlers = [
  /** GET /admin/refunds */
  http.get("*/api/admin/refunds", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const url = new URL(request.url);
    const page = queryInt(url.searchParams, "page", 1);
    const size = Math.min(queryInt(url.searchParams, "size", 20), 100);
    const status = (url.searchParams.get("status") ?? "").trim();
    const orderIdRaw = url.searchParams.get("order_id");
    const userIdRaw = url.searchParams.get("user_id");

    if (status && !REFUND_STATUSES.includes(status)) {
      return fail(1001, "参数错误", 400);
    }

    let orderId: number | null = null;
    if (orderIdRaw) {
      orderId = toInt(orderIdRaw);
      if (orderId === null || orderId <= 0) return fail(1001, "参数错误", 400);
    }

    let userId: number | null = null;
    if (userIdRaw) {
      userId = toInt(userIdRaw);
      if (userId === null || userId <= 0) return fail(1001, "参数错误", 400);
    }

    let items = [...mockDb.refunds];
    if (orderId !== null)
      items = items.filter(item => item.order_id === orderId);
    if (userId !== null) items = items.filter(item => item.user_id === userId);
    if (status) items = items.filter(item => item.status === status);

    items.sort((left, right) => right.id - left.id);

    const total = items.length;
    const start = (page - 1) * size;

    return ok({
      items: items.slice(start, start + size),
      total,
      page,
      size
    });
  }),

  /** GET /admin/refunds/:id */
  http.get("*/api/admin/refunds/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const refund = findRefund(Number(params.id));
    if (!refund) return fail(18001, "退款申请不存在", 404);
    return ok(refund);
  }),

  /** GET /admin/refunds/:id/audits */
  http.get("*/api/admin/refunds/:id/audits", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const refund = findRefund(Number(params.id));
    if (!refund) return fail(18001, "退款申请不存在", 404);

    const items = mockDb.refundAudits
      .filter(audit => audit.refund_id === refund.id)
      .sort((left, right) => left.id - right.id);
    return ok({ items });
  }),

  /** POST /admin/refunds/:id/review */
  http.post("*/api/admin/refunds/:id/review", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as Record<string, unknown>;
    const result = String(body.result ?? "");
    if (result !== "approve" && result !== "reject") {
      return fail(1001, "参数错误", 400);
    }
    const reason = validateReason(body.reason);
    if (!reason) return fail(1001, "参数错误", 400);

    const refund = findRefund(Number(params.id));
    if (!refund) return fail(18001, "退款申请不存在", 404);
    if (refund.status !== "applying") {
      return fail(18002, "退款状态迁移非法", 409);
    }

    const before = REFUND_STATUS_CODE[refund.status];
    const target: MockRefundStatus =
      result === "approve" ? "pending_refund" : "review_rejected";

    refund.status = target;
    refund.review_reason = reason;
    refund.reviewed_by = 1;
    refund.reviewed_at = mockDb.now();
    refund.updated_at = mockDb.now();

    appendAudit(
      refund.id,
      result === "approve" ? "review_approve" : "review_reject",
      before,
      REFUND_STATUS_CODE[target],
      reason
    );

    return ok(refund);
  }),

  /** POST /admin/refunds/:id/execute */
  http.post("*/api/admin/refunds/:id/execute", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as Record<string, unknown>;
    const result = String(body.result ?? "");
    if (result !== "success" && result !== "failure") {
      return fail(1001, "参数错误", 400);
    }
    const reason = validateReason(body.reason);
    if (!reason) return fail(1001, "参数错误", 400);

    const refund = findRefund(Number(params.id));
    if (!refund) return fail(18001, "退款申请不存在", 404);

    if (result === "success") {
      // 顺序幂等：已成功直接返回，不重复推进订单/库存
      if (refund.status === "refund_succeeded") return ok(refund);
      if (
        refund.status !== "pending_refund" &&
        refund.status !== "refund_failed"
      ) {
        return fail(18002, "退款状态迁移非法", 409);
      }

      const order = findOrder(refund.order_id);
      if (!order) return fail(9001, "订单不存在", 404);
      if (order.status !== "paid") {
        return fail(9002, "订单状态迁移非法", 409);
      }

      const before = REFUND_STATUS_CODE[refund.status];
      order.status = "refunded";
      order.refunded_at = mockDb.now();
      order.updated_at = mockDb.now();
      restoreInventory(order.id);

      refund.status = "refund_succeeded";
      refund.execute_reason = reason;
      refund.executed_by = 1;
      refund.executed_at = mockDb.now();
      refund.updated_at = mockDb.now();

      appendAudit(refund.id, "execute_success", before, 60, reason);
      return ok(refund);
    }

    if (refund.status !== "pending_refund") {
      return fail(18002, "退款状态迁移非法", 409);
    }

    const before = REFUND_STATUS_CODE[refund.status];
    refund.status = "refund_failed";
    refund.execute_reason = reason;
    refund.executed_by = 1;
    refund.executed_at = mockDb.now();
    refund.updated_at = mockDb.now();

    appendAudit(refund.id, "execute_failure", before, 30, reason);
    return ok(refund);
  }),

  /** POST /admin/refunds/:id/close */
  http.post("*/api/admin/refunds/:id/close", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as Record<string, unknown>;
    const reason = validateReason(body.reason);
    if (!reason) return fail(1001, "参数错误", 400);

    const refund = findRefund(Number(params.id));
    if (!refund) return fail(18001, "退款申请不存在", 404);
    if (refund.status !== "refund_failed") {
      return fail(18002, "退款状态迁移非法", 409);
    }

    refund.status = "closed";
    refund.execute_reason = reason;
    refund.executed_by = 1;
    refund.executed_at = mockDb.now();
    refund.updated_at = mockDb.now();

    appendAudit(refund.id, "close", 30, 70, reason);
    return ok(refund);
  })
];
