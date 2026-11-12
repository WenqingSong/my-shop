import { HttpResponse } from "msw";
import { MOCK_ACCESS_TOKEN } from "./admin";

/** 统一成功 envelope */
export function ok<T>(data: T) {
  return HttpResponse.json({ code: 0, message: "OK", data });
}

/** 统一业务失败 envelope（HTTP 默认 200，与后端一致） */
export function fail(code: number, message: string, status = 200) {
  return HttpResponse.json({ code, message, data: null }, { status });
}

/** 未认证（HTTP 401） */
export function unauthorized() {
  return fail(1001, "未认证或登录已过期", 401);
}

/** 校验管理员 Bearer Token */
export function isAuthorized(request: Request): boolean {
  return request.headers.get("Authorization") === `Bearer ${MOCK_ACCESS_TOKEN}`;
}

/** 解析整数查询参数 */
export function queryInt(
  params: URLSearchParams,
  key: string,
  fallback: number
): number {
  const raw = params.get(key);
  if (!raw) return fallback;
  const value = Number(raw);
  return Number.isFinite(value) && value > 0 ? Math.trunc(value) : fallback;
}

/** 解析可能为字符串或数字的金额（后端用 json.Number） */
export function toInt(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  const num = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(num) || !Number.isInteger(num)) return null;
  return num;
}
