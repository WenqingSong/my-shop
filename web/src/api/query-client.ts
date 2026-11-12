import { QueryClient } from '@tanstack/react-query'

/**
 * 服务端状态统一入口：商品、库存、订单等数据只允许通过这里缓存与失效。
 * 默认值按商城场景取值：30s 内复用缓存、窗口聚焦不自动刷新、失败重试一次。
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        retry: 1,
        refetchOnWindowFocus: false,
      },
      mutations: {
        retry: 0,
      },
    },
  })
}

export const queryClient = createQueryClient()
