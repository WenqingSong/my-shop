import { AlertTriangle, CheckCircle2, CreditCard, PackageCheck, XCircle } from 'lucide-react'
import { useState } from 'react'

import type { Order } from '@/api/orders'
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
import { useOrderAction, type OrderActionKind } from '@/hooks/use-orders'
import { describeOrderActionError } from '@/pages/account/order-action-errors'
import { cn } from '@/utils/cn'
import { describeOrderStatus } from '@/utils/orders'

/**
 * 订单操作区（W4-02）。
 *
 * 允许的操作严格来自后端状态闸门，不做任何前端扩展：
 * - `POST /orders/:id/pay`      仅 pending_payment(10)
 * - `POST /orders/:id/cancel`   仅 pending_payment(10)
 * - `POST /orders/:id/receive`  仅 shipped(30)
 *
 * 交付边界：
 * - 支付是**项目内置的模拟支付**，没有第三方 SDK、没有支付流水、不产生真实扣款，
 *   因此界面上不出现任何真实支付品牌，并且必须显式告知用户这一点；
 * - 不实现退款（W4-03）；
 * - 所有状态变更都以服务端响应为准，前端不做乐观更新。
 */
export interface OrderActionsProps {
  order: Order
}

interface Feedback {
  tone: 'success' | 'error'
  text: string
}

const SUCCESS_TEXT: Record<OrderActionKind, string> = {
  pay: '模拟支付已完成，订单状态已更新为「已支付」。',
  cancel: '订单已取消，服务端已在同一事务内恢复库存并释放已占用的优惠券（如适用）。',
  receive: '已确认收货，订单已完成。',
}

const DIALOG_TITLE: Record<OrderActionKind, string> = {
  pay: '模拟支付确认',
  cancel: '取消订单确认',
  receive: '确认收货',
}

/** 当前状态下给用户的一句话说明（不替代状态徽标）。 */
function describeActionsHint(status: Order['status']): string {
  switch (status) {
    case 'pending_payment':
      return '可进行模拟支付或取消订单。支付为项目内置模拟能力，不对接任何支付渠道。'
    case 'paid':
      return '订单已支付，等待商家发货。未发货前可以在「退款」区域申请整单退款；发货后可在本页确认收货。'
    case 'shipped':
      return '商家已发货。收到商品后可在本页确认收货。'
    case 'received':
    case 'completed':
      return '订单已完成，无需继续操作。'
    case 'cancelled':
      return '订单已取消（用户取消或超时未支付自动取消），不可继续操作。'
    case 'refunded':
      return '订单已退款，不可继续操作。'
    default:
      return '当前状态暂无可执行的操作。'
  }
}

function actionButtonLabel(action: OrderActionKind): string {
  if (action === 'pay') {
    return '模拟支付'
  }

  if (action === 'cancel') {
    return '取消订单'
  }

  return '确认收货'
}

export function OrderActions({ order }: OrderActionsProps) {
  const mutation = useOrderAction(order.id)
  const [dialog, setDialog] = useState<OrderActionKind | null>(null)
  const [feedback, setFeedback] = useState<Feedback | null>(null)

  const canPay = order.status === 'pending_payment'
  const canCancel = order.status === 'pending_payment'
  const canReceive = order.status === 'shipped'
  const hasActions = canPay || canCancel || canReceive

  const pendingAction = mutation.isPending ? mutation.variables : undefined

  const runAction = (action: OrderActionKind) => {
    setFeedback(null)
    mutation.mutate(action, {
      onSuccess: () => {
        setFeedback({ tone: 'success', text: SUCCESS_TEXT[action] })
      },
      onError: (error) => {
        setFeedback({ tone: 'error', text: describeOrderActionError(error, action) })
      },
      onSettled: () => {
        // 无论成功失败都关闭弹层：失败时页面已经通过缓存失效回到服务端真实状态，
        // 继续停留在确认框里只会让用户重复点击同一个已失效的操作。
        setDialog(null)
      },
    })
  }

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      // 请求在途时不允许关闭，避免用户以为操作被取消而重复发起。
      if (mutation.isPending) {
        return
      }

      setDialog(null)
    }
  }

  return (
    <section
      aria-labelledby="order-actions-heading"
      className="rounded-xl border border-border bg-surface p-4 shadow-card sm:p-5"
    >
      <h3 id="order-actions-heading" className="text-sm font-semibold text-foreground">
        订单操作
      </h3>

      <p className="mt-2 text-sm leading-6 text-muted-foreground">
        当前状态：{describeOrderStatus(order.status)}。{describeActionsHint(order.status)}
      </p>

      {hasActions ? (
        <div className="mt-4 flex flex-wrap gap-3">
          {canPay ? (
            <Button
              onClick={() => setDialog('pay')}
              disabled={mutation.isPending}
              isLoading={pendingAction === 'pay'}
            >
              <CreditCard aria-hidden="true" />
              {actionButtonLabel('pay')}
            </Button>
          ) : null}

          {canCancel ? (
            <Button
              variant="outline"
              onClick={() => setDialog('cancel')}
              disabled={mutation.isPending}
              isLoading={pendingAction === 'cancel'}
              className="text-danger hover:border-danger/30 hover:bg-danger-soft hover:text-danger"
            >
              <XCircle aria-hidden="true" />
              {actionButtonLabel('cancel')}
            </Button>
          ) : null}

          {canReceive ? (
            <Button
              onClick={() => setDialog('receive')}
              disabled={mutation.isPending}
              isLoading={pendingAction === 'receive'}
            >
              <PackageCheck aria-hidden="true" />
              {actionButtonLabel('receive')}
            </Button>
          ) : null}
        </div>
      ) : null}

      {feedback ? (
        <div
          role={feedback.tone === 'error' ? 'alert' : 'status'}
          className={cn(
            'mt-4 flex items-start gap-2 rounded-lg border px-3 py-2 text-sm',
            feedback.tone === 'error'
              ? 'border-danger/20 bg-danger-soft text-danger'
              : 'border-success/20 bg-success-soft text-success',
          )}
        >
          {feedback.tone === 'error' ? (
            <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          ) : (
            <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          )}
          {feedback.text}
        </div>
      ) : null}

      <Dialog open={dialog !== null} onOpenChange={handleOpenChange}>
        <DialogContent showCloseButton={!mutation.isPending}>
          <DialogHeader>
            <DialogTitle>{dialog ? DIALOG_TITLE[dialog] : ''}</DialogTitle>
            <DialogDescription>
              {dialog === 'pay'
                ? '确认后由服务端把订单状态从「待支付」推进到「已支付」。'
                : dialog === 'cancel'
                  ? '确认后订单会被关闭，且该操作不可撤销。'
                  : '确认后订单会直接完成，且该操作不可撤销。'}
            </DialogDescription>
          </DialogHeader>

          {dialog === 'pay' ? (
            <div className="flex flex-col gap-3">
              <div className="flex items-start gap-2 rounded-lg border border-warning/20 bg-warning-soft px-3 py-2 text-sm leading-6 text-warning">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>
                  当前为项目模拟支付，不会产生真实资金扣款；本页面不对接任何第三方支付渠道，
                  也没有支付流水与回调。
                </span>
              </div>
              <dl className="flex flex-col gap-2 text-sm">
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-muted-foreground">订单编号</dt>
                  <dd className="break-all font-mono text-[13px] text-foreground">
                    {order.order_no}
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <dt className="text-muted-foreground">应付金额</dt>
                  <dd>
                    <PriceDisplay value={order.total_amount} size="md" />
                  </dd>
                </div>
              </dl>
            </div>
          ) : null}

          {dialog === 'cancel' ? (
            <ul className="flex flex-col gap-1.5 rounded-lg border border-border bg-gray-50 px-3 py-2 text-sm leading-6 text-muted-foreground">
              <li>· 订单状态将变为「已取消」，不可恢复；</li>
              <li>· 服务端会在同一事务内恢复该订单占用的库存；</li>
              <li>· 若下单时使用了优惠券，券会被释放回可用状态。</li>
            </ul>
          ) : null}

          {dialog === 'receive' ? (
            <div className="rounded-lg border border-border bg-gray-50 px-3 py-2 text-sm leading-6 text-muted-foreground">
              请确认已实际收到全部商品。确认后订单状态将变为「已完成」，不可撤销。
            </div>
          ) : null}

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setDialog(null)}
              disabled={mutation.isPending}
            >
              再想想
            </Button>
            <Button
              onClick={() => {
                if (dialog) {
                  runAction(dialog)
                }
              }}
              isLoading={mutation.isPending}
              disabled={mutation.isPending}
            >
              {dialog === 'pay'
                ? '确认模拟支付'
                : dialog === 'cancel'
                  ? '确认取消订单'
                  : '确认收货'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
