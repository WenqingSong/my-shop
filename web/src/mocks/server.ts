import { setupServer } from 'msw/node'

import { handlers } from '@/mocks/handlers'

/** Node 环境（Vitest）使用的 MSW Server。 */
export const server = setupServer(...handlers)
