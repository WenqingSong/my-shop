import type { ComponentPropsWithoutRef } from 'react'

import { cn } from '@/utils/cn'

export interface PageContainerProps extends ComponentPropsWithoutRef<'div'> {
  /** default：1440px 商城主容器；narrow：1080px 正文/表单容器。 */
  width?: 'default' | 'narrow'
}

/**
 * 商城统一内容容器：负责最大宽度与响应式左右留白。
 * 页面与公共布局都必须通过它控制水平边距，避免出现多种宽度基准。
 */
export function PageContainer({
  width = 'default',
  className,
  children,
  ...props
}: PageContainerProps) {
  return (
    <div
      className={cn(
        'mx-auto w-full px-4 sm:px-6 xl:px-8',
        width === 'default' ? 'max-w-[1440px]' : 'max-w-[1080px]',
        className,
      )}
      {...props}
    >
      {children}
    </div>
  )
}
