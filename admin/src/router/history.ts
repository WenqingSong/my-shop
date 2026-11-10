import {
  type RouterHistory,
  createWebHistory,
  createWebHashHistory
} from "vue-router";

/**
 * 获取路由历史模式。
 *
 * 取值约定（见 .env.* 的 `VITE_ROUTER_HISTORY`）：
 * - `hash`：Hash 模式
 * - `h5`：HTML5 History 模式
 * - `hash,<base>` / `h5,<base>`：带 base 的模式
 *
 * 单独成文件的原因：`router/utils.ts` 与 `router/index.ts` 存在相互引用，
 * 该函数不依赖路由实例，独立后便于单元测试与复用。
 */
export function getHistoryMode(routerHistory: string): RouterHistory {
  // len为1 代表只有历史模式 为2 代表历史模式中存在base参数
  const historyMode = (routerHistory ?? "h5").split(",");
  const leftMode = historyMode[0];
  const rightMode = historyMode[1];

  if (historyMode.length === 1) {
    if (leftMode === "hash") {
      return createWebHashHistory("");
    } else if (leftMode === "h5") {
      return createWebHistory("");
    }
  } else if (historyMode.length === 2) {
    if (leftMode === "hash") {
      return createWebHashHistory(rightMode);
    } else if (leftMode === "h5") {
      return createWebHistory(rightMode);
    }
  }

  // 兜底：配置缺失时按 HTML5 history 处理
  return createWebHistory("");
}
