import type { FavoriteItem } from '@/api/types'
import { mockProducts } from '@/mocks/data/catalog'

/**
 * MSW 收藏状态（仅测试）。
 * 商品信息实时从 mockProducts 快照读取；商品缺失时模拟 product_deleted。
 */
interface MockFavoriteRecord {
  id: number
  userId: number
  productId: number
  createdAt: string
}

let records: MockFavoriteRecord[] = []
let sequence = 0
/** 测试专用：把指定商品在收藏 mock 中视为已下架，不改变商品列表本身。 */
let offShelfOverrides = new Set<number>()

function nowText(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

function findFavoriteProduct(productId: number) {
  const product = mockProducts.find((item) => item.id === productId)

  if (!product) {
    return undefined
  }

  return offShelfOverrides.has(productId) ? { ...product, status: 'off_shelf' as const } : product
}

function toFavoriteItem(record: MockFavoriteRecord): FavoriteItem {
  const product = findFavoriteProduct(record.productId)
  const available = product?.status === 'on_shelf'

  return {
    id: record.id,
    product_id: record.productId,
    product_name: product?.name ?? '',
    product_main_image: product?.main_image ?? '',
    product_price: product?.price ?? null,
    available,
    unavailable_reason: product ? (available ? '' : 'off_shelf') : 'product_deleted',
    created_at: record.createdAt,
  }
}

export function resetMockFavorites(): void {
  records = []
  sequence = 0
  offShelfOverrides = new Set()
}

/** 测试专用：模拟商品已下架（仅影响收藏 mock 的可用性判断）。 */
export function markMockProductOffShelf(productId: number): void {
  offShelfOverrides.add(productId)
}

export function listMockFavorites(
  userId: number,
  page: number,
  size: number,
): { items: FavoriteItem[]; total: number; page: number; size: number } {
  const all = records
    .filter((record) => record.userId === userId)
    .sort((a, b) => b.id - a.id)
  const start = (page - 1) * size

  return {
    items: all.slice(start, start + size).map(toFavoriteItem),
    total: all.length,
    page,
    size,
  }
}

export function checkMockFavorite(userId: number, productId: number): boolean {
  return records.some((record) => record.userId === userId && record.productId === productId)
}

export function addMockFavorite(
  userId: number,
  productId: number,
):
  | { ok: true; item: FavoriteItem }
  | { ok: false; status: number; code: number; message: string } {
  const product = findFavoriteProduct(productId)

  if (!product) {
    return { ok: false, status: 404, code: 4001, message: '商品不存在' }
  }

  if (product.status !== 'on_shelf') {
    return { ok: false, status: 409, code: 11001, message: '商品不可收藏' }
  }

  const existing = records.find(
    (record) => record.userId === userId && record.productId === productId,
  )

  if (existing) {
    return { ok: true, item: toFavoriteItem(existing) }
  }

  sequence += 1
  const record: MockFavoriteRecord = {
    id: sequence,
    userId,
    productId,
    createdAt: nowText(),
  }
  records = [...records, record]

  return { ok: true, item: toFavoriteItem(record) }
}

export function removeMockFavorite(userId: number, productId: number): void {
  records = records.filter(
    (record) => !(record.userId === userId && record.productId === productId),
  )
}

/** 测试专用：预置收藏（可包含已下架/已删除商品，用于覆盖不可用状态）。 */
export function seedMockFavorite(userId: number, productId: number): void {
  if (checkMockFavorite(userId, productId)) {
    return
  }

  sequence += 1
  records = [
    ...records,
    { id: sequence, userId, productId, createdAt: nowText() },
  ]
}
