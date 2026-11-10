import { http } from "msw";
import { mockDb, type MockInventoryLog } from "./mock-db";
import {
  fail,
  isAuthorized,
  ok,
  queryInt,
  toInt,
  unauthorized
} from "./shared";

/**
 * 库存 Mock。
 *
 * 行为对齐 `internal/logic/inventory`：
 * - 增减数量必须是正整数，否则 6002；
 * - 扣减库存不足返回 6001；
 * - 增加是 upsert（首次即初始化）；
 * - 每次变更写一条流水，reason 恒为空（后端请求没有 reason 字段）；
 * - 没有库存记录时查询返回 quantity = 0 且 created_at = null。
 */
function skuExists(skuId: number) {
  return mockDb.skus.some(sku => sku.id === skuId);
}

function appendLog(
  skuId: number,
  changeType: "increase" | "deduct",
  quantity: number,
  beforeQty: number,
  afterQty: number
) {
  const log: MockInventoryLog = {
    id: mockDb.nextLogId++,
    sku_id: skuId,
    change_type: changeType,
    change_qty: quantity,
    before_qty: beforeQty,
    after_qty: afterQty,
    operator_admin_id: 1,
    reason: "",
    created_at: mockDb.now()
  };
  mockDb.logs.push(log);
  return log;
}

function readQuantity(skuId: number): number {
  return mockDb.inventories.get(skuId)?.quantity ?? 0;
}

function writeQuantity(skuId: number, quantity: number) {
  const existed = mockDb.inventories.get(skuId);
  mockDb.inventories.set(skuId, {
    sku_id: skuId,
    quantity,
    created_at: existed?.created_at ?? mockDb.now(),
    updated_at: mockDb.now()
  });
}

function inventoryPayload(skuId: number) {
  const record = mockDb.inventories.get(skuId);
  if (!record) {
    return {
      sku_id: skuId,
      quantity: 0,
      created_at: null,
      updated_at: null
    };
  }
  return { ...record };
}

export const inventoryHandlers = [
  /** GET /admin/inventories/:sku_id */
  http.get("*/api/admin/inventories/:sku_id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const skuId = Number(params.sku_id);
    if (!skuExists(skuId)) return fail(5001, "SKU 不存在", 404);
    return ok(inventoryPayload(skuId));
  }),

  /** GET /admin/inventories/:sku_id/logs */
  http.get("*/api/admin/inventories/:sku_id/logs", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const skuId = Number(params.sku_id);
    if (!skuExists(skuId)) return fail(5001, "SKU 不存在", 404);

    const url = new URL(request.url);
    const page = queryInt(url.searchParams, "page", 1);
    const size = Math.min(queryInt(url.searchParams, "size", 20), 100);

    const all = mockDb.logs
      .filter(log => log.sku_id === skuId)
      .sort((left, right) => right.id - left.id);
    const start = (page - 1) * size;

    return ok({
      items: all.slice(start, start + size),
      total: all.length,
      page,
      size
    });
  }),

  /** POST /admin/inventories/:sku_id/increase */
  http.post(
    "*/api/admin/inventories/:sku_id/increase",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();
      const skuId = Number(params.sku_id);
      if (!skuExists(skuId)) return fail(5001, "SKU 不存在", 404);

      const body = (await request.json()) as Record<string, unknown>;
      const quantity = toInt(body.quantity);
      if (quantity === null || quantity < 1) {
        return fail(6002, "库存数量非法", 400);
      }

      const before = readQuantity(skuId);
      const after = before + quantity;
      writeQuantity(skuId, after);
      appendLog(skuId, "increase", quantity, before, after);

      return ok(inventoryPayload(skuId));
    }
  ),

  /** POST /admin/inventories/:sku_id/deduct */
  http.post(
    "*/api/admin/inventories/:sku_id/deduct",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();
      const skuId = Number(params.sku_id);
      if (!skuExists(skuId)) return fail(5001, "SKU 不存在", 404);

      const body = (await request.json()) as Record<string, unknown>;
      const quantity = toInt(body.quantity);
      if (quantity === null || quantity < 1) {
        return fail(6002, "库存数量非法", 400);
      }

      const before = readQuantity(skuId);
      if (before < quantity) return fail(6001, "库存不足", 409);

      const after = before - quantity;
      writeQuantity(skuId, after);
      appendLog(skuId, "deduct", quantity, before, after);

      return ok(inventoryPayload(skuId));
    }
  )
];
