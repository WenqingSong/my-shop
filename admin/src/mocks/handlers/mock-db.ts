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

interface MockDb {
  categories: MockCategory[];
  products: MockProduct[];
  skus: MockSku[];
  inventories: Map<number, MockInventory>;
  logs: MockInventoryLog[];
  nextCategoryId: number;
  nextProductId: number;
  nextSkuId: number;
  nextLogId: number;
  reset: () => void;
  now: () => string;
}

export const mockDb: MockDb = {
  categories: [],
  products: [],
  skus: [],
  inventories: new Map(),
  logs: [],
  nextCategoryId: 1,
  nextProductId: 1,
  nextSkuId: 1,
  nextLogId: 1,
  reset() {
    this.categories = seedCategories();
    this.products = seedProducts();
    this.skus = seedSkus();
    this.inventories = seedInventories();
    this.logs = seedLogs();
    this.nextCategoryId = 100;
    this.nextProductId = 3001;
    this.nextSkuId = 4001;
    this.nextLogId = 100;
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
