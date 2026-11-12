/**
 * 轮播图 API 模块。
 *
 * 真实路由：
 * - `GET    /admin/banners`       后台列表（AdminAuth，全部状态）
 * - `GET    /admin/banners/:id`   后台详情
 * - `POST   /admin/banners`       创建（banner:create）
 * - `PUT    /admin/banners/:id`   更新（banner:update，部分更新）
 * - `DELETE /admin/banners/:id`   删除（banner:delete）
 *
 * 启停通过 update 的 status 字段完成，没有独立启停接口。
 */
import { http } from "@/utils/http";
import type {
  BannerDTO,
  CreateBannerPayload,
  UpdateBannerPayload
} from "../types/banner";

/** 后台全部轮播图（按 sort,id 升序） */
export function listBanners() {
  return http.get<{ items: BannerDTO[] }, undefined>(
    "/admin/banners",
    undefined,
    { silent: true }
  );
}

/** 轮播图详情 */
export function getBanner(id: number) {
  return http.get<BannerDTO, undefined>(`/admin/banners/${id}`, undefined, {
    silent: true
  });
}

/** 创建轮播图 */
export function createBanner(payload: CreateBannerPayload) {
  return http.post<BannerDTO, typeof payload>(
    "/admin/banners",
    { data: payload },
    { silent: true }
  );
}

/** 更新轮播图（仅提交变更字段） */
export function updateBanner(id: number, payload: UpdateBannerPayload) {
  return http.request<BannerDTO>(
    "put",
    `/admin/banners/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除轮播图 */
export function deleteBanner(id: number) {
  return http.request<null>("delete", `/admin/banners/${id}`, undefined, {
    silent: true
  });
}
