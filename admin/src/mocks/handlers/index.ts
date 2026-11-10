import type { HttpHandler } from "msw";
import { adminHandlers } from "./admin";
import { dashboardHandlers } from "./dashboard";

/**
 * Mock 处理器集合。
 *
 * 约定：
 * - 只覆盖**已经确认契约**的接口（管理员认证、运营工作台），
 *   不影子实现在后续阶段才会出现的业务接口。
 * - handler 中的数值是演示夹具，不是真实业务数据；
 *   单元测试可以 `server.use(...)` 覆盖成空数据、403 等场景。
 * - 请求仍由真实 `src/utils/http` 的 Axios 实例发出，Mock 只在网络层拦截。
 */
export const handlers: HttpHandler[] = [...adminHandlers, ...dashboardHandlers];
