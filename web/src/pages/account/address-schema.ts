import { z } from 'zod'

/**
 * 与后端 internal/logic/address/address.go 校验保持一致：
 * - recipient_name：trim 后 1-32 字符
 * - phone：^1[3-9]\d{9}$
 * - province/city/district：trim 后 1-32 字符（自由文本）
 * - detail：trim 后 1-255 字符
 *
 * Go 使用 utf8.RuneCountInString；这里用 Array.from(...).length 近似按 Unicode 码点计数。
 */
function runeLength(value: string): number {
  return Array.from(value).length
}

function requiredText(label: string, maxLength: number) {
  return z
    .string()
    .trim()
    .min(1, `请输入${label}`)
    .refine((value) => runeLength(value) <= maxLength, `${label}不能超过 ${maxLength} 个字符`)
}

export const addressFormSchema = z.object({
  recipient_name: requiredText('收货人姓名', 32),
  phone: z
    .string()
    .trim()
    .regex(/^1[3-9]\d{9}$/, '请输入正确的 11 位手机号'),
  province: requiredText('省份', 32),
  city: requiredText('城市', 32),
  district: requiredText('区/县', 32),
  detail: requiredText('详细地址', 255),
  is_default: z.boolean(),
})

export type AddressFormValues = z.infer<typeof addressFormSchema>
