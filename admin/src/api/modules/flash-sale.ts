/**
 * 秒杀活动 API 模块。
 *
 * 真实路由（仅两条）：
 * - `POST /admin/flash-sales`        创建（flash_sale:create）
 * - `PUT  /admin/flash-sales/:id`    更新（flash_sale:update）
 *
 * 后端没有活动列表/详情/删除接口。管理端只维护配置，
 * 不接触 Redis Lua 准入、Kafka、Outbox、库存扣减与请求状态。
 */
import { http } from "@/utils/http";
import type {
  ActivityDTO,
  CreateActivityPayload,
  UpdateActivityPayload
} from "../types/flash-sale";

/** 创建秒杀活动（创建即启用；SKU 绑定必须非空） */
export function createFlashSale(payload: CreateActivityPayload) {
  return http.post<ActivityDTO, typeof payload>(
    "/admin/flash-sales",
    { data: payload },
    { silent: true }
  );
}

/**
 * 更新秒杀活动（部分更新）。
 *
 * `skus` 只允许更新已绑定的 SKU；新增/删除绑定会被后端拒绝（12007）。
 */
export function updateFlashSale(id: number, payload: UpdateActivityPayload) {
  return http.request<ActivityDTO>(
    "put",
    `/admin/flash-sales/${id}`,
    { data: payload },
    { silent: true }
  );
}
