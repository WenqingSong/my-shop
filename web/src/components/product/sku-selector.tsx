import { Check } from 'lucide-react'
import { useState } from 'react'

import type { Sku, SkuAttr, SkuProductValue } from '@/api/types'
import { PriceDisplay } from '@/components/common/price-display'
import { cn } from '@/utils/cn'
import { buildProductValueKey, describeSkuSpec } from '@/utils/sku'

export interface SkuSelectorProps {
  skus: Sku[]
  /** 后端 van-sku 规格组（无规格为空数组）。 */
  attr: SkuAttr[]
  /** 后端 van-sku 组合映射（键=规格值文本逗号连接；无规格为空对象）。 */
  productValue: Record<string, SkuProductValue>
  selectedSkuId: number | null
  /** 选择结果始终回传真实 SKU（含 sku_id）；规格不完整时为 null。 */
  onSelect: (sku: Sku | null) => void
}

const optionClass = (selected: boolean) =>
  cn(
    'inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors duration-150',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-2',
    selected
      ? 'border-primary bg-primary-soft text-primary'
      : 'border-border-strong bg-surface text-foreground hover:border-brand-300 hover:text-primary',
  )

/**
 * SKU 规格选择。
 *
 * 数据事实（api/product/v1/product.go）：
 * - `attr[]` 是规格组（如「颜色」「尺码」），`attrValueArr` 是该组可选值；
 * - `productValue` 的键是按 attr 顺序用逗号连接的规格值文本，值是真实 SKU（含 sku_id）；
 * - `skus[]` 只有 enabled SKU，**没有 stock**；stock 只在 productValue 中。
 *
 * 选择行为：
 * - 规格组合完整且命中 productValue → onSelect(真实 SKU，按 sku_id 关联)；
 * - 组合不完整或未命中 → onSelect(null)，不猜测、不按名称拼字符串；
 * - 无规格商品（attr 为空，或 productValue 为空）→ 退化为 SKU 列表，仍按真实 sku_id 选择。
 */
export function SkuSelector({
  skus,
  attr,
  productValue,
  selectedSkuId,
  onSelect,
}: SkuSelectorProps) {
  const groups = attr.filter((group) => group.attrName.trim() !== '' && group.attrValueArr.length > 0)
  const useMatrix = groups.length > 0 && Object.keys(productValue).length > 0
  const [selectedValues, setSelectedValues] = useState<(string | null)[]>(() =>
    groups.map(() => null),
  )

  const handleValueSelect = (groupIndex: number, value: string) => {
    const next = [...selectedValues]
    next[groupIndex] = value
    setSelectedValues(next)

    const key = buildProductValueKey(next)

    if (key === null) {
      onSelect(null)
      return
    }

    const combination = productValue[key]
    const sku = combination ? (skus.find((item) => item.id === combination.sku_id) ?? null) : null
    onSelect(sku)
  }

  if (skus.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border-strong bg-gray-25 px-4 py-3 text-sm text-muted-foreground">
        该商品暂未配置可售规格
      </p>
    )
  }

  if (!useMatrix) {
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
              className={optionClass(selected)}
            >
              {selected ? <Check className="size-4" aria-hidden="true" /> : null}
              <span>{describeSkuSpec(sku)}</span>
              <PriceDisplay value={sku.price} size="sm" tone="neutral" className="text-xs" />
            </button>
          )
        })}
      </div>
    )
  }

  const allSelected = selectedValues.every((value) => value !== null)
  const combinationUnavailable = allSelected && selectedSkuId === null

  return (
    <div role="group" aria-label="商品规格" className="flex flex-col gap-4">
      {groups.map((group, groupIndex) => (
        <fieldset key={`${String(groupIndex)}-${group.attrName}`} className="min-w-0">
          <legend className="text-xs font-medium text-muted-foreground">{group.attrName}</legend>
          <div className="mt-2 flex flex-wrap gap-2">
            {group.attrValueArr.map((value) => {
              const selected = selectedValues[groupIndex] === value

              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => handleValueSelect(groupIndex, value)}
                  className={optionClass(selected)}
                >
                  {selected ? <Check className="size-4" aria-hidden="true" /> : null}
                  <span>{value}</span>
                </button>
              )
            })}
          </div>
        </fieldset>
      ))}

      {combinationUnavailable ? (
        <p role="status" className="text-xs leading-5 text-warning">
          该规格组合暂不可售，请更换规格。
        </p>
      ) : null}
    </div>
  )
}
