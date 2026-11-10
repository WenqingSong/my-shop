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
  auth: {
    all: ['auth'] as const,
    me: () => ['auth', 'me'] as const,
  },
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
  addresses: {
    all: ['addresses'] as const,
    list: () => ['addresses', 'list'] as const,
  },
  /**
   * 购物车：后端只有「整份列表」接口（GET /cart），没有分页与计数接口。
   * 因此 Header 徽标与购物车页面共用同一个 key，天然复用同一份缓存，
   * 不会为了展示数量额外发一次请求。
   */
  cart: {
    all: ['cart'] as const,
    list: () => ['cart', 'list'] as const,
  },
  favorites: {
    all: ['favorites'] as const,
    lists: () => ['favorites', 'list'] as const,
    list: (page: number, size: number) => ['favorites', 'list', { page, size }] as const,
    checks: () => ['favorites', 'check'] as const,
    check: (productId: number) => ['favorites', 'check', String(productId)] as const,
  },
} as const
