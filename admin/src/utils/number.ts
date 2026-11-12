/**
 * 数字展示工具。
 *
 * 不依赖 `toLocaleString` 的运行时 ICU 差异，保证 Node（测试）与浏览器输出一致。
 */

/** 整数千分位格式化，例：`1234567` → `"1,234,567"` */
export function formatThousands(value: number): string {
  const rounded = Math.round(value);
  const negative = rounded < 0;
  const digits = String(Math.abs(rounded));
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${negative ? "-" : ""}${grouped}`;
}

/** 安全取数：`undefined` / `NaN` / 非数字统一按 0 处理 */
export function toSafeNumber(value: unknown): number {
  const num = Number(value);
  return Number.isFinite(num) ? num : 0;
}
