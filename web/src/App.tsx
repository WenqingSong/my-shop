import { RouterProvider } from 'react-router'

import { AppProviders } from '@/providers/app-providers'
import { router } from '@/router'

export default function App() {
  return (
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>
  )
}
