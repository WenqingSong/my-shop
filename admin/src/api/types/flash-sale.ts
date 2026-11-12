/**
 * 秒杀活动 DTO（`api/flashsale/v1/flash_sale.go`）。
 *
 * 后台**只有创建与更新**两个接口，没有列表、详情、删除、独立启停接口。
 * 更新是部分更新：未提交字段保持不变；`skus` 只允许更新**已绑定**的 SKU，
 * 不能新增或删除绑定。
 *
 * 秒杀交易链路（Redis Lua 准入、Kafka、Outbox、消费者）属于后端内部实现，
 * 管理端只能维护活动配置，不能触碰库存/请求状态。
 */
export type FlashSaleStatus = "enabled" | "disabled";

/** 活动 × SKU 绑定 */
export interface ActivitySkuDTO {
  id: number;
  sku_id: number;
  /** 秒杀价（整数分） */
  flash_price: number;
  /** 秒杀初始库存 */
  total_stock: number;
  /** 已售数量（后端真实计数） */
  sold: number;
}

/** 活动（创建/更新响应） */
export interface ActivityDTO {
  id: number;
  name: string;
  status: FlashSaleStatus;
  start_time: string | null;
  end_time: string | null;
  skus: ActivitySkuDTO[];
  created_at: string | null;
  updated_at: string | null;
}

/** SKU 绑定输入 */
export interface ActivitySkuInput {
  sku_id: number;
  /** 整数分，>0 且 ≤99999999 */
  flash_price: number;
  /** 正整数 */
  total_stock: number;
}

/** 创建活动请求 */
export interface CreateActivityPayload {
  name: string;
  start_time: string;
  end_time: string;
  skus: ActivitySkuInput[];
}

/** 更新活动请求（部分更新） */
export interface UpdateActivityPayload {
  name?: string;
  status?: FlashSaleStatus;
  start_time?: string;
  end_time?: string;
  skus?: ActivitySkuInput[];
}
