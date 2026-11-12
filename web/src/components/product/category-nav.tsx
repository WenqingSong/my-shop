import { Link } from 'react-router'

import type { Category } from '@/api/types'
import { cn } from '@/utils/cn'
import { catalogHref } from '@/utils/catalog-search'
import { collectLeafCategories } from '@/utils/category-tree'

export interface CategoryNavProps {
  categories: Category[]
  activeCategoryId?: number
}

function categoryLinkClass(active: boolean): string {
  return cn(
    'block rounded-md px-2 py-1.5 text-sm transition-colors duration-150',
    active
      ? 'bg-primary-soft font-medium text-primary'
      : 'text-muted-foreground hover:bg-gray-100 hover:text-foreground',
  )
}

/** 桌面端分类筛选：顶级分类作为分组标题，只有叶子分类可点击（与后端精确匹配语义一致）。 */
export function CategorySidebar({ categories, activeCategoryId }: CategoryNavProps) {
  return (
    <nav aria-label="商品分类筛选" className="rounded-xl border border-border bg-surface p-4 shadow-card">
      <h2 className="px-2 text-sm font-semibold text-foreground">商品分类</h2>
      <ul className="mt-2 space-y-0.5">
        <li>
          <Link
            to={catalogHref({})}
            aria-current={activeCategoryId === undefined ? 'page' : undefined}
            className={categoryLinkClass(activeCategoryId === undefined)}
          >
            全部商品
          </Link>
        </li>

        {categories.map((category) => {
          // 没有子分类时，顶级分类本身就是叶子，可以直接作为筛选项。
          if (!category.children || category.children.length === 0) {
            return (
              <li key={category.id}>
                <Link
                  to={catalogHref({ categoryId: category.id })}
                  aria-current={category.id === activeCategoryId ? 'page' : undefined}
                  className={categoryLinkClass(category.id === activeCategoryId)}
                >
                  {category.name}
                </Link>
              </li>
            )
          }

          return (
            <li key={category.id} className="pt-2">
              <p className="px-2 text-xs font-medium tracking-wide text-muted-foreground">
                {category.name}
              </p>
              <ul className="mt-1 space-y-0.5">
                {collectLeafCategories(category.children).map((leaf) => (
                  <li key={leaf.id}>
                    <Link
                      to={catalogHref({ categoryId: leaf.id })}
                      aria-current={leaf.id === activeCategoryId ? 'page' : undefined}
                      className={categoryLinkClass(leaf.id === activeCategoryId)}
                    >
                      {leaf.name}
                    </Link>
                  </li>
                ))}
              </ul>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

/** 移动端分类筛选：横向滚动叶子分类，避免在窄屏渲染整棵树。 */
export function CategoryChips({ categories, activeCategoryId }: CategoryNavProps) {
  const leaves = collectLeafCategories(categories)
  const chipClass = (active: boolean) =>
    cn(
      'inline-flex h-9 items-center whitespace-nowrap rounded-full border px-3.5 text-sm transition-colors duration-150',
      active
        ? 'border-primary bg-primary-soft font-medium text-primary'
        : 'border-border bg-surface text-muted-foreground hover:border-brand-300 hover:text-primary',
    )

  return (
    <nav aria-label="商品分类筛选" className="-mx-4 overflow-x-auto px-4 sm:-mx-6 sm:px-6">
      <ul className="flex w-max items-center gap-2 pb-1">
        <li>
          <Link
            to={catalogHref({})}
            aria-current={activeCategoryId === undefined ? 'page' : undefined}
            className={chipClass(activeCategoryId === undefined)}
          >
            全部商品
          </Link>
        </li>
        {leaves.map((leaf) => {
          const active = leaf.id === activeCategoryId

          return (
            <li key={leaf.id}>
              <Link
                to={catalogHref({ categoryId: leaf.id })}
                aria-current={active ? 'page' : undefined}
                className={chipClass(active)}
              >
                {leaf.name}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
