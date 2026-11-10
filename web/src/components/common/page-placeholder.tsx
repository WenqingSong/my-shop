import { Compass } from 'lucide-react'
import type { ComponentPropsWithoutRef, ReactNode } from 'react'

import { cn } from '@/utils/cn'

export interface PagePlaceholderProps extends Omit<ComponentPropsWithoutRef<'div'>, 'title'> {
  eyebrow?: string
  title: string
  description?: string
  icon?: ReactNode
  actions?: ReactNode
}

/**
 * 页面级占位：用于未开发路由、未接入能力的页面，
 * 保证「未完成」也有统一、可读、可导航的表达。
 */
export function PagePlaceholder({
  eyebrow,
  title,
  description,
  icon,
  actions,
  className,
  ...props
}: PagePlaceholderProps) {
  return (
    <div
      className={cn(
        'mx-auto flex max-w-xl flex-col items-center gap-4 rounded-2xl border border-border bg-surface px-6 py-14 text-center shadow-card sm:px-10',
        className,
      )}
      {...props}
    >
      <span className="grid size-14 place-items-center rounded-full bg-primary-soft text-primary">
        {icon ?? <Compass className="size-7" aria-hidden="true" />}
      </span>
      {eyebrow ? (
        <span className="text-xs font-medium uppercase tracking-[0.18em] text-primary">
          {eyebrow}
        </span>
      ) : null}
      <h1 className="text-2xl font-semibold leading-8 text-foreground">{title}</h1>
      {description ? (
        <p className="text-sm leading-6 text-muted-foreground">{description}</p>
      ) : null}
      {actions ? <div className="mt-2 flex flex-wrap items-center justify-center gap-3">{actions}</div> : null}
    </div>
  )
}
