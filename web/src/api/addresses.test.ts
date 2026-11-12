import { describe, expect, it } from 'vitest'

import { createAddress, deleteAddress, fetchAddresses, updateAddress } from '@/api/addresses'
import { ApiError } from '@/api/client'
import { signInAsDemo } from '@/test/auth'

const baseInput = {
  recipient_name: '张三',
  phone: '13800138000',
  province: '广东省',
  city: '深圳市',
  district: '南山区',
  detail: '科技园路 1 号 101 室',
}

describe('收货地址 API', () => {
  it('未登录访问返回 401', async () => {
    await expect(fetchAddresses()).rejects.toMatchObject({ status: 401 })
  })

  it('完成新增、列表、编辑、默认地址切换与删除', async () => {
    await signInAsDemo()

    expect((await fetchAddresses()).items).toEqual([])

    const first = await createAddress(baseInput)
    expect(first.is_default).toBe(true)

    const second = await createAddress({
      ...baseInput,
      recipient_name: '李四',
      phone: '13900139000',
      detail: '科技园路 2 号 202 室',
      is_default: true,
    })
    expect(second.is_default).toBe(true)

    const afterSecond = (await fetchAddresses()).items
    expect(afterSecond).toHaveLength(2)
    expect(afterSecond.find((item) => item.id === first.id)?.is_default).toBe(false)

    const updated = await updateAddress(first.id, { detail: '科技园路 3 号 303 室' })
    expect(updated.detail).toBe('科技园路 3 号 303 室')

    await updateAddress(first.id, { is_default: true })
    const afterDefault = (await fetchAddresses()).items
    expect(afterDefault.find((item) => item.id === first.id)?.is_default).toBe(true)
    expect(afterDefault.find((item) => item.id === second.id)?.is_default).toBe(false)

    await deleteAddress(second.id)
    expect((await fetchAddresses()).items.map((item) => item.id)).toEqual([first.id])
  })

  it('手机号格式错误返回 1001', async () => {
    await signInAsDemo()

    const error = await createAddress({ ...baseInput, phone: '12345' }).catch(
      (reason: unknown) => reason,
    )

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).code).toBe('1001')
    expect((error as ApiError).status).toBe(400)
  })

  it('删除不存在的地址返回 404/7001', async () => {
    await signInAsDemo()

    const error = await deleteAddress(999).catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(404)
    expect((error as ApiError).code).toBe('7001')
  })
})
