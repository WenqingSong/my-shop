import { setupWorker } from "msw/browser";
import { handlers } from "./handlers";

/** 浏览器端 Service Worker 实例，仅在 `VITE_MOCK_ENABLED=true` 时被加载 */
export const worker = setupWorker(...handlers);
