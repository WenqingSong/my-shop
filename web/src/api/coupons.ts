import { authGetData } from '@/api/auth-request'

/**
 * 优惠券 API（用户侧）。
 *
 * 事实来源：
 * - `api/coupon/v1/coupon.go:40-54`（UserCoupon）、`:106-114`（ListMineReq/Res）；
 * - `internal/cmd/routes_frontend.go:141-142`：
 *   `POST /coupons/:template_id/claim`、`GET /my/coupons`；
 * - `internal/logic/coupon/coupon.go:252-272`（ListMine：LEFT JOIN 模板快照，按 id 倒序）；
 * - `internal/logic/order/order.go:166-178`（下单用券）、`:485-495`（支付核销）、`:756-759`（取消释放）。
 *
 * 能力边界（不得在前端补齐）：
 * 1. 后端**没有**面向普通用户的「可领取优惠券模板列表」接口；
 *    `/admin/coupons` 需要管理员权限，普通用户 token 返回 403；
 * 2. 因此本期不实现领券中心，也不在前端硬编码模板 id 或虚构优惠券活动；
 * 3. 没有「订单可用券查询 / 优惠试算」接口，前端只能基于金额与有效期做**估算**，
 *    最终可用性、优惠金额、订单金额全部由 `POST /orders` 服务端判定；
 * 4. 券是**平台通用固定金额满减券**，后端没有商品/分类「适用范围」字段。
 */

/**
 * 用户券状态枚举（与 `api/coupon/v1/coupon.go:10-15` 一一对应）。
 *
 * DB 存 TINYINT 10/20/30。**后端没有 expired 状态**：
 * 过期只体现为 `use_end_time` 已过，由服务端在下单时拒绝（19007）。
 */
export type UserCouponStatus = 'available' | 'locked' | 'used'

/**
 * 用户已领取券（含模板关键字段快照）。
 *
 * 金额均为整数分，由服务端返回；前端只展示与估算，不做权威计算。
 */
export interface UserCoupon {
  id: number
  template_id: number
  status: UserCouponStatus
  /** 当前预占/核销该券的订单 id；释放后为 null。 */
  order_id: number | null
  received_at: string | null
  locked_at: string | null
  used_at: string | null
  /** 模板名（后端 LEFT JOIN 快照）。 */
  name: string
  /** 满减门槛（整数分，0 表示无门槛）。 */
  threshold_amount: number
  /** 减免金额（整数分）。 */
  discount_amount: number
  use_start_time: string | null
  use_end_time: string | null
}

/** GET /my/coupons 响应 data：本人优惠券，按 id 倒序，无分页。 */
export interface MyCouponsResult {
  items: UserCoupon[]
}

/**
 * GET /my/coupons：查询本人优惠券。
 *
 * 后端没有分页与状态筛选参数，状态筛选只能在浏览器本地完成。
 * `order_id` 可用于跳转关联订单（locked/used 时存在）。
 */
export function fetchMyCoupons(signal?: AbortSignal): Promise<MyCouponsResult> {
  return authGetData<MyCouponsResult>('/my/coupons', { signal })
}

/**
 * 领取优惠券（后端存在，但**本期不接入**）。
 *
 * 保留说明而不是实现：`POST /coupons/:template_id/claim` 需要真实 template_id，
 * 而普通用户没有任何获取可领取模板的公开接口。在没有数据来源的情况下实现
 * 「输入券 ID 领取」属于无业务价值的伪功能，本期不提供。
 */
export const COUPON_CLAIM_NOT_AVAILABLE_REASON =
  '后端没有面向用户的公开优惠券模板列表接口，本期不提供领券中心。'
