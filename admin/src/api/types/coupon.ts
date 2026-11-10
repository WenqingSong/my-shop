/**
 * 优惠券模板 DTO（`api/coupon/v1/coupon.go`）。
 *
 * 后端 V1 只支持**满减（固定金额）**一种规则：
 * `threshold_amount`（门槛，0=无门槛）与 `discount_amount`（减免）。
 * 没有折扣率、商品范围、每人限领、领取后有效期等字段。
 *
 * 金额单位：整数分。
 * 后台只有 创建 / 列表 / 停止领取 三个接口，没有详情、编辑、删除、重新启用。
 */
export interface CouponTemplateDTO {
  id: number;
  name: string;
  /** 满减门槛（整数分，0=无门槛） */
  threshold_amount: number;
  /** 减免金额（整数分，>0） */
  discount_amount: number;
  /** 发放总量 */
  total_quantity: number;
  /** 已发放数量（后端真实计数） */
  issued_quantity: number;
  claim_start_time: string | null;
  claim_end_time: string | null;
  use_start_time: string | null;
  use_end_time: string | null;
  /** 1=正常可领取，0=已停止新领取 */
  status: 0 | 1;
  created_at: string | null;
  updated_at: string | null;
}

/** 创建优惠券模板请求（时间必填，后端要求 start < end） */
export interface CreateCouponPayload {
  name: string;
  threshold_amount: number;
  discount_amount: number;
  total_quantity: number;
  claim_start_time: string;
  claim_end_time: string;
  use_start_time: string;
  use_end_time: string;
}
