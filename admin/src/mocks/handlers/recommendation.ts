import { http } from "msw";
import { mockDb, type MockPosition, type MockRecommendItem } from "./mock-db";
import { fail, isAuthorized, ok, toInt, unauthorized } from "./shared";

/**
 * 推荐位 Mock（对齐 `internal/logic/recommendation/recommendation.go`）。
 *
 * - 推荐位 code 创建后不可变，重复 → 15002；
 * - 添加商品：商品必须存在（任意状态），重复 → 15005；
 * - 排序：必须非空、无重复、恰好覆盖全部已加入商品，缺漏 → 15006；
 * - 删除推荐位级联删除关系。
 */
const CODE_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;
const MAX_NAME = 64;

function findPosition(id: number): MockPosition | null {
  return mockDb.positions.find(item => item.id === id) ?? null;
}

function toPosition(position: MockPosition) {
  return { ...position };
}

function toItem(item: MockRecommendItem) {
  return { ...item };
}

function itemsOf(positionId: number): MockRecommendItem[] {
  return mockDb.recommendItems
    .filter(item => item.position_id === positionId)
    .sort((left, right) => left.sort - right.sort || left.id - right.id);
}

export const recommendationHandlers = [
  /** GET /admin/recommend-positions */
  http.get("*/api/admin/recommend-positions", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const items = [...mockDb.positions]
      .sort((left, right) => left.id - right.id)
      .map(toPosition);
    return ok({ items });
  }),

  /** GET /admin/recommend-positions/:id */
  http.get("*/api/admin/recommend-positions/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const position = findPosition(Number(params.id));
    if (!position) return fail(15001, "推荐位不存在", 404);
    return ok({
      ...toPosition(position),
      items: itemsOf(position.id).map(toItem)
    });
  }),

  /** POST /admin/recommend-positions */
  http.post("*/api/admin/recommend-positions", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const code = String(body.code ?? "").trim();
    if (!CODE_PATTERN.test(code)) return fail(15003, "推荐位参数非法", 400);

    const name = String(body.name ?? "").trim();
    if (!name || [...name].length > MAX_NAME) {
      return fail(15003, "推荐位参数非法", 400);
    }

    const status = body.status === undefined ? 1 : toInt(body.status);
    if (status !== 0 && status !== 1) {
      return fail(15003, "推荐位参数非法", 400);
    }

    if (mockDb.positions.some(item => item.code === code)) {
      return fail(15002, "推荐位 code 已存在", 409);
    }

    const position: MockPosition = {
      id: mockDb.nextPositionId++,
      code,
      name,
      status: status as 0 | 1,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };
    mockDb.positions.push(position);
    return ok(toPosition(position));
  }),

  /** PUT /admin/recommend-positions/:id */
  http.put(
    "*/api/admin/recommend-positions/:id",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const position = findPosition(Number(params.id));
      if (!position) return fail(15001, "推荐位不存在", 404);

      const body = (await request.json()) as Record<string, unknown>;

      if (body.name !== undefined) {
        const name = String(body.name).trim();
        if (!name || [...name].length > MAX_NAME) {
          return fail(15003, "推荐位参数非法", 400);
        }
        position.name = name;
      }
      if (body.status !== undefined) {
        const status = toInt(body.status);
        if (status !== 0 && status !== 1) {
          return fail(15003, "推荐位参数非法", 400);
        }
        position.status = status as 0 | 1;
      }

      position.updated_at = mockDb.now();
      return ok(toPosition(position));
    }
  ),

  /** DELETE /admin/recommend-positions/:id */
  http.delete("*/api/admin/recommend-positions/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const index = mockDb.positions.findIndex(item => item.id === id);
    if (index < 0) return fail(15001, "推荐位不存在", 404);

    mockDb.positions.splice(index, 1);
    mockDb.recommendItems = mockDb.recommendItems.filter(
      item => item.position_id !== id
    );
    return ok(null);
  }),

  /** POST /admin/recommend-positions/:id/items */
  http.post(
    "*/api/admin/recommend-positions/:id/items",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const position = findPosition(Number(params.id));
      if (!position) return fail(15001, "推荐位不存在", 404);

      const body = (await request.json()) as Record<string, unknown>;
      const productId = toInt(body.product_id);
      if (productId === null || productId <= 0) {
        return fail(15003, "推荐位参数非法", 400);
      }

      const product = mockDb.products.find(item => item.id === productId);
      if (!product) return fail(4001, "商品不存在", 404);

      const duplicated = mockDb.recommendItems.some(
        item =>
          item.position_id === position.id && item.product_id === productId
      );
      if (duplicated) return fail(15005, "推荐商品已存在", 409);

      const item: MockRecommendItem = {
        id: mockDb.nextRecommendItemId++,
        position_id: position.id,
        product_id: productId,
        sort: toInt(body.sort) ?? 0,
        created_at: mockDb.now(),
        updated_at: mockDb.now()
      };
      mockDb.recommendItems.push(item);
      return ok(toItem(item));
    }
  ),

  /** DELETE /admin/recommend-positions/:id/items/:product_id */
  http.delete(
    "*/api/admin/recommend-positions/:id/items/:product_id",
    ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const positionId = Number(params.id);
      const productId = Number(params.product_id);

      const index = mockDb.recommendItems.findIndex(
        item => item.position_id === positionId && item.product_id === productId
      );
      if (index < 0) return fail(15004, "推荐商品关系不存在", 404);

      mockDb.recommendItems.splice(index, 1);
      return ok(null);
    }
  ),

  /** PUT /admin/recommend-positions/:id/items/sort */
  http.put(
    "*/api/admin/recommend-positions/:id/items/sort",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const position = findPosition(Number(params.id));
      if (!position) return fail(15001, "推荐位不存在", 404);

      const body = (await request.json()) as Record<string, unknown>;
      const raw = Array.isArray(body.product_ids) ? body.product_ids : [];
      const productIds = raw.map(value => toInt(value));

      if (productIds.length === 0) {
        return fail(15003, "推荐位参数非法", 400);
      }

      const seen = new Set<number>();
      for (const pid of productIds) {
        if (pid === null || pid <= 0 || seen.has(pid)) {
          return fail(15003, "推荐位参数非法", 400);
        }
        seen.add(pid);
      }

      const existing = itemsOf(position.id);
      const existingSet = new Set(existing.map(item => item.product_id));

      for (const pid of productIds) {
        if (!existingSet.has(pid!)) {
          return fail(15004, "推荐商品关系不存在", 404);
        }
      }
      for (const item of existing) {
        if (!seen.has(item.product_id)) {
          return fail(15006, "排序未覆盖全部推荐商品", 409);
        }
      }

      productIds.forEach((pid, index) => {
        const item = mockDb.recommendItems.find(
          entry => entry.position_id === position.id && entry.product_id === pid
        )!;
        item.sort = index;
        item.updated_at = mockDb.now();
      });

      return ok({ items: itemsOf(position.id).map(toItem) });
    }
  )
];
