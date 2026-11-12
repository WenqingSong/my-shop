import { expect } from "vitest";
import { BusinessError } from "@/api/types/envelope";

/**
 * 断言请求以指定后端业务码失败。
 *
 * 后端对业务错误可能使用 HTTP 400/404/409（不是 200），
 * 此时 Axios 抛出的是 `AxiosError` + `businessCode`，而不是 `BusinessError`。
 * 两种形态都要覆盖，避免测试只验证了"HTTP 200 + code != 0"这一条路径。
 */
export async function expectBusinessCode(
  promise: Promise<unknown>,
  code: number
) {
  let caught: unknown;

  try {
    await promise;
  } catch (cause) {
    caught = cause;
  }

  expect(caught, "期望请求失败，但实际成功").toBeDefined();

  const actual =
    caught instanceof BusinessError
      ? caught.code
      : (caught as { businessCode?: number }).businessCode;

  expect(actual, `期望业务码 ${code}，实际 ${String(actual)}`).toBe(code);
}
