import { z } from 'zod'

/**
 * 与后端 IAM 校验保持一致：
 * - username：`^[a-zA-Z0-9]{3,24}$`（internal/logic/iam/iam.go validateUsername）
 * - password：UTF-8 字节长度 8-24（后端使用 len(password)，按字节计算）
 *
 * 后端没有“确认密码”、短信验证码、邮箱等字段，前端不增加这些后端不支持的输入项。
 */
export const USERNAME_PATTERN = /^[a-zA-Z0-9]{3,24}$/

const MIN_PASSWORD_BYTES = 8
const MAX_PASSWORD_BYTES = 24

const encoder = new TextEncoder()

export function passwordByteLength(value: string): number {
  return encoder.encode(value).length
}

export function isPasswordLengthValid(value: string): boolean {
  const length = passwordByteLength(value)
  return length >= MIN_PASSWORD_BYTES && length <= MAX_PASSWORD_BYTES
}

export const usernameFieldSchema = z
  .string()
  .min(1, '请输入用户名')
  .regex(USERNAME_PATTERN, '用户名需为 3-24 位字母或数字')

export const passwordFieldSchema = z
  .string()
  .min(1, '请输入密码')
  .refine(isPasswordLengthValid, '密码长度需为 8-24 个字符')

export const loginSchema = z.object({
  username: usernameFieldSchema,
  password: passwordFieldSchema,
})

export const registerSchema = z.object({
  username: usernameFieldSchema,
  password: passwordFieldSchema,
})

export type LoginFormValues = z.infer<typeof loginSchema>
export type RegisterFormValues = z.infer<typeof registerSchema>
