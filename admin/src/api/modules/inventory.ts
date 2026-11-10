/**
 * 库存 API 模块。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET  /admin/inventories/:sku_id`          查询单个 SKU 库存
 * - `GET  /admin/inventories/:sku_id/logs`     库存流水（分页，id 倒序）
 * - `POST /admin/inventories/:sku_id/increase` 增加库存（inventory:increase）
 * - `POST /admin/inventories/:sku_id/deduct`   扣减库存（inventory:deduct）
 *
 * 重要边界：
 * - 后端**没有**库存列表/搜索接口，也没有"设定库存总量"的接口。
 *   本模块只暴露增量语义，禁止用前端拼出覆盖式写入。
 * - 增减请求体只有 `quantity`，没有 `reason`。
 */
import { http } from "@/utils/http";
import type {
  InventoryDTO,
  InventoryLogListQuery,
  InventoryLogListResult
} from "../types/inventory";

/** 查询单个 SKU 库存（无记录时后端返回 quantity=0） */
export function getInventory(skuId: number) {
  return http.get<InventoryDTO, undefined>(
    `/admin/inventories/${skuId}`,
    undefined,
    { silent: true }
  );
}

/** 增加库存（正整数） */
export function increaseInventory(skuId: number, quantity: number) {
  return http.post<InventoryDTO, { quantity: number }>(
    `/admin/inventories/${skuId}/increase`,
    { data: { quantity } },
    { silent: true }
  );
}

/** 扣减库存（库存不足时后端返回 6001） */
export function deductInventory(skuId: number, quantity: number) {
  return http.post<InventoryDTO, { quantity: number }>(
    `/admin/inventories/${skuId}/deduct`,
    { data: { quantity } },
    { silent: true }
  );
}

/** 库存流水（只读，分页） */
export function listInventoryLogs(
  skuId: number,
  query: InventoryLogListQuery = {}
) {
  return http.get<InventoryLogListResult, undefined>(
    `/admin/inventories/${skuId}/logs`,
    { params: query },
    { silent: true }
  );
}
