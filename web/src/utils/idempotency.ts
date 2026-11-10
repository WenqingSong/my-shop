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
 * 与后端 requestHash 的口径保持一致：来源 + 地址 + 优惠券 + 按 sku 升序的 sku:qty。
 * 本阶段不使用优惠券（固定 0）。
 */
export function buildOrderSignature(
  addressId: number,
  lines: readonly { sku_id: number; quantity: number }[],
): string {
  const normalized = [...lines]
    .sort((a, b) => a.sku_id - b.sku_id)
    .map((line) => `${String(line.sku_id)}:${String(line.quantity)}`)
    .join(',')

  return `cart|${String(addressId)}|0|${normalized}`
}
