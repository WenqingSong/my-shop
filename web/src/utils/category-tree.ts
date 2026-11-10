import type { Category } from '@/api/types'

export interface CategoryNode {
  category: Category
  depth: number
}

/** 深度优先展开分类树，depth 从 0 开始，用于侧边栏缩进渲染。 */
export function flattenCategoryTree(items: Category[], depth = 0): CategoryNode[] {
  return items.flatMap((category) => [
    { category, depth },
    ...(category.children && category.children.length > 0
      ? flattenCategoryTree(category.children, depth + 1)
      : []),
  ])
}

/**
 * 收集叶子分类。
 *
 * 后端只允许商品绑定「存在且启用的叶子分类」（docs/design/product.md INV-005），
 * 且 GET /products 的 category_id 是精确匹配、不做子树聚合，
 * 因此前台可筛选的分类必须落在叶子节点上，不能拿顶级分类直接过滤。
 */
export function collectLeafCategories(items: Category[]): Category[] {
  return items.flatMap((category) =>
    category.children && category.children.length > 0
      ? collectLeafCategories(category.children)
      : [category],
  )
}

/** 在分类树中按 id 查找节点（用于详情页面包屑）。 */
export function findCategoryById(items: Category[], categoryId: number): Category | undefined {
  for (const item of items) {
    if (item.id === categoryId) {
      return item
    }

    if (item.children && item.children.length > 0) {
      const found = findCategoryById(item.children, categoryId)
      if (found) {
        return found
      }
    }
  }

  return undefined
}

/** 汇总整棵树的分类数量（含子分类），用于首页入口的辅助文案。 */
export function countCategories(items: Category[]): number {
  return items.reduce(
    (total, item) => total + 1 + (item.children ? countCategories(item.children) : 0),
    0,
  )
}
