/**
 * 库存领域 DTO。
 *
 * 依据：`api/inventory/v1/inventory.go`。
 *
 * 已确认的后端事实：
 * - **没有**库存列表 / 全量库存接口，只能按 `sku_id` 查询单个 SKU。
 * - `quantity` 是当前库存；后端**不提供**锁定库存、可售库存字段，前端不得推导。
 * - 增减库存只有 `increase` / `deduct` 两个接口，语义是"增量"，
 *   不存在"直接覆盖库存总量"的接口。
 * - 增减请求体**只有 quantity**，没有 reason；流水里的 `reason` 后端恒写空字符串。
 * - 流水只读，无修改/删除接口。
 */

/** 变更类型（后端字符串枚举，DB 存 1/2） */
export type InventoryChangeType = "increase" | "deduct";

/** 库存 DTO */
export interface InventoryDTO {
  sku_id: number;
  /** 当前库存数量；无记录时后端返回 0 */
  quantity: number;
  /** 无库存记录时后端返回 null */
  created_at: string | null;
  updated_at: string | null;
}

/** 库存流水 DTO */
export interface InventoryLogDTO {
  id: number;
  sku_id: number;
  change_type: InventoryChangeType;
  /** 变更量（正数，方向由 change_type 决定） */
  change_qty: number;
  before_qty: number;
  after_qty: number;
  /** 操作者管理员 id；后端不返回用户名 */
  operator_admin_id: number | null;
  /** 后端当前恒为空字符串（请求未提供 reason） */
  reason: string;
  created_at: string | null;
}

/** 库存流水查询参数 */
export interface InventoryLogListQuery {
  page?: number;
  size?: number;
}

/** 库存流水响应 */
export interface InventoryLogListResult {
  items: InventoryLogDTO[];
  total: number;
  page: number;
  size: number;
}

/** 库存调整类型（与后端变更类型一致，不使用"覆盖"语义） */
export type InventoryAdjustType = InventoryChangeType;

/**
 * 前端库存调整输入。
 *
 * `reason` 仅用于操作确认与本地审计提示：后端接口不接收该字段，
 * 调用方必须显式提示"原因不会被保存"，不能静默丢弃。
 */
export interface InventoryAdjustInput {
  skuId: number;
  type: InventoryAdjustType;
  quantity: number;
  reason?: string;
}
