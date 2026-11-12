/// <reference types="vitest/config" />
import { rmSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const projectRoot = fileURLToPath(new URL('.', import.meta.url))

/**
 * 生产构建剔除 Mock 专用静态资源。
 *
 * `public/mockServiceWorker.js` 与 `public/mock/*.svg` 只服务于 dev / Playwright 的 MSW；
 * Vite 会把 public 目录原样复制到 dist，若不处理，生产包会带上
 * Service Worker 脚本与 Mock 图片（测试专用资源泄漏 + 部署体积）。
 *
 * 运行时 Mock 已由 `main.tsx` 的 `VITE_ENABLE_MOCK === 'true'` 严格隔离，
 * 这里补上静态资源侧的隔离。
 */
function stripMockArtifacts(): Plugin {
  return {
    name: 'surgecart-strip-mock-artifacts',
    apply: 'build',
    closeBundle() {
      rmSync(resolve(projectRoot, 'dist/mock'), { recursive: true, force: true })
      rmSync(resolve(projectRoot, 'dist/mockServiceWorker.js'), { force: true })
    },
  }
}

/**
 * 本地开发时把 /api 转发到后端入口（默认 Nginx 8080）。
 * 生产环境由 Nginx 直接反代，不经过 Vite。
 */
const devProxyTarget = process.env.VITE_DEV_PROXY_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react(), tailwindcss(), stripMockArtifacts()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    // 显式绑定 IPv4 回环，保证 Playwright 与本地开发使用同一地址。
    host: '127.0.0.1',
    port: 5173,
    strictPort: false,
    proxy: {
      // 后端契约路径不带 /api 前缀，dev 代理负责剥离；
      // SPA 自身使用 /products、/categories 作为页面路由，因此不能用同源根路径直连后端。
      '/api': {
        target: devProxyTarget,
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
      // 轮播图等本地静态资源（后端 /storage/banners/*）。
      '/storage': {
        target: devProxyTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['tests/e2e/**', 'node_modules/**', 'dist/**'],
    restoreMocks: true,
    clearMocks: true,
    css: false,
  },
})
