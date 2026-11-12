import { useMemo, useState } from 'react'

import { ProductImage } from '@/components/product/product-image'
import { cn } from '@/utils/cn'

export interface ProductGalleryProps {
  name: string
  mainImage: string
  images: string[]
}

/** 商品图册：主图 + 缩略图切换；同图去重，保持后端 sort 顺序。 */
export function ProductGallery({ name, mainImage, images }: ProductGalleryProps) {
  const gallery = useMemo(() => {
    const merged = [mainImage, ...images].map((url) => url.trim()).filter((url) => url !== '')
    return Array.from(new Set(merged))
  }, [mainImage, images])

  const [activeIndex, setActiveIndex] = useState(0)
  const activeImage = gallery[activeIndex] ?? ''

  return (
    <div className="flex flex-col gap-3">
      <ProductImage
        key={activeImage}
        src={activeImage}
        alt={name}
        eager
        className="aspect-square w-full rounded-xl border border-border"
      />

      {gallery.length > 1 ? (
        <ul className="flex flex-wrap gap-2" aria-label="商品图片列表">
          {gallery.map((url, index) => {
            const selected = index === activeIndex

            return (
              <li key={url}>
                <button
                  type="button"
                  onClick={() => setActiveIndex(index)}
                  aria-label={`查看第 ${index + 1} 张商品图片`}
                  aria-pressed={selected}
                  className={cn(
                    'block size-16 overflow-hidden rounded-lg border transition-colors duration-150',
                    selected ? 'border-primary' : 'border-border hover:border-brand-300',
                  )}
                >
                  <ProductImage src={url} alt="" className="size-full" />
                </button>
              </li>
            )
          })}
        </ul>
      ) : null}
    </div>
  )
}
