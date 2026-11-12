import { cva, type VariantProps } from 'class-variance-authority'

/**
 * 按钮样式定义（与 Button 组件分离，便于 Fast Refresh 与锚点复用）。
 * 规范见 DESIGN_SYSTEM.md「按钮」章节。
 */
export const buttonVariants = cva(
  [
    'inline-flex shrink-0 select-none items-center justify-center gap-2 whitespace-nowrap rounded-md',
    'font-medium transition-colors duration-150',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background',
    'disabled:pointer-events-none disabled:opacity-50',
    '[&_svg]:size-4 [&_svg]:shrink-0',
  ].join(' '),
  {
    variants: {
      variant: {
        primary: 'bg-primary text-primary-foreground shadow-xs hover:bg-primary-hover active:bg-primary-active',
        secondary: 'bg-primary-soft text-primary hover:bg-brand-100',
        outline:
          'border border-border-strong bg-surface text-foreground hover:border-brand-300 hover:bg-primary-soft hover:text-primary',
        ghost: 'text-muted-foreground hover:bg-gray-100 hover:text-foreground',
        danger: 'bg-danger text-white shadow-xs hover:bg-danger-hover',
        link: 'h-auto p-0 text-primary underline-offset-4 hover:underline',
      },
      size: {
        sm: 'h-8 px-3 text-[13px]',
        md: 'h-10 px-4 text-sm',
        lg: 'h-12 rounded-lg px-6 text-base',
        icon: 'size-10',
      },
    },
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  },
)

export type ButtonVariantProps = VariantProps<typeof buttonVariants>
