import { ApiError } from '@/api/client'

/**
 * 订单错误码文案（internal/codes/codes.go 9000 域 + 库存/地址/SKU/认证）。
 *
 * 只做「把后端业务码翻译成用户可读文案」，不改变后端语义，也不在前端推断订单状态。
 * 特别注意：超时/网络异常的语义是「结果未知」，此时不允许当作失败直接重新下单，
 * 而是提示用户重试——重试会复用同一幂等键，后端命中后返回既有订单。
 */
export function describeOrderError(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return '下单失败，请稍后重试'
  }

  if (error.code === '9004') {
    return '购物车中没有勾选的商品，请返回购物车重新选择'
  }

  if (error.code === '9003') {
    return '部分商品已下架或规格已停售，请返回购物车调整'
  }

  if (error.code === '6001') {
    return '库存不足，请返回购物车调整数量后重试'
  }

  if (error.code === '9005') {
    return '购买数量不合法，请返回购物车调整'
  }

  if (error.code === '9006') {
    return '订单内容已变化，请刷新结算页后重新提交'
  }

  if (error.code === '5001') {
    return '商品规格不存在，请返回购物车调整'
  }

  if (error.code === '7001') {
    return '收货地址不存在或已被删除，请重新选择'
  }

  // 优惠券域（W4-04）：internal/codes/codes.go:143-150。
  if (error.code === '19003') {
    return '优惠券不存在或不属于当前账号，请重新选择优惠券'
  }

  if (error.code === '19007') {
    return '优惠券当前不可用（可能已被占用、已使用、超出有效期或未达到门槛），已为你刷新优惠券列表，请重新选择或改用其他优惠券'
  }

  if (error.code === '1001') {
    return '请求参数有误，请检查收货地址与商品后重试'
  }

  if (error.code === '1002' || error.status === 401) {
    return '登录状态已失效，请重新登录'
  }

  if (error.kind === 'timeout') {
    return '请求超时，订单结果未知，订单可能已创建；请勿重复下单。可重试（相同幂等键不会重复创建订单），并留意购物车是否已被清空。'
  }

  if (error.kind === 'network') {
    return '网络异常，订单结果未知，订单可能已创建；请勿重复下单。请检查网络后重试（相同幂等键不会重复创建订单），并留意购物车是否已被清空。'
  }

  return error.message || '下单失败，请稍后重试'
}

/** 是否为「结果未知」类错误（超时/网络异常），用于提示重试语义。 */
export function isUnknownResultError(error: unknown): boolean {
  return error instanceof ApiError && (error.kind === 'timeout' || error.kind === 'network')
}

/** 是否为幂等冲突，需要用户刷新内容后重新提交。 */
export function isIdempotencyConflict(error: unknown): boolean {
  return error instanceof ApiError && error.code === '9006'
}

/**
 * 是否为服务端拒绝优惠券（19003 不存在/非本人、19007 不可用）。
 *
 * 这两种错误说明本地券缓存已过期：调用方需要刷新券列表并回落到「不使用优惠券」，
 * 否则用户会反复提交同一个必然失败的请求。
 */
export function isCouponRejected(error: unknown): boolean {
  return error instanceof ApiError && (error.code === '19003' || error.code === '19007')
}
