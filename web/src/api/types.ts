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
