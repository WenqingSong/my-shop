import { http } from "msw";
import { mockDb, type MockCategory } from "./mock-db";
import { fail, isAuthorized, ok, toInt, unauthorized } from "./shared";

/**
 * 分类 Mock。
 *
 * 行为对齐后端：
 * - `GET /categories` 公开、树形、**仅启用项**；
 * - 删除有子分类 → 3003；有商品引用 → 3005；
 * - 层级上限 3 级，父级非法 → 3004；同级重名 → 3002。
 */
const MAX_LEVEL = 3;

function buildTree(nodes: MockCategory[]): MockCategory[] {
  const enabled = nodes.filter(node => node.status === 1);
  const byParent = new Map<number, MockCategory[]>();

  for (const node of enabled) {
    const siblings = byParent.get(node.parent_id) ?? [];
    siblings.push(node);
    byParent.set(node.parent_id, siblings);
  }

  const sortNodes = (list: MockCategory[]) =>
    [...list].sort(
      (left, right) => left.sort - right.sort || left.id - right.id
    );

  const walk = (
    parentId: number
  ): Array<MockCategory & { children?: MockCategory[] }> =>
    sortNodes(byParent.get(parentId) ?? []).map(node => {
      const children = walk(node.id);
      return children.length > 0 ? { ...node, children } : { ...node };
    });

  return walk(0) as MockCategory[];
}

function findCategory(id: number) {
  return mockDb.categories.find(category => category.id === id) ?? null;
}

/** 分类深度：顶级 = 1 */
function depthOf(category: MockCategory): number {
  let depth = 1;
  let current = category;
  const seen = new Set<number>([category.id]);
  while (current.parent_id !== 0) {
    const parent = findCategory(current.parent_id);
    if (!parent || seen.has(parent.id)) return MAX_LEVEL + 1;
    seen.add(parent.id);
    depth += 1;
    current = parent;
  }
  return depth;
}

function isDescendant(candidateId: number, ancestorId: number): boolean {
  let current = findCategory(candidateId);
  const seen = new Set<number>();
  while (current && current.parent_id !== 0) {
    if (current.parent_id === ancestorId) return true;
    if (seen.has(current.id)) return false;
    seen.add(current.id);
    current = findCategory(current.parent_id);
  }
  return false;
}

function nameConflict(
  parentId: number,
  name: string,
  excludeId?: number
): boolean {
  return mockDb.categories.some(
    category =>
      category.parent_id === parentId &&
      category.name === name &&
      category.id !== excludeId
  );
}

export const categoryHandlers = [
  /** GET /categories（公开，仅启用项，树形） */
  http.get("*/api/categories", () =>
    ok({ items: buildTree(mockDb.categories) })
  ),

  /** GET /categories/:id */
  http.get("*/api/categories/:id", ({ params }) => {
    const category = findCategory(Number(params.id));
    if (!category) return fail(3001, "分类不存在", 404);
    return ok(category);
  }),

  /** POST /categories */
  http.post("*/api/categories", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const name = String(body.name ?? "").trim();
    if (!name) return fail(1001, "参数错误");
    if ([...name].length > 64) return fail(1001, "参数错误");

    const parentId = toInt(body.parent_id) ?? 0;
    if (parentId !== 0) {
      const parent = findCategory(parentId);
      if (!parent) return fail(3004, "父分类非法");
      if (depthOf(parent) + 1 > MAX_LEVEL) return fail(3004, "父分类非法");
    }

    if (nameConflict(parentId, name)) return fail(3002, "同级重名", 409);

    const status = toInt(body.status) ?? 1;
    if (status !== 0 && status !== 1) return fail(1001, "参数错误");

    const id = mockDb.nextCategoryId++;
    mockDb.categories.push({
      id,
      parent_id: parentId,
      name,
      sort: toInt(body.sort) ?? 0,
      status,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    });

    return ok({ id });
  }),

  /** PUT /categories/:id */
  http.put("*/api/categories/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const category = findCategory(Number(params.id));
    if (!category) return fail(3001, "分类不存在", 404);

    const body = (await request.json()) as Record<string, unknown>;

    if (body.name !== undefined) {
      const name = String(body.name).trim();
      if (!name || [...name].length > 64) return fail(1001, "参数错误");
      if (nameConflict(category.parent_id, name, category.id)) {
        return fail(3002, "同级重名", 409);
      }
      category.name = name;
    }

    if (body.parent_id !== undefined) {
      const parentId = toInt(body.parent_id) ?? 0;
      if (parentId === category.id || isDescendant(parentId, category.id)) {
        return fail(3004, "父分类非法");
      }
      if (parentId !== 0) {
        const parent = findCategory(parentId);
        if (!parent) return fail(3004, "父分类非法");
        if (depthOf(parent) + 1 > MAX_LEVEL) return fail(3004, "父分类非法");
      }
      if (
        nameConflict(
          parentId,
          body.name !== undefined ? String(body.name) : category.name,
          category.id
        )
      ) {
        return fail(3002, "同级重名", 409);
      }
      category.parent_id = parentId;
    }

    if (body.sort !== undefined) {
      const sort = toInt(body.sort);
      if (sort === null) return fail(1001, "参数错误");
      category.sort = sort;
    }

    if (body.status !== undefined) {
      const status = toInt(body.status);
      if (status !== 0 && status !== 1) return fail(1001, "参数错误");
      category.status = status;
    }

    category.updated_at = mockDb.now();
    return ok(category);
  }),

  /** DELETE /categories/:id */
  http.delete("*/api/categories/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const id = Number(params.id);
    const category = findCategory(id);
    if (!category) return fail(3001, "分类不存在", 404);

    const hasChildren = mockDb.categories.some(item => item.parent_id === id);
    if (hasChildren) return fail(3003, "有子分类不能删除", 409);

    const hasProducts = mockDb.products.some(item => item.category_id === id);
    if (hasProducts) {
      return fail(3005, "分类下存在商品，不能删除", 409);
    }

    mockDb.categories = mockDb.categories.filter(item => item.id !== id);
    return ok(null);
  })
];
