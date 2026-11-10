import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import { createCoupon, listCoupons, stopCoupon } from "@/api/modules/coupon";
import {
  buildCreateCouponPayload,
  emptyCouponForm,
  filterCoupons,
  sliceCoupons,
  toCouponView,
  validateCouponForm
} from "@/api/adapters/coupon-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

function validForm() {
  return {
    ...emptyCouponForm(),
    name: "测试满减券",
    thresholdYuan: 100,
    discountYuan: 20,
    totalQuantity: 100,
    claimRange: [
      new Date(2026, 10, 1, 0, 0, 0),
      new Date(2026, 11, 1, 0, 0, 0)
    ] as [Date, Date],
    useRange: [
      new Date(2026, 10, 1, 0, 0, 0),
      new Date(2026, 11, 31, 23, 59, 59)
    ] as [Date, Date]
  };
}

describe("优惠券模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("列表按 id 倒序返回全部模板", async () => {
    const result = await listCoupons();

    expect(result.items.map(item => item.id)).toEqual([8003, 8002, 8001]);
    expect(result.items[0].status).toBe(0);
    expect(result.items[1].issued_quantity).toBe(200);
  });

  it("金额为整数分，视图转换为元", async () => {
    const result = await listCoupons();
    const view = toCouponView(result.items.find(item => item.id === 8001)!);

    expect(view.thresholdCents).toBe(10000);
    expect(view.thresholdText).toBe("满 ¥100.00 可用");
    expect(view.discountText).toBe("¥20.00");
    expect(view.issuedQuantity).toBe(120);
    expect(view.totalQuantity).toBe(1000);
  });

  it("无门槛券展示为无门槛", async () => {
    const result = await listCoupons();
    const view = toCouponView(result.items.find(item => item.id === 8002)!);
    expect(view.thresholdText).toBe("无门槛");
  });

  it("客户端筛选基于完整数据集（关键词 + 状态）", async () => {
    const result = await listCoupons();

    expect(
      filterCoupons(result.items, "新人", "").map(item => item.id)
    ).toEqual([8001]);
    expect(filterCoupons(result.items, "", 0).map(item => item.id)).toEqual([
      8003
    ]);
    expect(filterCoupons(result.items, "不存在", "").length).toBe(0);
  });

  it("分页在完整数据集上执行", async () => {
    const result = await listCoupons();
    expect(sliceCoupons(result.items, 2, 2).map(item => item.id)).toEqual([
      8001
    ]);
  });

  it("创建优惠券：元转分、新建即可领取、已发放为 0", async () => {
    const created = await createCoupon(buildCreateCouponPayload(validForm()));

    expect(created.threshold_amount).toBe(10000);
    expect(created.discount_amount).toBe(2000);
    expect(created.status).toBe(1);
    expect(created.issued_quantity).toBe(0);
    expect(created.claim_start_time).toBe("2026-11-01 00:00:00");

    const list = await listCoupons();
    expect(list.items).toHaveLength(4);
  });

  it("创建请求时间格式为 YYYY-MM-DD HH:mm:ss", () => {
    const payload = buildCreateCouponPayload(validForm());
    expect(payload.claim_start_time).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/
    );
    expect(payload.use_end_time).toMatch(/23:59:59$/);
  });

  it("后端拒绝非法金额（400/19002）", async () => {
    const payload = buildCreateCouponPayload(validForm());
    await expectBusinessCode(
      createCoupon({ ...payload, discount_amount: 0 }),
      19002
    );
  });

  it("停止领取：1 → 0，且幂等", async () => {
    const first = await stopCoupon(8001);
    expect(first.status).toBe(0);

    const second = await stopCoupon(8001);
    expect(second.status).toBe(0);
  });

  it("停止不存在的模板返回 19001", async () => {
    await expectBusinessCode(stopCoupon(999999), 19001);
  });

  it("表单校验：名称、门槛、减免、数量、时间", () => {
    const errors = validateCouponForm(emptyCouponForm());
    expect(errors.name).toBe("请输入优惠券名称");
    expect(errors.discount).toBe("请输入减免金额");
    expect(errors.quantity).toBeDefined();
    expect(errors.claimRange).toBeDefined();
    expect(errors.useRange).toBeDefined();

    const tooLong = validateCouponForm({
      ...validForm(),
      name: "a".repeat(65)
    });
    expect(tooLong.name).toContain("64");
  });

  it("减免金额必须大于 0，最多两位小数", () => {
    expect(
      validateCouponForm({ ...validForm(), discountYuan: 0 }).discount
    ).toContain("大于 0");
    expect(
      validateCouponForm({ ...validForm(), discountYuan: 1.234 }).discount
    ).toBeDefined();
  });

  it("时间区间必须开始早于结束", () => {
    const reversed: [Date, Date] = [
      new Date(2026, 11, 1),
      new Date(2026, 10, 1)
    ];
    expect(
      validateCouponForm({ ...validForm(), claimRange: reversed }).claimRange
    ).toContain("晚于");
  });

  it("无权访问时展示权限提示", async () => {
    server.use(
      mswHttp.get("*/api/admin/coupons", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await listCoupons();
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("使用后端真实权限码", () => {
    expect(PERMISSION.COUPON_CREATE).toBe("coupon:create");
    expect(PERMISSION.COUPON_LIST).toBe("coupon:list");
    expect(PERMISSION.COUPON_STOP).toBe("coupon:stop");
    expect(Object.values(PERMISSION)).not.toContain("coupon:update");
  });
});
