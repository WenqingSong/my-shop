import { Star } from 'lucide-react'

import { cn } from '@/utils/cn'

export interface RatingStarsProps {
  /** 1-5 星，允许小数（平均分）；展示时四舍五入。 */
  value: number
  size?: 'sm' | 'md'
  className?: string
}

/** 星级展示：仅渲染图形与可读的 aria-label，不承担评分交互。 */
export function RatingStars({ value, size = 'md', className }: RatingStarsProps) {
  const rounded = Math.round(value)
  const iconClass = size === 'sm' ? 'size-3.5' : 'size-4'

  return (
    <span
      role="img"
      aria-label={`${value.toFixed(1)} 星`}
      className={cn('inline-flex items-center gap-0.5 text-warning', className)}
    >
      {[1, 2, 3, 4, 5].map((star) => (
        <Star
          key={star}
          aria-hidden="true"
          className={cn(iconClass, star <= rounded ? 'fill-warning text-warning' : 'fill-transparent text-gray-300')}
        />
      ))}
    </span>
  )
}
