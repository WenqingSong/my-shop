import { Search } from 'lucide-react'
import type { FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/utils/cn'

export interface SearchBoxProps {
  className?: string
}

/**
 * 搜索框视觉组件。
 * W0 只交付控件与交互态，不连接任何商品搜索接口：
 * 输入可正常使用，提交按钮保持 disabled 并说明开放阶段。
 */
export function SearchBox({ className }: SearchBoxProps) {
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
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
          type="search"
          name="q"
          aria-label="搜索关键词"
          placeholder="搜索商品 / 品牌 / 分类"
          autoComplete="off"
          className="h-11 rounded-r-none border-r-0 pl-10 pr-3 focus-visible:relative focus-visible:z-10"
        />
      </div>
      <Button
        type="submit"
        className="h-11 shrink-0 rounded-l-none px-6"
        disabled
        title="商品搜索将在 W1 阶段接入"
      >
        搜索
      </Button>
    </form>
  )
}
