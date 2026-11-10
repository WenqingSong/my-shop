import { describe, expect, it } from 'vitest'

import { buildOrderSignature, createIdempotencyKey } from '@/utils/idempotency'

const LINES = [
  { sku_id: 2, quantity: 1 },
  { sku_id: 1, quantity: 3 },
]

describe('createIdempotencyKey', () => {
  it('生成 32 位十六进制字符串（符合后端 VARCHAR(64) 与唯一键）', () => {
    const key = createIdempotencyKey()
    expect(key).toHaveLength(32)
    expect(key).toMatch(/^[0-9a-f]+$/)
  })

  it('每次生成都不同', () => {
    const keys = new Set(Array.from({ length: 20 }, () => createIdempotencyKey()))
    expect(keys.size).toBe(20)
  })
})

describe('buildOrderSignature（与后端 requestHash 同口径）', () => {
  it('同一地址 + 同一批商品 + 同一张券 → 指纹相同', () => {
    expect(buildOrderSignature(7, LINES, 5)).toBe(buildOrderSignature(7, LINES, 5))
  })

  it('商品输入顺序不影响指纹（按 sku_id 升序归一化）', () => {
    const reversed = [...LINES].reverse()
    expect(buildOrderSignature(7, LINES, 5)).toBe(buildOrderSignature(7, reversed, 5))
  })

  it('换券 → 指纹变化（必须生成新幂等键）', () => {
    expect(buildOrderSignature(7, LINES, 5)).not.toBe(buildOrderSignature(7, LINES, 6))
  })

  it('取消用券 → 指纹变化', () => {
    expect(buildOrderSignature(7, LINES, 5)).not.toBe(buildOrderSignature(7, LINES, 0))
    expect(buildOrderSignature(7, LINES)).toBe(buildOrderSignature(7, LINES, 0))
  })

  it('换地址或商品数量变化 → 指纹变化', () => {
    expect(buildOrderSignature(7, LINES, 5)).not.toBe(buildOrderSignature(8, LINES, 5))
    expect(buildOrderSignature(7, LINES, 5)).not.toBe(
      buildOrderSignature(7, [{ sku_id: 1, quantity: 4 }], 5),
    )
  })

  it('格式为 source|address_id|coupon_id|sku:qty（与后端一致）', () => {
    expect(buildOrderSignature(7, LINES, 5)).toBe('cart|7|5|1:3,2:1')
  })
})
