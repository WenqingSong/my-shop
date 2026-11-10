/**
 * 库存 Adapter。
 *
 * 硬性边界（来自后端实现）：
 * - 后端只提供"增量增加 / 条件扣减"，没有"设置库存总量"的接口；
 * - 后端不提供锁定库存、可售库存字段，前端**不推导**；
 * - 增减请求没有 reason，流水 reason 恒为空，前端不能假装已保存原因。
 */
import type {
  InventoryAdjustInput,
  InventoryAdjustType,
  InventoryDTO,
  InventoryLogDTO
} from "../types/inventory";

/** 调整类型选项（与后端枚举一致） */
export const INVENTORY_CHANGE_TYPE_OPTIONS: Array<{
  value: InventoryAdjustType;
  label: string;
}> = [
  { value: "increase", label: "增加库存" },
  { value: "deduct", label: "扣减库存" }
];

/** 单次调整允许的最大数量（防止明显误操作；后端无显式上限） */
export const INVENTORY_ADJUST_MAX = 1_000_000;

/** 变更类型文案 */
export function formatChangeType(type: string): string {
  if (type === "increase") return "增加";
  if (type === "deduct") return "扣减";
  return `未知类型（${type}）`;
}

/** 库存视图模型 */
export interface InventoryView {
  skuId: number;
  /** 当前库存；无记录时后端返回 0 */
  quantity: number;
  /** 后端是否存在库存记录（created_at 为 null 表示无记录） */
  hasRecord: boolean;
  createdAt: string | null;
  updatedAt: string | null;
}

/** DTO → 视图模型 */
export function toInventoryView(dto: InventoryDTO): InventoryView {
  return {
    skuId: dto.sku_id,
    quantity: dto.quantity,
    hasRecord: dto.created_at !== null && dto.created_at !== undefined,
    createdAt: dto.created_at,
    updatedAt: dto.updated_at
  };
}

/** 库存流水视图模型 */
export interface InventoryLogView {
  id: number;
  skuId: number;
  changeType: InventoryAdjustType;
  changeTypeLabel: string;
  /** 展示用符号：增加 `+` / 扣减 `-` */
  direction: "+" | "-";
  changeQty: number;
  beforeQty: number;
  afterQty: number;
  /** 例：`+20` / `-5` */
  deltaText: string;
  reason: string;
  /** 后端未记录原因时的展示值 */
  reasonText: string;
  /** 后端只返回管理员 id，没有用户名 */
  operatorText: string;
  createdAt: string | null;
}

/** 流水 DTO → 视图模型 */
export function toInventoryLogView(dto: InventoryLogDTO): InventoryLogView {
  const direction = dto.change_type === "deduct" ? "-" : "+";
  const reason = (dto.reason ?? "").trim();

  return {
    id: dto.id,
    skuId: dto.sku_id,
    changeType: dto.change_type,
    changeTypeLabel: formatChangeType(dto.change_type),
    direction,
    changeQty: dto.change_qty,
    beforeQty: dto.before_qty,
    afterQty: dto.after_qty,
    deltaText: `${direction}${dto.change_qty}`,
    reason,
    reasonText: reason || "—",
    operatorText:
      dto.operator_admin_id === null || dto.operator_admin_id === undefined
        ? "—"
        : `管理员 #${dto.operator_admin_id}`,
    createdAt: dto.created_at
  };
}

/**
 * 校验调整数量。
 *
 * 只做"必须是正整数"这一条前端能确定的事：
 * 不校验是否超过当前库存（后端 6001 才是权威，也能避免展示值过期导致误拦截）。
 *
 * @returns 错误文案；通过时返回 `null`
 */
export function validateAdjustQuantity(value: unknown): string | null {
  if (value === null || value === undefined || value === "") {
    return "请输入调整数量";
  }

  const num = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(num)) return "调整数量必须是数字";
  if (!Number.isInteger(num)) return "调整数量必须是正整数";
  if (num <= 0) return "调整数量必须大于 0";
  if (num > INVENTORY_ADJUST_MAX) {
    return `单次调整数量不能超过 ${INVENTORY_ADJUST_MAX}`;
  }

  return null;
}

/** 调整请求（真正发给后端的部分） */
export interface AdjustmentRequest {
  type: InventoryAdjustType;
  /** 路径参数 */
  skuId: number;
  /** 请求体，只包含 quantity */
  quantity: number;
}

/**
 * 前端输入 → 后端请求。
 *
 * `reason` 被放进 `provisional` 而不是请求体：后端不接收该字段，
 * 调用方必须把 `reasonPersisted = false` 告诉用户，不能静默丢弃。
 */
export function buildAdjustmentRequest(input: InventoryAdjustInput): {
  request: AdjustmentRequest;
  provisional: { reason: string; reasonPersisted: false };
} {
  const reason = (input.reason ?? "").trim();

  return {
    request: {
      type: input.type,
      skuId: input.skuId,
      quantity: input.quantity
    },
    provisional: { reason, reasonPersisted: false }
  };
}
