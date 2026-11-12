import { getData } from '@/api/envelope'
import type { CategoryListResult } from '@/api/types'

/**
 * GET /categories（公开，仅启用项，树形；同级按 sort、id 升序）
 * 后端：api/categories/v1/categories.go ListReq / ListRes。
 */
export function fetchCategoryTree(signal?: AbortSignal): Promise<CategoryListResult> {
  return getData<CategoryListResult>('/categories', { signal })
}
