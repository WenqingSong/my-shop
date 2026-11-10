import { Slot } from '@radix-ui/react-slot'
import { Loader2 } from 'lucide-react'
import type { ComponentProps } from 'react'

import { buttonVariants, type ButtonVariantProps } from '@/components/ui/button-variants'
import { cn } from '@/utils/cn'

export interface ButtonProps extends ComponentProps<'button'>, ButtonVariantProps {
  /** 以子元素作为渲染节点（例如 <a> / <Link>），保留按钮样式。 */
  asChild?: boolean
  /** 提交中：展示 spinner、锁定交互并标记 aria-busy。 */
  isLoading?: boolean
}

export function Button({
  className,
  variant,
  size,
  asChild = false,
  isLoading = false,
  disabled,
  children,
  ...props
}: ButtonProps) {
  const Comp = asChild ? Slot : 'button'

  return (
    <Comp
      className={cn(buttonVariants({ variant, size }), className)}
      disabled={disabled || isLoading}
      aria-busy={isLoading || undefined}
      {...props}
    >
      {asChild ? (
        children
      ) : (
        <>
          {isLoading ? <Loader2 className="animate-spin" aria-hidden="true" /> : null}
          {children}
        </>
      )}
    </Comp>
  )
}
