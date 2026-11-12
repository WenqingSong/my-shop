import { Check } from 'lucide-react'

import type { Sku } from '@/api/types'
import { PriceDisplay } from '@/components/common/price-display'
import { cn } from '@/utils/cn'

export interface SkuSelectorProps {
  skus: Sku[]
  selectedSkuId: number | null
  onSelect: (sku: Sku) => void
}

/**
 * SKU 规格选择。
 *
 * 后端 SKU 只有 name / price / status，没有规格维度（颜色、尺寸）与库存字段，
 * 因此这里渲染为单层规格列表，不虚构多维规格矩阵。
 */
export function SkuSelector({ skus, selectedSkuId, onSelect }: SkuSelectorProps) {
  if (skus.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border-strong bg-gray-25 px-4 py-3 text-sm text-muted-foreground">
        该商品暂未配置可售规格
      </p>
    )
  }

  return (
    <div role="group" aria-label="商品规格" className="flex flex-wrap gap-2">
      {skus.map((sku) => {
        const selected = sku.id === selectedSkuId

        return (
          <button
            key={sku.id}
            type="button"
            aria-pressed={selected}
            onClick={() => onSelect(sku)}
            className={cn(
              'inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors duration-150',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-2',
              selected
                ? 'border-primary bg-primary-soft text-primary'
                : 'border-border-strong bg-surface text-foreground hover:border-brand-300 hover:text-primary',
            )}
          >
            {selected ? <Check className="size-4" aria-hidden="true" /> : null}
            <span>{sku.name}</span>
            <PriceDisplay value={sku.price} size="sm" tone="neutral" className="text-xs" />
          </button>
        )
      })}
    </div>
  )
}
