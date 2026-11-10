/**
 * 订单 API 模块。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET  /admin/orders`       后台分页列表（order:list）
 * - `GET  /admin/orders/:id`   后台详情，含商品快照（order:view）
 * - `POST /admin/orders/:id/ship` 发货（order:ship）
 *
 * 订单状态**只能**通过后端业务接口变更：本模块不提供任何"改 status"的方法，
 * 也不存在通用 PUT。
 */
import { http } from "@/utils/http";
import type {
  AdminOrderListQuery,
  AdminOrderListResult,
  OrderDTO
} from "../types/order";

/** 后台订单分页列表 */
export function listOrders(query: AdminOrderListQuery = {}) {
  return http.get<AdminOrderListResult, undefined>(
    "/admin/orders",
    { params: query },
    { silent: true }
  );
}

/** 后台订单详情（含订单项快照） */
export function getOrder(id: number) {
  return http.get<OrderDTO, undefined>(`/admin/orders/${id}`, undefined, {
    silent: true
  });
}

/**
 * 发货（仅 paid → shipped）。
 *
 * 后端 `ShipReq` 只有路径参数 `id`，**没有**物流公司/运单号字段，
 * 因此请求体为空；存在有效退款申请时后端返回 18005。
 */
export function shipOrder(id: number) {
  return http.post<OrderDTO, undefined>(`/admin/orders/${id}/ship`, undefined, {
    silent: true
  });
}

/**
 * 按订单号精确定位订单 id。
 *
 * 退款列表后端只支持 `order_id` 过滤、没有 `order_no` 参数，
 * 因此"按订单编号搜索退款"需要先调用订单列表（`order_no` 精确匹配），
 * 拿到 id 后再查退款。需要调用者具备 `order:list` 权限。
 *
 * @returns 未找到返回 `null`
 */
export async function findOrderIdByOrderNo(
  orderNo: string
): Promise<number | null> {
  const trimmed = orderNo.trim();
  if (!trimmed) return null;

  const result = await listOrders({ order_no: trimmed, page: 1, size: 1 });
  return result.items[0]?.id ?? null;
}
