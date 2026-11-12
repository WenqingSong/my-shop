import type { ProductSort, SortOrder } from '@/api/products'

/**
 * 商品列表页 URL 查询状态。
 *
 * 用 URL 作为筛选条件的唯一事实来源：搜索、分类、排序、分页都可以直接分享/刷新，
 * 也可以被浏览器前进后退正确恢复，避免组件内再维护一份容易漂移的 state。
 */
export interface CatalogSearch {
  /** 搜索关键词（URL 参数 q），已 trim；空串表示无关键词。 */
  q: string
  /** 分类 id（URL 参数 category_id）；不传表示全部分类。 */
  categoryId?: number
  /** 页码，从 1 开始。 */
  page: number
  /** 排序字段（仅后端白名单）。 */
  sort?: ProductSort
  /** 排序方向。 */
  order?: SortOrder
}

const PRODUCT_SORTS: readonly ProductSort[] = ['id', 'price', 'created_at', 'updated_at']

function isProductSort(value: string | null): value is ProductSort {
  return value !== null && (PRODUCT_SORTS as readonly string[]).includes(value)
}

function parsePositiveInt(value: string | null, fallback: number): number {
  if (!value || !/^\d+$/.test(value)) {
    return fallback
  }

  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback
}

/** 从 URLSearchParams 解析列表页状态，非法值统一回落到默认值。 */
export function parseCatalogSearch(params: URLSearchParams): CatalogSearch {
  const q = (params.get('q') ?? '').trim()
  const rawCategoryId = params.get('category_id')
  const categoryId = rawCategoryId ? parsePositiveInt(rawCategoryId, 0) || undefined : undefined
  const rawSort = params.get('sort')
  const sort = isProductSort(rawSort) ? rawSort : undefined
  const rawOrder = params.get('order')
  const order: SortOrder | undefined =
    sort !== undefined && (rawOrder === 'asc' || rawOrder === 'desc') ? rawOrder : sort ? 'desc' : undefined

  return {
    q,
    ...(categoryId ? { categoryId } : {}),
    page: parsePositiveInt(params.get('page'), 1),
    ...(sort ? { sort, order } : {}),
  }
}

/** 生成商品列表页查询参数；默认值不写入 URL，保持链接简短可读。 */
export function buildCatalogParams(state: Partial<CatalogSearch>): URLSearchParams {
  const params = new URLSearchParams()
  const q = state.q?.trim()

  if (q) {
    params.set('q', q)
  }

  if (state.categoryId && state.categoryId > 0) {
    params.set('category_id', String(state.categoryId))
  }

  if (state.page && state.page > 1) {
    params.set('page', String(state.page))
  }

  if (state.sort) {
    params.set('sort', state.sort)
    params.set('order', state.order ?? 'desc')
  }

  return params
}

/** 生成商品列表页地址。 */
export function catalogHref(state: Partial<CatalogSearch>): string {
  const query = buildCatalogParams(state).toString()
  return query ? `/products?${query}` : '/products'
}

/** 排序下拉框选项；空值表示后端默认排序（created_at desc）。 */
export const CATALOG_SORT_OPTIONS = [
  { value: '', label: '最新上架' },
  { value: 'price:asc', label: '价格从低到高' },
  { value: 'price:desc', label: '价格从高到低' },
] as const

/** 把 URL 状态转成排序下拉框的 value。 */
export function toSortValue(state: Pick<CatalogSearch, 'sort' | 'order'>): string {
  if (!state.sort) {
    return ''
  }

  return `${state.sort}:${state.order ?? 'desc'}`
}

/** 把排序下拉框的 value 转成请求参数；空值表示使用后端默认排序。 */
export function fromSortValue(value: string): Pick<CatalogSearch, 'sort' | 'order'> {
  if (value === 'price:asc') {
    return { sort: 'price', order: 'asc' }
  }

  if (value === 'price:desc') {
    return { sort: 'price', order: 'desc' }
  }

  return {}
}
