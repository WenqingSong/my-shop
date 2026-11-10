import { ApiError } from '@/api/client'

/**
 * 退款错误文案。
 *
 * 错误码事实来源：`internal/codes/codes.go:136-140, 254-258`
 * （退款域 18000-18999）：
 * - 18001 退款申请不存在或非本人 → 404
 * - 18002 非法退款状态迁移 → 409
 * - 18003 订单非「已支付未发货」，不可退款 → 409
 * - 18004 同一订单已存在有效退款申请 → 409
 * - 18005 退款与发货互斥冲突 → 409
 *
 * 这里只做「业务码 → 用户可执行动作」的翻译，不掩盖后端返回的真实错误。
 * 18002/18003/18004 都意味着本地视图已过期，调用方会同步失效缓存。
 */
export type RefundActionKind = 'apply' | 'revoke'

export function describeRefundActionError(error: unknown, action: RefundActionKind): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (error.code === '1002' || error.status === 401) {
    return '登录状态已失效，请重新登录后再试'
  }

  if (error.code === '18001' || error.status === 404) {
    return action === 'apply'
      ? '订单不存在或无权操作，请返回订单列表查看'
      : '退款申请不存在或无权操作，请返回退款列表查看'
  }

  if (error.code === '18002') {
    return action === 'apply'
      ? '订单当前状态不允许申请退款，已为你刷新最新状态'
      : '退款状态已变化（可能已审核通过），无法撤销，已为你刷新最新状态'
  }

  if (error.code === '18003') {
    return '只有「已支付且未发货」的订单可以申请退款，已为你刷新最新状态'
  }

  if (error.code === '18004') {
    return '该订单已有处理中的退款申请，请直接查看现有退款记录'
  }

  if (error.code === '18005') {
    return '退款与发货冲突，订单可能已发货，已为你刷新最新状态'
  }

  if (error.code === '1001') {
    return action === 'apply'
      ? '退款原因不能为空且不超过 255 个字符'
      : '请求参数异常，请刷新页面后重试'
  }

  if (error.kind === 'timeout') {
    return '请求超时，请稍后重试'
  }

  if (error.kind === 'network') {
    return '网络异常，请检查网络后重试'
  }

  if (error.kind === 'http' && (error.status ?? 0) >= 500) {
    return '服务暂时不可用，请稍后重试'
  }

  return error.message || '操作失败，请稍后重试'
}
