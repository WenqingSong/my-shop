import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { mockDb, resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import { createFlashSale, updateFlashSale } from "@/api/modules/flash-sale";
import {
  buildCreatePayload,
  buildUpdatePayload,
  createBindingForm,
  toActivityView,
  validateCreateForm,
  validateUpdateForm,
  type FlashSaleBindingForm,
  type FlashSaleCreateForm
} from "@/api/adapters/flash-sale-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

function binding(skuId: number, price = 199, stock = 10): FlashSaleBindingForm {
  const item = createBindingForm();
  item.productId = 1001;
  item.productName = "轻量跑步鞋";
  item.skuId = skuId;
  item.skuName = `SKU ${skuId}`;
  item.flashPriceYuan = price;
  item.totalStock = stock;
  return item;
}

function createForm(): FlashSaleCreateForm {
  return {
    name: "测试闪购",
    range: [new Date(2026, 10, 20, 10), new Date(2026, 10, 20, 12)],
    bindings: [binding(2001), binding(2002, 209, 5)]
  };
}

describe("秒杀活动模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("创建活动：默认启用、绑定 sold=0、价格保持整数分", async () => {
    const activity = await createFlashSale(buildCreatePayload(createForm()));

    expect(activity.status).toBe("enabled");
    expect(activity.skus).toHaveLength(2);
    expect(activity.skus[0]).toMatchObject({
      sku_id: 2001,
      flash_price: 19900,
      total_stock: 10,
      sold: 0
    });
    expect(activity.start_time).toBe("2026-11-20 10:00:00");
    expect(mockDb.activities).toHaveLength(2);
  });

  it("视图汇总真实库存与已售", async () => {
    const activity = await createFlashSale(buildCreatePayload(createForm()));
    const view = toActivityView(activity);

    expect(view.totalStock).toBe(15);
    expect(view.totalSold).toBe(0);
    expect(view.skus[0].flashPriceText).toBe("¥199.00");
  });

  it("重复 SKU、未知 SKU 均返回 12007", async () => {
    const duplicated = createForm();
    duplicated.bindings = [binding(2001), binding(2001)];
    await expectBusinessCode(
      createFlashSale(buildCreatePayload(duplicated)),
      12007
    );

    const unknown = createForm();
    unknown.bindings = [binding(999999)];
    await expectBusinessCode(
      createFlashSale(buildCreatePayload(unknown)),
      12007
    );
  });

  it("创建校验：名称、时间、绑定非空与正整数库存", () => {
    expect(validateCreateForm({ name: "", bindings: [] })).toEqual(
      expect.arrayContaining([
        "请输入活动名称",
        "请选择活动开始/结束时间",
        "至少需要绑定一个 SKU"
      ])
    );

    const reversed = createForm();
    reversed.range = [new Date(2026, 10, 20, 12), new Date(2026, 10, 20, 10)];
    expect(
      validateCreateForm(reversed).some(text => text.includes("晚于"))
    ).toBe(true);

    const bad = createForm();
    bad.bindings = [binding(2001, 0, 0)];
    const errors = validateCreateForm(bad);
    expect(errors.some(text => text.includes("秒杀价"))).toBe(true);
    expect(errors.some(text => text.includes("库存"))).toBe(true);
  });

  it("更新状态为禁用（部分更新）", async () => {
    const updated = await updateFlashSale(8101, { status: "disabled" });

    expect(updated.status).toBe("disabled");
    expect(updated.name).toBe("周末闪购");
    expect(updated.skus).toHaveLength(2);
  });

  it("更新名称不影响已绑定 SKU 与时间", async () => {
    const updated = await updateFlashSale(8101, { name: "周末闪购（改）" });

    expect(updated.name).toBe("周末闪购（改）");
    expect(updated.start_time).toBe("2026-11-15 10:00:00");
    expect(updated.skus[0].total_stock).toBe(50);
  });

  it("更新未绑定的 SKU 返回 12007", async () => {
    await expectBusinessCode(
      updateFlashSale(8101, {
        skus: [{ sku_id: 2003, flash_price: 10000, total_stock: 5 }]
      }),
      12007
    );
  });

  it("更新库存低于已售数量返回 12007", async () => {
    await expectBusinessCode(
      updateFlashSale(8101, {
        skus: [{ sku_id: 2001, flash_price: 19900, total_stock: 5 }]
      }),
      12007
    );
  });

  it("更新不存在的活动返回 12001", async () => {
    await expectBusinessCode(updateFlashSale(999999, { name: "x" }), 12001);
  });

  it("更新校验：ID、空变更、时间区间", () => {
    expect(
      validateUpdateForm({ id: null, name: "", status: "", bindings: [] })
    ).toEqual(
      expect.arrayContaining([
        "请输入有效的活动 ID",
        "请至少填写一项要更新的内容"
      ])
    );

    const reversed = validateUpdateForm({
      id: 8101,
      name: "",
      status: "",
      range: [new Date(2026, 10, 20, 12), new Date(2026, 10, 20, 10)],
      bindings: []
    });
    expect(reversed.some(text => text.includes("晚于"))).toBe(true);
  });

  it("更新请求只包含填写的字段", () => {
    const payload = buildUpdatePayload({
      id: 8101,
      name: " 新名称 ",
      status: "enabled",
      range: undefined,
      bindings: []
    });

    expect(payload).toEqual({ name: "新名称", status: "enabled" });
  });

  it("重复提交防护：并发创建只执行一次", async () => {
    const payload = buildCreatePayload(createForm());
    const guard = useSubmitGuard();
    const before = mockDb.activities.length;

    const first = guard.run(() => createFlashSale(payload));
    const second = await guard.run(() => createFlashSale(payload));

    expect(second.executed).toBe(false);
    await first;
    expect(mockDb.activities.length).toBe(before + 1);
  });

  it("无权访问时展示后端提示", async () => {
    server.use(
      mswHttp.post("*/api/admin/flash-sales", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await createFlashSale(buildCreatePayload(createForm()));
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("使用后端真实权限码", () => {
    expect(PERMISSION.FLASH_SALE_CREATE).toBe("flash_sale:create");
    expect(PERMISSION.FLASH_SALE_UPDATE).toBe("flash_sale:update");
    expect(Object.values(PERMISSION)).not.toContain("flash_sale:list");
  });
});
