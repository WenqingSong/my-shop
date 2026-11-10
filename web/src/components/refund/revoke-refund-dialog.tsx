import { Undo2 } from 'lucide-react'
import { useState } from 'react'

import type { Refund } from '@/api/refunds'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useRevokeRefund } from '@/hooks/use-refunds'
import { describeRefundActionError } from '@/pages/account/refund-errors'
import { canRevokeRefund } from '@/utils/refunds'

export interface RevokeRefundFeedback {
  tone: 'success' | 'error'
  text: string
}

export interface RevokeRefundDialogProps {
  refund: Refund
  /**
   * 撤销结果回调。
   *
   * 反馈必须由调用方（详情页）持有：撤销成功后退款状态变为 `revoked`，
   * 本组件会因 `canRevokeRefund` 为 false 而卸载，内部 state 会一起丢失。
   * 失败时也可能因缓存刷新使状态变为 `pending_refund` 而卸载。
   */
  onResult: (feedback: RevokeRefundFeedback) => void
}

/**
 * 撤销退款申请。
 *
 * 后端只允许 `applying(10) → revoked(50)`（`internal/logic/refund/refund.go:199-255`），
 * 因此入口只在 `status === 'applying'` 时渲染；其余状态不提供按钮，
 * 而不是"按钮可点但必然报错"。
 *
 * 撤销是真实后端写操作：成功后用服务端返回的 Refund 覆盖缓存，
 * 不做"先把状态改成已撤销"的本地伪装。
 */
export function RevokeRefundDialog({ refund, onResult }: RevokeRefundDialogProps) {
  const mutation = useRevokeRefund()
  const [open, setOpen] = useState(false)

  if (!canRevokeRefund(refund)) {
    return null
  }

  const handleOpenChange = (nextOpen: boolean) => {
    // 请求在途时不允许关闭，避免用户以为操作被取消而重复发起。
    if (!nextOpen && mutation.isPending) {
      return
    }

    setOpen(nextOpen)
  }

  const handleConfirm = () => {
    mutation.mutate(refund.id, {
      onSuccess: () => {
        onResult({
          tone: 'success',
          text: '退款申请已撤销，订单保持已支付状态。如需退款可以重新申请。',
        })
        setOpen(false)
      },
      onError: (error) => {
        onResult({ tone: 'error', text: describeRefundActionError(error, 'revoke') })
        setOpen(false)
      },
    })
  }

  return (
    <>
      <Button
        variant="outline"
        onClick={() => setOpen(true)}
        disabled={mutation.isPending}
        isLoading={mutation.isPending}
        className="text-danger hover:border-danger/30 hover:bg-danger-soft hover:text-danger"
      >
        <Undo2 aria-hidden="true" />
        撤销退款申请
      </Button>

      <Dialog open={open} onOpenChange={handleOpenChange}>
        <DialogContent showCloseButton={!mutation.isPending}>
          <DialogHeader>
            <DialogTitle>撤销退款申请确认</DialogTitle>
            <DialogDescription>
              撤销后本次退款申请将变为「已撤销」，且该操作不可恢复。
            </DialogDescription>
          </DialogHeader>

          <div className="rounded-lg border border-border bg-gray-50 px-3 py-2 text-sm leading-6 text-muted-foreground">
            <p>· 订单状态保持「已支付」，不会因为撤销而改变；</p>
            <p>· 撤销不涉及库存或优惠券变动；</p>
            <p>· 撤销后如仍需退款，可以重新提交申请。</p>
          </div>

          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={mutation.isPending}>
              再想想
            </Button>
            <Button onClick={handleConfirm} isLoading={mutation.isPending} disabled={mutation.isPending}>
              确认撤销
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
