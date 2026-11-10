import { ImageOff } from 'lucide-react'
import { useState } from 'react'

import { cn } from '@/utils/cn'

export interface ProductImageProps {
  src: string
  alt: string
  /** 由调用方控制比例与外边距，例如 aspect-square。 */
  className?: string
  /** 首屏主图应使用 eager，列表卡片保持默认 lazy。 */
  eager?: boolean
}

/**
 * 商品图片：统一处理空地址、加载失败与比例容器。
 * 图片缺失时展示明确占位，不渲染破图，也不使用远程占位服务。
 */
export function ProductImage({ src, alt, className, eager = false }: ProductImageProps) {
  const [failedSrc, setFailedSrc] = useState<string | null>(null)
  const hasImage = src.trim() !== '' && failedSrc !== src

  return (
    <div className={cn('overflow-hidden bg-gray-100', className)}>
      {hasImage ? (
        <img
          src={src}
          alt={alt}
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          onError={() => setFailedSrc(src)}
          className="size-full object-cover"
        />
      ) : (
        <div className="flex size-full flex-col items-center justify-center gap-1.5 text-gray-400">
          <ImageOff className="size-7" aria-hidden="true" />
          <span className="text-[11px]">暂无图片</span>
        </div>
      )}
    </div>
  )
}
