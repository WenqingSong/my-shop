import { PackageOpen } from 'lucide-react'
import type { ComponentPropsWithoutRef, ReactNode } from 'react'

import { cn } from '@/utils/cn'

export interface EmptyStateProps extends Omit<ComponentPropsWithoutRef<'div'>, 'title'> {
  title: string
  description?: string
  icon?: ReactNode
  action?: ReactNode
}

export function EmptyState({
  title,
  description,
  icon,
  action,
  className,
  ...props
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border-strong bg-surface px-6 py-14 text-center',
        className,
      )}
      {...props}
    >
      <span className="grid size-12 place-items-center rounded-full bg-gray-100 text-gray-500">
        {icon ?? <PackageOpen className="size-6" aria-hidden="true" />}
      </span>
      <p className="text-base font-medium text-foreground">{title}</p>
      {description ? (
        <p className="max-w-sm text-sm leading-6 text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}
