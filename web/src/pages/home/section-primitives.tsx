import type { ReactNode } from 'react'

import { cn } from '@/utils/cn'

export interface SectionHeadingProps {
  eyebrow: string
  title: string
  description?: string
  className?: string
}

export function SectionHeading({ eyebrow, title, description, className }: SectionHeadingProps) {
  return (
    <div className={cn('max-w-2xl', className)}>
      <p className="text-xs font-medium uppercase tracking-[0.18em] text-primary">{eyebrow}</p>
      <h2 className="mt-2 text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
        {title}
      </h2>
      {description ? (
        <p className="mt-3 text-sm leading-6 text-muted-foreground sm:text-base">{description}</p>
      ) : null}
    </div>
  )
}

export interface PanelProps {
  title?: string
  description?: string
  children: ReactNode
  className?: string
}

export function Panel({ title, description, children, className }: PanelProps) {
  return (
    <div className={cn('rounded-xl border border-border bg-surface p-5 shadow-card sm:p-6', className)}>
      {title ? <h3 className="text-sm font-semibold text-foreground">{title}</h3> : null}
      {description ? (
        <p className="mt-1 text-xs leading-5 text-muted-foreground">{description}</p>
      ) : null}
      <div className={cn(title ? 'mt-4' : '', 'min-w-0')}>{children}</div>
    </div>
  )
}
