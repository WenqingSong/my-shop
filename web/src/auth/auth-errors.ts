import { ApiError } from '@/api/client'

export type AuthErrorMode = 'login' | 'register'

/**
 * 把认证接口错误转换成用户可读文案。
 *
 * 业务码来源：internal/codes/codes.go
 * - 1001 参数错误（用户名/密码格式）
 * - 1002 未授权（登录时通常表示账号被禁用）
 * - 2001 用户名已存在
 * - 2002 用户名或密码错误（后端不区分“用户不存在”与“密码错误”）
 */
export function describeAuthError(error: unknown, mode: AuthErrorMode): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (mode === 'register' && error.code === '2001') {
    return '用户名已存在，请更换后重试'
  }

  if (error.code === '2002') {
    return '用户名或密码错误'
  }

  if (error.code === '1002') {
    return mode === 'login'
      ? '账号不可用或已被禁用，请联系客服'
      : '登录状态已失效，请重新登录'
  }

  if (error.code === '1001') {
    return '请检查用户名与密码格式后重试'
  }

  if (error.kind === 'timeout') {
    return '请求超时，请稍后重试'
  }

  if (error.kind === 'network') {
    return '网络异常，请检查网络后重试'
  }

  return error.message || '操作失败，请稍后重试'
}
