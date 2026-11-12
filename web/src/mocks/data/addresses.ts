import type { Address } from '@/api/types'

/**
 * MSW 收货地址状态（仅测试）。
 * 模拟后端的默认地址语义：首条自动默认、设置默认时取消其它默认、删除后允许无默认。
 */
export interface MockAddressInput {
  recipient_name: string
  phone: string
  province: string
  city: string
  district: string
  detail: string
  is_default?: boolean
}

let addressesByUser = new Map<number, Address[]>()
let sequence = 0

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

export function resetMockAddresses(): void {
  addressesByUser = new Map()
  sequence = 0
}

export function listMockAddresses(userId: number): Address[] {
  return [...(addressesByUser.get(userId) ?? [])].sort((a, b) => a.id - b.id)
}

export function createMockAddress(userId: number, input: MockAddressInput): Address {
  const list = addressesByUser.get(userId) ?? []
  const isDefault = list.length === 0 || input.is_default === true

  if (isDefault) {
    for (const address of list) {
      address.is_default = false
    }
  }

  sequence += 1
  const timestamp = nowText()
  const address: Address = {
    id: sequence,
    recipient_name: input.recipient_name,
    phone: input.phone,
    province: input.province,
    city: input.city,
    district: input.district,
    detail: input.detail,
    is_default: isDefault,
    created_at: timestamp,
    updated_at: timestamp,
  }

  addressesByUser.set(userId, [...list, address])
  return address
}

export function updateMockAddress(
  userId: number,
  addressId: number,
  patch: Partial<MockAddressInput>,
): Address | null {
  const list = addressesByUser.get(userId) ?? []
  const address = list.find((item) => item.id === addressId)

  if (!address) {
    return null
  }

  if (patch.is_default === true) {
    for (const item of list) {
      item.is_default = item.id === addressId
    }
  } else if (patch.is_default === false) {
    address.is_default = false
  }

  if (patch.recipient_name !== undefined) address.recipient_name = patch.recipient_name
  if (patch.phone !== undefined) address.phone = patch.phone
  if (patch.province !== undefined) address.province = patch.province
  if (patch.city !== undefined) address.city = patch.city
  if (patch.district !== undefined) address.district = patch.district
  if (patch.detail !== undefined) address.detail = patch.detail
  address.updated_at = nowText()

  return address
}

export function deleteMockAddress(userId: number, addressId: number): boolean {
  const list = addressesByUser.get(userId) ?? []
  const next = list.filter((item) => item.id !== addressId)

  if (next.length === list.length) {
    return false
  }

  addressesByUser.set(userId, next)
  return true
}
