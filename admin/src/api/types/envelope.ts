/**
 * 后端统一响应 envelope。
 *
 * 契约（初步交接版本，尚未冻结为 `contracts/openapi.yaml`）：
 * - 成功：`{ code: 0, message: "OK", data: ... }`
 * - 失败：`{ code: 1004, message: "未找到", data: null }`
 * - 业务成功条件：`code === 0`，**不能**仅凭 HTTP 200 判断。
 *
 * 该结构由 `src/utils/http` 的响应拦截器统一解析：
 * - `code === 0` → 返回 `data`
 * - `code !== 0` → 抛出 `BusinessError`（保留 code 与 message）
 */
export interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
}

/** 业务成功码 */
export const API_SUCCESS_CODE = 0;

/** 业务错误（HTTP 状态可能仍是 200） */
export class BusinessError extends Error {
  /** 后端业务错误码 */
  public readonly code: number;
  /** 后端原始 message，便于排查 */
  public readonly serverMessage: string;

  constructor(code: number, message: string) {
    super(message || `业务错误（code=${code}）`);
    this.name = "BusinessError";
    this.code = code;
    this.serverMessage = message;
  }
}

/** 判断一个响应体是否符合 envelope 结构 */
export function isApiEnvelope(
  payload: unknown
): payload is ApiEnvelope<unknown> {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "code" in payload &&
    typeof (payload as { code: unknown }).code === "number"
  );
}
