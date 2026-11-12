import { describe, expect, it } from 'vitest'

import { formatPrice, formatPriceParts } from '@/utils/price'

describe('formatPrice', () => {
  it('把整数分格式化为人民币金额', () => {
    expect(formatPrice(0)).toBe('¥0.00')
    expect(formatPrice(5)).toBe('¥0.05')
    expect(formatPrice(1990)).toBe('¥19.90')
    expect(formatPrice(129900)).toBe('¥1,299.00')
    expect(formatPrice(123456789)).toBe('¥1,234,567.89')
  })

  it('负数金额保留负号，用于退款等场景', () => {
    expect(formatPrice(-1990)).toBe('-¥19.90')
  })

  it('拆分为可分别排版的价格片段', () => {
    expect(formatPriceParts(129900)).toEqual({
      negative: false,
      symbol: '¥',
      integer: '1,299',
      decimal: '00',
      formatted: '¥1,299.00',
    })
  })

  it('拒绝非整数分入参，避免浮点误差进入界面', () => {
    expect(() => formatPrice(19.9)).toThrow(TypeError)
    expect(() => formatPrice(Number.NaN)).toThrow(TypeError)
    expect(() => formatPrice(Number.POSITIVE_INFINITY)).toThrow(TypeError)
  })
})
