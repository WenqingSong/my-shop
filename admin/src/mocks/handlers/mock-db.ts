/**
 * MSW 内存数据仓库。
 *
 * 用途：本地演示（`VITE_MOCK_ENABLED=true`）与 Vitest 夹具。
 * **不是生产数据**，也不会进入生产构建的业务路径。
 *
 * 设计约束：
 * - 结构与后端 DTO 保持一致（snake_case、价格整数分、状态枚举止）。
 * - 跨领域共享同一份状态：删除 SKU 时能真实复现"有库存记录 → 5005"。
 * - 每个测试文件在 `beforeEach` 调用 `resetMockDb()` 恢复初始夹具。
 */

export interface MockCategory {
  id: number;
  parent_id: number;
  name: string;
  sort: number;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface MockProduct {
  id: number;
  name: string;
  brand: string;
  category_id: number;
  price: number;
  main_image: string;
  detail: string;
  status: "draft" | "on_shelf" | "off_shelf";
  view_count: number;
  images: string[];
  created_at: string;
  updated_at: string;
}

export interface MockSku {
  id: number;
  product_id: number;
  name: string;
  price: number;
  status: "enabled" | "disabled";
  created_at: string;
  updated_at: string;
}

export interface MockInventory {
  sku_id: number;
  quantity: number;
  created_at: string;
  updated_at: string;
}

export interface MockInventoryLog {
  id: number;
  sku_id: number;
  change_type: "increase" | "deduct";
  change_qty: number;
  before_qty: number;
  after_qty: number;
  operator_admin_id: number | null;
  reason: string;
  created_at: string;
}

export interface MockOrderItem {
  id: number;
  order_id: number;
  sku_id: number;
  product_id: number;
  sku_name: string;
  product_name: string;
  product_main_image: string;
  price: number;
  quantity: number;
}

export interface MockOrder {
  id: number;
  order_no: string;
  user_id: number;
  status:
    | "pending_payment"
    | "paid"
    | "shipped"
    | "received"
    | "completed"
    | "cancelled"
    | "refunded";
  total_amount: number;
  original_amount: number;
  discount_amount: number;
  user_coupon_id: number | null;
  idempotency_key: string;
  recipient_name: string;
  phone: string;
  province: string;
  city: string;
  district: string;
  detail: string;
  address_id: number | null;
  expire_at: string | null;
  cancel_reason: 1 | 2 | null;
  paid_at: string | null;
  shipped_at: string | null;
  received_at: string | null;
  completed_at: string | null;
  cancelled_at: string | null;
  refunded_at: string | null;
  created_at: string;
  updated_at: string;
}

export type MockRefundStatus =
  | "applying"
  | "pending_refund"
  | "refund_failed"
  | "review_rejected"
  | "revoked"
  | "refund_succeeded"
  | "closed";

export interface MockRefund {
  id: number;
  order_id: number;
  user_id: number;
  status: MockRefundStatus;
  amount: number;
  reason: string;
  review_reason: string;
  reviewed_by: number | null;
  reviewed_at: string | null;
  execute_reason: string;
  executed_by: number | null;
  executed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface MockRefundAudit {
  id: number;
  refund_id: number;
  action:
    | "apply"
    | "review_approve"
    | "review_reject"
    | "execute_success"
    | "execute_failure"
    | "close"
    | "revoke";
  operator_type: 1 | 2;
  operator_id: number;
  operator_username: string;
  before_status: number;
  after_status: number;
  reason: string;
  created_at: string;
}

export interface MockCoupon {
  id: number;
  name: string;
  threshold_amount: number;
  discount_amount: number;
  total_quantity: number;
  issued_quantity: number;
  claim_start_time: string;
  claim_end_time: string;
  use_start_time: string;
  use_end_time: string;
  status: 0 | 1;
  created_at: string;
  updated_at: string;
}

export interface MockActivity {
  id: number;
  name: string;
  status: "enabled" | "disabled";
  start_time: string;
  end_time: string;
  created_at: string;
  updated_at: string;
}

export interface MockActivitySku {
  id: number;
  activity_id: number;
  sku_id: number;
  flash_price: number;
  total_stock: number;
  sold: number;
}

export interface MockBanner {
  id: number;
  title: string;
  image_url: string;
  link_url: string;
  sort: number;
  status: 0 | 1;
  created_at: string;
  updated_at: string;
}

export interface MockPosition {
  id: number;
  code: string;
  name: string;
  status: 0 | 1;
  created_at: string;
  updated_at: string;
}

export interface MockRecommendItem {
  id: number;
  position_id: number;
  product_id: number;
  sort: number;
  created_at: string;
  updated_at: string;
}

export interface MockArticle {
  id: number;
  author_id: number;
  author_username: string;
  title: string;
  content: string;
  created_at: string;
  updated_at: string;
}

/** 固定时间戳，保证测试断言稳定 */
const SEED_TIME = "2026-11-01 10:00:00";
const MOCK_IMAGE_HOST = "https://mock-upload.local/files";

function seedCategories(): MockCategory[] {
  return [
    {
      id: 11,
      parent_id: 0,
      name: "运动鞋",
      sort: 1,
      status: 1,
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 12,
      parent_id: 11,
      name: "跑步鞋",
      sort: 1,
      status: 1,
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 13,
      parent_id: 0,
      name: "服饰",
      sort: 2,
      status: 1,
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 14,
      parent_id: 13,
      name: "T恤",
      sort: 1,
      status: 1,
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 15,
      parent_id: 0,
      name: "配件",
      sort: 3,
      status: 1,
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    }
  ];
}

function seedProducts(): MockProduct[] {
  return [
    {
      id: 1001,
      name: "轻量跑步鞋",
      brand: "Surge",
      category_id: 12,
      price: 32900,
      main_image: `${MOCK_IMAGE_HOST}/seed-runner.png`,
      detail: "轻量缓震，适合日常 5-10 公里训练。",
      status: "on_shelf",
      view_count: 320,
      images: [`${MOCK_IMAGE_HOST}/seed-runner-2.png`],
      created_at: "2026-10-20 09:30:00",
      updated_at: "2026-11-02 11:20:00"
    },
    {
      id: 1002,
      name: "基础训练 T 恤",
      brand: "Surge",
      category_id: 14,
      price: 8900,
      main_image: "",
      detail: "速干面料，日常训练基础款。",
      status: "draft",
      view_count: 0,
      images: [],
      created_at: "2026-11-03 14:00:00",
      updated_at: "2026-11-03 14:00:00"
    },
    {
      id: 1003,
      name: "运动水壶 600ml",
      brand: "",
      category_id: 15,
      price: 4900,
      main_image: "",
      detail: "",
      status: "off_shelf",
      view_count: 88,
      images: [],
      created_at: "2026-10-11 08:00:00",
      updated_at: "2026-10-30 16:45:00"
    }
  ];
}

function seedSkus(): MockSku[] {
  return [
    {
      id: 2001,
      product_id: 1001,
      name: "黑 / 40",
      price: 32900,
      status: "enabled",
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 2002,
      product_id: 1001,
      name: "黑 / 41",
      price: 32900,
      status: "enabled",
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 2003,
      product_id: 1001,
      name: "白 / 40",
      price: 33900,
      status: "disabled",
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 2004,
      product_id: 1002,
      name: "默认规格",
      price: 8900,
      status: "enabled",
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    },
    {
      id: 2005,
      product_id: 1003,
      name: "默认规格",
      price: 4900,
      status: "disabled",
      created_at: SEED_TIME,
      updated_at: SEED_TIME
    }
  ];
}

function seedInventories(): Map<number, MockInventory> {
  // 2003 故意不建记录：验证"无记录 = 0"
  return new Map([
    [
      2001,
      {
        sku_id: 2001,
        quantity: 25,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      }
    ],
    [
      2002,
      {
        sku_id: 2002,
        quantity: 3,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      }
    ],
    [
      2004,
      {
        sku_id: 2004,
        quantity: 120,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      }
    ]
  ]);
}

function seedLogs(): MockInventoryLog[] {
  return [
    {
      id: 1,
      sku_id: 2001,
      change_type: "increase",
      change_qty: 30,
      before_qty: 0,
      after_qty: 30,
      operator_admin_id: 1,
      reason: "",
      created_at: "2026-10-21 10:00:00"
    },
    {
      id: 2,
      sku_id: 2001,
      change_type: "deduct",
      change_qty: 5,
      before_qty: 30,
      after_qty: 25,
      operator_admin_id: 1,
      reason: "",
      created_at: "2026-11-02 11:20:00"
    }
  ];
}

/**
 * 订单夹具。
 *
 * 覆盖全部 7 种订单状态，并刻意构造：
 * - 5006：paid 且存在 applying 退款 → 发货应返回 18005；
 * - 5007：paid 且存在 pending_refund 退款 → 可执行退款成功/失败；
 * - 5008：paid 且存在 refund_failed 退款 → 可重试成功或关闭。
 */
function seedOrders(): { orders: MockOrder[]; items: MockOrderItem[] } {
  const item = (
    id: number,
    orderId: number,
    skuId: number,
    productId: number,
    skuName: string,
    productName: string,
    image: string,
    price: number,
    quantity: number
  ): MockOrderItem => ({
    id,
    order_id: orderId,
    sku_id: skuId,
    product_id: productId,
    sku_name: skuName,
    product_name: productName,
    product_main_image: image,
    price,
    quantity
  });

  const runnerImage = `${MOCK_IMAGE_HOST}/seed-runner.png`;

  const items: MockOrderItem[] = [
    item(
      6001,
      5001,
      2001,
      1001,
      "黑 / 40",
      "轻量跑步鞋",
      runnerImage,
      32900,
      2
    ),
    item(6002, 5002, 2004, 1002, "默认规格", "基础训练 T 恤", "", 8900, 1),
    item(
      6003,
      5003,
      2002,
      1001,
      "黑 / 41",
      "轻量跑步鞋",
      runnerImage,
      32900,
      1
    ),
    item(6004, 5004, 2005, 1003, "默认规格", "运动水壶 600ml", "", 4900, 1),
    item(
      6005,
      5005,
      2001,
      1001,
      "黑 / 40",
      "轻量跑步鞋",
      runnerImage,
      32900,
      1
    ),
    item(
      6006,
      5006,
      2003,
      1001,
      "白 / 40",
      "轻量跑步鞋",
      runnerImage,
      33900,
      1
    ),
    item(
      6007,
      5007,
      2001,
      1001,
      "黑 / 40",
      "轻量跑步鞋",
      runnerImage,
      32900,
      1
    ),
    item(
      6008,
      5008,
      2002,
      1001,
      "黑 / 41",
      "轻量跑步鞋",
      runnerImage,
      32900,
      2
    ),
    item(6009, 5009, 2004, 1002, "默认规格", "基础训练 T 恤", "", 8900, 1),
    item(6010, 5010, 2001, 1001, "黑 / 40", "轻量跑步鞋", runnerImage, 32900, 1)
  ];

  const base = {
    user_coupon_id: null as number | null,
    address_id: 9001,
    province: "上海市",
    city: "上海市",
    district: "浦东新区",
    detail: "张江路 88 号 3 楼",
    expire_at: null as string | null,
    cancel_reason: null as 1 | 2 | null,
    paid_at: null as string | null,
    shipped_at: null as string | null,
    received_at: null as string | null,
    completed_at: null as string | null,
    cancelled_at: null as string | null,
    refunded_at: null as string | null
  };

  const orders: MockOrder[] = [
    {
      ...base,
      id: 5001,
      order_no: "SC202611010001",
      user_id: 101,
      status: "paid",
      total_amount: 65800,
      original_amount: 65800,
      discount_amount: 0,
      idempotency_key: "mock-idem-5001",
      recipient_name: "张三",
      phone: "13800000001",
      created_at: "2026-11-01 10:00:00",
      updated_at: "2026-11-01 10:05:00",
      paid_at: "2026-11-01 10:05:00",
      expire_at: "2026-11-01 10:15:00"
    },
    {
      ...base,
      id: 5002,
      order_no: "SC202611010002",
      user_id: 102,
      status: "pending_payment",
      total_amount: 8900,
      original_amount: 8900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5002",
      recipient_name: "李四",
      phone: "13800000002",
      created_at: "2026-11-01 11:00:00",
      updated_at: "2026-11-01 11:00:00",
      expire_at: "2026-11-01 11:15:00"
    },
    {
      ...base,
      id: 5003,
      order_no: "SC202611020003",
      user_id: 103,
      status: "shipped",
      total_amount: 32900,
      original_amount: 32900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5003",
      recipient_name: "王五",
      phone: "13800000003",
      created_at: "2026-11-02 09:00:00",
      updated_at: "2026-11-03 10:00:00",
      paid_at: "2026-11-02 09:10:00",
      shipped_at: "2026-11-03 10:00:00"
    },
    {
      ...base,
      id: 5004,
      order_no: "SC202611020004",
      user_id: 104,
      status: "refunded",
      total_amount: 4900,
      original_amount: 4900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5004",
      recipient_name: "赵六",
      phone: "13800000004",
      created_at: "2026-11-02 12:00:00",
      updated_at: "2026-11-04 09:00:00",
      paid_at: "2026-11-02 12:05:00",
      refunded_at: "2026-11-04 09:00:00"
    },
    {
      ...base,
      id: 5005,
      order_no: "SC202611030005",
      user_id: 101,
      status: "cancelled",
      total_amount: 32900,
      original_amount: 32900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5005",
      recipient_name: "张三",
      phone: "13800000001",
      created_at: "2026-11-03 08:00:00",
      updated_at: "2026-11-03 08:10:00",
      cancelled_at: "2026-11-03 08:10:00",
      cancel_reason: 1
    },
    {
      ...base,
      id: 5006,
      order_no: "SC202611040006",
      user_id: 105,
      status: "paid",
      total_amount: 33900,
      original_amount: 33900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5006",
      recipient_name: "孙七",
      phone: "13800000005",
      created_at: "2026-11-04 14:00:00",
      updated_at: "2026-11-09 09:00:00",
      paid_at: "2026-11-04 14:05:00"
    },
    {
      ...base,
      id: 5007,
      order_no: "SC202611050007",
      user_id: 106,
      status: "paid",
      total_amount: 32900,
      original_amount: 32900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5007",
      recipient_name: "周八",
      phone: "13800000006",
      created_at: "2026-11-05 15:00:00",
      updated_at: "2026-11-09 10:00:00",
      paid_at: "2026-11-05 15:05:00"
    },
    {
      ...base,
      id: 5008,
      order_no: "SC202611060008",
      user_id: 107,
      status: "paid",
      total_amount: 65800,
      original_amount: 65800,
      discount_amount: 0,
      idempotency_key: "mock-idem-5008",
      recipient_name: "吴九",
      phone: "13800000007",
      created_at: "2026-11-06 16:00:00",
      updated_at: "2026-11-09 11:00:00",
      paid_at: "2026-11-06 16:05:00"
    },
    {
      ...base,
      id: 5009,
      order_no: "SC202611070009",
      user_id: 108,
      status: "received",
      total_amount: 8900,
      original_amount: 8900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5009",
      recipient_name: "郑十",
      phone: "13800000008",
      created_at: "2026-11-07 17:00:00",
      updated_at: "2026-11-09 10:00:00",
      paid_at: "2026-11-07 17:05:00",
      shipped_at: "2026-11-08 09:00:00",
      received_at: "2026-11-09 10:00:00"
    },
    {
      ...base,
      id: 5010,
      order_no: "SC202611080010",
      user_id: 109,
      status: "completed",
      total_amount: 32900,
      original_amount: 32900,
      discount_amount: 0,
      idempotency_key: "mock-idem-5010",
      recipient_name: "钱十一",
      phone: "13800000009",
      created_at: "2026-11-08 18:00:00",
      updated_at: "2026-11-09 12:00:00",
      paid_at: "2026-11-08 18:05:00",
      shipped_at: "2026-11-09 08:00:00",
      received_at: "2026-11-09 11:00:00",
      completed_at: "2026-11-09 12:00:00"
    }
  ];

  return { orders, items };
}

function seedRefunds(): {
  refunds: MockRefund[];
  audits: MockRefundAudit[];
} {
  const refunds: MockRefund[] = [
    {
      id: 7001,
      order_id: 5006,
      user_id: 105,
      status: "applying",
      amount: 33900,
      reason: "尺码不合适，申请退款",
      review_reason: "",
      reviewed_by: null,
      reviewed_at: null,
      execute_reason: "",
      executed_by: null,
      executed_at: null,
      created_at: "2026-11-09 09:00:00",
      updated_at: "2026-11-09 09:00:00"
    },
    {
      id: 7002,
      order_id: 5007,
      user_id: 106,
      status: "pending_refund",
      amount: 32900,
      reason: "重复下单",
      review_reason: "同意退款",
      reviewed_by: 1,
      reviewed_at: "2026-11-09 10:00:00",
      execute_reason: "",
      executed_by: null,
      executed_at: null,
      created_at: "2026-11-09 09:30:00",
      updated_at: "2026-11-09 10:00:00"
    },
    {
      id: 7003,
      order_id: 5008,
      user_id: 107,
      status: "refund_failed",
      amount: 65800,
      reason: "商品质量问题",
      review_reason: "同意退款",
      reviewed_by: 1,
      reviewed_at: "2026-11-09 10:30:00",
      execute_reason: "支付渠道返回失败",
      executed_by: 1,
      executed_at: "2026-11-09 11:00:00",
      created_at: "2026-11-09 10:10:00",
      updated_at: "2026-11-09 11:00:00"
    },
    {
      id: 7004,
      order_id: 5004,
      user_id: 104,
      status: "refund_succeeded",
      amount: 4900,
      reason: "不想要了",
      review_reason: "同意退款",
      reviewed_by: 1,
      reviewed_at: "2026-11-03 09:00:00",
      execute_reason: "退款成功",
      executed_by: 1,
      executed_at: "2026-11-04 09:00:00",
      created_at: "2026-11-03 08:30:00",
      updated_at: "2026-11-04 09:00:00"
    }
  ];

  const audits: MockRefundAudit[] = [
    {
      id: 1001,
      refund_id: 7001,
      action: "apply",
      operator_type: 1,
      operator_id: 105,
      operator_username: "u105",
      before_status: 0,
      after_status: 10,
      reason: "尺码不合适，申请退款",
      created_at: "2026-11-09 09:00:00"
    },
    {
      id: 1002,
      refund_id: 7002,
      action: "apply",
      operator_type: 1,
      operator_id: 106,
      operator_username: "u106",
      before_status: 0,
      after_status: 10,
      reason: "重复下单",
      created_at: "2026-11-09 09:30:00"
    },
    {
      id: 1003,
      refund_id: 7002,
      action: "review_approve",
      operator_type: 2,
      operator_id: 1,
      operator_username: "admin",
      before_status: 10,
      after_status: 20,
      reason: "同意退款",
      created_at: "2026-11-09 10:00:00"
    },
    {
      id: 1004,
      refund_id: 7003,
      action: "apply",
      operator_type: 1,
      operator_id: 107,
      operator_username: "u107",
      before_status: 0,
      after_status: 10,
      reason: "商品质量问题",
      created_at: "2026-11-09 10:10:00"
    },
    {
      id: 1005,
      refund_id: 7003,
      action: "review_approve",
      operator_type: 2,
      operator_id: 1,
      operator_username: "admin",
      before_status: 10,
      after_status: 20,
      reason: "同意退款",
      created_at: "2026-11-09 10:30:00"
    },
    {
      id: 1006,
      refund_id: 7003,
      action: "execute_failure",
      operator_type: 2,
      operator_id: 1,
      operator_username: "admin",
      before_status: 20,
      after_status: 30,
      reason: "支付渠道返回失败",
      created_at: "2026-11-09 11:00:00"
    },
    {
      id: 1007,
      refund_id: 7004,
      action: "apply",
      operator_type: 1,
      operator_id: 104,
      operator_username: "u104",
      before_status: 0,
      after_status: 10,
      reason: "不想要了",
      created_at: "2026-11-03 08:30:00"
    },
    {
      id: 1008,
      refund_id: 7004,
      action: "review_approve",
      operator_type: 2,
      operator_id: 1,
      operator_username: "admin",
      before_status: 10,
      after_status: 20,
      reason: "同意退款",
      created_at: "2026-11-03 09:00:00"
    },
    {
      id: 1009,
      refund_id: 7004,
      action: "execute_success",
      operator_type: 2,
      operator_id: 1,
      operator_username: "admin",
      before_status: 20,
      after_status: 60,
      reason: "退款成功",
      created_at: "2026-11-04 09:00:00"
    }
  ];

  return { refunds, audits };
}

function seedCoupons(): MockCoupon[] {
  return [
    {
      id: 8001,
      name: "新人满减券",
      threshold_amount: 10000,
      discount_amount: 2000,
      total_quantity: 1000,
      issued_quantity: 120,
      claim_start_time: "2026-11-01 00:00:00",
      claim_end_time: "2026-12-31 23:59:59",
      use_start_time: "2026-11-01 00:00:00",
      use_end_time: "2027-01-31 23:59:59",
      status: 1,
      created_at: "2026-10-25 10:00:00",
      updated_at: "2026-10-25 10:00:00"
    },
    {
      id: 8002,
      name: "无门槛 5 元券",
      threshold_amount: 0,
      discount_amount: 500,
      total_quantity: 200,
      issued_quantity: 200,
      claim_start_time: "2026-11-01 00:00:00",
      claim_end_time: "2026-11-30 23:59:59",
      use_start_time: "2026-11-01 00:00:00",
      use_end_time: "2026-12-15 23:59:59",
      status: 1,
      created_at: "2026-10-26 11:00:00",
      updated_at: "2026-10-26 11:00:00"
    },
    {
      id: 8003,
      name: "双十一大额券",
      threshold_amount: 50000,
      discount_amount: 10000,
      total_quantity: 500,
      issued_quantity: 50,
      claim_start_time: "2026-11-01 00:00:00",
      claim_end_time: "2026-11-11 23:59:59",
      use_start_time: "2026-11-11 00:00:00",
      use_end_time: "2026-11-20 23:59:59",
      status: 0,
      created_at: "2026-10-28 09:00:00",
      updated_at: "2026-11-02 09:30:00"
    }
  ];
}

function seedActivities(): {
  activities: MockActivity[];
  bindings: MockActivitySku[];
} {
  return {
    activities: [
      {
        id: 8101,
        name: "周末闪购",
        status: "enabled",
        start_time: "2026-11-15 10:00:00",
        end_time: "2026-11-15 12:00:00",
        created_at: "2026-11-05 09:00:00",
        updated_at: "2026-11-05 09:00:00"
      }
    ],
    bindings: [
      {
        id: 9101,
        activity_id: 8101,
        sku_id: 2001,
        flash_price: 19900,
        total_stock: 50,
        sold: 12
      },
      {
        id: 9102,
        activity_id: 8101,
        sku_id: 2002,
        flash_price: 20900,
        total_stock: 30,
        sold: 5
      }
    ]
  };
}

function seedBanners(): MockBanner[] {
  return [
    {
      id: 8201,
      title: "双十一主会场",
      image_url: `${MOCK_IMAGE_HOST}/banner-1.png`,
      link_url: "/products/1001",
      sort: 1,
      status: 1,
      created_at: "2026-10-20 10:00:00",
      updated_at: "2026-10-20 10:00:00"
    },
    {
      id: 8202,
      title: "新人专享",
      image_url: `${MOCK_IMAGE_HOST}/banner-2.png`,
      link_url: "",
      sort: 2,
      status: 1,
      created_at: "2026-10-21 10:00:00",
      updated_at: "2026-10-21 10:00:00"
    },
    {
      id: 8203,
      title: "旧活动横幅",
      image_url: `${MOCK_IMAGE_HOST}/banner-3.png`,
      link_url: "/articles/1",
      sort: 3,
      status: 0,
      created_at: "2026-09-01 10:00:00",
      updated_at: "2026-09-10 10:00:00"
    }
  ];
}

function seedRecommendations(): {
  positions: MockPosition[];
  items: MockRecommendItem[];
} {
  return {
    positions: [
      {
        id: 8301,
        code: "home-top",
        name: "首页顶部推荐",
        status: 1,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      },
      {
        id: 8302,
        code: "home-side",
        name: "首页侧栏推荐",
        status: 0,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      }
    ],
    items: [
      {
        id: 9201,
        position_id: 8301,
        product_id: 1001,
        sort: 0,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      },
      {
        id: 9202,
        position_id: 8301,
        product_id: 1002,
        sort: 1,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      },
      {
        id: 9203,
        position_id: 8302,
        product_id: 1003,
        sort: 0,
        created_at: SEED_TIME,
        updated_at: SEED_TIME
      }
    ]
  };
}

function seedArticles(): MockArticle[] {
  return [
    {
      id: 8401,
      author_id: 101,
      author_username: "u101",
      title: "SurgeCart 选购指南",
      content:
        "第一步：确认使用场景。\n第二步：选择合适尺码与配色。\n第三步：关注活动期间的优惠券。",
      created_at: "2026-10-20 09:00:00",
      updated_at: "2026-10-20 09:00:00"
    },
    {
      id: 8402,
      author_id: 102,
      author_username: "u102",
      title: "跑步鞋保养技巧",
      content: "避免暴晒，定期清洗鞋面，保持鞋内干燥。",
      created_at: "2026-10-25 14:00:00",
      updated_at: "2026-10-25 14:00:00"
    },
    {
      id: 8403,
      author_id: 101,
      author_username: "u101",
      title: "双十一活动说明",
      content: "活动时间以商品详情页为准，优惠券数量有限，先到先得。",
      created_at: "2026-11-01 08:00:00",
      updated_at: "2026-11-02 10:00:00"
    },
    {
      id: 8404,
      author_id: 103,
      author_username: "u103",
      title: "如何选择运动水壶",
      content: "容量、材质与密封性是三个关键指标。",
      created_at: "2026-11-05 16:00:00",
      updated_at: "2026-11-05 16:00:00"
    }
  ];
}

interface MockDb {
  categories: MockCategory[];
  products: MockProduct[];
  skus: MockSku[];
  inventories: Map<number, MockInventory>;
  logs: MockInventoryLog[];
  orders: MockOrder[];
  orderItems: MockOrderItem[];
  refunds: MockRefund[];
  refundAudits: MockRefundAudit[];
  coupons: MockCoupon[];
  activities: MockActivity[];
  activitySkus: MockActivitySku[];
  banners: MockBanner[];
  positions: MockPosition[];
  recommendItems: MockRecommendItem[];
  articles: MockArticle[];
  nextCategoryId: number;
  nextProductId: number;
  nextSkuId: number;
  nextLogId: number;
  nextRefundAuditId: number;
  nextCouponId: number;
  nextActivityId: number;
  nextActivitySkuId: number;
  nextBannerId: number;
  nextPositionId: number;
  nextRecommendItemId: number;
  reset: () => void;
  now: () => string;
}

export const mockDb: MockDb = {
  categories: [],
  products: [],
  skus: [],
  inventories: new Map(),
  logs: [],
  orders: [],
  orderItems: [],
  refunds: [],
  refundAudits: [],
  coupons: [],
  activities: [],
  activitySkus: [],
  banners: [],
  positions: [],
  recommendItems: [],
  articles: [],
  nextCategoryId: 1,
  nextProductId: 1,
  nextSkuId: 1,
  nextLogId: 1,
  nextRefundAuditId: 1,
  nextCouponId: 1,
  nextActivityId: 1,
  nextActivitySkuId: 1,
  nextBannerId: 1,
  nextPositionId: 1,
  nextRecommendItemId: 1,
  reset() {
    this.categories = seedCategories();
    this.products = seedProducts();
    this.skus = seedSkus();
    this.inventories = seedInventories();
    this.logs = seedLogs();

    const { orders, items } = seedOrders();
    this.orders = orders;
    this.orderItems = items;

    const { refunds, audits } = seedRefunds();
    this.refunds = refunds;
    this.refundAudits = audits;

    this.coupons = seedCoupons();
    const activitySeed = seedActivities();
    this.activities = activitySeed.activities;
    this.activitySkus = activitySeed.bindings;
    this.banners = seedBanners();
    const recommendSeed = seedRecommendations();
    this.positions = recommendSeed.positions;
    this.recommendItems = recommendSeed.items;
    this.articles = seedArticles();

    this.nextCategoryId = 100;
    this.nextProductId = 3001;
    this.nextSkuId = 4001;
    this.nextLogId = 100;
    this.nextRefundAuditId = 2000;
    this.nextCouponId = 8500;
    this.nextActivityId = 8600;
    this.nextActivitySkuId = 8700;
    this.nextBannerId = 8800;
    this.nextPositionId = 8900;
    this.nextRecommendItemId = 9300;
  },
  now() {
    // 固定"当前时间"，避免测试断言依赖真实时钟
    return "2026-11-10 12:00:00";
  }
};

/** 测试与演示重置入口 */
export function resetMockDb() {
  mockDb.reset();
}

mockDb.reset();

/** 图片占位服务地址（MSW 会拦截并返回 SVG） */
export const MOCK_IMAGE_PREFIX = MOCK_IMAGE_HOST;
