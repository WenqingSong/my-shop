const pad = (value: number) => String(value).padStart(2, "0");

/**
 * 格式化为 RFC3339 字符串（带本地时区偏移，例如 `2026-11-10T00:00:00+08:00`）。
 *
 * 后端 `/admin/dashboard/orders/status` 要求 `start_time`/`end_time` 为 RFC3339
 * 且必须同传。这里保留本地偏移而不是统一转成 UTC（`toISOString()`），
 * 便于在后端日志和网络面板中直接看出"业务侧是哪一天"。
 */
export function toRFC3339(date: Date): string {
  const offsetMinutes = -date.getTimezoneOffset();
  const sign = offsetMinutes >= 0 ? "+" : "-";
  const abs = Math.abs(offsetMinutes);
  const datePart = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const timePart = `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
  return `${datePart}T${timePart}${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
}

/**
 * 生成"最近 N 天"的时间窗（左闭右开 `[start, end)`，与后端切分口径一致）。
 *
 * - `days = 7` 时返回：6 天前的本地 00:00:00 ～ 明天本地 00:00:00。
 * - 时区说明：以浏览器本地时区计算。若浏览器时区与后端业务时区
 *   （`dashboard.timezone`，默认 `Asia/Shanghai`）不一致，边界会有偏差。
 */
export function getRecentDaysRange(days = 7, now: Date = new Date()) {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  start.setDate(start.getDate() - (days - 1));
  const end = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  end.setDate(end.getDate() + 1);
  return { start, end };
}

/** 生成 RFC3339 形式的最近 N 天查询参数 */
export function getRecentDaysRFC3339(days = 7, now: Date = new Date()) {
  const { start, end } = getRecentDaysRange(days, now);
  return { start_time: toRFC3339(start), end_time: toRFC3339(end) };
}
