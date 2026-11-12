/**
 * 金额换算。
 *
 * 后端约定（`api/dashboard/v1`）：金额字段单位是**分**（int64）。
 * 前端展示统一转换为**元**并保留两位小数。
 */

/** 分 → 元（数值） */
export function centsToYuan(cents: number): number {
  return Math.round(cents) / 100;
}

/**
 * 分 → 元（带千分位的展示字符串）。
 *
 * 例：`123456` → `"1,234.56"`；`0` → `"0.00"`；`-50` → `"-0.50"`。
 */
export function formatCentsAsYuan(cents: number): string {
  const fixed = centsToYuan(cents).toFixed(2);
  const negative = fixed.startsWith("-");
  const unsigned = negative ? fixed.slice(1) : fixed;
  const [integer, decimal] = unsigned.split(".");
  const grouped = integer.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${negative ? "-" : ""}${grouped}.${decimal}`;
}

/** 带货币符号的展示字符串，例：`"¥1,234.56"` */
export function formatCentsAsCny(cents: number): string {
  return `¥${formatCentsAsYuan(cents)}`;
}

/**
 * 元 → 整数分（精确换算）。
 *
 * 规则与 A2 商品价格一致：
 * - 空值/非数字/负数返回 `null`；
 * - 超过 `maxCents` 返回 `null`；
 * - 小数位超过两位（例如 1.234）返回 `null`，不做静默四舍五入。
 */
export function parseYuanToCents(
  value: number | string | null | undefined,
  maxCents = 99_999_999
): number | null {
  if (value === null || value === undefined || value === "") return null;
  const num = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(num) || num < 0) return null;

  const cents = Math.round(num * 100);
  if (!Number.isFinite(cents) || cents > maxCents) return null;
  if (Math.abs(num * 100 - cents) > 1e-6) return null;
  return cents;
}
