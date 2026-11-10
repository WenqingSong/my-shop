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
