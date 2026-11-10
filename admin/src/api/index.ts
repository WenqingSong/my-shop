/**
 * API 层统一出口。
 *
 * 约定：
 * - `generated/`：由 `pnpm api:types` 从后端 `contracts/openapi.yaml` 生成的类型（禁止手工编辑）。
 * - `modules/`：按业务域拆分的请求方法，直接使用下方 `http` 实例。
 *
 * A0 阶段不实现任何业务接口：后端 OpenAPI 契约（B0）交付后，
 * 先执行 `pnpm api:types` 生成类型，再在 `modules/` 中编写带类型的请求函数。
 */
export { http, setHttpErrorHandler } from "@/utils/http";
export type { HttpErrorHandler } from "@/utils/http";
