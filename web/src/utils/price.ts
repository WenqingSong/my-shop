const CURRENCY_SYMBOL = '¥'

const integerFormatter = new Intl.NumberFormat('zh-CN', {
  maximumFractionDigits: 0,
  useGrouping: true,
})

export interface PriceParts {
  /** 是否为负数（退款、抵扣等场景） */
  negative: boolean
  /** 货币符号，人民币固定为 ¥ */
  symbol: string
  /** 带千分位的整数元部分，例如 "1,299" */
  integer: string
  /** 两位小数部分，例如 "00" */
  decimal: string
  /** 完整展示文本，例如 "¥1,299.00" */
  formatted: string
}

/**
 * 将整数分转换为展示用的价格片段。
 *
 * 该函数只负责展示格式化，不承担任何交易金额计算。
 * 入参必须是整数分（例如 129900 表示 ¥1,299.00），非法入参直接抛错，
 * 避免浮点误差静默扩散到界面上。
 */
export function formatPriceParts(cents: number): PriceParts {
  if (!Number.isFinite(cents) || !Number.isInteger(cents)) {
    throw new TypeError(`价格必须是整数分，收到：${String(cents)}`)
  }

  const negative = cents < 0
  const absolute = Math.abs(cents)
  const yuan = Math.floor(absolute / 100)
  const fen = absolute % 100

  const integer = integerFormatter.format(yuan)
  const decimal = String(fen).padStart(2, '0')
  const sign = negative ? '-' : ''

  return {
    negative,
    symbol: CURRENCY_SYMBOL,
    integer,
    decimal,
    formatted: `${sign}${CURRENCY_SYMBOL}${integer}.${decimal}`,
  }
}

/** 将整数分格式化为 "¥1,299.00" 形式的文本。 */
export function formatPrice(cents: number): string {
  return formatPriceParts(cents).formatted
}
