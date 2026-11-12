import { Minus, Plus } from 'lucide-react'
import { useState } from 'react'

import { cn } from '@/utils/cn'

export interface QuantityStepperProps {
  value: number
  min?: number
  max?: number
  disabled?: boolean
  /** 无障碍标签主语（商品名），用于区分同一页面的多个步进器。 */
  label: string
  onChange: (value: number) => void
  className?: string
}

const controlClass =
  'grid size-9 shrink-0 place-items-center text-muted-foreground transition-colors duration-150 ' +
  'hover:bg-gray-100 hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40 ' +
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-inset'

/**
 * 数量步进器（商品详情与购物车共用）。
 *
 * 只接受整数：输入框允许临时为空，失焦时回填为最近一次合法值；
 * 合法范围内边输入边提交，由调用方决定是否乐观更新与如何合并请求。
 */
export function QuantityStepper({
  value,
  min = 1,
  max = 999,
  disabled = false,
  label,
  onChange,
  className,
}: QuantityStepperProps) {
  const [draft, setDraft] = useState(String(value))
  const [syncedValue, setSyncedValue] = useState(value)

  // 受控值变化（例如服务端回写或撤销）时同步输入框；
  // 用「渲染期调整 state」而不是 effect，避免多一次级联渲染（React 官方推荐写法）。
  if (value !== syncedValue) {
    setSyncedValue(value)
    setDraft(String(value))
  }

  const clamp = (next: number) => Math.min(Math.max(next, min), max)

  const handleChange = (raw: string) => {
    const digits = raw.replace(/\D/g, '')
    setDraft(digits)

    if (digits === '') {
      return
    }

    const parsed = Number(digits)

    if (Number.isSafeInteger(parsed) && parsed >= min && parsed <= max) {
      onChange(parsed)
    }
  }

  const handleBlur = () => {
    const parsed = Number(draft)

    if (draft === '' || !Number.isSafeInteger(parsed)) {
      setDraft(String(value))
      return
    }

    const next = clamp(parsed)
    setDraft(String(next))

    if (next !== value) {
      onChange(next)
    }
  }

  return (
    <div
      className={cn(
        'inline-flex items-center overflow-hidden rounded-md border border-border-strong bg-surface',
        className,
      )}
    >
      <button
        type="button"
        aria-label={`减少数量（${label}）`}
        onClick={() => onChange(clamp(value - 1))}
        disabled={disabled || value <= min}
        className={controlClass}
      >
        <Minus className="size-4" aria-hidden="true" />
      </button>

      <input
        type="text"
        inputMode="numeric"
        autoComplete="off"
        aria-label={`购买数量（${label}）`}
        value={draft}
        disabled={disabled}
        onChange={(event) => handleChange(event.target.value)}
        onBlur={handleBlur}
        className={cn(
          'h-9 w-11 border-x border-border-strong bg-surface text-center text-sm tabular-nums',
          'text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
          'focus-visible:ring-inset disabled:cursor-not-allowed disabled:bg-gray-50 disabled:text-muted-foreground',
        )}
      />

      <button
        type="button"
        aria-label={`增加数量（${label}）`}
        onClick={() => onChange(clamp(value + 1))}
        disabled={disabled || value >= max}
        className={controlClass}
      >
        <Plus className="size-4" aria-hidden="true" />
      </button>
    </div>
  )
}
