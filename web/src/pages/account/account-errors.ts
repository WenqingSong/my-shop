import { ApiError } from '@/api/client'

/** 收货地址错误码（internal/codes/codes.go 7000 域）。 */
export function describeAddressError(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (error.code === '7001') {
    return '地址不存在或已被删除，请刷新后重试'
  }

  if (error.code === '7002') {
    return '默认地址设置冲突，请重试'
  }

  if (error.code === '1001') {
    return '请检查收货人、手机号与地址信息'
  }

  if (error.code === '1002') {
    return '登录状态已失效，请重新登录'
  }

  if (error.kind === 'timeout') {
    return '请求超时，请稍后重试'
  }

  if (error.kind === 'network') {
    return '网络异常，请检查网络后重试'
  }

  return error.message || '操作失败，请稍后重试'
}

/** 收藏错误码（4001 商品不存在 / 11001 不可收藏）。 */
export function describeFavoriteError(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (error.code === '11001') {
    return '商品已下架，暂时无法收藏'
  }

  if (error.code === '4001') {
    return '商品不存在或已删除'
  }

  if (error.code === '1002') {
    return '登录状态已失效，请重新登录'
  }

  if (error.kind === 'timeout') {
    return '请求超时，请稍后重试'
  }

  if (error.kind === 'network') {
    return '网络异常，请检查网络后重试'
  }

  return error.message || '操作失败，请稍后重试'
}
