import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  findOrderIdByOrderNo,
  getOrder,
  listOrders,
  shipOrder
} from "@/api/modules/order";
import { updateProduct } from "@/api/modules/product";
import {
  buildItemsSummary,
  canShip,
  formatCancelReason,
  formatOrderStatus,
  isTerminalOrderStatus,
  ORDER_STATUS_OPTIONS,
  orderStatusTagType,
  sumItemQuantity,
  toOrderListQuery,
  toOrderView
} from "@/api/adapters/order-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { toRFC3339 } from "@/utils/datetime";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

/** 用本地时间构造区间，避免测试机时区导致边界漂移 */
function localRange(month: number, startDay: number, endDay: number) {
  return {
    start: new Date(2026, month - 1, startDay, 0, 0, 0),
    end: new Date(2026, month - 1, endDay, 0, 0, 0)
  };
}

describe("订单模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("订单列表", () => {
    it("服务端分页：默认 id 倒序", async () => {
      const result = await listOrders({ page: 1, size: 3 });

      expect(result.total).toBe(10);
      expect(result.page).toBe(1);
      expect(result.size).toBe(3);
      expect(result.items.map(item => item.id)).toEqual([5010, 5009, 5008]);
    });

    it("第二页返回剩余数据，total 保持全量", async () => {
      const result = await listOrders({ page: 2, size: 8 });

      expect(result.total).toBe(10);
      expect(result.items.map(item => item.id)).toEqual([5002, 5001]);
    });

    it("订单号精确匹配", async () => {
      const result = await listOrders({ order_no: "SC202611010001" });

      expect(result.total).toBe(1);
      expect(result.items[0].id).toBe(5001);
    });

    it("订单号模糊值不会命中", async () => {
      const result = await listOrders({ order_no: "SC20261101" });
      expect(result.total).toBe(0);
    });

    it("按用户 id 精确筛选", async () => {
      const result = await listOrders({ user_id: 101 });
      expect(result.items.map(item => item.id)).toEqual([5005, 5001]);
    });

    it("按状态字符串筛选", async () => {
      const result = await listOrders({ status: "paid" });
      expect(result.items.map(item => item.id)).toEqual([
        5008, 5007, 5006, 5001
      ]);
    });

    it("按下单时间范围筛选（左闭右开）", async () => {
      const { start, end } = localRange(11, 5, 7);
      const result = await listOrders({
        start_time: toRFC3339(start),
        end_time: toRFC3339(end)
      });

      expect(result.items.map(item => item.id)).toEqual([5008, 5007]);
    });

    it("空数据时返回空列表而不是报错", async () => {
      const result = await listOrders({ status: "cancelled", user_id: 999 });

      expect(result.items).toEqual([]);
      expect(result.total).toBe(0);
    });

    it("非法状态返回 400 业务错误", async () => {
      await expectBusinessCode(
        listOrders({ status: "unknown" as never }),
        1001
      );
    });

    it("无权访问时展示权限提示", async () => {
      server.use(
        mswHttp.get("*/api/admin/orders", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      try {
        await listOrders();
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
      }
    });

    it("按订单号定位订单 id（退款列表的组合查询基础）", async () => {
      await expect(findOrderIdByOrderNo("SC202611010001")).resolves.toBe(5001);
      await expect(findOrderIdByOrderNo("NOT-EXIST")).resolves.toBeNull();
      await expect(findOrderIdByOrderNo("   ")).resolves.toBeNull();
    });
  });

  describe("订单详情与快照", () => {
    it("详情包含订单项快照、收货与支付信息", async () => {
      const detail = await getOrder(5001);
      const view = toOrderView(detail);

      expect(view.orderNo).toBe("SC202611010001");
      expect(view.statusLabel).toBe("已支付");
      expect(view.itemCount).toBe(2);
      expect(view.items[0]).toMatchObject({
        productName: "轻量跑步鞋",
        skuName: "黑 / 40",
        quantity: 2,
        priceCents: 32900,
        subtotalCents: 65800
      });
      expect(view.recipientName).toBe("张三");
      expect(view.phone).toBe("13800000001");
      expect(view.addressText).toContain("张江路 88 号");
      expect(view.paidText).not.toBe("—");
    });

    it("金额全部按整数分展示，不做浮点运算", async () => {
      const view = toOrderView(await getOrder(5001));

      expect(view.originalText).toBe("¥658.00");
      expect(view.discountText).toBe("¥0.00");
      expect(view.totalText).toBe("¥658.00");
      expect(view.items[0].subtotalText).toBe("¥658.00");
      expect(view.totalCents).toBe(65800);
    });

    it("商品改名后历史订单仍展示下单时快照", async () => {
      await updateProduct(1001, { name: "轻量跑步鞋（2026 新款）" });

      const view = toOrderView(await getOrder(5001));
      expect(view.items[0].productName).toBe("轻量跑步鞋");
    });

    it("后端未提供支付方式字段时不伪造", async () => {
      const detail = await getOrder(5001);
      expect("payment_method" in detail).toBe(false);
      expect("transaction_no" in detail).toBe(false);
    });

    it("订单不存在返回 9001", async () => {
      await expectBusinessCode(getOrder(999999), 9001);
    });

    it("取消原因与状态文案映射", async () => {
      const view = toOrderView(await getOrder(5005));
      expect(view.statusLabel).toBe("已取消");
      expect(view.cancelReasonText).toBe("用户取消");
    });
  });

  describe("发货", () => {
    it("已支付订单可发货并写入发货时间", async () => {
      const updated = await shipOrder(5001);

      expect(updated.status).toBe("shipped");
      expect(updated.shipped_at).not.toBeNull();

      const reloaded = await getOrder(5001);
      expect(reloaded.status).toBe("shipped");
    });

    it("非已支付订单发货返回 9002", async () => {
      await expectBusinessCode(shipOrder(5002), 9002);
    });

    it("存在有效退款申请时拒绝发货（18005）", async () => {
      await expectBusinessCode(shipOrder(5006), 18005);

      const order = await getOrder(5006);
      expect(order.status).toBe("paid");
    });

    it("重复发货返回状态迁移错误", async () => {
      await shipOrder(5001);
      await expectBusinessCode(shipOrder(5001), 9002);
    });

    it("发货失败时保留后端原文供页面展示", async () => {
      try {
        await shipOrder(5006);
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("退款与发货冲突");
      }
    });

    it("发货按钮只对 paid 订单显示", () => {
      expect(canShip("paid")).toBe(true);
      [
        "pending_payment",
        "shipped",
        "received",
        "completed",
        "cancelled",
        "refunded"
      ].forEach(status => expect(canShip(status)).toBe(false));
    });
  });

  describe("适配层", () => {
    it("7 种订单状态全部有确定文案与标签", () => {
      expect(ORDER_STATUS_OPTIONS).toHaveLength(7);
      expect(ORDER_STATUS_OPTIONS.map(option => option.label)).toEqual([
        "待支付",
        "已支付",
        "已发货",
        "已收货",
        "已完成",
        "已取消",
        "已退款"
      ]);
      expect(orderStatusTagType("refunded")).toBe("danger");
      expect(formatOrderStatus("unknown")).toBe("未知状态（unknown）");
      expect(isTerminalOrderStatus("completed")).toBe(true);
      expect(isTerminalOrderStatus("paid")).toBe(false);
    });

    it("取消原因未知值不猜测", () => {
      expect(formatCancelReason(null)).toBe("—");
      expect(formatCancelReason(1)).toBe("用户取消");
      expect(formatCancelReason(2)).toBe("超时未支付");
      expect(formatCancelReason(9)).toContain("未知原因");
    });

    it("商品摘要与件数来自订单项快照", () => {
      const items = [
        { product_name: "A", sku_name: "A-1", quantity: 2 },
        { product_name: "B", sku_name: "B-1", quantity: 1 }
      ] as never;

      expect(buildItemsSummary(items)).toBe("A 等 2 种商品");
      expect(sumItemQuantity(items)).toBe(3);
      expect(buildItemsSummary([])).toBe("—");
    });

    it("查询参数只在有值时发送，并转换为 RFC3339", () => {
      const { start, end } = localRange(11, 1, 8);
      const query = toOrderListQuery({
        page: 1,
        size: 20,
        orderNo: "  SC001  ",
        userId: 0,
        status: "",
        range: [start, end]
      });

      expect(query).toMatchObject({
        page: 1,
        size: 20,
        order_no: "SC001",
        sort: "id",
        order: "desc"
      });
      expect("user_id" in query).toBe(false);
      expect("status" in query).toBe(false);
      expect(query.start_time).toMatch(
        /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$/
      );
      expect(query.end_time).not.toBe(query.start_time);
    });

    it("非法时间区间不发送半截参数", () => {
      const start = new Date(2026, 10, 8);
      const end = new Date(2026, 10, 1);
      const query = toOrderListQuery({
        page: 1,
        size: 10,
        range: [start, end]
      });

      expect("start_time" in query).toBe(false);
      expect("end_time" in query).toBe(false);
    });
  });

  describe("权限码", () => {
    it("使用后端真实订单权限码", () => {
      expect(PERMISSION.ORDER_LIST).toBe("order:list");
      expect(PERMISSION.ORDER_VIEW).toBe("order:view");
      expect(PERMISSION.ORDER_SHIP).toBe("order:ship");
    });
  });
});
