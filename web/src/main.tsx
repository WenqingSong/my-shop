import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from '@/App'
import '@/styles/globals.css'

/**
 * MSW 只在显式开启时加载，避免默认情况下掩盖真实接口问题。
 * 动态 import 保证 Mock 代码不进入生产首屏 chunk。
 */
async function startMocking(): Promise<void> {
  if (import.meta.env.VITE_ENABLE_MOCK !== 'true') {
    return
  }

  const { startMockWorker } = await import('@/mocks/browser')
  await startMockWorker()
}

async function bootstrap(): Promise<void> {
  await startMocking()

  const rootElement = document.getElementById('root')

  if (!rootElement) {
    throw new Error('找不到 #root 挂载节点')
  }

  createRoot(rootElement).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

void bootstrap()
