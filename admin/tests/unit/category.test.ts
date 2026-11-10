import { beforeEach, describe, expect, it } from "vitest";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  createCategory,
  deleteCategory,
  getCategory,
  listCategories,
  updateCategory
} from "@/api/modules/category";
import {
  buildCategoryParentOptions,
  buildCreateCategoryPayload,
  collectCategoryIds,
  emptyCategoryForm,
  flattenCategoryTree,
  isLeafCategory,
  toCategoryForm,
  toCategoryNameMap,
  validateCategoryForm
} from "@/api/adapters/category-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("商品分类模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  describe("树形列表", () => {
    it("返回树形结构并按 sort 排序", async () => {
      const result = await listCategories();

      expect(result.items.map(item => item.name)).toEqual([
        "运动鞋",
        "服饰",
        "配件"
      ]);
      expect(result.items[0].children?.map(item => item.name)).toEqual([
        "跑步鞋"
      ]);
    });

    it("扁平化保留层级与父级名称", async () => {
      const result = await listCategories();
      const rows = flattenCategoryTree(result.items);

      expect(rows.map(row => [row.id, row.depth, row.parentName])).toEqual([
        [11, 0, ""],
        [12, 1, "运动鞋"],
        [13, 0, ""],
        [14, 1, "服饰"],
        [15, 0, ""]
      ]);
    });

    it("分类名映射可直接用于商品列表", async () => {
      const result = await listCategories();
      const map = toCategoryNameMap(result.items);

      expect(map.get(12)).toBe("跑步鞋");
      expect(map.get(15)).toBe("配件");
    });

    it("叶子分类判断与后端一致", async () => {
      const result = await listCategories();
      const parent = result.items[0];
      const leaf = parent.children?.[0];

      expect(isLeafCategory(parent)).toBe(false);
      expect(leaf && isLeafCategory(leaf)).toBe(true);
    });
  });

  describe("新增与编辑", () => {
    it("新增子分类后出现在父级 children 中", async () => {
      const created = await createCategory({
        parent_id: 11,
        name: "篮球鞋",
        sort: 2,
        status: 1
      });

      expect(created.id).toBeGreaterThan(0);

      const result = await listCategories();
      expect(result.items[0].children?.map(item => item.name)).toEqual([
        "跑步鞋",
        "篮球鞋"
      ]);
    });

    it("同级重名返回 3002", async () => {
      await expectBusinessCode(
        createCategory({ parent_id: 11, name: "跑步鞋" }),
        3002
      );
    });

    it("超过 3 级时返回 3004", async () => {
      // 11 → 12 已经是第 2 级，再挂一级就是第 3 级
      const level3 = await createCategory({ parent_id: 12, name: "竞速跑鞋" });
      expect(level3.id).toBeGreaterThan(0);

      await expectBusinessCode(
        createCategory({ parent_id: level3.id, name: "第 4 级" }),
        3004
      );
    });

    it("编辑回显与更新字段", async () => {
      const detail = await getCategory(12);
      const form = toCategoryForm(detail);

      expect(form).toEqual({
        name: "跑步鞋",
        parentId: 11,
        sort: 1,
        status: 1
      });

      const updated = await updateCategory(12, {
        name: "跑步鞋（改）",
        sort: 9
      });
      expect(updated.name).toBe("跑步鞋（改）");
      expect(updated.sort).toBe(9);
    });

    it("父级下拉禁用自身与后代，避免形成环", async () => {
      const result = await listCategories();
      const options = buildCategoryParentOptions(result.items, 11);

      expect(options.find(option => option.value === 0)?.disabled).toBe(false);
      expect(options.find(option => option.value === 11)?.disabled).toBe(true);
      expect(options.find(option => option.value === 12)?.disabled).toBe(true);
      expect(options.find(option => option.value === 13)?.disabled).toBe(false);
    });

    it("收集自身及后代 id", async () => {
      const result = await listCategories();
      expect(collectCategoryIds(result.items, 11).sort()).toEqual([11, 12]);
    });

    it("分类名必填且不超过 64 字符", () => {
      expect(validateCategoryForm(emptyCategoryForm()).name).toBe(
        "请输入分类名称"
      );
      expect(
        validateCategoryForm({ ...emptyCategoryForm(), name: "a".repeat(65) })
          .name
      ).toContain("64");
    });

    it("排序必须是整数，状态只能是 0/1", () => {
      const errors = validateCategoryForm({
        name: "合法",
        parentId: 0,
        sort: 1.5,
        status: 2
      });

      expect(errors.sort).toBeDefined();
      expect(errors.status).toBeDefined();
    });

    it("创建请求只包含后端支持的字段", () => {
      const payload = buildCreateCategoryPayload({
        name: "  新分类 ",
        parentId: 13,
        sort: 3,
        status: 0
      });

      expect(payload).toEqual({
        parent_id: 13,
        name: "新分类",
        sort: 3,
        status: 0
      });
    });
  });

  describe("删除", () => {
    it("有子分类时后端拒绝删除，不做级联", async () => {
      await expectBusinessCode(deleteCategory(11), 3003);

      const result = await listCategories();
      expect(result.items.map(item => item.id)).toContain(11);
    });

    it("有商品引用时后端拒绝删除", async () => {
      await expectBusinessCode(deleteCategory(12), 3005);
    });

    it("删除失败时展示后端原文", async () => {
      try {
        await deleteCategory(11);
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("有子分类不能删除");
      }

      try {
        await deleteCategory(12);
      } catch (cause) {
        expect(resolveErrorMessage(cause)).toBe("分类下存在商品，不能删除");
      }
    });

    it("无子分类且无商品引用时可以删除", async () => {
      const created = await createCategory({ parent_id: 0, name: "临时分类" });

      await expect(deleteCategory(created.id)).resolves.toBeNull();

      const result = await listCategories();
      expect(result.items.map(item => item.name)).not.toContain("临时分类");
    });
  });

  describe("禁用分类的已知限制", () => {
    it("禁用后从树中消失，但详情接口仍能读到完整记录", async () => {
      await updateCategory(15, { status: 0 });

      const tree = await listCategories();
      expect(tree.items.map(item => item.id)).not.toContain(15);

      const detail = await getCategory(15);
      expect(detail.status).toBe(0);
    });
  });
});
