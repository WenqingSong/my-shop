import { AlertTriangle, RotateCcw } from 'lucide-react'
import type { ComponentPropsWithoutRef, ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/utils/cn'

export interface ErrorStateProps extends Omit<ComponentPropsWithoutRef<'div'>, 'title'> {
  title?: string
  description?: string
  /** 提供后展示「重新加载」按钮。 */
  onRetry?: () => void
  retryLabel?: string
  action?: ReactNode
}

export function ErrorState({
  title = '加载失败',
  description = '请检查网络连接后重试，如问题持续出现请联系客服。',
  onRetry,
  retryLabel = '重新加载',
  action,
  className,
  ...props
}: ErrorStateProps) {
  return (
    <div
      role="alert"
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-xl border border-danger/15 bg-surface px-6 py-14 text-center',
        className,
      )}
      {...props}
    >
      <span className="grid size-12 place-items-center rounded-full bg-danger-soft text-danger">
        <AlertTriangle className="size-6" aria-hidden="true" />
      </span>
      <p className="text-base font-medium text-foreground">{title}</p>
      <p className="max-w-sm text-sm leading-6 text-muted-foreground">{description}</p>
      {onRetry || action ? (
        <div className="mt-2 flex items-center gap-2">
          {onRetry ? (
            <Button variant="outline" size="sm" onClick={onRetry}>
              <RotateCcw aria-hidden="true" />
              {retryLabel}
            </Button>
          ) : null}
          {action}
        </div>
      ) : null}
    </div>
  )
}
