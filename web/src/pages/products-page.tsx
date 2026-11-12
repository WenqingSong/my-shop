import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { SearchX, X } from 'lucide-react'
import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router'

import { fetchCategoryTree } from '@/api/categories'
import { fetchProductList, PRODUCT_PAGE_SIZE } from '@/api/products'
import { queryKeys } from '@/api/query-keys'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'
import { Pagination } from '@/components/common/pagination'
import { CategoryChips, CategorySidebar } from '@/components/product/category-nav'
import { ProductGrid, ProductGridSkeleton } from '@/components/product/product-grid'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { cn } from '@/utils/cn'
import {
  buildCatalogParams,
  CATALOG_SORT_OPTIONS,
  fromSortValue,
  parseCatalogSearch,
  toSortValue,
  type CatalogSearch,
} from '@/utils/catalog-search'
import { findCategoryById } from '@/utils/category-tree'

function FilterChip({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-border bg-surface py-1 pl-3 pr-1 text-xs text-muted-foreground">
      {label}
      <button
        type="button"
        onClick={onRemove}
        aria-label={`移除筛选：${label}`}
        className="grid size-5 place-items-center rounded-full text-gray-400 transition-colors hover:bg-gray-100 hover:text-foreground"
      >
        <X className="size-3.5" aria-hidden="true" />
      </button>
    </span>
  )
}

export function ProductsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const search = useMemo(() => parseCatalogSearch(searchParams), [searchParams])

  const categoriesQuery = useQuery({
    queryKey: queryKeys.categories.tree(),
    queryFn: ({ signal }) => fetchCategoryTree(signal),
  })

  const productsQuery = useQuery({
    queryKey: queryKeys.products.list({
      page: search.page,
      size: PRODUCT_PAGE_SIZE,
      categoryId: search.categoryId,
      keyword: search.q || undefined,
      sort: search.sort,
      order: search.order,
    }),
    queryFn: ({ signal }) =>
      fetchProductList(
        {
          page: search.page,
          size: PRODUCT_PAGE_SIZE,
          categoryId: search.categoryId,
          keyword: search.q,
          sort: search.sort,
          order: search.order,
        },
        signal,
      ),
    placeholderData: keepPreviousData,
  })

  const categories = categoriesQuery.data?.items ?? []
  const activeCategory = search.categoryId
    ? findCategoryById(categories, search.categoryId)
    : undefined
  const hasFilters = search.q !== '' || search.categoryId !== undefined || search.sort !== undefined
  const data = productsQuery.data
  const totalPages = data ? Math.max(1, Math.ceil(data.total / PRODUCT_PAGE_SIZE)) : 1

  const pageTitle = search.q
    ? `搜索“${search.q}”的结果 | SurgeCart 商城`
    : `${activeCategory?.name ?? '全部商品'} | SurgeCart 商城`
  useDocumentTitle(pageTitle)

  const updateSearch = useCallback(
    (patch: Partial<CatalogSearch>, options?: { replace?: boolean }) => {
      const next: CatalogSearch = { ...search, ...patch }
      setSearchParams(buildCatalogParams(next), { replace: options?.replace ?? false })
    },
    [search, setSearchParams],
  )

  const clearFilters = () => {
    updateSearch({ q: '', categoryId: undefined, sort: undefined, order: undefined, page: 1 })
  }

  const heading = search.q ? `“${search.q}”的搜索结果` : (activeCategory?.name ?? '全部商品')

  return (
    <PageContainer className="py-8 lg:py-10">
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
          {heading}
        </h1>
        <p className="text-sm leading-6 text-muted-foreground">
          浏览在售商品，支持关键词搜索、分类筛选与价格排序。
        </p>
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-8">
        <aside className="hidden lg:block">
          {categoriesQuery.isPending ? (
            <div className="h-64 animate-pulse rounded-xl border border-border bg-surface" aria-hidden="true" />
          ) : null}
          {categoriesQuery.isError ? (
            <div className="rounded-xl border border-border bg-surface p-4 text-sm text-muted-foreground">
              <p>分类加载失败</p>
              <Button variant="outline" size="sm" className="mt-3" onClick={() => void categoriesQuery.refetch()}>
                重新加载
              </Button>
            </div>
          ) : null}
          {!categoriesQuery.isPending && !categoriesQuery.isError ? (
            <CategorySidebar categories={categories} activeCategoryId={search.categoryId} />
          ) : null}
        </aside>

        <div id="catalog-results" className="min-w-0">
          <div className="lg:hidden">
            <CategoryChips categories={categories} activeCategoryId={search.categoryId} />
          </div>

          <div className="mt-4 flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-sm text-muted-foreground" aria-live="polite">
              {data ? (
                <>
                  共 <span className="font-medium text-foreground">{data.total}</span> 件商品
                  {data.items.length > 0 ? (
                    <span className="ml-2 text-xs">
                      第 {data.page} / {totalPages} 页
                    </span>
                  ) : null}
                </>
              ) : (
                '正在统计商品'
              )}
            </p>

            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              排序
              <select
                value={toSortValue(search)}
                onChange={(event) => {
                  const nextSort = fromSortValue(event.target.value)
                  updateSearch({ sort: nextSort.sort, order: nextSort.order, page: 1 })
                }}
                className="h-9 rounded-md border border-border-strong bg-surface px-2 text-sm text-foreground focus-visible:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/25"
              >
                {CATALOG_SORT_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          </div>

          {hasFilters ? (
            <div className="mt-4 flex flex-wrap items-center gap-2">
              {search.q ? (
                <FilterChip label={`关键词：${search.q}`} onRemove={() => updateSearch({ q: '', page: 1 })} />
              ) : null}
              {activeCategory ? (
                <FilterChip
                  label={`分类：${activeCategory.name}`}
                  onRemove={() => updateSearch({ categoryId: undefined, page: 1 })}
                />
              ) : null}
              <button
                type="button"
                onClick={clearFilters}
                className="text-xs font-medium text-primary hover:text-primary-hover"
              >
                清除筛选
              </button>
            </div>
          ) : null}

          <div className="mt-5">
            {productsQuery.isPending ? <ProductGridSkeleton count={10} /> : null}

            {productsQuery.isError ? (
              <ErrorState
                title="商品加载失败"
                description={
                  search.q || search.categoryId
                    ? '没有获取到符合条件的商品，请稍后重试或调整筛选条件。'
                    : '商品列表暂时无法获取，请稍后重试。'
                }
                onRetry={() => void productsQuery.refetch()}
              />
            ) : null}

            {data && data.items.length === 0 ? (
              <EmptyState
                icon={<SearchX className="size-6" aria-hidden="true" />}
                title="没有找到符合条件的商品"
                description="可以尝试更换关键词，或清除分类筛选后重新浏览。"
                action={
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    {search.page > 1 ? (
                      <Button variant="outline" size="sm" onClick={() => updateSearch({ page: 1 }, { replace: true })}>
                        回到第一页
                      </Button>
                    ) : null}
                    {hasFilters ? (
                      <Button size="sm" onClick={clearFilters}>
                        清除筛选
                      </Button>
                    ) : null}
                  </div>
                }
              />
            ) : null}

            {data && data.items.length > 0 ? (
              <>
                <ProductGrid
                  products={data.items}
                  className={cn(productsQuery.isFetching && 'opacity-60 transition-opacity duration-150')}
                />
                <Pagination
                  page={search.page}
                  totalPages={totalPages}
                  onPageChange={(page) => {
                    updateSearch({ page }, { replace: true })
                    document.getElementById('catalog-results')?.scrollIntoView?.({ block: 'start' })
                  }}
                  className="mt-8"
                />
              </>
            ) : null}
          </div>
        </div>
      </div>
    </PageContainer>
  )
}
