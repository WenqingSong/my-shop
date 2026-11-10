import { QueryClient } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'

import { AppProviders } from '@/providers/app-providers'
import { routes } from '@/router'

/**
 * 测试专用 QueryClient：关闭重试、禁用缓存保留，
 * 让错误态与加载态在用例中可确定地出现，不与其它用例共享数据。
 */
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 0,
        gcTime: 0,
        refetchOnWindowFocus: false,
      },
      mutations: {
        retry: 0,
      },
    },
  })
}

/** 使用与生产一致的路由表渲染应用，可指定初始 URL（含查询参数）。 */
export function renderApp(initialRoute = '/') {
  const router = createMemoryRouter(routes, { initialEntries: [initialRoute] })
  const client = createTestQueryClient()

  const result = render(
    <AppProviders client={client}>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  return { ...result, router, client }
}
