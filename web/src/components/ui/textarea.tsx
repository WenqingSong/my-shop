import type { ComponentProps } from 'react'

import { cn } from '@/utils/cn'

/** 多行输入框，视觉规范与 Input 保持一致。 */
export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return (
    <textarea
      className={cn(
        'flex min-h-24 w-full rounded-md border border-border-strong bg-surface px-3 py-2 text-sm text-foreground',
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
