import type { ProductSort } from '@/api/products'

/**
 * 统一 Query Key 工厂。
 *
 * 约定：
 * - 所有服务端数据 key 都从这里生成，禁止在组件里手写字符串数组；
 * - 列表 key 携带完整查询参数，参数变化自动产生新缓存；
 * - 详情 key 用 String(id) 归一化，避免路由参数 "12" 与数字 12 命中不同缓存。
 */
/**
 * 订单试算 key 参数：所有会改变金额的输入都必须参与。
 *
 * `lines` 是已勾选行的预处理指纹（`sku_id:quantity:current_price`，按 sku_id 排序），
 * 用字符串而不是数组，保证同一份输入在任何渲染顺序下都命中同一个 key。
 */
export interface OrderQuoteKeyParams {
  addressId: number
  couponId: number
  lines: string
}

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
    /**
     * 订单试算（POST /orders/quote）。
     *
     * key 携带全部影响金额的输入：地址、优惠券与已勾选商品行指纹（含当前单价）。
     * 任一输入变化都会产生新 key，从而不会把上一份试算结果当作最新金额展示。
     */
    quotes: () => ['orders', 'quote'] as const,
    quote: (params: OrderQuoteKeyParams) => ['orders', 'quote', params] as const,
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
   * 没有分页、状态筛选与可用券查询；优惠金额试算走 orders.quote，因此这里只有一个列表 key。
   */
  coupons: {
    all: ['coupons'] as const,
    list: () => ['coupons', 'list'] as const,
  },
} as const
