import { describe, expect, it } from 'vitest'

import { readAuthLocationState, resolveRedirectTarget } from '@/auth/redirect'

describe('resolveRedirectTarget', () => {
  it('允许站内路径并保留查询参数', () => {
    expect(resolveRedirectTarget('/account')).toBe('/account')
    expect(resolveRedirectTarget('/account?tab=security')).toBe('/account?tab=security')
  })

  it('拒绝外站、协议相对地址与非法值', () => {
    expect(resolveRedirectTarget('https://evil.example')).toBe('/')
    expect(resolveRedirectTarget('//evil.example')).toBe('/')
    expect(resolveRedirectTarget('account')).toBe('/')
    expect(resolveRedirectTarget(null)).toBe('/')
    expect(resolveRedirectTarget(123)).toBe('/')
  })

  it('拒绝回到登录/注册页，避免循环跳转', () => {
    expect(resolveRedirectTarget('/login')).toBe('/')
    expect(resolveRedirectTarget('/login?from=%2Faccount')).toBe('/')
    expect(resolveRedirectTarget('/register')).toBe('/')
  })
})

describe('readAuthLocationState', () => {
  it('只读取字符串字段', () => {
    expect(readAuthLocationState({ from: '/account', registeredUsername: 'demo' })).toEqual({
      from: '/account',
      registeredUsername: 'demo',
    })
    expect(readAuthLocationState({ from: 123, registeredUsername: null })).toEqual({})
    expect(readAuthLocationState(null)).toEqual({})
  })
})
