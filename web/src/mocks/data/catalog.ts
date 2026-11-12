import type { BannerItem, Category, Product, ProductDetail, ReviewItem, Sku } from '@/api/types'

/**
 * Mock 数据（仅用于 Vitest / Playwright，不代表真实后端数据）。
 *
 * 结构严格对齐后端源码：
 * - 响应包装 {code,message,data}：internal/middleware/response.go
 * - 商品/分类/SKU/评价/轮播字段：api/*\/v1/*.go
 * - 时间字段为 gtime JSON 字符串格式 "YYYY-MM-DD HH:mm:ss"
 *
 * 商品图片使用 web/public/mock/*.svg 本地占位资源，避免测试依赖外部网络。
 */

export const mockCategories: Category[] = [
  {
    id: 1,
    parent_id: 0,
    name: '数码',
    sort: 1,
    status: 1,
    created_at: '2026-09-01 10:00:00',
    updated_at: '2026-09-01 10:00:00',
    children: [
      {
        id: 11,
        parent_id: 1,
        name: '手机',
        sort: 1,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
      {
        id: 12,
        parent_id: 1,
        name: '耳机音箱',
        sort: 2,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
      {
        id: 13,
        parent_id: 1,
        name: '智能穿戴',
        sort: 3,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
    ],
  },
  {
    id: 2,
    parent_id: 0,
    name: '家居生活',
    sort: 2,
    status: 1,
    created_at: '2026-09-01 10:00:00',
    updated_at: '2026-09-01 10:00:00',
    children: [
      {
        id: 21,
        parent_id: 2,
        name: '厨房用品',
        sort: 1,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
      {
        id: 22,
        parent_id: 2,
        name: '收纳整理',
        sort: 2,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
    ],
  },
  {
    id: 3,
    parent_id: 0,
    name: '服饰鞋包',
    sort: 3,
    status: 1,
    created_at: '2026-09-01 10:00:00',
    updated_at: '2026-09-01 10:00:00',
    children: [
      {
        id: 31,
        parent_id: 3,
        name: '男装',
        sort: 1,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
      {
        id: 32,
        parent_id: 3,
        name: '女装',
        sort: 2,
        status: 1,
        created_at: '2026-09-01 10:00:00',
        updated_at: '2026-09-01 10:00:00',
        children: [],
      },
    ],
  },
  {
    id: 4,
    parent_id: 0,
    name: '美妆个护',
    sort: 4,
    status: 1,
    created_at: '2026-09-01 10:00:00',
    updated_at: '2026-09-01 10:00:00',
    children: [],
  },
]

interface ProductSeed {
  id: number
  name: string
  brand: string
  categoryId: number
  price: number
  detail: string
}

const seeds: ProductSeed[] = [
  { id: 1, name: '星海 X1 Pro 5G 手机 12GB+256GB', brand: '星海', categoryId: 11, price: 429900, detail: '6.7 英寸高刷屏，5000mAh 电池，支持 66W 快充。\n出厂搭载最新系统版本，提供 12 个月整机保修。' },
  { id: 2, name: '星海 Note 12 手机 8GB+128GB', brand: '星海', categoryId: 11, price: 199900, detail: '轻薄机身与长续航兼顾，适合日常通勤使用。' },
  { id: 3, name: '云听 Air 3 主动降噪耳机', brand: '云听', categoryId: 12, price: 89900, detail: '双麦克风主动降噪，单次续航 6 小时，配合充电盒可使用 24 小时。' },
  { id: 4, name: '云听 Studio 头戴式耳机', brand: '云听', categoryId: 12, price: 129900, detail: '40mm 动圈单元，蛋白皮耳罩，支持有线与蓝牙双模式。' },
  { id: 5, name: '星海 Watch S 智能手表', brand: '星海', categoryId: 13, price: 159900, detail: '1.43 英寸 AMOLED 屏幕，支持心率与血氧监测。' },
  { id: 6, name: '轻岚 手环 5 运动版', brand: '轻岚', categoryId: 13, price: 29900, detail: '轻量腕带，支持 14 天续航与多种运动模式。' },
  { id: 7, name: '悦厨 不粘炒锅 28cm', brand: '悦厨', categoryId: 21, price: 25900, detail: '铝合金锅体，不粘涂层，适配燃气灶与电磁炉。' },
  { id: 8, name: '悦厨 双层蒸锅 26cm', brand: '悦厨', categoryId: 21, price: 18900, detail: '食品级不锈钢，双层可拆分，容量适合三口之家。' },
  { id: 9, name: '暖居 玻璃保鲜盒六件套', brand: '暖居', categoryId: 22, price: 9900, detail: '耐热玻璃盒体，可微波加热，四扣密封设计。' },
  { id: 10, name: '暖居 布艺收纳箱 45L', brand: '暖居', categoryId: 22, price: 5900, detail: '加厚布艺外层，可折叠收纳，适合衣柜与床底。' },
  { id: 11, name: '简行 男士纯棉衬衫', brand: '简行', categoryId: 31, price: 16900, detail: '100% 精梳棉，修身版型，适合通勤与日常穿着。' },
  { id: 12, name: '简行 男士休闲夹克', brand: '简行', categoryId: 31, price: 39900, detail: '防风面料，立领设计，内里加薄绒。' },
  { id: 13, name: '简行 男士直筒牛仔裤', brand: '简行', categoryId: 31, price: 22900, detail: '中腰直筒版型，弹力面料，四季可穿。' },
  { id: 14, name: '云裳 女士针织开衫', brand: '云裳', categoryId: 32, price: 25900, detail: '柔软针织面料，宽松版型，适合春秋叠穿。' },
  { id: 15, name: '云裳 女士中长款风衣', brand: '云裳', categoryId: 32, price: 59900, detail: '翻领双排扣设计，附带可拆卸腰带。' },
  { id: 16, name: '云裳 女士百褶半身裙', brand: '云裳', categoryId: 32, price: 19900, detail: '垂坠感面料，高腰百褶设计，长度及小腿。' },
  { id: 17, name: '净研 氨基酸洁面乳 120g', brand: '净研', categoryId: 4, price: 7900, detail: '温和氨基酸配方，绵密泡沫，适合日常清洁。' },
  { id: 18, name: '净研 保湿面霜 50g', brand: '净研', categoryId: 4, price: 15900, detail: '含神经酰胺成分，帮助维持肌肤屏障。' },
  { id: 19, name: '净研 轻透防晒乳 SPF50+', brand: '净研', categoryId: 4, price: 11900, detail: '清爽不黏腻，SPF50+ PA++++，适合通勤使用。' },
  { id: 20, name: '星海 65W 氮化镓充电器', brand: '星海', categoryId: 11, price: 12900, detail: '双口输出，支持主流快充协议，体积小巧。' },
  { id: 21, name: '云听 便携蓝牙音箱', brand: '云听', categoryId: 12, price: 34900, detail: 'IPX7 防水，12 小时续航，支持双机串联。' },
  { id: 22, name: '轻岚 智能体脂秤', brand: '轻岚', categoryId: 13, price: 13900, detail: '八电极测量，支持体重、体脂等多项数据记录。' },
  { id: 23, name: '悦厨 便携咖啡杯 400ml', brand: '悦厨', categoryId: 21, price: 8900, detail: '双层真空保温，杯盖可拆洗，适合通勤携带。' },
  { id: 24, name: '暖居 折叠收纳凳', brand: '暖居', categoryId: 22, price: 6900, detail: '承重 120kg，可折叠收纳，兼顾坐凳与储物。' },
  { id: 25, name: '简行 男士轻量运动鞋', brand: '简行', categoryId: 31, price: 29900, detail: '透气网面，缓震中底，适合日常健走。' },
  { id: 26, name: '云裳 女士通勤托特包', brand: '云裳', categoryId: 32, price: 45900, detail: '大容量分层设计，可放入 14 英寸笔记本。' },
]

function imageFor(id: number): string {
  return `/mock/product-${String(((id - 1) % 3) + 1)}.svg`
}

export const mockProducts: Product[] = seeds.map((seed, index) => {
  const createdAt = `2026-10-${String((index % 28) + 1).padStart(2, '0')} 10:00:00`

  return {
    id: seed.id,
    name: seed.name,
    brand: seed.brand,
    category_id: seed.categoryId,
    price: seed.price,
    main_image: imageFor(seed.id),
    detail: seed.detail,
    status: 'on_shelf',
    view_count: 120 + seed.id * 7,
    images: [imageFor(seed.id), imageFor(seed.id + 1)],
    created_at: createdAt,
    updated_at: createdAt,
  }
})

const SKU_NAMES = ['标准款', '升级款'] as const

/** SKU id 使用 product.id * 10 + 序号，保证同一 Mock 会话内稳定。 */
export function mockSkusFor(product: Product): Sku[] {
  return SKU_NAMES.map((name, index) => ({
    id: product.id * 10 + index + 1,
    product_id: product.id,
    name,
    price: product.price + index * 5000,
    status: 'enabled',
    created_at: product.created_at,
    updated_at: product.updated_at,
  }))
}

export function findMockProduct(productId: number): Product | undefined {
  return mockProducts.find((product) => product.id === productId)
}

export function mockProductDetail(productId: number): ProductDetail | undefined {
  const product = findMockProduct(productId)
  return product ? { ...product, skus: mockSkusFor(product) } : undefined
}

export const mockBanners: BannerItem[] = [
  { id: 1, title: '新品首发', image_url: '/storage/banners/banner-1.png', link_url: '/products?category_id=11', sort: 1 },
  { id: 2, title: '耳机音箱专场', image_url: '/storage/banners/banner-2.png', link_url: '/products?category_id=12', sort: 2 },
  { id: 3, title: '家居焕新', image_url: '/storage/banners/banner-3.png', link_url: '/products?category_id=21', sort: 3 },
]

const reviewContents = [
  '包装完好，物流很快，手机手感不错。',
  '屏幕显示细腻，日常使用流畅。',
  '续航符合预期，充电速度也快。',
  '拍照效果不错，夜景比想象中好。',
  '整体满意，唯一不足是机身略重。',
  '第二次购买了，给家人也换了一台。',
  '客服回复及时，发票开具顺利。',
]

export const mockReviews: ReviewItem[] = [
  ...reviewContents.map((content, index) => ({
    id: index + 1,
    product_id: 1,
    sku_id: 11,
    rating: [5, 4, 5, 3, 4, 5, 4][index] ?? 5,
    content,
    user_id: 1001 + index,
    created_at: `2026-11-0${String(index + 1)} 09:30:00`,
  })),
  {
    id: 101,
    product_id: 2,
    sku_id: 21,
    rating: 4,
    content: '机身轻薄，价格合适。',
    user_id: 2001,
    created_at: '2026-11-03 14:20:00',
  },
  {
    id: 102,
    product_id: 2,
    sku_id: 21,
    rating: 5,
    content: '给长辈买的，操作简单。',
    user_id: 2002,
    created_at: '2026-11-04 18:05:00',
  },
]

export const mockRecommendation = {
  code: 'home',
  name: '编辑推荐',
  items: [3, 5, 9, 17, 22].flatMap((productId) => {
    const product = findMockProduct(productId)
    return product
      ? [
          {
            product_id: product.id,
            name: product.name,
            main_image: product.main_image,
            price: product.price,
            sort: product.id,
          },
        ]
      : []
  }),
}
