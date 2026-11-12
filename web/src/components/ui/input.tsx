import type { ComponentProps } from 'react'

import { cn } from '@/utils/cn'

/** 输入框规范见 DESIGN_SYSTEM.md「表单」章节。 */
export function Input({ className, type = 'text', ...props }: ComponentProps<'input'>) {
  return (
    <input
      type={type}
      className={cn(
        'flex h-10 w-full rounded-md border border-border-strong bg-surface px-3 py-2 text-sm text-foreground',
        'placeholder:text-gray-400',
        'transition-colors duration-150',
        'hover:border-gray-400',
        'focus-visible:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/25',
        'disabled:cursor-not-allowed disabled:bg-gray-50 disabled:text-muted-foreground',
        'aria-invalid:border-danger aria-invalid:focus-visible:border-danger aria-invalid:focus-visible:ring-danger/20',
        className,
      )}
      {...props}
    />
  )
}
