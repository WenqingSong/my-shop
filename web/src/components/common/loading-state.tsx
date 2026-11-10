import { Loader2 } from 'lucide-react'
import type { ComponentPropsWithoutRef } from 'react'

import { cn } from '@/utils/cn'

export interface LoadingStateProps extends ComponentPropsWithoutRef<'div'> {
  label?: string
  description?: string
}

export function LoadingState({
  label = '正在加载',
  description,
  className,
  ...props
}: LoadingStateProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        'flex flex-col items-center justify-center gap-3 px-6 py-16 text-center',
        className,
      )}
      {...props}
    >
      <span className="grid size-10 place-items-center rounded-full bg-primary-soft text-primary">
        <Loader2 className="size-5 animate-spin" aria-hidden="true" />
      </span>
      <p className="text-sm font-medium text-foreground">{label}</p>
      {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
    </div>
  )
}
