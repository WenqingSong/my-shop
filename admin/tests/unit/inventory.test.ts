import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { server } from "../setup";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  deductInventory,
  getInventory,
  increaseInventory,
  listInventoryLogs
} from "@/api/modules/inventory";
import {
  buildAdjustmentRequest,
  formatChangeType,
  toInventoryLogView,
  toInventoryView,
  validateAdjustQuantity
} from "@/api/adapters/inventory-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("库存模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("库存查询", () => {
    it("按 SKU 查询真实库存", async () => {
      const inventory = await getInventory(2001);
      const view = toInventoryView(inventory);

      expect(view).toMatchObject({
        skuId: 2001,
        quantity: 25,
        hasRecord: true
      });
    });

    it("无库存记录时后端返回 0 且 created_at 为 null", async () => {
      const inventory = await getInventory(2003);
      const view = toInventoryView(inventory);

      expect(view.quantity).toBe(0);
      expect(view.hasRecord).toBe(false);
    });

    it("SKU 不存在时返回 5001", async () => {
      await expectBusinessCode(getInventory(999999), 5001);
    });

    it("不展示后端未提供的锁定库存 / 可售库存字段", async () => {
      const inventory = await getInventory(2001);

      expect(Object.keys(inventory).sort()).toEqual([
        "created_at",
        "quantity",
        "sku_id",
        "updated_at"
      ]);
    });
  });

  describe("库存调整", () => {
    it("增加库存走 increase 增量接口并写流水", async () => {
      const updated = await increaseInventory(2002, 10);

      expect(updated.quantity).toBe(13);

      const logs = await listInventoryLogs(2002, { page: 1, size: 10 });
      expect(logs.total).toBe(1);
      expect(logs.items[0]).toMatchObject({
        change_type: "increase",
        change_qty: 10,
        before_qty: 3,
        after_qty: 13,
        // 后端不接收 reason，流水恒为空
        reason: ""
      });
    });

    it("扣减库存走 deduct 条件接口", async () => {
      const updated = await deductInventory(2002, 3);
      expect(updated.quantity).toBe(0);
    });

    it("库存不足时后端返回 6001，前端不乐观更新", async () => {
      await expectBusinessCode(deductInventory(2002, 999), 6001);

      const still = await getInventory(2002);
      expect(still.quantity).toBe(3);
    });

    it("库存不足展示后端原文", async () => {
      try {
        await deductInventory(2002, 999);
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("库存不足");
      }
    });

    it("数量必须是正整数", () => {
      expect(validateAdjustQuantity(0)).toBe("调整数量必须大于 0");
      expect(validateAdjustQuantity(-5)).toBe("调整数量必须大于 0");
      expect(validateAdjustQuantity(1.5)).toBe("调整数量必须是正整数");
      expect(validateAdjustQuantity(null)).toBe("请输入调整数量");
      expect(validateAdjustQuantity("abc")).toBe("调整数量必须是数字");
      expect(validateAdjustQuantity(1)).toBeNull();
      expect(validateAdjustQuantity(1000001)).toContain("不能超过");
    });

    it("调整原因被显式标记为不持久化，而不是静默丢弃", () => {
      const { request, provisional } = buildAdjustmentRequest({
        skuId: 2001,
        type: "increase",
        quantity: 5,
        reason: " 盘点补录 "
      });

      expect(request).toEqual({ type: "increase", skuId: 2001, quantity: 5 });
      expect("reason" in request).toBe(false);
      expect(provisional).toEqual({
        reason: "盘点补录",
        reasonPersisted: false
      });
    });

    it("使用后端真实权限码，且没有“设置库存”权限", () => {
      expect(PERMISSION.INVENTORY_INCREASE).toBe("inventory:increase");
      expect(PERMISSION.INVENTORY_DEDUCT).toBe("inventory:deduct");

      const codes: string[] = Object.values(PERMISSION);
      expect(codes.some(code => code.startsWith("inventory:set"))).toBe(false);
    });
  });

  describe("重复提交防护", () => {
    it("请求进行中的重复调用被拦截，只执行一次", async () => {
      const guard = useSubmitGuard();
      let calls = 0;
      let release: () => void = () => {};

      const task = () =>
        new Promise<void>(resolve => {
          calls += 1;
          release = resolve;
        });

      const first = guard.run(task);
      const second = await guard.run(task);

      expect(guard.submitting.value).toBe(true);
      expect(second.executed).toBe(false);
      expect(calls).toBe(1);

      release();
      const firstResult = await first;
      expect(firstResult.executed).toBe(true);
      expect(guard.submitting.value).toBe(false);
    });

    it("任务失败后自动解锁，允许重试", async () => {
      const guard = useSubmitGuard();

      await expect(
        guard.run(async () => {
          throw new Error("boom");
        })
      ).rejects.toThrow("boom");

      expect(guard.submitting.value).toBe(false);
      const retry = await guard.run(async () => "ok");
      expect(retry).toEqual({ executed: true, value: "ok" });
    });
  });

  describe("库存流水", () => {
    it("按 SKU 分页查询，id 倒序", async () => {
      const firstPage = await listInventoryLogs(2001, { page: 1, size: 1 });
      const secondPage = await listInventoryLogs(2001, { page: 2, size: 1 });

      expect(firstPage.total).toBe(2);
      expect(firstPage.items.map(item => item.id)).toEqual([2]);
      expect(secondPage.items.map(item => item.id)).toEqual([1]);
    });

    it("流水字段映射：方向、原因占位、操作人", async () => {
      const result = await listInventoryLogs(2001, { page: 1, size: 10 });
      const increaseLog = result.items.find(
        item => item.change_type === "increase"
      );
      expect(increaseLog).toBeDefined();

      const view = toInventoryLogView(increaseLog!);

      expect(view.changeTypeLabel).toBe("增加");
      expect(view.direction).toBe("+");
      expect(view.deltaText).toBe("+30");
      expect(view.reasonText).toBe("—");
      expect(view.operatorText).toBe("管理员 #1");
    });

    it("未知变更类型不猜测语义", () => {
      expect(formatChangeType("freeze")).toBe("未知类型（freeze）");
    });

    it("流水接口失败时给出可展示错误", async () => {
      server.use(
        mswHttp.get("*/api/admin/inventories/:sku_id/logs", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      try {
        await listInventoryLogs(2001);
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
      }
    });
  });
});
