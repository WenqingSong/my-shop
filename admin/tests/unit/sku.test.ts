import { beforeEach, describe, expect, it } from "vitest";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import { getProduct, updateProduct } from "@/api/modules/product";
import { syncProductSkus } from "@/api/modules/sku";
import type { SkuDTO } from "@/api/types/sku";
import {
  buildSkuEditorState,
  buildSkuName,
  createSpecGroup,
  generateCombinations,
  generateSkuDrafts,
  inferSpecGroups,
  parseSkuName,
  planSkuSync,
  validateSkuDrafts,
  type SkuDraft
} from "@/api/adapters/sku-adapter";
import {
  centsToYuanInput,
  toProductForm,
  yuanToCents
} from "@/api/adapters/product-adapter";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

function draft(partial: Partial<SkuDraft> & Pick<SkuDraft, "name">): SkuDraft {
  return {
    key: partial.name,
    values: [],
    priceCents: 1000,
    status: "enabled",
    ...partial
  };
}

describe("SKU 与商品规格", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("规格组合生成", () => {
    it("颜色 × 尺码生成 2×3=6 个组合", () => {
      const groups = [
        createSpecGroup("颜色", ["黑", "白"]),
        createSpecGroup("尺码", ["40", "41", "42"])
      ];

      const combinations = generateCombinations(groups);

      expect(combinations).toHaveLength(6);
      expect(combinations).toEqual([
        ["黑", "40"],
        ["黑", "41"],
        ["黑", "42"],
        ["白", "40"],
        ["白", "41"],
        ["白", "42"]
      ]);
    });

    it("SKU 名称按「 / 」拼接并可反向解析", () => {
      expect(buildSkuName(["黑", "40"])).toBe("黑 / 40");
      expect(buildSkuName([])).toBe("默认规格");
      expect(parseSkuName("黑 / 40")).toEqual(["黑", "40"]);
      expect(parseSkuName("默认规格")).toEqual(["默认规格"]);
    });

    it("没有规格组时退化为单行默认 SKU", () => {
      const drafts = generateSkuDrafts([], { defaultPriceCents: 9900 });

      expect(drafts).toHaveLength(1);
      expect(drafts[0].name).toBe("默认规格");
      expect(drafts[0].priceCents).toBe(9900);
    });

    it("生成时复用同名 SKU 的价格与状态", () => {
      const existing = [
        {
          id: 1,
          product_id: 1,
          name: "黑 / 40",
          price: 12345,
          status: "disabled"
        }
      ] as unknown as SkuDTO[];

      const drafts = generateSkuDrafts(
        [
          createSpecGroup("颜色", ["黑"]),
          createSpecGroup("尺码", ["40", "41"])
        ],
        { defaultPriceCents: 100, existing }
      );

      expect(drafts[0]).toMatchObject({
        name: "黑 / 40",
        priceCents: 12345,
        status: "disabled"
      });
      expect(drafts[1]).toMatchObject({
        name: "黑 / 41",
        priceCents: 100,
        status: "enabled"
      });
    });
  });

  describe("编辑回显", () => {
    it("从后端 SKU 还原规格组，不凭空增减行", async () => {
      const detail = await getProduct(1001);
      const state = buildSkuEditorState(detail.skus, detail.price);

      expect(state.groups).toHaveLength(2);
      expect(state.groups[0].name).toBe("规格 1");
      expect(state.groups[1].name).toBe("规格 2");
      expect(state.drafts).toHaveLength(3);
      expect(state.drafts.map(item => item.name)).toEqual([
        "黑 / 40",
        "黑 / 41",
        "白 / 40"
      ]);
      // 价格与状态完全沿用后端
      expect(state.drafts[2].priceCents).toBe(33900);
      expect(state.drafts[2].status).toBe("disabled");
    });

    it("名称段数不一致时退化为单组，不猜测矩阵", () => {
      const skus = [
        { id: 1, product_id: 1, name: "黑 / 40", price: 1, status: "enabled" },
        { id: 2, product_id: 1, name: "均码", price: 1, status: "enabled" }
      ] as unknown as SkuDTO[];

      const groups = inferSpecGroups(skus);

      expect(groups).toHaveLength(1);
      expect(groups[0].values).toEqual(["黑 / 40", "均码"]);
    });

    it("无 SKU 时提供一行默认 SKU", () => {
      const state = buildSkuEditorState([], 1234);

      expect(state.groups).toHaveLength(0);
      expect(state.drafts).toHaveLength(1);
      expect(state.drafts[0].priceCents).toBe(1234);
    });

    it("商品表单与 SKU 回显可同时构建", async () => {
      const detail = await getProduct(1001);
      const form = toProductForm(detail);
      const state = buildSkuEditorState(detail.skus, detail.price);

      expect(
        centsToYuanInput(form.priceYuan === null ? 0 : form.priceYuan * 100)
      ).toBe(329);
      expect(state.drafts[0].priceCents).toBe(32900);
    });
  });

  describe("同步计划", () => {
    it("按名称计算创建 / 更新 / 删除", () => {
      const existing = [
        {
          id: 2001,
          product_id: 1001,
          name: "黑 / 40",
          price: 32900,
          status: "enabled"
        },
        {
          id: 2002,
          product_id: 1001,
          name: "黑 / 41",
          price: 32900,
          status: "enabled"
        },
        {
          id: 2003,
          product_id: 1001,
          name: "白 / 40",
          price: 33900,
          status: "disabled"
        }
      ] as unknown as SkuDTO[];

      const plan = planSkuSync(
        [
          draft({ name: "黑 / 40", priceCents: 29900 }),
          draft({ name: "黑 / 41", priceCents: 32900 }),
          draft({ name: "白 / 42", priceCents: 35900, status: "disabled" })
        ],
        existing
      );

      expect(plan.create.map(item => item.name)).toEqual(["白 / 42"]);
      expect(plan.update).toHaveLength(1);
      expect(plan.update[0].existing.id).toBe(2001);
      expect(plan.update[0].payload).toEqual({ price: 29900 });
      expect(plan.remove.map(item => item.id)).toEqual([2003]);
    });

    it("状态变化也会进入更新计划", () => {
      const existing = [
        {
          id: 1,
          product_id: 1,
          name: "默认规格",
          price: 100,
          status: "enabled"
        }
      ] as unknown as SkuDTO[];

      const plan = planSkuSync(
        [draft({ name: "默认规格", priceCents: 100, status: "disabled" })],
        existing
      );

      expect(plan.update[0].payload).toEqual({ status: "disabled" });
    });
  });

  describe("SKU 校验", () => {
    it("名称必填、唯一且不超过 128 字符", () => {
      const errors = validateSkuDrafts([
        draft({ name: "重复" }),
        draft({ name: "重复" }),
        draft({ name: " " }),
        draft({ name: "a".repeat(129) })
      ]);

      expect(errors.some(text => text.includes("重复"))).toBe(true);
      expect(errors.some(text => text.includes("请输入 SKU 名称"))).toBe(true);
      expect(errors.some(text => text.includes("128"))).toBe(true);
    });

    it("价格必须是 0 ~ 999999.99 元之间的整数分", () => {
      const errors = validateSkuDrafts([
        draft({ name: "A", priceCents: -1 }),
        draft({ name: "B", priceCents: 99_999_999 + 1 }),
        draft({ name: "C", priceCents: 1.5 })
      ]);

      expect(errors).toHaveLength(3);
    });

    it("至少保留一行 SKU", () => {
      expect(validateSkuDrafts([])).toEqual(["至少需要一行 SKU"]);
    });

    it("价格换算保持整数分精度", () => {
      expect(yuanToCents(329)).toBe(32900);
      expect(centsToYuanInput(33900)).toBe(339);
    });
  });

  describe("同步到后端", () => {
    it("创建、更新、删除分别调用真实接口并计数", async () => {
      const before = await getProduct(1003);

      const result = await syncProductSkus(
        1003,
        [
          draft({ name: "默认规格", priceCents: 5900 }),
          draft({ name: "礼盒装", priceCents: 6900 })
        ],
        before.skus,
        { create: true, update: true, remove: true }
      );

      expect(result).toMatchObject({
        created: 1,
        updated: 1,
        removed: 0,
        skipped: 0
      });
      expect(result.failures).toEqual([]);

      const after = await getProduct(1003);
      expect(after.skus.map(sku => [sku.name, sku.price])).toEqual([
        ["默认规格", 5900],
        ["礼盒装", 6900]
      ]);
    });

    it("无权限时跳过动作且不伪造成功计数", async () => {
      const before = await getProduct(1003);

      const result = await syncProductSkus(
        1003,
        [draft({ name: "默认规格", priceCents: 9900 })],
        before.skus,
        { create: false, update: false, remove: false }
      );

      expect(result).toMatchObject({
        created: 0,
        updated: 0,
        removed: 0,
        skipped: 1
      });

      const after = await getProduct(1003);
      expect(after.skus[0].price).toBe(4900);
    });

    it("删除有库存记录的 SKU 时保留失败明细", async () => {
      const before = await getProduct(1001);

      const result = await syncProductSkus(
        1001,
        [draft({ name: "黑 / 41", priceCents: 32900 })],
        before.skus,
        { create: true, update: true, remove: true }
      );

      expect(result.removed).toBe(1); // 白 / 40 无库存，可删除
      expect(result.failures).toHaveLength(1); // 黑 / 40 有库存，被后端拒绝
      expect(result.failures[0].name).toBe("黑 / 40");
      expect(result.failures[0].message).toContain("库存记录");
    });

    it("商品更新与 SKU 同步是两条独立链路", async () => {
      await updateProduct(1003, { name: "运动水壶 750ml" });
      const detail = await getProduct(1003);

      expect(detail.name).toBe("运动水壶 750ml");
      expect(detail.skus[0].name).toBe("默认规格");
    });
  });
});
