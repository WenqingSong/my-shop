import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { queryClient as defaultQueryClient } from '@/api/query-client'

export interface AppProvidersProps {
  children: ReactNode
  /** 测试可注入独立 QueryClient，避免用例之间共享缓存。 */
  client?: QueryClient
}

export function AppProviders({ children, client = defaultQueryClient }: AppProvidersProps) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}
