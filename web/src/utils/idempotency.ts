/**
 * 订单幂等键生成。
 *
 * 后端 `orders.idempotency_key` 为 VARCHAR(64)，唯一键 `uk_user_idempotency(user_id, idempotency_key)`。
 * 使用 UUID v4（去掉连字符，32 字符）即可满足长度与随机性要求。
 *
 * 注意：幂等键只在**同一次提交语义**内复用（同一地址 + 同一批商品），
 * 内容变化后必须换新键，否则后端会按「同键不同内容」返回 9006。
 */
export function createIdempotencyKey(): string {
  const cryptoApi = globalThis.crypto as Crypto | undefined

  if (cryptoApi && typeof cryptoApi.randomUUID === 'function') {
    return cryptoApi.randomUUID().replace(/-/g, '')
  }

  // 兜底：无 randomUUID 的环境（老浏览器 / 测试环境）。
  const random = () => Math.random().toString(16).slice(2, 10).padEnd(8, '0')

  return `${Date.now().toString(16)}${random()}${random()}`.slice(0, 32)
}

/**
 * 下单内容指纹（仅用于判断「幂等键能否复用」）。
 *
 * 与后端 `requestHash` 的口径严格一致
 * （`internal/logic/order/order.go:916-933`）：
 *
 * ```
 * source|address_id|coupon_id|sku_id:qty|...
 * ```
 *
 * `couponId` 必须参与指纹（后端同样把它写进 hash）：
 * - 换券 / 取消用券都算「内容变化」→ 必须生成新幂等键，
 *   否则会命中旧键并因 request_hash 不同返回 9006；
 * - 只有「同一地址 + 同一批商品 + 同一张券」的重试才复用原键。
 *
 * 0 表示不使用优惠券（与后端 `req.CouponId` 缺省值一致）。
 */
export function buildOrderSignature(
  addressId: number,
  lines: readonly { sku_id: number; quantity: number }[],
  couponId = 0,
): string {
  const normalized = [...lines]
    .sort((a, b) => a.sku_id - b.sku_id)
    .map((line) => `${String(line.sku_id)}:${String(line.quantity)}`)
    .join(',')

  return `cart|${String(addressId)}|${String(couponId)}|${normalized}`
}
