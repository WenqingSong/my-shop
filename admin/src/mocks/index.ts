/**
 * MSW 启动入口。
 *
 * 设计要点：
 * - 与真实 API Client 分离：Mock 通过 Service Worker 在网络层拦截请求，
 *   业务代码始终使用 `src/utils/http` 的 Axios 实例，无需感知 Mock 是否存在。
 * - 按需加载：只有 `VITE_MOCK_ENABLED=true` 时 `src/main.ts` 才会动态导入本模块，
 *   生产构建默认不会拉取 MSW 运行时。
 * - 子路径部署：Service Worker 地址基于 `import.meta.env.BASE_URL`，
 *   与 `/console/` 部署路径保持一致。
 */
export async function startMockServer() {
  const { worker } = await import("./browser");

  await worker.start({
    // 未被显式 Mock 的请求直接放行给真实后端
    onUnhandledFrame: "bypass",
    quiet: false,
    serviceWorker: {
      url: `${import.meta.env.BASE_URL}mockServiceWorker.js`
    }
  });

  console.info("[MSW] Mock 已启用，未匹配的请求会转发到真实后端");
}
