import { fileURLToPath, URL } from "node:url";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url))
    }
  },
  test: {
    environment: "jsdom",
    globals: true,
    include: ["tests/**/*.{test,spec}.ts"],
    // 与 .env.* 保持一致的关键变量，保证测试环境可断言部署路径
    env: {
      VITE_PUBLIC_PATH: "/console/",
      VITE_ROUTER_HISTORY: "h5,/console/",
      VITE_API_BASE_URL: "/api",
      VITE_MOCK_ENABLED: "false"
    }
  }
});
