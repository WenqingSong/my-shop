import { authDeleteData, authGetData, authPostData, authPutData } from '@/api/auth-request'
import type { Address } from '@/api/types'

/**
 * 收货地址 API（均需登录，作用于 Principal.UserID）。
 *
 * 事实来源：
 * - api/address/v1/address.go（Address / CreateReq / UpdateReq / DeleteReq）
 * - internal/cmd/routes_frontend.go（POST/GET /addresses、GET/PUT/DELETE /addresses/:id）
 * - internal/logic/address/address.go（默认地址事务、7001/7002 错误码）
 */

export interface AddressInput {
  recipient_name: string
  phone: string
  province: string
  city: string
  district: string
  detail: string
  is_default?: boolean
}

/** 更新时所有字段可选；只传 is_default 即可设置/取消默认。 */
export type AddressUpdateInput = Partial<AddressInput>

/** GET /addresses：仅本人，按 id 升序。 */
export function fetchAddresses(signal?: AbortSignal): Promise<{ items: Address[] }> {
  return authGetData<{ items: Address[] }>('/addresses', { signal })
}

/** POST /addresses：首条地址由后端自动设为默认。 */
export function createAddress(input: AddressInput): Promise<Address> {
  return authPostData<Address>('/addresses', input)
}

/** PUT /addresses/:id：字段按提交覆盖。 */
export function updateAddress(addressId: number, input: AddressUpdateInput): Promise<Address> {
  return authPutData<Address>(`/addresses/${String(addressId)}`, input)
}

/** DELETE /addresses/:id：成功时后端 data=null；不存在/非本人返回 404/7001。 */
export function deleteAddress(addressId: number): Promise<null> {
  return authDeleteData<null>(`/addresses/${String(addressId)}`)
}
