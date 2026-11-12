import { describe, expect, it } from 'vitest'

import { ApiError } from '@/api/client'
import { unwrapEnvelope } from '@/api/envelope'

describe('unwrapEnvelope', () => {
  it('code=0 时返回 data', () => {
    expect(unwrapEnvelope<{ items: number[] }>({ code: 0, message: 'OK', data: { items: [1, 2] } })).toEqual({
      items: [1, 2],
    })
  })

  it('code 非 0 时抛出带业务码的 ApiError', () => {
    const error = (() => {
      try {
        unwrapEnvelope({ code: 4001, message: '商品不存在', data: null })
        return null
      } catch (reason) {
        return reason
      }
    })()

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).code).toBe('4001')
    expect((error as ApiError).message).toBe('商品不存在')
  })

  it('响应不是统一包装结构时抛出 parse 错误', () => {
    const error = (() => {
      try {
        unwrapEnvelope('<html>gateway error</html>')
        return null
      } catch (reason) {
        return reason
      }
    })()

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).kind).toBe('parse')
  })
})
