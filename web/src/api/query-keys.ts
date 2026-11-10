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
  /**
   * 订单：W3-02 使用「创建 + 详情」，W4-01 增加「列表」。
   *
   * - 列表 key 不带参数：后端 `GET /orders` 既不支持状态筛选也不支持分页，
   *   筛选/分页是纯浏览器行为，不应该产生新的缓存 key；
   * - 详情 key 用 String(id) 归一化，创建成功后可直接写入缓存供成功页即时展示。
   */
  orders: {
    all: ['orders'] as const,
    list: () => ['orders', 'list'] as const,
    details: () => ['orders', 'detail'] as const,
    detail: (orderId: number) => ['orders', 'detail', String(orderId)] as const,
  },
  /**
   * 退款：W4-03 新增。
   *
   * - `list()` 不带参数：`GET /refunds` 没有分页与状态筛选，状态筛选是纯浏览器行为，
   *   不应该产生新的缓存 key；
   * - `list(orderId)` 对应后端真实支持的 `?order_id=` 服务端过滤，
   *   用于订单详情页的「关联退款记录」，与全量列表是两份不同的服务端结果；
   * - 详情 key 用 String(id) 归一化，与订单保持一致的约定。
   */
  refunds: {
    all: ['refunds'] as const,
    lists: () => ['refunds', 'list'] as const,
    list: (orderId?: number) =>
      ['refunds', 'list', orderId && orderId > 0 ? String(orderId) : 'all'] as const,
    details: () => ['refunds', 'detail'] as const,
    detail: (refundId: number) => ['refunds', 'detail', String(refundId)] as const,
  },
  /**
   * 优惠券：W4-04 新增。
   *
   * 后端只有「我的优惠券」一个用户端查询接口（GET /my/coupons），
   * 没有分页、状态筛选、可用券查询与优惠试算，因此只有一个列表 key。
   */
  coupons: {
    all: ['coupons'] as const,
    list: () => ['coupons', 'list'] as const,
  },
} as const
