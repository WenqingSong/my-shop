import { describe, expect, it } from 'vitest'

import { buildPaginationItems } from '@/utils/pagination'

describe('buildPaginationItems', () => {
  it('总页数为 0 时返回空数组', () => {
    expect(buildPaginationItems(1, 0)).toEqual([])
  })

  it('总页数不超过 7 时全部展开', () => {
    expect(buildPaginationItems(3, 5)).toEqual([1, 2, 3, 4, 5])
  })

  it('当前页靠前时只在尾部折叠', () => {
    expect(buildPaginationItems(1, 12)).toEqual([1, 2, 'ellipsis-end', 12])
  })

  it('当前页居中时两端折叠', () => {
    expect(buildPaginationItems(6, 12)).toEqual([
      1,
      'ellipsis-start',
      5,
      6,
      7,
      'ellipsis-end',
      12,
    ])
  })

  it('越界页码会被收敛到合法范围', () => {
    expect(buildPaginationItems(99, 12)).toEqual([
      1,
      'ellipsis-start',
      11,
      12,
    ])
    expect(buildPaginationItems(-3, 12)).toEqual([1, 2, 'ellipsis-end', 12])
  })
})
