import { ApiError } from '@/api/client'

/**
 * 购物车错误码文案（internal/codes/codes.go 8000 域 + SKU 5001 + 认证 1002）。
 * 只做「把后端业务码翻译成用户可读文案」，不改变后端错误语义。
 */
export function describeCartError(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return '操作失败，请稍后重试'
  }

  if (error.code === '8001') {
    return '条目已不存在，可能已在其他页面删除，请刷新后重试'
  }

  if (error.code === '8002') {
    return '数量需为 1–999 之间的整数'
  }

  if (error.code === '8003') {
    return '商品已下架或规格已停售，无法加入购物车'
  }

  if (error.code === '5001') {
    return '商品规格不存在，请重新选择'
  }

  if (error.code === '1002' || error.status === 401) {
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
