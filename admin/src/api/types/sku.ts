/**
 * SKU 领域 DTO。
 *
 * 依据：`api/sku/v1/sku.go` + `internal/migrations/sql/*_skus.up.sql`。
 *
 * 已确认的后端事实（这是 A2 最重要的边界）：
 * - SKU 是**平铺模型**：只有 `id / product_id / name / price / status`。
 * - 数据库 `skus` 表**没有** sku_code、spec、image、stock 列。
 * - 没有独立的 SKU 列表接口；读取只能通过商品详情内嵌的 `skus`。
 * - 同一商品下 `name` 唯一（uk_product_name），冲突返回 5004。
 * - 删除是物理删除；SKU 存在库存记录时返回 5005。
 *
 * 规格组 / 规格值 / SKU 编码 / SKU 图片均为**前端 provisional**，
 * 不会写入后端，详见 `api/adapters/sku-adapter.ts`。
 */

/** SKU 状态（后端字符串枚举，DB 存 1/0） */
export type SkuStatus = "enabled" | "disabled";

/** SKU DTO */
export interface SkuDTO {
  id: number;
  product_id: number;
  /** SKU 名称，同商品内唯一，≤128 字符 */
  name: string;
  /** 价格：整数分 */
  price: number;
  status: SkuStatus;
  created_at: string | null;
  updated_at: string | null;
}

/** 创建 SKU 请求体（product_id 在 body 中） */
export interface CreateSkuPayload {
  product_id: number;
  name: string;
  price: number;
  status?: SkuStatus;
}

/** 更新 SKU 请求体（product_id 不可变） */
export interface UpdateSkuPayload {
  name?: string;
  price?: number;
  status?: SkuStatus;
}
