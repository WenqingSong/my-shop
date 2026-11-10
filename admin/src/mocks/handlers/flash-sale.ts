import { http } from "msw";
import { mockDb, type MockActivity, type MockActivitySku } from "./mock-db";
import { fail, isAuthorized, ok, toInt, unauthorized } from "./shared";

/**
 * 秒杀活动 Mock（对齐 `internal/logic/flashsale`）。
 *
 * 只实现管理端 Create/Update：
 * - 创建：名称/时间/SKU 绑定（非空、无重复、SKU 存在、价格>0、库存>0），创建即启用；
 * - 更新：部分更新；skus 只允许更新已绑定项，且 total_stock 不得小于 sold。
 *
 * 不涉及 Redis/Kafka/Outbox/库存扣减与请求状态。
 */
const MAX_NAME_LEN = 64;
const MAX_PRICE = 99_999_999;

function parseTime(value: unknown): number | null {
  if (typeof value !== "string" || !value.trim()) return null;
  const ts = Date.parse(value.trim().replace(" ", "T"));
  return Number.isFinite(ts) ? ts : null;
}

function activityPayload(activity: MockActivity) {
  const skus = mockDb.activitySkus
    .filter(item => item.activity_id === activity.id)
    .sort((left, right) => left.id - right.id)
    .map(item => ({
      id: item.id,
      sku_id: item.sku_id,
      flash_price: item.flash_price,
      total_stock: item.total_stock,
      sold: item.sold
    }));

  return {
    id: activity.id,
    name: activity.name,
    status: activity.status,
    start_time: activity.start_time,
    end_time: activity.end_time,
    skus,
    created_at: activity.created_at,
    updated_at: activity.updated_at
  };
}

interface BindingInput {
  skuId: number;
  flashPrice: number;
  totalStock: number;
}

/** 校验绑定列表；返回错误时直接给 12007 */
function parseBindings(input: unknown): BindingInput[] | null {
  if (!Array.isArray(input) || input.length === 0) return null;

  const seen = new Set<number>();
  const result: BindingInput[] = [];

  for (const raw of input) {
    const item = raw as Record<string, unknown>;
    const skuId = toInt(item.sku_id);
    const price = toInt(item.flash_price);
    const stock = toInt(item.total_stock);

    if (skuId === null || skuId <= 0 || seen.has(skuId)) return null;
    seen.add(skuId);

    if (price === null || price <= 0 || price > MAX_PRICE) return null;
    if (stock === null || stock <= 0) return null;

    const sku = mockDb.skus.find(entry => entry.id === skuId);
    if (!sku) return null;

    result.push({ skuId, flashPrice: price, totalStock: stock });
  }

  return result;
}

export const flashSaleHandlers = [
  /** POST /admin/flash-sales */
  http.post("*/api/admin/flash-sales", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const name = String(body.name ?? "").trim();
    if (!name || [...name].length > MAX_NAME_LEN) {
      return fail(12007, "秒杀活动参数非法", 400);
    }

    const start = parseTime(body.start_time);
    const end = parseTime(body.end_time);
    if (start === null || end === null || end <= start) {
      return fail(12007, "秒杀活动参数非法", 400);
    }

    const bindings = parseBindings(body.skus);
    if (!bindings) return fail(12007, "秒杀活动参数非法", 400);

    const activity: MockActivity = {
      id: mockDb.nextActivityId++,
      name,
      status: "enabled",
      start_time: String(body.start_time),
      end_time: String(body.end_time),
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };
    mockDb.activities.push(activity);

    for (const binding of bindings) {
      const skuBinding: MockActivitySku = {
        id: mockDb.nextActivitySkuId++,
        activity_id: activity.id,
        sku_id: binding.skuId,
        flash_price: binding.flashPrice,
        total_stock: binding.totalStock,
        sold: 0
      };
      mockDb.activitySkus.push(skuBinding);
    }

    return ok(activityPayload(activity));
  }),

  /** PUT /admin/flash-sales/:id */
  http.put("*/api/admin/flash-sales/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const activity = mockDb.activities.find(
      item => item.id === Number(params.id)
    );
    if (!activity) return fail(12001, "秒杀活动不存在", 404);

    const body = (await request.json()) as Record<string, unknown>;

    let name = activity.name;
    if (body.name !== undefined) {
      const next = String(body.name).trim();
      if (!next || [...next].length > MAX_NAME_LEN) {
        return fail(12007, "秒杀活动参数非法", 400);
      }
      name = next;
    }

    let status = activity.status;
    if (body.status !== undefined) {
      if (body.status !== "enabled" && body.status !== "disabled") {
        return fail(12007, "秒杀活动参数非法", 400);
      }
      status = body.status;
    }

    let startText = activity.start_time;
    let endText = activity.end_time;
    if (body.start_time !== undefined) startText = String(body.start_time);
    if (body.end_time !== undefined) endText = String(body.end_time);

    const start = parseTime(startText);
    const end = parseTime(endText);
    if (start === null || end === null || end <= start) {
      return fail(12007, "秒杀活动参数非法", 400);
    }

    if (body.skus !== undefined) {
      const bindings = parseBindings(body.skus);
      if (!bindings) return fail(12007, "秒杀活动参数非法", 400);

      for (const binding of bindings) {
        const existing = mockDb.activitySkus.find(
          item =>
            item.activity_id === activity.id && item.sku_id === binding.skuId
        );
        // 未绑定该活动，或库存低于已售 → 后端 12007
        if (!existing || binding.totalStock < existing.sold) {
          return fail(12007, "秒杀活动参数非法", 400);
        }
      }

      for (const binding of bindings) {
        const existing = mockDb.activitySkus.find(
          item =>
            item.activity_id === activity.id && item.sku_id === binding.skuId
        )!;
        existing.flash_price = binding.flashPrice;
        existing.total_stock = binding.totalStock;
      }
    }

    activity.name = name;
    activity.status = status;
    activity.start_time = startText;
    activity.end_time = endText;
    activity.updated_at = mockDb.now();

    return ok(activityPayload(activity));
  })
];
