export type PaginationItem = number | 'ellipsis-start' | 'ellipsis-end'

/**
 * 生成分页按钮序列：首尾页固定，当前页左右各留 1 页，其余折叠为省略号。
 * 纯函数便于单独测试，不依赖组件渲染。
 */
export function buildPaginationItems(page: number, totalPages: number): PaginationItem[] {
  if (totalPages <= 0) {
    return []
  }

  const current = Math.min(Math.max(Math.trunc(page), 1), totalPages)

  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, index) => index + 1)
  }

  const items: PaginationItem[] = [1]
  const start = Math.max(2, current - 1)
  const end = Math.min(totalPages - 1, current + 1)

  if (start > 2) {
    items.push('ellipsis-start')
  }

  for (let index = start; index <= end; index += 1) {
    items.push(index)
  }

  if (end < totalPages - 1) {
    items.push('ellipsis-end')
  }

  items.push(totalPages)

  return items
}
