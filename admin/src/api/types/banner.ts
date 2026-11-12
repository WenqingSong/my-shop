/**
 * 轮播图 DTO（`api/banner/v1/banner.go`）。
 *
 * 没有独立启停接口：`status` 通过创建/更新接口写入。
 * 没有展示位置、有效期字段。
 */
export interface BannerDTO {
  id: number;
  title: string;
  /** 图片地址（≤255 字符） */
  image_url: string;
  /** 跳转目标（≤512，空串表示无跳转） */
  link_url: string;
  sort: number;
  /** 1 启用 / 0 禁用 */
  status: 0 | 1;
  created_at: string | null;
  updated_at: string | null;
}

/** 创建轮播图请求 */
export interface CreateBannerPayload {
  title: string;
  image_url: string;
  link_url: string;
  sort: number;
  status: 0 | 1;
}

/** 更新轮播图请求（部分更新） */
export interface UpdateBannerPayload {
  title?: string;
  image_url?: string;
  link_url?: string;
  sort?: number;
  status?: 0 | 1;
}
