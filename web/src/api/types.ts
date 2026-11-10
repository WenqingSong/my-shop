/**
 * 商城前台业务 DTO。
 *
 * 事实来源：仓库内 Go 后端源码（当前仓库没有 contracts/openapi.yaml，因此未生成
 * OpenAPI 类型）。字段名与 JSON tag 逐一对应，未做 camelCase 转换，避免在缺少契约时
 * 引入一层可能漂移的映射。新增字段必须先确认后端源码，禁止在前端凭空扩展。
 *
 * 对应源码：
 * - Product / ProductDetail   api/product/v1/product.go
 * - Sku                       api/sku/v1/sku.go
 * - Category                  api/categories/v1/categories.go
 * - BannerItem                api/banner/v1/banner.go
 * - ReviewItem                api/review/v1/review.go
 * - RecommendationItem        api/recommendation/v1/recommendation.go
 * - Address                   api/address/v1/address.go
 * - FavoriteItem              api/favorite/v1/favorite.go
 */

/** 与后端 api/product/v1/product.go 的字符串枚举一致。 */
export type ProductStatus = 'draft' | 'on_shelf' | 'off_shelf'

/** 与后端 api/sku/v1/sku.go 的字符串枚举一致。 */
export type SkuStatus = 'enabled' | 'disabled'

/** 商品 SPU。price 为整数分（后端 int64），前端不得用浮点元参与展示计算。 */
export interface Product {
  id: number
  name: string
  brand: string
  category_id: number
  price: number
  main_image: string
  detail: string
  status: ProductStatus
  view_count: number
  images: string[]
  created_at: string | null
  updated_at: string | null
}

/** 商品 SKU。后端仅提供 id/name/price/status，没有规格维度与库存字段。 */
export interface Sku {
  id: number
  product_id: number
  name: string
  price: number
  status: SkuStatus
  created_at: string | null
  updated_at: string | null
}

/** 前台商品详情：SPU 字段 + 仅 enabled 的 SKU 列表。 */
export interface ProductDetail extends Product {
  skus: Sku[]
}

/** GET /products 响应 data。 */
export interface ProductListResult {
  items: Product[]
  total: number
  page: number
  size: number
}

/** 分类节点。children 仅树形接口（GET /categories）返回。 */
export interface Category {
  id: number
  parent_id: number
  name: string
  sort: number
  /** 1 启用 / 0 禁用；前台树接口只返回启用项。 */
  status: number
  created_at: string | null
  updated_at: string | null
  children?: Category[]
}

/** GET /categories 响应 data。 */
export interface CategoryListResult {
  items: Category[]
}

/** 公开轮播图条目（仅 status=1）。 */
export interface BannerItem {
  id: number
  title: string
  image_url: string
  link_url: string
  sort: number
}

/** GET /banners 响应 data。 */
export interface BannerListResult {
  items: BannerItem[]
}

/** 公开评价条目（仅 status=published）。user_id 为后端直接暴露的买家 id。 */
export interface ReviewItem {
  id: number
  product_id: number
  sku_id: number
  /** 1-5 星 */
  rating: number
  content: string
  user_id: number
  created_at: string | null
}

/** GET /products/:id/reviews 响应 data；avg_rating 为实时聚合的平均分。 */
export interface ReviewListResult {
  items: ReviewItem[]
  page: number
  size: number
  total: number
  avg_rating: number
  count: number
}

/** 前台推荐位商品快照（仅 on_shelf 商品）。 */
export interface RecommendationItem {
  product_id: number
  name: string
  main_image: string
  price: number
  sort: number
}

/** GET /recommendations/:code 响应 data；推荐位不存在或禁用时 code/name 为空、items 为空。 */
export interface RecommendationResult {
  code: string
  name: string
  items: RecommendationItem[]
}

/**
 * 收货地址（api/address/v1/address.go）。
 * 省/市/区为自由文本（不是行政区划 code）；is_default 为布尔。
 */
export interface Address {
  id: number
  recipient_name: string
  phone: string
  province: string
  city: string
  district: string
  detail: string
  is_default: boolean
  created_at: string | null
  updated_at: string | null
}

/**
 * 购物车条目（api/cart/v1/cart.go CartItem）。
 *
 * 商品/SKU/库存信息由后端 LEFT JOIN 实时返回，前端不自行判断可购性与库存：
 * - SKU 被删除：product_id / current_price / stock 为 null，product_name/sku_name 为空串；
 * - available=false 时按 unavailable_reason 区分 sku_deleted / disabled / off_shelf；
 * - insufficient 表示「购物车数量 > 当前库存」，由后端计算（无库存记录视为 0）；
 * - price_changed 表示当前 SKU 价与加购时快照价不一致，加购快照价不锁价。
 */
export interface CartItem {
  id: number
  sku_id: number
  quantity: number
  /** 服务端持久化的勾选状态（默认 true）。 */
  selected: boolean
  /** 加购时的 SKU 价格快照（整数分），不参与成交价计算。 */
  price_snapshot: number
  product_id: number | null
  product_name: string
  product_main_image: string
  sku_name: string
  /** 当前 SKU 价格（整数分）；SKU 被删除时为 null。 */
  current_price: number | null
  price_changed: boolean
  available: boolean
  /** 空串表示可购；sku_deleted / disabled / off_shelf。 */
  unavailable_reason: string
  /** 当前库存；SKU 被删除时为 null。 */
  stock: number | null
  insufficient: boolean
  created_at: string | null
  updated_at: string | null
}

/**
 * 收藏条目（api/favorite/v1/favorite.go）。
 * 商品信息为服务端实时联查快照；商品删除时 product_name/main_image 为空、
 * product_price 为 null，available=false。
 */
export interface FavoriteItem {
  id: number
  product_id: number
  product_name: string
  product_main_image: string
  /** 整数分；商品删除时为 null。 */
  product_price: number | null
  available: boolean
  /** 空串表示可用；off_shelf / product_deleted。 */
  unavailable_reason: string
  created_at: string | null
}
