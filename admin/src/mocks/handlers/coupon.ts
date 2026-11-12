import { http } from "msw";
import { mockDb, type MockCoupon } from "./mock-db";
import { fail, isAuthorized, ok, toInt, unauthorized } from "./shared";

/**
 * 优惠券 Mock（对齐 `internal/logic/coupon/coupon.go`）。
 *
 * - 只有创建 / 列表 / 停止领取；
 * - 创建时领取与使用时间均必填，且 start < end；
 * - 新建模板 status=1、issued_quantity=0；
 * - 停止领取幂等，只把 1 → 0。
 */
const MAX_AMOUNT = 99_999_999;
const MAX_NAME_LEN = 64;

function parseTime(value: unknown): number | null {
  if (typeof value !== "string" || !value.trim()) return null;
  const ts = Date.parse(value.trim().replace(" ", "T"));
  return Number.isFinite(ts) ? ts : null;
}

function toTemplate(coupon: MockCoupon) {
  return { ...coupon };
}

export const couponHandlers = [
  /** GET /admin/coupons */
  http.get("*/api/admin/coupons", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const items = [...mockDb.coupons]
      .sort((left, right) => right.id - left.id)
      .map(toTemplate);
    return ok({ items });
  }),

  /** POST /admin/coupons */
  http.post("*/api/admin/coupons", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const name = String(body.name ?? "").trim();
    if (!name || [...name].length > MAX_NAME_LEN) {
      return fail(19002, "优惠券模板参数非法", 400);
    }

    const threshold = toInt(body.threshold_amount);
    if (threshold === null || threshold < 0 || threshold > MAX_AMOUNT) {
      return fail(19002, "优惠券模板参数非法", 400);
    }

    const discount = toInt(body.discount_amount);
    if (discount === null || discount <= 0 || discount > MAX_AMOUNT) {
      return fail(19002, "优惠券模板参数非法", 400);
    }

    const total = toInt(body.total_quantity);
    if (total === null || total <= 0) {
      return fail(19002, "优惠券模板参数非法", 400);
    }

    const claimStart = parseTime(body.claim_start_time);
    const claimEnd = parseTime(body.claim_end_time);
    const useStart = parseTime(body.use_start_time);
    const useEnd = parseTime(body.use_end_time);
    if (
      claimStart === null ||
      claimEnd === null ||
      useStart === null ||
      useEnd === null ||
      claimEnd <= claimStart ||
      useEnd <= useStart
    ) {
      return fail(19002, "优惠券模板参数非法", 400);
    }

    const coupon: MockCoupon = {
      id: mockDb.nextCouponId++,
      name,
      threshold_amount: threshold,
      discount_amount: discount,
      total_quantity: total,
      issued_quantity: 0,
      claim_start_time: String(body.claim_start_time),
      claim_end_time: String(body.claim_end_time),
      use_start_time: String(body.use_start_time),
      use_end_time: String(body.use_end_time),
      status: 1,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };

    mockDb.coupons.push(coupon);
    return ok(toTemplate(coupon));
  }),

  /** POST /admin/coupons/:id/stop */
  http.post("*/api/admin/coupons/:id/stop", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const coupon = mockDb.coupons.find(item => item.id === Number(params.id));
    if (!coupon) return fail(19001, "优惠券模板不存在", 404);

    if (coupon.status === 1) {
      coupon.status = 0;
      coupon.updated_at = mockDb.now();
    }
    return ok(toTemplate(coupon));
  })
];
