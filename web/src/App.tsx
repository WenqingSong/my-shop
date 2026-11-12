import { RouterProvider } from 'react-router'

import { AppErrorBoundary } from '@/components/common/app-error-boundary'
import { AppProviders } from '@/providers/app-providers'
import { router } from '@/router'

export default function App() {
  return (
    <AppErrorBoundary>
      <AppProviders>
        <RouterProvider router={router} />
      </AppProviders>
    </AppErrorBoundary>
  )
}
