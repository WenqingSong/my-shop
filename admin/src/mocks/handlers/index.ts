import type { HttpHandler } from "msw";

/**
 * Mock 处理器集合。
 *
 * A0 阶段刻意保持为空：不编造任何电商业务数据，也不影子实现生产 API。
 * 后续阶段需要 Mock 时，在本目录新增文件（例如 `admin.ts`），
 * 使用 `http.get("/api/xxx", resolver)` 定义并加入下方数组即可，
 * 请求仍由真实 `src/utils/http` 的 Axios 实例发出，Mock 只做网络层拦截。
 */
export const handlers: HttpHandler[] = [];
