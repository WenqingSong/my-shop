import { defineConfig, devices } from '@playwright/test'

const PORT = 5173
const BASE_URL = `http://127.0.0.1:${PORT}`

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  // MSW Service Worker 注册与 Vite 首次依赖优化并行时会出现空页面，
  // 统一串行执行，保证本地与 CI 结果一致（用例规模小，耗时可控）。
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  timeout: 30_000,
  expect: { timeout: 5_000 },
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: {
    command: `npm run dev -- --port ${PORT} --strictPort`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    // E2E 使用 MSW Mock 数据，避免依赖本地后端进程；
    // 推荐位 code 同时在此开启，以覆盖首页推荐区块渲染。
    env: {
      VITE_ENABLE_MOCK: 'true',
      VITE_HOME_RECOMMEND_CODE: 'home',
    },
  },
})
