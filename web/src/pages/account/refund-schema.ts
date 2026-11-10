import { z } from 'zod'

import { REFUND_REASON_MAX_LENGTH } from '@/api/refunds'

/**
 * 与后端 `internal/logic/refund/refund.go:126-132` 的 Apply 校验保持一致：
 * - `strings.TrimSpace(reason)` 后不能为空；
 * - `utf8.RuneCountInString(reason) <= 255`。
 *
 * Go 按 Unicode 码点计数，前端用 `Array.from(...).length` 对齐（与地址表单同一做法）。
 * 前端校验只用于提前反馈；最终判定仍以服务端 1001/18003/18004 为准。
 */
function runeLength(value: string): number {
  return Array.from(value).length
}

export const refundApplySchema = z.object({
  reason: z
    .string()
    .trim()
    .min(1, '请填写退款原因')
    .refine(
      (value) => runeLength(value) <= REFUND_REASON_MAX_LENGTH,
      `退款原因不能超过 ${String(REFUND_REASON_MAX_LENGTH)} 个字符`,
    ),
})

export type RefundApplyValues = z.infer<typeof refundApplySchema>
