/**
 * 优惠券模板 API 模块。
 *
 * 真实路由：
 * - `POST /admin/coupons`            创建（coupon:create）
 * - `GET  /admin/coupons`            列表（coupon:list，全部模板、无分页参数）
 * - `POST /admin/coupons/:id/stop`   停止新领取（coupon:stop，幂等）
 *
 * 没有详情 / 编辑 / 删除 / 重新启用接口。
 */
import { http } from "@/utils/http";
import type { CouponTemplateDTO, CreateCouponPayload } from "../types/coupon";

/** 全部优惠券模板（按 id 倒序，后端一次性返回） */
export function listCoupons() {
  return http.get<{ items: CouponTemplateDTO[] }, undefined>(
    "/admin/coupons",
    undefined,
    { silent: true }
  );
}

/** 创建优惠券模板 */
export function createCoupon(payload: CreateCouponPayload) {
  return http.post<CouponTemplateDTO, typeof payload>(
    "/admin/coupons",
    { data: payload },
    { silent: true }
  );
}

/** 停止新领取（status 1 → 0，已领取的券不受影响） */
export function stopCoupon(id: number) {
  return http.post<CouponTemplateDTO, undefined>(
    `/admin/coupons/${id}/stop`,
    undefined,
    { silent: true }
  );
}
