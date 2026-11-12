import { Search } from 'lucide-react'
import type { FormEvent } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { catalogHref } from '@/utils/catalog-search'
import { cn } from '@/utils/cn'

export interface SearchBoxProps {
  className?: string
}

/**
 * 商品搜索入口：提交后跳转 /products?q=关键词。
 * 输入框默认值跟随当前列表页关键词，切换页面或清空筛选后自动复位。
 */
export function SearchBox({ className }: SearchBoxProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const currentKeyword = location.pathname === '/products' ? (searchParams.get('q') ?? '') : ''

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const keyword = String(formData.get('q') ?? '').trim()
    navigate(catalogHref({ q: keyword }))
  }

  return (
    <form
      role="search"
      aria-label="商品搜索"
      onSubmit={handleSubmit}
      className={cn('flex w-full min-w-0 items-center', className)}
    >
      <div className="relative flex-1">
        <Search
          className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          key={currentKeyword}
          type="search"
          name="q"
          defaultValue={currentKeyword}
          aria-label="搜索关键词"
          placeholder="搜索商品 / 品牌"
          autoComplete="off"
          className="h-11 rounded-r-none border-r-0 pl-10 pr-3 focus-visible:relative focus-visible:z-10"
        />
      </div>
      <Button type="submit" className="h-11 shrink-0 rounded-l-none px-6">
        搜索
      </Button>
    </form>
  )
}
