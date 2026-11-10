import { describe, expect, it } from 'vitest'

import {
  isPasswordLengthValid,
  loginSchema,
  passwordByteLength,
  registerSchema,
  USERNAME_PATTERN,
} from '@/auth/validation'

describe('用户名校验', () => {
  it('只接受 3-24 位字母或数字', () => {
    expect(USERNAME_PATTERN.test('abc')).toBe(true)
    expect(USERNAME_PATTERN.test('User1234567890')).toBe(true)
    expect(USERNAME_PATTERN.test('ab')).toBe(false)
    expect(USERNAME_PATTERN.test('user_name')).toBe(false)
    expect(USERNAME_PATTERN.test('用户123')).toBe(false)
    expect(USERNAME_PATTERN.test('a'.repeat(25))).toBe(false)
  })
})

describe('密码校验（与后端字节长度一致）', () => {
  it('按 UTF-8 字节计算 8-24 长度', () => {
    expect(passwordByteLength('密码12')).toBe(8)
    expect(isPasswordLengthValid('密码12')).toBe(true)
    expect(isPasswordLengthValid('1234567')).toBe(false)
    expect(isPasswordLengthValid('12345678')).toBe(true)
    expect(isPasswordLengthValid('a'.repeat(24))).toBe(true)
    expect(isPasswordLengthValid('a'.repeat(25))).toBe(false)
  })
})

describe('表单 schema', () => {
  it('登录/注册只包含后端支持的 username/password', () => {
    expect(Object.keys(loginSchema.shape)).toEqual(['username', 'password'])
    expect(Object.keys(registerSchema.shape)).toEqual(['username', 'password'])
  })

  it('拒绝空值与非法格式', () => {
    expect(loginSchema.safeParse({ username: '', password: '' }).success).toBe(false)
    expect(loginSchema.safeParse({ username: 'a b', password: '12345678' }).success).toBe(false)
    expect(loginSchema.safeParse({ username: 'demo', password: '12345678' }).success).toBe(true)
  })
})
