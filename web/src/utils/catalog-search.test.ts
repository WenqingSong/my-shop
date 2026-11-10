import { describe, expect, it } from 'vitest'

import {
  buildCatalogParams,
  catalogHref,
  fromSortValue,
  parseCatalogSearch,
  toSortValue,
} from '@/utils/catalog-search'

describe('parseCatalogSearch', () => {
  it('解析完整查询参数', () => {
    const state = parseCatalogSearch(
      new URLSearchParams('q=+星海+&category_id=11&page=2&sort=price&order=asc'),
    )

    expect(state).toEqual({
      q: '星海',
      categoryId: 11,
      page: 2,
      sort: 'price',
      order: 'asc',
    })
  })

  it('非法参数回落到默认值', () => {
    const state = parseCatalogSearch(
      new URLSearchParams('q=%20%20&category_id=abc&page=-1&sort=evil&order=sideways'),
    )

    expect(state).toEqual({ q: '', page: 1 })
  })

  it('sort 存在但 order 缺失时按 desc 处理', () => {
    expect(parseCatalogSearch(new URLSearchParams('sort=created_at'))).toEqual({
      q: '',
      page: 1,
      sort: 'created_at',
      order: 'desc',
    })
  })
})

describe('buildCatalogParams / catalogHref', () => {
  it('默认值不写入 URL', () => {
    expect(buildCatalogParams({ q: '', page: 1 }).toString()).toBe('')
    expect(catalogHref({})).toBe('/products')
  })

  it('写入筛选与分页参数', () => {
    const params = buildCatalogParams({ q: ' 星海 ', categoryId: 11, page: 3, sort: 'price', order: 'asc' })

    expect(params.get('q')).toBe('星海')
    expect(params.get('category_id')).toBe('11')
    expect(params.get('page')).toBe('3')
    expect(params.get('sort')).toBe('price')
    expect(params.get('order')).toBe('asc')
    expect(catalogHref({ q: '星海', page: 1 })).toBe('/products?q=%E6%98%9F%E6%B5%B7')
  })
})

describe('排序下拉框转换', () => {
  it('URL 状态转下拉 value', () => {
    expect(toSortValue({})).toBe('')
    expect(toSortValue({ sort: 'price', order: 'asc' })).toBe('price:asc')
  })

  it('下拉 value 转请求参数', () => {
    expect(fromSortValue('price:asc')).toEqual({ sort: 'price', order: 'asc' })
    expect(fromSortValue('price:desc')).toEqual({ sort: 'price', order: 'desc' })
    expect(fromSortValue('')).toEqual({})
  })
})
