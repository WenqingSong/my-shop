import { afterAll, afterEach, beforeAll } from "vitest";
import { setupServer } from "msw/node";
import { handlers } from "@/mocks/handlers";

/**
 * 测试用 MSW Server。
 *
 * 与浏览器端 `src/mocks/browser.ts` 共用同一份 handlers，
 * 保证测试夹具与"开发环境 Mock"行为一致。
 */
export const server = setupServer(...handlers);

// 未匹配的请求直接报错，防止测试偷偷打到真实网络
beforeAll(() => server.listen({ onUnhandledRequest: "error" } as never));

afterEach(() => {
  server.resetHandlers();
  window.sessionStorage.clear();
});

afterAll(() => server.close());
