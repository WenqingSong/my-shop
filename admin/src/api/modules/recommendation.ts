/**
 * 推荐位 API 模块。
 *
 * 真实路由：
 * - `GET    /admin/recommend-positions`                    列表（AdminAuth）
 * - `GET    /admin/recommend-positions/:id`                详情（含推荐商品关系）
 * - `POST   /admin/recommend-positions`                    创建（recommend:create）
 * - `PUT    /admin/recommend-positions/:id`                更新 name/status（recommend:update）
 * - `DELETE /admin/recommend-positions/:id`                删除（recommend:delete，级联关系）
 * - `POST   /admin/recommend-positions/:id/items`          添加商品（recommend:item）
 * - `DELETE /admin/recommend-positions/:id/items/:product_id` 移除商品
 * - `PUT    /admin/recommend-positions/:id/items/sort`     全量排序
 */
import { http } from "@/utils/http";
import type {
  AddRecommendItemPayload,
  CreatePositionPayload,
  RecommendItemDTO,
  RecommendPositionDTO,
  RecommendPositionDetailDTO,
  UpdatePositionPayload
} from "../types/recommendation";

/** 后台全部推荐位（按 id 升序） */
export function listPositions() {
  return http.get<{ items: RecommendPositionDTO[] }, undefined>(
    "/admin/recommend-positions",
    undefined,
    { silent: true }
  );
}

/** 推荐位详情（含推荐商品关系） */
export function getPosition(id: number) {
  return http.get<RecommendPositionDetailDTO, undefined>(
    `/admin/recommend-positions/${id}`,
    undefined,
    { silent: true }
  );
}

/** 创建推荐位 */
export function createPosition(payload: CreatePositionPayload) {
  return http.post<RecommendPositionDTO, typeof payload>(
    "/admin/recommend-positions",
    { data: payload },
    { silent: true }
  );
}

/** 更新推荐位（code 不可变） */
export function updatePosition(id: number, payload: UpdatePositionPayload) {
  return http.request<RecommendPositionDTO>(
    "put",
    `/admin/recommend-positions/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除推荐位（后端级联删除推荐商品关系） */
export function deletePosition(id: number) {
  return http.request<null>(
    "delete",
    `/admin/recommend-positions/${id}`,
    undefined,
    { silent: true }
  );
}

/** 添加推荐商品（商品必须存在；重复添加 15005） */
export function addPositionItem(id: number, payload: AddRecommendItemPayload) {
  return http.post<RecommendItemDTO, typeof payload>(
    `/admin/recommend-positions/${id}/items`,
    { data: payload },
    { silent: true }
  );
}

/** 移除推荐商品 */
export function removePositionItem(id: number, productId: number) {
  return http.request<null>(
    "delete",
    `/admin/recommend-positions/${id}/items/${productId}`,
    undefined,
    { silent: true }
  );
}

/**
 * 调整推荐商品排序（全量覆盖语义）。
 *
 * `productIds` 必须非空、无重复，且恰好覆盖该推荐位全部已加入商品，
 * 否则后端返回 15006。
 */
export function updatePositionItemSort(id: number, productIds: number[]) {
  return http.request<{ items: RecommendItemDTO[] }>(
    "put",
    `/admin/recommend-positions/${id}/items/sort`,
    { data: { product_ids: productIds } },
    { silent: true }
  );
}
