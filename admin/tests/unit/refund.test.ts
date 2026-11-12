import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb, mockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  closeRefund,
  executeRefund,
  getRefund,
  listRefundAudits,
  listRefunds,
  reviewRefund
} from "@/api/modules/refund";
import { getOrder, shipOrder } from "@/api/modules/order";
import { getInventory } from "@/api/modules/inventory";
import {
  formatAuditAction,
  formatOperatorType,
  formatRefundStatus,
  formatRefundStatusByCode,
  getRefundActions,
  REFUND_STATUS_OPTIONS,
  refundStatusTagType,
  toRefundAuditView,
  toRefundView,
  validateRefundReason
} from "@/api/adapters/refund-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("退款与售后模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("退款列表", () => {
    it("服务端分页，id 倒序", async () => {
      const result = await listRefunds({ page: 1, size: 2 });

      expect(result.total).toBe(4);
      expect(result.items.map(item => item.id)).toEqual([7004, 7003]);
    });

    it("按状态筛选", async () => {
      const result = await listRefunds({ status: "pending_refund" });
      expect(result.items.map(item => item.id)).toEqual([7002]);
    });

    it("按订单 id 筛选", async () => {
      const result = await listRefunds({ order_id: 5008 });
      expect(result.items.map(item => item.id)).toEqual([7003]);
    });

    it("按用户 id 筛选", async () => {
      const result = await listRefunds({ user_id: 105 });
      expect(result.items.map(item => item.id)).toEqual([7001]);
    });

    it("非法状态返回 400", async () => {
      await expectBusinessCode(listRefunds({ status: "nope" as never }), 1001);
    });

    it("无权访问时展示权限提示", async () => {
      server.use(
        mswHttp.get("*/api/admin/refunds", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      try {
        await listRefunds();
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
      }
    });
  });

  describe("退款详情与审计", () => {
    it("详情字段按 DTO 解析，金额为整数分", async () => {
      const detail = await getRefund(7002);
      const view = toRefundView(detail);

      expect(view.statusLabel).toBe("待退款");
      expect(view.amountCents).toBe(32900);
      expect(view.amountText).toBe("¥329.00");
      expect(view.orderId).toBe(5007);
      expect(view.reasonText).toBe("重复下单");
      expect(view.reviewReasonText).toBe("同意退款");
      expect(view.reviewedByText).toBe("管理员 #1");
    });

    it("审计记录按 id 升序，动作与前后状态可映射", async () => {
      const result = await listRefundAudits(7002);
      const views = result.items.map(toRefundAuditView);

      expect(views.map(item => item.action)).toEqual([
        "apply",
        "review_approve"
      ]);
      expect(views[0].actionLabel).toBe("提交退款申请");
      expect(views[0].operatorText).toBe("用户 u106");
      expect(views[1].operatorText).toBe("管理员 admin");
      expect(views[1].beforeStatusText).toBe("申请中");
      expect(views[1].afterStatusText).toBe("待退款");
    });

    it("退款不存在返回 18001", async () => {
      await expectBusinessCode(getRefund(999999), 18001);
    });

    it("7 种退款状态全部有确定文案", () => {
      expect(REFUND_STATUS_OPTIONS).toHaveLength(7);
      expect(REFUND_STATUS_OPTIONS.map(item => item.label)).toEqual([
        "申请中",
        "待退款",
        "退款失败",
        "审核拒绝",
        "已撤销",
        "退款成功",
        "已关闭"
      ]);
      expect(refundStatusTagType("refund_succeeded")).toBe("success");
      expect(formatRefundStatus("x")).toBe("未知状态（x）");
      expect(formatRefundStatusByCode(0)).toBe("—");
      expect(formatRefundStatusByCode(60)).toBe("退款成功");
      expect(formatRefundStatusByCode(99)).toContain("未知状态");
      expect(formatAuditAction("revoke")).toBe("用户撤销申请");
      expect(formatOperatorType(1)).toBe("用户");
      expect(formatOperatorType(2)).toBe("管理员");
    });
  });

  describe("退款审核", () => {
    it("审核通过：申请中 → 待退款，并写入审核信息与审计", async () => {
      const refund = await reviewRefund(7001, {
        result: "approve",
        reason: "同意退款"
      });

      expect(refund.status).toBe("pending_refund");
      expect(refund.review_reason).toBe("同意退款");
      expect(refund.reviewed_by).toBe(1);
      expect(refund.reviewed_at).not.toBeNull();

      const audits = await listRefundAudits(7001);
      expect(audits.items).toHaveLength(2);
      expect(audits.items[1]).toMatchObject({
        action: "review_approve",
        before_status: 10,
        after_status: 20
      });
    });

    it("审核拒绝：申请中 → 审核拒绝，订单保持已支付且可发货", async () => {
      const refund = await reviewRefund(7001, {
        result: "reject",
        reason: "不符合退款条件"
      });

      expect(refund.status).toBe("review_rejected");

      const order = await getOrder(5006);
      expect(order.status).toBe("paid");

      const shipped = await shipOrder(5006);
      expect(shipped.status).toBe("shipped");
    });

    it("重复审核返回 18002", async () => {
      await reviewRefund(7001, { result: "approve", reason: "同意" });
      await expectBusinessCode(
        reviewRefund(7001, { result: "reject", reason: "反悔" }),
        18002
      );
    });

    it("审核原因必填（后端 400）", async () => {
      await expectBusinessCode(
        reviewRefund(7001, { result: "approve", reason: "" }),
        1001
      );
      await expectBusinessCode(
        reviewRefund(7001, { result: "approve", reason: "   " }),
        1001
      );
    });

    it("前端原因校验：必填且不超过 255 字符", () => {
      expect(validateRefundReason("")).toBe("请填写原因");
      expect(validateRefundReason("   ")).toBe("请填写原因");
      expect(validateRefundReason("a".repeat(255))).toBeNull();
      expect(validateRefundReason("a".repeat(256))).toContain("255");
    });
  });

  describe("退款执行与关闭", () => {
    it("执行成功：待退款 → 退款成功，订单变已退款并恢复库存", async () => {
      const beforeInventory = await getInventory(2001);

      const refund = await executeRefund(7002, {
        result: "success",
        reason: "渠道退款成功"
      });
      expect(refund.status).toBe("refund_succeeded");

      const order = await getOrder(5007);
      expect(order.status).toBe("refunded");
      expect(order.refunded_at).not.toBeNull();

      const afterInventory = await getInventory(2001);
      expect(afterInventory.quantity).toBe(beforeInventory.quantity + 1);

      const audits = await listRefundAudits(7002);
      expect(audits.items.at(-1)).toMatchObject({
        action: "execute_success",
        before_status: 20,
        after_status: 60
      });
    });

    it("执行失败：待退款 → 退款失败，订单与库存不变", async () => {
      const beforeInventory = await getInventory(2001);

      const refund = await executeRefund(7002, {
        result: "failure",
        reason: "渠道返回失败"
      });
      expect(refund.status).toBe("refund_failed");

      const order = await getOrder(5007);
      expect(order.status).toBe("paid");

      const afterInventory = await getInventory(2001);
      expect(afterInventory.quantity).toBe(beforeInventory.quantity);
    });

    it("退款失败后可重试成功", async () => {
      const refund = await executeRefund(7003, {
        result: "success",
        reason: "重试成功"
      });

      expect(refund.status).toBe("refund_succeeded");

      const order = await getOrder(5008);
      expect(order.status).toBe("refunded");

      const inventory = await getInventory(2002);
      // 订单 5008 含 SKU 2002 × 2，种子库存 3
      expect(inventory.quantity).toBe(5);
    });

    it("关闭退款：退款失败 → 已关闭，订单仍可发货", async () => {
      const refund = await closeRefund(7003, "用户取消退款");
      expect(refund.status).toBe("closed");

      const order = await getOrder(5008);
      expect(order.status).toBe("paid");

      const shipped = await shipOrder(5008);
      expect(shipped.status).toBe("shipped");
    });

    it("关闭后不能再执行退款（18002）", async () => {
      await closeRefund(7003, "用户取消退款");
      await expectBusinessCode(
        executeRefund(7003, { result: "success", reason: "again" }),
        18002
      );
    });

    it("待退款状态不能直接关闭（18002）", async () => {
      await expectBusinessCode(closeRefund(7002, "关闭"), 18002);
    });

    it("已退款成功再次执行成功是幂等返回，不重复恢复库存", async () => {
      const before = await getInventory(2005);

      const refund = await executeRefund(7004, {
        result: "success",
        reason: "重复确认"
      });

      expect(refund.status).toBe("refund_succeeded");
      expect(before.quantity).toBe(0);
      expect(mockDb.inventories.has(2005)).toBe(false);
    });

    it("执行原因为空返回 400", async () => {
      await expectBusinessCode(
        executeRefund(7002, { result: "success", reason: "" }),
        1001
      );
      await expectBusinessCode(closeRefund(7003, "  "), 1001);
    });

    it("执行失败时保留后端原文", async () => {
      try {
        await closeRefund(7002, "关闭");
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("退款状态迁移非法");
      }
    });
  });

  describe("操作可用性", () => {
    it("按退款状态计算审核 / 执行 / 关闭动作", () => {
      expect(getRefundActions("applying")).toEqual({
        review: true,
        executeSuccess: false,
        executeFailure: false,
        close: false
      });
      expect(getRefundActions("pending_refund")).toEqual({
        review: false,
        executeSuccess: true,
        executeFailure: true,
        close: false
      });
      expect(getRefundActions("refund_failed")).toEqual({
        review: false,
        executeSuccess: true,
        executeFailure: false,
        close: true
      });
      ["review_rejected", "revoked", "refund_succeeded", "closed"].forEach(
        status => {
          expect(getRefundActions(status)).toEqual({
            review: false,
            executeSuccess: false,
            executeFailure: false,
            close: false
          });
        }
      );
    });

    it("使用后端真实退款权限码，且没有修改状态的权限", () => {
      expect(PERMISSION.REFUND_LIST).toBe("refund:list");
      expect(PERMISSION.REFUND_VIEW).toBe("refund:view");
      expect(PERMISSION.REFUND_REVIEW).toBe("refund:review");
      expect(PERMISSION.REFUND_EXECUTE).toBe("refund:execute");

      const codes: string[] = Object.values(PERMISSION);
      expect(codes.some(code => code === "refund:update")).toBe(false);
    });
  });
});
