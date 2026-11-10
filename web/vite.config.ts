/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

/**
 * 本地开发时把 /api 转发到后端入口（默认 Nginx 8080）。
 * 生产环境由 Nginx 直接反代，不经过 Vite。
 */
const devProxyTarget = process.env.VITE_DEV_PROXY_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
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
