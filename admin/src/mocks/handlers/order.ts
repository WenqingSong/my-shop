import { http } from "msw";
import { mockDb, type MockOrder } from "./mock-db";
import {
  fail,
  isAuthorized,
  ok,
  queryInt,
  toInt,
  unauthorized
} from "./shared";

/**
 * 订单 Mock（对齐 `internal/logic/order/order.go`）。
 *
 * - 列表：轻量字段 + 服务端分页/筛选/排序；
 * - 详情：含订单项快照；
 * - 发货：仅 paid → shipped；存在有效退款申请（10/20/30）返回 18005。
 *
 * 状态枚举为字符串，不能直接改订单 status。
 */
const ORDER_STATUSES = [
  "pending_payment",
  "paid",
  "shipped",
  "received",
  "completed",
  "cancelled",
  "refunded"
];

function parseServerTime(value: string): number {
  return Date.parse(value.replace(" ", "T"));
}

function findOrder(id: number): MockOrder | null {
  return mockDb.orders.find(order => order.id === id) ?? null;
}

function itemsOf(orderId: number) {
  return mockDb.orderItems.filter(item => item.order_id === orderId);
}

function toDetail(order: MockOrder) {
  return { ...order, items: itemsOf(order.id) };
}

function hasActiveRefund(orderId: number): boolean {
  return mockDb.refunds.some(
    refund =>
      refund.order_id === orderId &&
      (refund.status === "applying" ||
        refund.status === "pending_refund" ||
        refund.status === "refund_failed")
  );
}

export const orderHandlers = [
  /** GET /admin/orders */
  http.get("*/api/admin/orders", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const url = new URL(request.url);
    const page = queryInt(url.searchParams, "page", 1);
    const size = Math.min(queryInt(url.searchParams, "size", 20), 100);
    const status = (url.searchParams.get("status") ?? "").trim();
    const orderNo = (url.searchParams.get("order_no") ?? "").trim();
    const userIdRaw = url.searchParams.get("user_id");
    const sort = url.searchParams.get("sort") ?? "id";
    const order = url.searchParams.get("order") ?? "desc";

    if (status && !ORDER_STATUSES.includes(status)) {
      return fail(1001, "参数错误", 400);
    }
    if (sort !== "id" && sort !== "created_at") {
      return fail(1001, "参数错误", 400);
    }
    if (order !== "asc" && order !== "desc") {
      return fail(1001, "参数错误", 400);
    }

    let userId: number | null = null;
    if (userIdRaw) {
      userId = toInt(userIdRaw);
      if (userId === null || userId <= 0) return fail(1001, "参数错误", 400);
    }

    let startTs: number | null = null;
    let endTs: number | null = null;
    const startRaw = url.searchParams.get("start_time");
    const endRaw = url.searchParams.get("end_time");
    if (startRaw) {
      const parsed = Date.parse(startRaw);
      if (!Number.isFinite(parsed)) return fail(1001, "参数错误", 400);
      startTs = parsed;
    }
    if (endRaw) {
      const parsed = Date.parse(endRaw);
      if (!Number.isFinite(parsed)) return fail(1001, "参数错误", 400);
      endTs = parsed;
    }
    if (startTs !== null && endTs !== null && startTs >= endTs) {
      return fail(1001, "参数错误", 400);
    }

    let items = [...mockDb.orders];
    if (orderNo) items = items.filter(item => item.order_no === orderNo);
    if (userId !== null) items = items.filter(item => item.user_id === userId);
    if (status) items = items.filter(item => item.status === status);
    if (startTs !== null) {
      items = items.filter(
        item => parseServerTime(item.created_at) >= startTs!
      );
    }
    if (endTs !== null) {
      items = items.filter(item => parseServerTime(item.created_at) < endTs!);
    }

    const direction = order === "asc" ? 1 : -1;
    items.sort((left, right) => {
      if (sort === "created_at") {
        return (
          (parseServerTime(left.created_at) -
            parseServerTime(right.created_at)) *
          direction
        );
      }
      return (left.id - right.id) * direction;
    });

    const total = items.length;
    const start = (page - 1) * size;
    const paged = items.slice(start, start + size).map(item => ({
      id: item.id,
      order_no: item.order_no,
      user_id: item.user_id,
      status: item.status,
      total_amount: item.total_amount,
      created_at: item.created_at
    }));

    return ok({ items: paged, total, page, size });
  }),

  /** GET /admin/orders/:id */
  http.get("*/api/admin/orders/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const order = findOrder(Number(params.id));
    if (!order) return fail(9001, "订单不存在", 404);
    return ok(toDetail(order));
  }),

  /** POST /admin/orders/:id/ship */
  http.post("*/api/admin/orders/:id/ship", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const order = findOrder(Number(params.id));
    if (!order) return fail(9001, "订单不存在", 404);
    if (order.status !== "paid") {
      return fail(9002, "订单状态迁移非法", 409);
    }
    if (hasActiveRefund(order.id)) {
      return fail(18005, "退款与发货冲突", 409);
    }

    order.status = "shipped";
    order.shipped_at = mockDb.now();
    order.updated_at = mockDb.now();
    return ok(toDetail(order));
  })
];
