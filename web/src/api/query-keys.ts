import type { ProductSort } from '@/api/products'

/**
 * 统一 Query Key 工厂。
 *
 * 约定：
 * - 所有服务端数据 key 都从这里生成，禁止在组件里手写字符串数组；
 * - 列表 key 携带完整查询参数，参数变化自动产生新缓存；
 * - 详情 key 用 String(id) 归一化，避免路由参数 "12" 与数字 12 命中不同缓存。
 */
export interface ProductListKeyParams {
  page: number
  size: number
  categoryId?: number
  keyword?: string
  sort?: ProductSort
  order?: 'asc' | 'desc'
}

export const queryKeys = {
  categories: {
    all: ['categories'] as const,
    tree: () => ['categories', 'tree'] as const,
  },
  banners: {
    all: ['banners'] as const,
    list: () => ['banners', 'list'] as const,
  },
  products: {
    all: ['products'] as const,
    lists: () => ['products', 'list'] as const,
    list: (params: ProductListKeyParams) => ['products', 'list', params] as const,
    details: () => ['products', 'detail'] as const,
    detail: (productId: number) => ['products', 'detail', String(productId)] as const,
  },
  reviews: {
    all: ['reviews'] as const,
    productList: (productId: number, page: number, size: number) =>
      ['reviews', 'product', String(productId), { page, size }] as const,
  },
  recommendations: {
    all: ['recommendations'] as const,
    byCode: (code: string) => ['recommendations', code] as const,
  },
} as const
