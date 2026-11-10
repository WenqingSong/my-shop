import { ApiError } from '@/api/client'
import type { OrderActionKind } from '@/hooks/use-orders'

/**
 * 订单操作错误文案。
 *
 * 错误码事实来源：`internal/codes/codes.go:83-89`（订单域 9000-9999）。
 * 这里只做「业务码 → 用户可执行动作」的翻译，不掩盖后端返回的真实错误。
 *
 * 9002 是本模块最关键的码：它表示「状态闸门没通过」，
 * 常见原因是订单已超时被服务端取消、或状态已被其它请求推进。
 * 此时前端缓存一定已过期，调用方会同时失效详情与列表（见 useOrderAction），
 * 文案需要明确告诉用户「已经帮你刷新」。
 */
export function describeOrderActionError(error: unknown, action: OrderActionKind): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (error.code === '1002' || error.status === 401) {
    return '登录状态已失效，请重新登录后再试'
  }

  if (error.code === '9001' || error.status === 404) {
    return '订单不存在或无权操作，请返回订单列表查看'
  }

  if (error.code === '9002') {
    if (action === 'pay') {
      return '订单状态已变化（可能已超时自动取消），已为你刷新最新状态'
    }

    if (action === 'cancel') {
      return '订单当前不是待支付状态，无法取消，已为你刷新最新状态'
    }

    return '订单当前不是已发货状态，无法确认收货，已为你刷新最新状态'
  }

  if (error.code === '1001') {
    return '请求参数异常，请刷新页面后重试'
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
