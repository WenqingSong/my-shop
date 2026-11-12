import { HttpResponse, http } from "msw";

/**
 * 运营工作台 Mock。
 *
 * 用途：本地演示与 Vitest 夹具。字段结构与 `api/dashboard/v1/dashboard.go`
 * 完全一致（snake_case），**数值是构造出来的演示数据，不代表真实业务规模**。
 *
 * 每个 handler 都显式校验 Authorization，用于验证"请求层确实注入了 Bearer Token"。
 */
const MOCK_TOKEN = "mock-access-token";

function unauthorized() {
  return HttpResponse.json(
    { code: 1001, message: "未认证或登录已过期", data: null },
    { status: 401 }
  );
}

function authorized(request: Request): boolean {
  return request.headers.get("Authorization") === `Bearer ${MOCK_TOKEN}`;
}

export const dashboardHandlers = [
  /** GET /admin/dashboard/overview */
  http.get("*/api/admin/dashboard/overview", ({ request }) => {
    if (!authorized(request)) return unauthorized();
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        user_total: 1280,
        today_new_users: 36,
        product_total: 240,
        order_total: 5821,
        today_new_orders: 128,
        // 单位：分。展示时应为 21,536.50 元
        today_order_amount: 2153650
      }
    });
  }),

  /** GET /admin/dashboard/orders/trend */
  http.get("*/api/admin/dashboard/orders/trend", ({ request }) => {
    if (!authorized(request)) return unauthorized();
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        days: [
          { date: "2026-11-04", order_count: 42 },
          { date: "2026-11-05", order_count: 57 },
          { date: "2026-11-06", order_count: 61 },
          { date: "2026-11-07", order_count: 88 },
          { date: "2026-11-08", order_count: 73 },
          { date: "2026-11-09", order_count: 96 },
          { date: "2026-11-10", order_count: 128 }
        ]
      }
    });
  }),

  /** GET /admin/dashboard/orders/status */
  http.get("*/api/admin/dashboard/orders/status", ({ request }) => {
    if (!authorized(request)) return unauthorized();
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        total: 545,
        status_distribution: [
          { status: 10, count: 60 },
          { status: 20, count: 140 },
          { status: 30, count: 105 },
          { status: 40, count: 98 },
          { status: 50, count: 82 },
          { status: 60, count: 40 },
          { status: 70, count: 20 }
        ]
      }
    });
  }),

  /** GET /admin/dashboard/products/top */
  http.get("*/api/admin/dashboard/products/top", ({ request }) => {
    if (!authorized(request)) return unauthorized();
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        items: [
          {
            rank: 1,
            product_id: 1001,
            product_name: "演示商品 A",
            sales: 320
          },
          {
            rank: 2,
            product_id: 1002,
            product_name: "演示商品 B",
            sales: 275
          },
          {
            rank: 3,
            product_id: 1003,
            product_name: "演示商品 C",
            sales: 198
          }
        ]
      }
    });
  }),

  /** GET /admin/dashboard/flash-sales */
  http.get("*/api/admin/dashboard/flash-sales", ({ request }) => {
    if (!authorized(request)) return unauthorized();
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        activity_total: 12,
        activity_status: { enabled: 5, disabled: 7 },
        request_status: { queued: 18, success: 940, failed: 32, dead: 3 },
        current_queued: 18,
        success_orders: 902
      }
    });
  })
];
