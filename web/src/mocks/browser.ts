import { setupWorker } from 'msw/browser'

import { handlers } from '@/mocks/handlers'

export const worker = setupWorker(...handlers)

/** 启动浏览器 Mock：未匹配的请求直接放行到真实网络。 */
export async function startMockWorker(): Promise<void> {
  await worker.start({
    onUnhandledFrame: 'bypass',
    serviceWorker: { url: '/mockServiceWorker.js' },
  })
}
