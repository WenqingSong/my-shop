import { describe, expect, it } from 'vitest'

import type { Order, OrderStatus } from '@/api/orders'
import {
  ORDER_STATUS_GROUPS,
  ORDER_STATUS_LABELS,
  buildOrderProgress,
  describeCancelReason,
  describeOrderStatus,
  describeOrderStatusTone,
  filterOrdersByGroup,
  formatOrderAddress,
  formatOrderTime,
  orderTotalQuantity,
} from '@/utils/orders'

const ALL_STATUSES: OrderStatus[] = [
  'pending_payment',
  'paid',
  'shipped',
  'received',
  'completed',
  'cancelled',
  'refunded',
]

function makeOrder(overrides: Partial<Order> = {}): Order {
  return {
    id: 1,
    order_no: '1794298503424e96d154a5788',
    user_id: 1,
    status: 'pending_payment',
    total_amount: 1_998,
    original_amount: 1_998,
    discount_amount: 0,
    user_coupon_id: null,
    idempotency_key: 'key-1',
    recipient_name: '张三',
    phone: '13800138000',
    province: '广东省',
    city: '深圳市',
    district: '南山区',
    detail: '科技园路 1 号',
    address_id: 1,
    expire_at: '2026-11-10 16:30:00',
    cancel_reason: null,
    paid_at: null,
    shipped_at: null,
    received_at: null,
    completed_at: null,
    cancelled_at: null,
    refunded_at: null,
    created_at: '2026-11-10 15:15:00',
    updated_at: '2026-11-10 15:15:00',
    items: [
      {
        id: 1,
        sku_id: 9,
        product_id: 3,
        sku_name: '默认规格',
        product_name: '测试商品',
        product_main_image: '',
        price: 999,
        quantity: 2,
      },
    ],
    ...overrides,
  }
}

describe('订单状态分组（纯前端分组，不是后端状态）', () => {
  it('7 个后端状态都被且仅被一个分组覆盖', () => {
    for (const status of ALL_STATUSES) {
      const matched = ORDER_STATUS_GROUPS.filter(
        (group) => group.statuses.length > 0 && group.statuses.includes(status),
      )

      expect(matched, `状态 ${status} 应只属于一个分组`).toHaveLength(1)
    }
  })

  it('分组顺序与页面标签固定为 全部/待支付/待发货/待收货/已完成/已关闭', () => {
    expect(ORDER_STATUS_GROUPS.map((group) => group.label)).toEqual([
      '全部',
      '待支付',
      '待发货',
      '待收货',
      '已完成',
      '已关闭',
    ])
  })

  it('已关闭分组同时覆盖 cancelled 与 refunded', () => {
    const orders = [
      makeOrder({ id: 1, status: 'cancelled' }),
      makeOrder({ id: 2, status: 'refunded' }),
      makeOrder({ id: 3, status: 'paid' }),
    ]

    expect(filterOrdersByGroup(orders, 'closed').map((order) => order.id)).toEqual([1, 2])
  })

  it('待发货只覆盖 paid，待收货只覆盖 shipped', () => {
    const orders = [
      makeOrder({ id: 1, status: 'paid' }),
      makeOrder({ id: 2, status: 'shipped' }),
      makeOrder({ id: 3, status: 'pending_payment' }),
    ]

    expect(filterOrdersByGroup(orders, 'pending_shipment').map((order) => order.id)).toEqual([1])
    expect(filterOrdersByGroup(orders, 'pending_receipt').map((order) => order.id)).toEqual([2])
  })

  it('全部不过滤且返回副本（不修改入参）', () => {
    const orders = [makeOrder({ id: 1 }), makeOrder({ id: 2, status: 'completed' })]
    const all = filterOrdersByGroup(orders, 'all')

    expect(all).toHaveLength(2)
    expect(all).not.toBe(orders)
  })
})

describe('订单状态文案与色调', () => {
  it('7 个状态都有中文文案', () => {
    for (const status of ALL_STATUSES) {
      expect(ORDER_STATUS_LABELS[status]).toBeTruthy()
      expect(describeOrderStatus(status)).toBe(ORDER_STATUS_LABELS[status])
    }
  })

  it('未知状态原样返回，不抛出', () => {
    expect(describeOrderStatus('unknown_status')).toBe('unknown_status')
  })

  it('色调映射：待支付 warning、已完成 success、已退款 danger、已取消 neutral', () => {
    expect(describeOrderStatusTone('pending_payment')).toBe('warning')
    expect(describeOrderStatusTone('paid')).toBe('primary')
    expect(describeOrderStatusTone('completed')).toBe('success')
    expect(describeOrderStatusTone('refunded')).toBe('danger')
    expect(describeOrderStatusTone('cancelled')).toBe('neutral')
  })
})

describe('订单生命周期进度（由当前状态推导）', () => {
  it('待支付：第一步为当前，其余待办', () => {
    const progress = buildOrderProgress('pending_payment')

    expect(progress.halted).toBe(false)
    expect(progress.steps.map((step) => step.state)).toEqual(['current', 'todo', 'todo', 'todo'])
  })

  it('已支付 / 已发货：逐步推进', () => {
    expect(buildOrderProgress('paid').steps.map((step) => step.state)).toEqual([
      'done',
      'current',
      'todo',
      'todo',
    ])
    expect(buildOrderProgress('shipped').steps.map((step) => step.state)).toEqual([
      'done',
      'done',
      'current',
      'todo',
    ])
  })

  it('已完成与已收货：全部完成', () => {
    expect(buildOrderProgress('completed').steps.map((step) => step.state)).toEqual([
      'done',
      'done',
      'done',
      'done',
    ])
    expect(buildOrderProgress('received').steps.map((step) => step.state)).toEqual([
      'done',
      'done',
      'done',
      'done',
    ])
  })

  it('已取消：全部待办并标记终止', () => {
    const progress = buildOrderProgress('cancelled')

    expect(progress.halted).toBe(true)
    expect(progress.haltedLabel).toBe('订单已取消，交易终止')
    expect(progress.steps.every((step) => step.state === 'todo')).toBe(true)
  })

  it('已退款：支付已完成、履约在发货前终止', () => {
    const progress = buildOrderProgress('refunded')

    expect(progress.halted).toBe(true)
    expect(progress.steps.map((step) => step.state)).toEqual([
      'done',
      'current',
      'todo',
      'todo',
    ])
  })

  it('步骤标签固定为 待支付/待发货/待收货/已完成', () => {
    expect(buildOrderProgress('paid').steps.map((step) => step.label)).toEqual([
      '待支付',
      '待发货',
      '待收货',
      '已完成',
    ])
  })
})

describe('订单展示格式化', () => {
  it('取消原因按后端枚举映射', () => {
    expect(describeCancelReason(1)).toBe('用户主动取消')
    expect(describeCancelReason(2)).toBe('超时未支付自动取消')
    expect(describeCancelReason(null)).toBe('—')
  })

  it('地址使用订单快照拼接，不依赖地址簿', () => {
    const order = makeOrder({
      province: '浙江省',
      city: '杭州市',
      district: '西湖区',
      detail: '文三路 9 号',
    })

    expect(formatOrderAddress(order)).toBe('浙江省杭州市西湖区文三路 9 号')
  })

  it('商品件数为订单项数量之和', () => {
    const order = makeOrder({
      items: [
        {
          id: 1,
          sku_id: 1,
          product_id: 1,
          sku_name: 'A',
          product_name: 'A',
          product_main_image: '',
          price: 100,
          quantity: 2,
        },
        {
          id: 2,
          sku_id: 2,
          product_id: 2,
          sku_name: 'B',
          product_name: 'B',
          product_main_image: '',
          price: 200,
          quantity: 3,
        },
      ],
    })

    expect(orderTotalQuantity(order)).toBe(5)
  })

  it('时间为 null 时展示占位符，不伪造时间', () => {
    expect(formatOrderTime(null)).toBe('—')
    expect(formatOrderTime('2026-11-10 15:15:00')).toBe('2026-11-10 15:15:00')
  })
})
