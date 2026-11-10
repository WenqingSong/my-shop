import type { ReactNode } from 'react'

import { cn } from '@/utils/cn'

export interface AuthCardProps {
  title: string
  description?: string
  children: ReactNode
  footer?: ReactNode
  className?: string
}

/** 登录/注册共用的居中卡片：统一宽度、标题层级与留白。 */
export function AuthCard({ title, description, children, footer, className }: AuthCardProps) {
  return (
    <div
      className={cn(
        'w-full max-w-md rounded-2xl border border-border bg-surface p-6 shadow-card sm:p-8',
        className,
      )}
    >
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
      {description ? (
        <p className="mt-2 text-sm leading-6 text-muted-foreground">{description}</p>
      ) : null}
      <div className="mt-6">{children}</div>
      {footer ? <div className="mt-6 border-t border-border pt-4 text-sm">{footer}</div> : null}
    </div>
  )
}
