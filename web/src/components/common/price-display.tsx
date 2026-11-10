import type { ComponentPropsWithoutRef } from 'react'

import { cn } from '@/utils/cn'
import { formatPriceParts } from '@/utils/price'

type PriceSize = 'sm' | 'md' | 'lg'

const rootSizeClass: Record<PriceSize, string> = {
  sm: 'text-sm gap-px',
  md: 'text-xl gap-px',
  lg: 'text-3xl gap-0.5',
}

const symbolSizeClass: Record<PriceSize, string> = {
  sm: 'text-[11px]',
  md: 'text-sm',
  lg: 'text-lg',
}

const decimalSizeClass: Record<PriceSize, string> = {
  sm: 'text-[11px]',
  md: 'text-xs',
  lg: 'text-base',
}

export interface PriceDisplayProps extends Omit<ComponentPropsWithoutRef<'span'>, 'children'> {
  /** 金额，单位为「分」的整数，例如 129900 表示 ¥1,299.00。 */
  value: number
  size?: PriceSize
  /** price 用于商品价格（强调色），neutral 用于运费、小计说明等次要金额。 */
  tone?: 'price' | 'neutral'
}

/**
 * 价格展示组件：只负责把整数分渲染成人民币金额，
 * 不参与任何金额计算、折扣推导或精度处理。
 */
export function PriceDisplay({
  value,
  size = 'md',
  tone = 'price',
  className,
  ...props
}: PriceDisplayProps) {
  const parts = formatPriceParts(value)

  return (
    <span
      className={cn(
        'inline-flex items-baseline font-semibold leading-none tabular-nums',
        tone === 'price' ? 'text-price' : 'text-foreground',
        rootSizeClass[size],
        className,
      )}
      {...props}
    >
      <span className={cn('font-medium', symbolSizeClass[size])}>
        {parts.negative ? '-' : ''}
        {parts.symbol}
      </span>
      <span>{parts.integer}</span>
      <span className={cn('font-medium', decimalSizeClass[size])}>.{parts.decimal}</span>
    </span>
  )
}
