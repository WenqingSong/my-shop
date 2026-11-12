import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  createProduct,
  getProduct,
  listProducts,
  offShelfProduct,
  onShelfProduct,
  scanProducts,
  updateProduct
} from "@/api/modules/product";
import {
  buildCreatePayload,
  emptyProductForm,
  filterProductsByStatus,
  hasFormErrors,
  slicePage,
  sortProducts,
  toProductForm,
  toProductQuery,
  toProductView,
  validateProductForm,
  yuanToCents
} from "@/api/adapters/product-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("商品模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("列表与筛选", () => {
    it("按后端 DTO 解析列表、分页与价格（整数分）", async () => {
      const result = await listProducts({ page: 1, size: 10 });

      expect(result.total).toBe(3);
      expect(result.items).toHaveLength(3);

      // 默认按 id 倒序，最新商品在前
      expect(result.items[0].id).toBe(1003);

      const runner = result.items.find(item => item.id === 1001);
      expect(runner).toMatchObject({
        name: "轻量跑步鞋",
        category_id: 12,
        price: 32900,
        status: "on_shelf"
      });

      const view = toProductView(runner!);
      expect(view.priceText).toBe("¥329.00");
      expect(view.statusLabel).toBe("已上架");
    });

    it("分页只返回当前页数据，total 仍是全量", async () => {
      const first = await listProducts({ page: 1, size: 2 });
      const second = await listProducts({ page: 2, size: 2 });

      expect(first.total).toBe(3);
      expect(first.items.map(item => item.id)).toEqual([1003, 1002]);
      expect(second.items.map(item => item.id)).toEqual([1001]);
    });

    it("关键词同时匹配商品名与品牌", async () => {
      const byName = await listProducts({ keyword: "跑步" });
      const byBrand = await listProducts({ keyword: "surge" });

      expect(byName.items.map(item => item.id)).toEqual([1001]);
      expect(byBrand.total).toBe(2);
    });

    it("分类筛选用真实 category_id 参数", async () => {
      const result = await listProducts({ category_id: 14 });

      expect(result.total).toBe(1);
      expect(result.items[0].id).toBe(1002);
    });

    it("状态筛选不会发送给后端（后端无 status 参数）", () => {
      const query = toProductQuery({
        page: 2,
        size: 20,
        status: "draft",
        keyword: "  鞋  ",
        categoryId: 12
      });

      expect(query).toEqual({
        page: 2,
        size: 20,
        keyword: "鞋",
        category_id: 12
      });
      expect("status" in query).toBe(false);
    });

    it("跨页拉取后前端过滤 + 内存分页得到真实草稿商品", async () => {
      const scan = await scanProducts({});
      const drafts = filterProductsByStatus(scan.items, "draft");
      const pageItems = slicePage(drafts, 1, 10);

      expect(scan.truncated).toBe(false);
      expect(drafts.map(item => item.id)).toEqual([1002]);
      expect(pageItems).toHaveLength(1);
    });

    it("跨页排序按价格与时间生效", () => {
      const items = [
        { id: 1, price: 300, created_at: "2026-11-02 00:00:00" },
        { id: 2, price: 100, created_at: "2026-11-01 00:00:00" }
      ] as unknown as Parameters<typeof sortProducts>[0];

      expect(sortProducts(items, "price", "asc").map(item => item.id)).toEqual([
        2, 1
      ]);
      expect(
        sortProducts(items, "created_at", "desc").map(item => item.id)
      ).toEqual([1, 2]);
    });
  });

  describe("详情、创建与编辑", () => {
    it("详情包含全部状态 SKU", async () => {
      const detail = await getProduct(1001);

      expect(detail.name).toBe("轻量跑步鞋");
      expect(detail.skus).toHaveLength(3);
      expect(detail.skus.map(sku => sku.name)).toEqual([
        "黑 / 40",
        "黑 / 41",
        "白 / 40"
      ]);
    });

    it("创建商品由后端强制为 draft，并返回新 id", async () => {
      const created = await createProduct({
        name: "新品测试",
        brand: "Surge",
        category_id: 14,
        price: 12900,
        main_image: "",
        detail: "",
        images: []
      });

      expect(created.id).toBeGreaterThan(1003);
      expect(created.status).toBe("draft");
      expect(created.price).toBe(12900);

      const list = await listProducts({ keyword: "新品测试" });
      expect(list.total).toBe(1);
    });

    it("更新商品不会改变状态（后端忽略 status）", async () => {
      const before = await getProduct(1002);
      const updated = await updateProduct(1002, {
        name: "基础训练 T 恤（改）",
        ...({ status: "on_shelf" } as object)
      });

      expect(updated.name).toBe("基础训练 T 恤（改）");
      expect(updated.status).toBe(before.status);
    });

    it("编辑回显把分转换为元", async () => {
      const detail = await getProduct(1001);
      const form = toProductForm(detail);

      expect(form.priceYuan).toBe(329);
      expect(form.categoryId).toBe(12);
      expect(form.images).toHaveLength(1);
    });

    it("创建请求把元转换为整数分", () => {
      const form = emptyProductForm();
      Object.assign(form, {
        name: " 换算测试 ",
        brand: " ",
        categoryId: 14,
        priceYuan: 199.99,
        mainImage: "",
        detail: "detail",
        images: ["https://mock-upload.local/files/a.png"]
      });

      expect(buildCreatePayload(form)).toMatchObject({
        name: "换算测试",
        brand: "",
        price: 19999,
        category_id: 14
      });
    });
  });

  describe("上下架", () => {
    it("草稿商品可以上架，上架后可下架", async () => {
      const onShelf = await onShelfProduct(1002);
      expect(onShelf.status).toBe("on_shelf");

      const offShelf = await offShelfProduct(1002);
      expect(offShelf.status).toBe("off_shelf");
    });

    it("已上架商品重复上架返回后端状态迁移错误", async () => {
      await expectBusinessCode(onShelfProduct(1001), 4005);
    });

    it("状态迁移失败时保留后端 message 供页面展示", async () => {
      try {
        await onShelfProduct(1001);
        throw new Error("不应成功");
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("商品状态迁移非法");
      }
    });
  });

  describe("表单校验", () => {
    it("必填项缺失时给出字段级错误", () => {
      const errors = validateProductForm(emptyProductForm());

      expect(errors.name).toBe("请输入商品名称");
      expect(errors.categoryId).toBe("请选择商品分类");
      expect(errors.price).toBe("请输入商品价格");
      expect(hasFormErrors(errors)).toBe(true);
    });

    it("价格超出后端上限或小数位过多被拒绝", () => {
      const base = {
        ...emptyProductForm(),
        name: "合法名称",
        categoryId: 14
      };

      expect(
        validateProductForm({ ...base, priceYuan: 1000000 }).price
      ).toBeDefined();
      expect(
        validateProductForm({ ...base, priceYuan: 1.234 }).price
      ).toBeDefined();
      expect(
        validateProductForm({ ...base, priceYuan: 999999.99 }).price
      ).toBeUndefined();
    });

    it("商品名称超长被拒绝", () => {
      const form = {
        ...emptyProductForm(),
        name: "a".repeat(129),
        priceYuan: 1,
        categoryId: 14
      };
      expect(validateProductForm(form).name).toContain("128");
    });

    it("元与分互转是精确的", () => {
      expect(yuanToCents(0.05)).toBe(5);
      expect(yuanToCents(12.34)).toBe(1234);
      expect(yuanToCents(-1)).toBeNull();
      expect(yuanToCents("abc")).toBeNull();
    });

    it("提交时未发送未持久化字段（无 status / subtitle）", () => {
      const payload = buildCreatePayload({
        ...emptyProductForm(),
        name: "字段边界",
        categoryId: 14,
        priceYuan: 10
      });

      expect(Object.keys(payload).sort()).toEqual([
        "brand",
        "category_id",
        "detail",
        "images",
        "main_image",
        "name",
        "price"
      ]);
    });
  });

  describe("错误状态", () => {
    it("403 被归一化为权限提示", async () => {
      server.use(
        mswHttp.get("*/api/admin/products", () =>
          HttpResponse.json(
            { code: 1003, message: "没有访问权限", data: null },
            { status: 403 }
          )
        )
      );

      await expect(listProducts()).rejects.toBeTruthy();
      try {
        await listProducts();
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
      }
    });

    it("业务错误保留后端原文", async () => {
      server.use(
        mswHttp.get("*/api/admin/products", () =>
          HttpResponse.json({ code: 1000, message: "内部错误", data: null })
        )
      );

      try {
        await listProducts();
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("内部错误");
      }
    });
  });

  describe("权限码", () => {
    it("使用后端真实权限码，不编造读权限", () => {
      expect(PERMISSION.PRODUCT_CREATE).toBe("product:create");
      expect(PERMISSION.PRODUCT_UPDATE).toBe("product:update");
      expect(PERMISSION.PRODUCT_ON_SHELF).toBe("product:on_shelf");
      expect(PERMISSION.PRODUCT_OFF_SHELF).toBe("product:off_shelf");

      const codes: string[] = Object.values(PERMISSION);
      expect(codes.some(code => code.startsWith("product:read"))).toBe(false);
      expect(
        codes.some(
          code => code.endsWith(":delete") && code.startsWith("product:")
        )
      ).toBe(false);
    });
  });
});
