import { useQuery } from '@tanstack/react-query'
import { ArrowRight, ChevronRight, LayoutGrid } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { Link } from 'react-router'

import { fetchBanners } from '@/api/banners'
import { fetchCategoryTree } from '@/api/categories'
import { fetchProductList, HOME_PRODUCT_LIMIT } from '@/api/products'
import { queryKeys } from '@/api/query-keys'
import { fetchRecommendation } from '@/api/recommendations'
import type { BannerItem, Category } from '@/api/types'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'
import { ProductGrid, ProductGridSkeleton } from '@/components/product/product-grid'
import { Button } from '@/components/ui/button'
import { homeRecommendCode } from '@/config/env'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { SectionHeading } from '@/pages/home/section-primitives'
import { catalogHref } from '@/utils/catalog-search'
import { collectLeafCategories } from '@/utils/category-tree'
import { cn } from '@/utils/cn'

const HOME_LATEST_QUERY = {
  page: 1,
  size: HOME_PRODUCT_LIMIT,
  sort: 'created_at',
  order: 'desc',
} as const

function BannerTile({
  banner,
  className,
  eager = false,
}: {
  banner: BannerItem
  className?: string
  eager?: boolean
}) {
  const [imageFailed, setImageFailed] = useState(false)

  const content = (
    <div
      className={cn(
        'relative isolate flex w-full overflow-hidden rounded-2xl border border-border bg-gray-100',
        className,
      )}
    >
      {!imageFailed ? (
        <img
          src={banner.image_url}
          alt=""
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          onError={() => setImageFailed(true)}
          className="absolute inset-0 size-full object-cover"
        />
      ) : null}
      <div className="absolute inset-0 bg-gray-900/35" aria-hidden="true" />
      <div className="relative mt-auto w-full p-4 sm:p-5">
        <p className="text-base font-semibold text-white">{banner.title}</p>
        {banner.link_url ? (
          <span className="mt-1 inline-flex items-center gap-1 text-xs font-medium text-white/90">
            查看活动
            <ChevronRight className="size-3.5" aria-hidden="true" />
          </span>
        ) : null}
      </div>
    </div>
  )

  if (!banner.link_url) {
    return content
  }

  if (/^https?:\/\//i.test(banner.link_url)) {
    return (
      <a href={banner.link_url} target="_blank" rel="noreferrer noopener" className="block">
        {content}
      </a>
    )
  }

  return (
    <Link to={banner.link_url.startsWith('/') ? banner.link_url : `/${banner.link_url}`} className="block">
      {content}
    </Link>
  )
}

function Hero({ banners }: { banners: BannerItem[] }) {
  const [primaryBanner, ...secondaryBanners] = banners

  return (
    <section className="border-b border-border bg-surface">
      <PageContainer className="grid gap-10 py-12 lg:grid-cols-[minmax(0,1fr)_minmax(0,480px)] lg:items-center lg:gap-16 lg:py-16">
        <div>
          <span className="inline-flex items-center gap-2 rounded-full border border-brand-200 bg-primary-soft px-3 py-1 text-xs font-medium text-primary">
            <LayoutGrid className="size-3.5" aria-hidden="true" />
            SurgeCart 商城
          </span>
          <h1 className="mt-5 text-3xl font-semibold leading-tight tracking-tight text-foreground sm:text-4xl lg:text-5xl lg:leading-[1.15]">
            把好物，<span className="text-primary">稳稳</span>送到手
          </h1>
          <p className="mt-5 max-w-xl text-base leading-7 text-muted-foreground">
            浏览在售商品与分类，查看真实价格与规格信息。搜索、筛选与商品详情均来自商城后端实时数据。
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button asChild size="lg">
              <Link to="/products">
                浏览全部商品
                <ArrowRight aria-hidden="true" />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <a href="#categories">按分类选购</a>
            </Button>
          </div>
        </div>

        <div className="flex flex-col gap-3">
          {primaryBanner ? (
            <>
              <BannerTile banner={primaryBanner} eager className="aspect-[16/9]" />
              {secondaryBanners.length > 0 ? (
                <div className="grid grid-cols-2 gap-3">
                  {secondaryBanners.slice(0, 2).map((banner) => (
                    <BannerTile key={banner.id} banner={banner} className="aspect-[16/9]" />
                  ))}
                </div>
              ) : null}
            </>
          ) : (
            <div className="rounded-2xl border border-border bg-gray-25 p-6 shadow-card">
              <h2 className="text-base font-semibold text-foreground">按分类快速选购</h2>
              <p className="mt-1 text-sm leading-6 text-muted-foreground">
                当前没有配置中的轮播图，可直接从商品分类进入列表。
              </p>
              <Button asChild variant="outline" className="mt-4">
                <Link to="/products">查看全部商品</Link>
              </Button>
            </div>
          )}
        </div>
      </PageContainer>
    </section>
  )
}

function CategorySection({
  categories,
  isPending,
  isError,
  onRetry,
}: {
  categories: Category[]
  isPending: boolean
  isError: boolean
  onRetry: () => void
}) {
  return (
    <section id="categories" className="border-b border-border bg-background py-14 lg:py-16">
      <PageContainer className="flex flex-col gap-8">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <SectionHeading
            eyebrow="Categories"
            title="商品分类"
            description="按分类浏览在售商品；分类数据来自商城分类树接口，仅展示启用项。"
          />
          <Link
            to="/products"
            className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:text-primary-hover"
          >
            查看全部商品
            <ArrowRight className="size-4" aria-hidden="true" />
          </Link>
        </div>

        {isPending ? (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" aria-hidden="true">
            {Array.from({ length: 4 }, (_, index) => (
              <div key={index} className="h-36 animate-pulse rounded-xl border border-border bg-surface" />
            ))}
          </div>
        ) : null}

        {isError ? (
          <ErrorState
            title="分类加载失败"
            description="商品分类暂时无法获取，可稍后重试。"
            onRetry={onRetry}
            className="py-10"
          />
        ) : null}

        {!isPending && !isError && categories.length === 0 ? (
          <EmptyState title="暂无商品分类" description="分类正在整理中，请稍后查看。" />
        ) : null}

        {!isError && categories.length > 0 ? (
          <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {categories.map((category) => {
              const leaves = category.children?.length
                ? collectLeafCategories(category.children)
                : [category]
              const preview = leaves.slice(0, 6)

              return (
                <li
                  key={category.id}
                  className="flex flex-col rounded-xl border border-border bg-surface p-5 shadow-card"
                >
                  <h3 className="text-base font-semibold text-foreground">{category.name}</h3>
                  <p className="mt-1 text-xs text-muted-foreground">共 {leaves.length} 个可选购分类</p>
                  <ul className="mt-4 flex flex-wrap gap-2">
                    {preview.map((leaf) => (
                      <li key={leaf.id}>
                        <Link
                          to={catalogHref({ categoryId: leaf.id })}
                          className="inline-flex h-8 items-center rounded-full border border-border bg-gray-25 px-3 text-xs text-muted-foreground transition-colors duration-150 hover:border-brand-300 hover:text-primary"
                        >
                          {leaf.name}
                        </Link>
                      </li>
                    ))}
                  </ul>
                  {leaves.length > preview.length ? (
                    <p className="mt-3 text-xs text-muted-foreground">
                      另有 {leaves.length - preview.length} 个分类，可在商品列表页查看。
                    </p>
                  ) : null}
                </li>
              )
            })}
          </ul>
        ) : null}
      </PageContainer>
    </section>
  )
}

function LatestProductsSection({
  children,
  isPending,
  isError,
  onRetry,
  isEmpty,
}: {
  children: ReactNode
  isPending: boolean
  isError: boolean
  onRetry: () => void
  isEmpty: boolean
}) {
  return (
    <section className="border-b border-border bg-surface py-14 lg:py-16">
      <PageContainer className="flex flex-col gap-8">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <SectionHeading
            eyebrow="New Arrivals"
            title="最新上架"
            description="按上架时间排序的在售商品，点击卡片查看详情与可选规格。"
          />
          <Link
            to="/products"
            className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:text-primary-hover"
          >
            查看全部
            <ArrowRight className="size-4" aria-hidden="true" />
          </Link>
        </div>

        {isPending ? <ProductGridSkeleton count={5} /> : null}
        {isError ? (
          <ErrorState
            title="商品加载失败"
            description="商品列表暂时无法获取，请稍后重试。"
            onRetry={onRetry}
            className="py-10"
          />
        ) : null}
        {isEmpty ? (
          <EmptyState title="暂无在售商品" description="商品正在上架中，请稍后查看。" />
        ) : null}
        {!isError ? children : null}
      </PageContainer>
    </section>
  )
}

export function HomePage() {
  useDocumentTitle('SurgeCart 商城 | 品质好物，稳定抵达')

  const categoriesQuery = useQuery({
    queryKey: queryKeys.categories.tree(),
    queryFn: ({ signal }) => fetchCategoryTree(signal),
  })
  const bannersQuery = useQuery({
    queryKey: queryKeys.banners.list(),
    queryFn: ({ signal }) => fetchBanners(signal),
  })
  const latestQuery = useQuery({
    queryKey: queryKeys.products.list(HOME_LATEST_QUERY),
    queryFn: ({ signal }) => fetchProductList(HOME_LATEST_QUERY, signal),
  })
  const recommendationQuery = useQuery({
    queryKey: queryKeys.recommendations.byCode(homeRecommendCode),
    queryFn: ({ signal }) => fetchRecommendation(homeRecommendCode, signal),
    enabled: homeRecommendCode !== '',
  })

  const banners = bannersQuery.data?.items ?? []
  const categories = categoriesQuery.data?.items ?? []
  const latestProducts = latestQuery.data?.items ?? []
  const recommendation = recommendationQuery.data
  const recommendationItems = recommendation?.items ?? []

  return (
    <>
      <Hero banners={banners} />

      <CategorySection
        categories={categories}
        isPending={categoriesQuery.isPending}
        isError={categoriesQuery.isError}
        onRetry={() => void categoriesQuery.refetch()}
      />

      <LatestProductsSection
        isPending={latestQuery.isPending}
        isError={latestQuery.isError}
        onRetry={() => void latestQuery.refetch()}
        isEmpty={!latestQuery.isPending && !latestQuery.isError && latestProducts.length === 0}
      >
        {latestProducts.length > 0 ? <ProductGrid products={latestProducts} /> : null}
      </LatestProductsSection>

      {recommendation && recommendationItems.length > 0 ? (
        <section className="bg-background py-14 lg:py-16">
          <PageContainer className="flex flex-col gap-8">
            <SectionHeading
              eyebrow="Recommended"
              title={recommendation.name || '为你推荐'}
              description="运营在后台推荐位中配置的商品，仅展示在售商品。"
            />
            <ProductGrid
              products={recommendationItems.map((item) => ({
                id: item.product_id,
                name: item.name,
                main_image: item.main_image,
                price: item.price,
              }))}
            />
          </PageContainer>
        </section>
      ) : null}
    </>
  )
}
