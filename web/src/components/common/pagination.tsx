import { ChevronLeft, ChevronRight, MoreHorizontal } from 'lucide-react'
import type { ComponentPropsWithoutRef } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/utils/cn'
import { buildPaginationItems } from '@/utils/pagination'

export interface PaginationProps extends Omit<ComponentPropsWithoutRef<'nav'>, 'onChange'> {
  page: number
  totalPages: number
  onPageChange: (page: number) => void
}

export function Pagination({ page, totalPages, onPageChange, className, ...props }: PaginationProps) {
  if (totalPages <= 1) {
    return null
  }

  const current = Math.min(Math.max(Math.trunc(page), 1), totalPages)
  const items = buildPaginationItems(current, totalPages)

  return (
    <nav
      aria-label="分页导航"
      className={cn('flex flex-wrap items-center justify-center gap-1.5', className)}
      {...props}
    >
      <Button
        variant="outline"
        size="sm"
        onClick={() => onPageChange(current - 1)}
        disabled={current === 1}
        aria-label="上一页"
      >
        <ChevronLeft aria-hidden="true" />
        上一页
      </Button>

      <ul className="flex items-center gap-1">
        {items.map((item) => {
          if (typeof item !== 'number') {
            return (
              <li key={item} aria-hidden="true">
                <span className="grid size-8 place-items-center text-muted-foreground">
                  <MoreHorizontal className="size-4" />
                </span>
              </li>
            )
          }

          const isActive = item === current

          return (
            <li key={item}>
              <Button
                variant={isActive ? 'primary' : 'ghost'}
                size="sm"
                className="min-w-8 px-2"
                onClick={() => onPageChange(item)}
                aria-label={`第 ${item} 页`}
                aria-current={isActive ? 'page' : undefined}
              >
                {item}
              </Button>
            </li>
          )
        })}
      </ul>

      <Button
        variant="outline"
        size="sm"
        onClick={() => onPageChange(current + 1)}
        disabled={current === totalPages}
      >
        下一页
        <ChevronRight aria-hidden="true" />
      </Button>
    </nav>
  )
}
