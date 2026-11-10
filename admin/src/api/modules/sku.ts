/**
 * SKU API 模块。
 *
 * 真实路由（`internal/cmd/routes_admin.go`）：
 * - `POST   /admin/skus`      创建（sku:create）
 * - `PUT    /admin/skus/:id`  更新（sku:update）
 * - `DELETE /admin/skus/:id`  删除（sku:delete）
 *
 * 后端**没有**独立 SKU 列表接口；SKU 读取只能通过
 * `GET /admin/products/:id` 的 `skus` 字段。
 *
 * 同商品内 SKU 名称唯一；删除有库存记录的 SKU 会返回 5005。
 */
import { http } from "@/utils/http";
import { planSkuSync, type SkuDraft } from "../adapters/sku-adapter";
import type { CreateSkuPayload, SkuDTO, UpdateSkuPayload } from "../types/sku";
import { resolveErrorMessage } from "@/utils/error-message";

/** 创建 SKU */
export function createSku(payload: CreateSkuPayload) {
  return http.post<SkuDTO, typeof payload>(
    "/admin/skus",
    { data: payload },
    { silent: true }
  );
}

/** 更新 SKU（product_id 不可变） */
export function updateSku(id: number, payload: UpdateSkuPayload) {
  return http.request<SkuDTO>(
    "put",
    `/admin/skus/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除 SKU（物理删除；有库存记录时后端拒绝） */
export function deleteSku(id: number) {
  return http.request<null>("delete", `/admin/skus/${id}`, undefined, {
    silent: true
  });
}

/** SKU 同步时可执行的动作（由页面按权限码计算） */
export interface SkuSyncPermissions {
  create: boolean;
  update: boolean;
  remove: boolean;
}

/** 单条 SKU 同步失败 */
export interface SkuSyncFailure {
  action: "create" | "update" | "delete";
  name: string;
  message: string;
}

/** SKU 同步结果 */
export interface SkuSyncResult {
  created: number;
  updated: number;
  removed: number;
  /** 因权限不足而未执行的动作数 */
  skipped: number;
  failures: SkuSyncFailure[];
}

/**
 * 把规格矩阵同步到后端平铺 SKU。
 *
 * - 逐条调用真实接口，单个失败不中断其它行，最终由调用方汇总提示；
 * - 不做任何本地"假成功"：失败的变更不会算进 created/updated/removed；
 * - 顺序固定为 create → update → delete，便于在日志里复现。
 */
export async function syncProductSkus(
  productId: number,
  drafts: SkuDraft[],
  existing: SkuDTO[],
  permissions: SkuSyncPermissions
): Promise<SkuSyncResult> {
  const plan = planSkuSync(drafts, existing);
  const result: SkuSyncResult = {
    created: 0,
    updated: 0,
    removed: 0,
    skipped: 0,
    failures: []
  };

  for (const draft of plan.create) {
    if (!permissions.create) {
      result.skipped += 1;
      continue;
    }
    try {
      await createSku({
        product_id: productId,
        name: draft.name,
        price: draft.priceCents,
        status: draft.status
      });
      result.created += 1;
    } catch (cause) {
      result.failures.push({
        action: "create",
        name: draft.name,
        message: resolveErrorMessage(cause, "SKU 创建失败")
      });
    }
  }

  for (const item of plan.update) {
    if (!permissions.update) {
      result.skipped += 1;
      continue;
    }
    try {
      await updateSku(item.existing.id, item.payload);
      result.updated += 1;
    } catch (cause) {
      result.failures.push({
        action: "update",
        name: item.existing.name,
        message: resolveErrorMessage(cause, "SKU 更新失败")
      });
    }
  }

  for (const sku of plan.remove) {
    if (!permissions.remove) {
      result.skipped += 1;
      continue;
    }
    try {
      await deleteSku(sku.id);
      result.removed += 1;
    } catch (cause) {
      result.failures.push({
        action: "delete",
        name: sku.name,
        message: resolveErrorMessage(cause, "SKU 删除失败")
      });
    }
  }

  return result;
}
