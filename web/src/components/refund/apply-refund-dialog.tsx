import { zodResolver } from '@hookform/resolvers/zod'
import { AlertTriangle, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useNavigate } from 'react-router'

import type { Order } from '@/api/orders'
import { REFUND_REASON_MAX_LENGTH } from '@/api/refunds'
import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { useApplyRefund } from '@/hooks/use-refunds'
import { describeRefundActionError } from '@/pages/account/refund-errors'
import { refundApplySchema, type RefundApplyValues } from '@/pages/account/refund-schema'

export interface ApplyRefundDialogProps {
  order: Order
}

/**
 * 整单退款申请入口 + 表单。
 *
 * 与后端契约严格对齐（`api/refund/v1/refund.go:50-55`）：
 * - 请求体只有 `reason`，**不提交金额**；金额由服务端从 `orders.total_amount` 派生；
 * - 只支持整单退款，因此没有商品选择、数量、凭证上传（后端也没有上传接口）；
 * - 申请需要商家审核，提交成功只代表申请已受理，不代表退款完成或资金退回。
 *
 * 成功后跳转退款详情页（服务端返回的 Refund.id），并已写入详情缓存。
 */
export function ApplyRefundDialog({ order }: ApplyRefundDialogProps) {
  const navigate = useNavigate()
  const mutation = useApplyRefund(order.id)
  const [open, setOpen] = useState(false)
  const [serverError, setServerError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    reset,
    control,
    formState: { errors },
  } = useForm<RefundApplyValues>({
    resolver: zodResolver(refundApplySchema),
    defaultValues: { reason: '' },
  })

  // 用 useWatch 而不是 form.watch()：后者与 React Compiler 不兼容（eslint 会告警）。
  const reason = useWatch({ control, name: 'reason' }) ?? ''
  const reasonLength = Array.from(reason).length

  const onSubmit = handleSubmit((values) => {
    setServerError(null)
    mutation.mutate(values.reason, {
      onSuccess: (refund) => {
        setOpen(false)
        reset()
        navigate(`/account/refunds/${String(refund.id)}`)
      },
      onError: (error) => {
        setServerError(describeRefundActionError(error, 'apply'))
      },
    })
  })

  const handleOpenChange = (nextOpen: boolean) => {
    // 请求在途时不允许关闭，避免用户误以为提交被取消而重复发起。
    if (mutation.isPending) {
      return
    }

    if (!nextOpen) {
      setServerError(null)
    }

    setOpen(nextOpen)
  }

  return (
    <>
      <Button onClick={() => setOpen(true)} disabled={mutation.isPending}>
        <RotateCcw aria-hidden="true" />
        申请退款
      </Button>

      <Dialog open={open} onOpenChange={handleOpenChange}>
        <DialogContent showCloseButton={!mutation.isPending}>
          <DialogHeader>
            <DialogTitle>申请整单退款</DialogTitle>
            <DialogDescription>
              仅支持该订单整单退款，退款金额由服务端按订单实付金额计算，无法修改。
            </DialogDescription>
          </DialogHeader>

          <div className="flex items-start gap-2 rounded-lg border border-warning/20 bg-warning-soft px-3 py-2 text-sm leading-6 text-warning">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            <span>
              提交后申请进入「申请中」，需要商家审核。申请成功不代表退款完成，
              也不代表真实资金已经退回。
            </span>
          </div>

          <dl className="flex flex-col gap-2 rounded-lg border border-border bg-gray-50 px-3 py-2 text-sm">
            <div className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">订单编号</dt>
              <dd className="break-all font-mono text-[13px] text-foreground">{order.order_no}</dd>
            </div>
            <div className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">订单实付金额</dt>
              <dd>
                <PriceDisplay value={order.total_amount} size="md" />
              </dd>
            </div>
            <div className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">退款方式</dt>
              <dd className="text-foreground">整单退款</dd>
            </div>
          </dl>

          {serverError ? (
            <div
              role="alert"
              className="rounded-lg border border-danger/20 bg-danger-soft px-3 py-2 text-sm text-danger"
            >
              {serverError}
            </div>
          ) : null}

          <form onSubmit={onSubmit} noValidate className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between gap-2">
              <label htmlFor="refund-reason" className="text-sm font-medium text-foreground">
                退款原因
              </label>
              <span className="tabular-nums text-xs text-muted-foreground">
                {reasonLength}/{REFUND_REASON_MAX_LENGTH}
              </span>
            </div>
            <Textarea
              id="refund-reason"
              rows={4}
              placeholder="请说明退款原因，例如：下单后不想要了"
              aria-invalid={errors.reason ? true : undefined}
              aria-describedby={errors.reason ? 'refund-reason-error' : undefined}
              {...register('reason')}
            />
            {errors.reason ? (
              <p id="refund-reason-error" role="alert" className="text-xs text-danger">
                {errors.reason.message}
              </p>
            ) : null}
          </form>

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => handleOpenChange(false)}
              disabled={mutation.isPending}
            >
              再想想
            </Button>
            <Button
              onClick={() => void onSubmit()}
              isLoading={mutation.isPending}
              disabled={mutation.isPending}
            >
              提交退款申请
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
