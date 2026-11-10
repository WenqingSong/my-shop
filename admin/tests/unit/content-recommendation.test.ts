import { beforeEach, describe, expect, it } from "vitest";
import { resetMockDb, mockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  addPositionItem,
  createPosition,
  deletePosition,
  getPosition,
  listPositions,
  removePositionItem,
  updatePosition,
  updatePositionItemSort
} from "@/api/modules/recommendation";
import {
  buildSortPayload,
  emptyPositionForm,
  moveItem,
  toPositionView,
  toRecommendItemView,
  validatePositionForm,
  type RecommendItemView
} from "@/api/adapters/recommendation-adapter";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("商品推荐位模块", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("列表返回全部推荐位（含禁用）", async () => {
    const result = await listPositions();

    expect(result.items.map(item => item.id)).toEqual([8301, 8302]);
    expect(result.items[1].status).toBe(0);
    expect(toPositionView(result.items[0]).statusLabel).toBe("启用");
  });

  it("详情包含按 sort 排序的推荐商品关系", async () => {
    const detail = await getPosition(8301);

    expect(detail.code).toBe("home-top");
    expect(detail.items.map(item => item.product_id)).toEqual([1001, 1002]);
    expect(detail.items.map(item => item.sort)).toEqual([0, 1]);
  });

  it("推荐商品视图在无商品详情时只展示 product_id", () => {
    const view = toRecommendItemView({
      id: 1,
      position_id: 8301,
      product_id: 999,
      sort: 0,
      created_at: null,
      updated_at: null
    });

    expect(view.productName).toBe("商品 #999");
    expect(view.productLoaded).toBe(false);
  });

  it("创建推荐位：重复 code 返回 15002，非法 code 返回 15003", async () => {
    await expectBusinessCode(
      createPosition({ code: "home-top", name: "重复" }),
      15002
    );
    await expectBusinessCode(
      createPosition({ code: "Home Top", name: "非法" }),
      15003
    );
  });

  it("创建推荐位成功并默认启用", async () => {
    const created = await createPosition({
      code: "home-bottom",
      name: "首页底部"
    });

    expect(created.code).toBe("home-bottom");
    expect(created.status).toBe(1);
  });

  it("更新推荐位只允许 name/status，code 不变", async () => {
    const updated = await updatePosition(8301, { name: "顶部精选", status: 0 });

    expect(updated.name).toBe("顶部精选");
    expect(updated.status).toBe(0);
    expect(updated.code).toBe("home-top");
  });

  it("删除推荐位级联删除推荐商品关系", async () => {
    await expect(deletePosition(8301)).resolves.toBeNull();

    expect(mockDb.positions.map(item => item.id)).toEqual([8302]);
    expect(mockDb.recommendItems.some(item => item.position_id === 8301)).toBe(
      false
    );

    await expectBusinessCode(deletePosition(8301), 15001);
  });

  it("添加推荐商品：重复 15005、商品不存在 4001", async () => {
    await expectBusinessCode(
      addPositionItem(8301, { product_id: 1001 }),
      15005
    );
    await expectBusinessCode(
      addPositionItem(8301, { product_id: 999999 }),
      4001
    );

    const added = await addPositionItem(8301, { product_id: 1003, sort: 2 });
    expect(added.product_id).toBe(1003);
  });

  it("移除推荐商品：不存在 15004", async () => {
    await expectBusinessCode(removePositionItem(8301, 1003), 15004);

    await expect(removePositionItem(8301, 1002)).resolves.toBeNull();
    const detail = await getPosition(8301);
    expect(detail.items.map(item => item.product_id)).toEqual([1001]);
  });

  it("全量排序：提交全部商品后按顺序写 sort", async () => {
    const result = await updatePositionItemSort(8301, [1002, 1001]);

    expect(result.items.map(item => item.product_id)).toEqual([1002, 1001]);
    expect(result.items.map(item => item.sort)).toEqual([0, 1]);
  });

  it("排序缺漏商品返回 15006，未加入商品返回 15004", async () => {
    await expectBusinessCode(updatePositionItemSort(8301, [1001]), 15006);
    await expectBusinessCode(
      updatePositionItemSort(8301, [1001, 1002, 1003]),
      15004
    );
  });

  it("排序列表为空或重复返回 15003", async () => {
    await expectBusinessCode(updatePositionItemSort(8301, []), 15003);
    await expectBusinessCode(updatePositionItemSort(8301, [1001, 1001]), 15003);
  });

  it("移动工具生成新数组并支持全量排序载荷", () => {
    const items = [
      { productId: 1 },
      { productId: 2 },
      { productId: 3 }
    ] as RecommendItemView[];

    expect(moveItem(items, 0, 1).map(item => item.productId)).toEqual([
      2, 1, 3
    ]);
    expect(moveItem(items, 0, -1).map(item => item.productId)).toEqual([
      1, 2, 3
    ]);
    expect(buildSortPayload(moveItem(items, 2, -1))).toEqual([1, 3, 2]);
  });

  it("推荐位表单校验：code 规则、名称、状态", () => {
    const errors = validatePositionForm(emptyPositionForm(), "create");
    expect(errors.code).toBe("请输入推荐位 code");
    expect(errors.name).toBe("请输入推荐位名称");

    expect(
      validatePositionForm({ code: "Home-Top", name: "x", status: 1 }, "create")
        .code
    ).toBeDefined();
    expect(
      validatePositionForm({ code: "", name: "x", status: 1 }, "edit").code
    ).toBeUndefined();
  });

  it("使用后端真实权限码", () => {
    expect(PERMISSION.RECOMMEND_CREATE).toBe("recommend:create");
    expect(PERMISSION.RECOMMEND_UPDATE).toBe("recommend:update");
    expect(PERMISSION.RECOMMEND_DELETE).toBe("recommend:delete");
    expect(PERMISSION.RECOMMEND_ITEM).toBe("recommend:item");
  });
});
