import { Check, X } from 'lucide-react'

import { cn } from '@/utils/cn'
import { buildOrderProgress, type OrderProgressStep } from '@/utils/orders'

function StepDot({ step, index }: { step: OrderProgressStep; index: number }) {
  if (step.state === 'done') {
    return (
      <span className="grid size-8 place-items-center rounded-full bg-primary text-primary-foreground">
        <Check className="size-4" aria-hidden="true" />
      </span>
    )
  }

  if (step.state === 'current') {
    return (
      <span className="grid size-8 place-items-center rounded-full border-2 border-primary bg-surface text-xs font-semibold text-primary">
        {index + 1}
      </span>
    )
  }

  return (
    <span className="grid size-8 place-items-center rounded-full border border-border bg-surface text-xs font-medium text-muted-foreground">
      {index + 1}
    </span>
  )
}

export interface OrderProgressProps {
  status: string
  className?: string
}

/**
 * 订单生命周期进度。
 *
 * 边界说明（必须保留）：后端没有状态历史接口，这里展示的是**由当前状态推导**的进度，
 * 不是真实事件记录；页面上必须同时给出「推导」说明，避免误导为用户操作轨迹。
 * 终止态（已取消/已退款）额外展示终止提示，并把未到达的节点保持为待办样式。
 */
export function OrderProgress({ status, className }: OrderProgressProps) {
  const { steps, halted, haltedLabel } = buildOrderProgress(status)

  return (
    <div className={cn('flex flex-col gap-4', className)}>
      {halted && haltedLabel ? (
        <div
          role="status"
          className="flex items-center gap-2 rounded-lg border border-danger/15 bg-danger-soft px-3 py-2 text-sm text-danger"
        >
          <X className="size-4 shrink-0" aria-hidden="true" />
          {haltedLabel}
        </div>
      ) : null}

      <ol className="flex items-start" aria-label="订单生命周期进度">
        {steps.map((step, index) => (
          <li
            key={step.key}
            className={cn('flex items-start', index < steps.length - 1 ? 'flex-1' : 'flex-none')}
            aria-current={step.state === 'current' ? 'step' : undefined}
          >
            <div className="flex w-16 shrink-0 flex-col items-center gap-1.5">
              <StepDot step={step} index={index} />
              <span
                className={cn(
                  'text-xs',
                  step.state === 'todo' ? 'text-muted-foreground' : 'font-medium text-foreground',
                )}
              >
                {step.label}
              </span>
            </div>
            {index < steps.length - 1 ? (
              <span
                aria-hidden="true"
                className={cn(
                  'mt-4 h-0.5 flex-1 rounded',
                  step.state === 'done' ? 'bg-primary' : 'bg-border',
                )}
              />
            ) : null}
          </li>
        ))}
      </ol>
    </div>
  )
}
