/**
 * API 层统一出口。
 *
 * 约定：
 * - `types/`：契约相关的基础类型（统一 envelope、业务错误）。
 * - `generated/`：由 `pnpm api:types` 从后端 `contracts/openapi.yaml` 生成（禁止手工编辑）。
 * - `modules/`：按业务域拆分的请求方法，直接使用 `http` 实例。
 *
 * 当前阶段（A1）：`contracts/openapi.yaml` 尚未交付，`modules/*` 内的 DTO
 * 均标注为 PROVISIONAL，契约冻结后替换为生成类型即可，函数签名保持不变。
 */
export {
  http,
  setHttpErrorHandler,
  setUnauthorizedHandler
} from "@/utils/http";
export type { HttpErrorHandler, UnauthorizedHandler } from "@/utils/http";
export {
  BusinessError,
  isApiEnvelope,
  API_SUCCESS_CODE
} from "./types/envelope";
export type { ApiEnvelope } from "./types/envelope";
