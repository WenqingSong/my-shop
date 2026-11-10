import { useQuery } from '@tanstack/react-query'
import { ChevronRight, PackageCheck, ShoppingCart, Truck } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'

import { fetchCategoryTree } from '@/api/categories'
import { ApiError } from '@/api/client'
import { fetchProductDetail } from '@/api/products'
import { queryKeys } from '@/api/query-keys'
import type { ProductDetail, Sku } from '@/api/types'
import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'
import { PagePlaceholder } from '@/components/common/page-placeholder'
import { PriceDisplay } from '@/components/common/price-display'
import { ProductGallery } from '@/components/product/product-gallery'
import { ReviewSection } from '@/components/product/review-section'
import { SkuSelector } from '@/components/product/sku-selector'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { findCategoryById } from '@/utils/category-tree'

function DetailSkeleton() {
  return (
    <div role="status" aria-live="polite" className="grid gap-8 lg:grid-cols-[minmax(0,440px)_minmax(0,1fr)] lg:gap-12">
      <span className="sr-only">正在加载商品详情</span>
      <div className="aspect-square w-full animate-pulse rounded-xl bg-gray-100" aria-hidden="true" />
      <div className="space-y-4" aria-hidden="true">
        <div className="h-5 w-24 animate-pulse rounded-sm bg-gray-100" />
        <div className="h-9 w-4/5 animate-pulse rounded-sm bg-gray-100" />
        <div className="h-5 w-2/5 animate-pulse rounded-sm bg-gray-100" />
        <div className="h-28 w-full animate-pulse rounded-xl bg-gray-100" />
        <div className="h-12 w-2/5 animate-pulse rounded-md bg-gray-100" />
      </div>
    </div>
  )
}

function ProductUnavailable() {
  return (
    <div className="flex min-h-[50vh] items-center justify-center py-8">
      <PagePlaceholder
        eyebrow="404"
        title="商品不存在或已下架"
        description="该商品可能已下架，或者链接有误。可以返回商品列表继续浏览其他在售商品。"
        actions={
          <Button asChild>
            <Link to="/products">返回商品列表</Link>
          </Button>
        }
      />
    </div>
  )
}

function ProductDetailContent({
  product,
  categoryName,
}: {
  product: ProductDetail
  categoryName?: string
}) {
  const [selectedSkuId, setSelectedSkuId] = useState<number | null>(product.skus[0]?.id ?? null)
  const selectedSku: Sku | null = product.skus.find((sku) => sku.id === selectedSkuId) ?? null
  const currentPrice = selectedSku?.price ?? product.price

  return (
    <>
      <nav aria-label="面包屑" className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
        <Link to="/" className="hover:text-primary">
          首页
        </Link>
        <ChevronRight className="size-3" aria-hidden="true" />
        <Link to="/products" className="hover:text-primary">
          全部商品
        </Link>
        {categoryName ? (
          <>
            <ChevronRight className="size-3" aria-hidden="true" />
            <Link to={`/products?category_id=${String(product.category_id)}`} className="hover:text-primary">
              {categoryName}
            </Link>
          </>
        ) : null}
        <ChevronRight className="size-3" aria-hidden="true" />
        <span className="max-w-[280px] truncate text-foreground">{product.name}</span>
      </nav>

      <div className="mt-5 grid gap-8 lg:grid-cols-[minmax(0,440px)_minmax(0,1fr)] lg:gap-12">
        <ProductGallery name={product.name} mainImage={product.main_image} images={product.images} />

        <div className="min-w-0">
          <p className="text-sm text-muted-foreground">
            {product.brand.trim() ? product.brand : '未标注品牌'}
          </p>
          <h1 className="mt-2 text-2xl font-semibold leading-9 tracking-tight text-foreground sm:text-3xl sm:leading-10">
            {product.name}
          </h1>

          <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-1 text-sm text-muted-foreground">
            <div className="flex gap-1">
              <dt>浏览量</dt>
              <dd className="tabular-nums text-foreground">{product.view_count}</dd>
            </div>
            <div className="flex gap-1">
              <dt>商品编号</dt>
              <dd className="tabular-nums text-foreground">{product.id}</dd>
            </div>
          </dl>

          <div className="mt-5 rounded-xl bg-primary-soft px-5 py-4">
            <p className="text-xs text-muted-foreground">当前价格</p>
            <PriceDisplay value={currentPrice} size="lg" className="mt-1" />
            {selectedSku ? (
              <p className="mt-2 text-xs text-muted-foreground">已选规格：{selectedSku.name}</p>
            ) : null}
          </div>

          <section aria-labelledby="sku-heading" className="mt-6">
            <h2 id="sku-heading" className="text-sm font-semibold text-foreground">
              选择规格
            </h2>
            <div className="mt-3">
              <SkuSelector
                skus={product.skus}
                selectedSkuId={selectedSkuId}
                onSelect={(sku) => setSelectedSkuId(sku.id)}
              />
            </div>
          </section>

          <div className="mt-6 flex items-start gap-2.5 rounded-lg border border-border bg-gray-25 px-4 py-3">
            <PackageCheck className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">库存状态</p>
              <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                当前版本未提供公开库存查询接口，库存以下单时校验为准。
              </p>
            </div>
          </div>

          <div className="mt-6 flex flex-wrap items-center gap-3">
            <span title="购物车将在 W3 阶段开放" className="inline-block">
              <Button size="lg" disabled aria-describedby="cart-availability">
                <ShoppingCart aria-hidden="true" />
                加入购物车
              </Button>
            </span>
            <p id="cart-availability" className="text-xs leading-5 text-muted-foreground">
              购物车功能尚未开放，当前可浏览商品与规格信息。
            </p>
          </div>

          <ul className="mt-6 space-y-2 border-t border-border pt-4 text-xs leading-5 text-muted-foreground">
            <li className="flex items-center gap-2">
              <Truck className="size-3.5 shrink-0" aria-hidden="true" />
              配送与运费信息将在下单能力开放后展示。
            </li>
            <li className="flex items-center gap-2">
              <PackageCheck className="size-3.5 shrink-0" aria-hidden="true" />
              商品信息与价格来自商城后端，页面数据随接口实时更新。
            </li>
          </ul>
        </div>
      </div>

      <section aria-labelledby="detail-heading" className="mt-12 border-t border-border pt-8">
        <h2 id="detail-heading" className="text-xl font-semibold text-foreground">
          商品介绍
        </h2>
        {product.detail.trim() ? (
          <p className="mt-4 max-w-4xl whitespace-pre-line break-words text-sm leading-7 text-foreground">
            {product.detail}
          </p>
        ) : (
          <p className="mt-4 text-sm text-muted-foreground">商品详情暂未提供。</p>
        )}
      </section>

      <div className="mt-10">
        <ReviewSection productId={product.id} />
      </div>
    </>
  )
}

export function ProductDetailPage() {
  const { id } = useParams<{ id: string }>()
  const productId = Number(id)
  const isValidId = Number.isSafeInteger(productId) && productId > 0

  const productQuery = useQuery({
    queryKey: queryKeys.products.detail(isValidId ? productId : 0),
    queryFn: ({ signal }) => fetchProductDetail(productId, signal),
    enabled: isValidId,
    retry: (failureCount, error) => {
      // 404 是确定性结果（商品不存在或已下架），重试没有意义。
      if (error instanceof ApiError && error.isNotFound) {
        return false
      }

      return failureCount < 1
    },
  })

  const categoriesQuery = useQuery({
    queryKey: queryKeys.categories.tree(),
    queryFn: ({ signal }) => fetchCategoryTree(signal),
    enabled: isValidId,
    staleTime: 5 * 60_000,
  })

  const product = productQuery.data
  useDocumentTitle(product ? `${product.name} | SurgeCart 商城` : '商品详情 | SurgeCart 商城')

  if (!isValidId) {
    return <ProductUnavailable />
  }

  const categoryName = product
    ? findCategoryById(categoriesQuery.data?.items ?? [], product.category_id)?.name
    : undefined

  return (
    <PageContainer className="py-8 lg:py-10">
      {productQuery.isPending ? <DetailSkeleton /> : null}

      {productQuery.isError ? (
        productQuery.error instanceof ApiError && productQuery.error.isNotFound ? (
          <ProductUnavailable />
        ) : (
          <ErrorState
            title="商品详情加载失败"
            description="商品信息暂时无法获取，请稍后重试。"
            onRetry={() => void productQuery.refetch()}
          />
        )
      ) : null}

      {product ? <ProductDetailContent key={product.id} product={product} categoryName={categoryName} /> : null}
    </PageContainer>
  )
}
