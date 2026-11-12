import { describe, expect, it } from 'vitest'

import { mockCategories } from '@/mocks/data/catalog'
import {
  collectLeafCategories,
  countCategories,
  findCategoryById,
  flattenCategoryTree,
} from '@/utils/category-tree'

describe('category-tree', () => {
  it('只收集叶子分类（后端商品只能绑定叶子分类）', () => {
    const leaves = collectLeafCategories(mockCategories)

    expect(leaves.map((leaf) => leaf.id)).toEqual([11, 12, 13, 21, 22, 31, 32, 4])
  })

  it('在任意层级按 id 查找节点', () => {
    expect(findCategoryById(mockCategories, 13)?.name).toBe('智能穿戴')
    expect(findCategoryById(mockCategories, 4)?.name).toBe('美妆个护')
    expect(findCategoryById(mockCategories, 999)).toBeUndefined()
  })

  it('统计整棵树节点数量并展开层级', () => {
    expect(countCategories(mockCategories)).toBe(11)
    expect(flattenCategoryTree(mockCategories).find((node) => node.category.id === 13)?.depth).toBe(1)
  })
})
