import { authDeleteData, authGetData, authPostData, authPutData } from '@/api/auth-request'
import type { CartItem } from '@/api/types'

/**
 * 购物车 API（全部需要登录）。
 *
 * 事实来源：
 * - api/cart/v1/cart.go（CartItem / ListReq / AddReq / UpdateQuantityReq / UpdateSelectedReq / DeleteReq）
 * - internal/cmd/routes_frontend.go（/cart 与 /cart/items 路由，挂在 middleware.Auth 分组下）
 * - internal/logic/cart/cart.go（1..999 数量上限、原子累加、可购性与库存动态判定）
 * - internal/controller/cart/cart.go（缺失 Principal 返回 401）
 *
 * 后端没有「批量勾选 / 批量删除 / 购物车计数」接口：
 * 全选只能逐个条目调用 PUT /cart/items/:id/selected，
 * Header 数量由 GET /cart 的条目在客户端汇总，不额外请求计数接口。
 */

/** 后端 maxQuantity 常量（internal/logic/cart/cart.go）。 */
export const CART_QUANTITY_MIN = 1
export const CART_QUANTITY_MAX = 999

/** GET /cart 响应 data；无分页，返回本人全部条目（按 id 升序）。 */
export interface CartListResult {
  items: CartItem[]
}

/** GET /cart：列出本人购物车条目，包含实时可购性、库存与价格变化标识。 */
export function fetchCart(signal?: AbortSignal): Promise<CartListResult> {
  return authGetData<CartListResult>('/cart', { signal })
}

/**
 * POST /cart/items：加入购物车。
 * 重复添加同一 SKU 由后端在数据库层原子累加（累加后超过 999 整体回滚，返回 400/8002）。
 */
export function addCartItem(skuId: number, quantity: number): Promise<CartItem> {
  return authPostData<CartItem>('/cart/items', { sku_id: skuId, quantity })
}

/** PUT /cart/items/:id：修改条目数量（1..999）。 */
export function updateCartItemQuantity(itemId: number, quantity: number): Promise<CartItem> {
  return authPutData<CartItem>(`/cart/items/${String(itemId)}`, { quantity })
}

/** PUT /cart/items/:id/selected：勾选 / 取消勾选（服务端持久化）。 */
export function updateCartItemSelected(itemId: number, selected: boolean): Promise<CartItem> {
  return authPutData<CartItem>(`/cart/items/${String(itemId)}/selected`, { selected })
}

/** DELETE /cart/items/:id：删除条目；不存在或非本人统一 404/8001。 */
export function deleteCartItem(itemId: number): Promise<null> {
  return authDeleteData<null>(`/cart/items/${String(itemId)}`)
}
