/**
 * 推荐位 DTO（`api/recommendation/v1/recommendation.go`）。
 *
 * 数据模型是「推荐位 → 推荐商品关系」两层：
 * - 推荐位：code（创建后不可变）、name、status；
 * - 推荐商品关系：product_id + sort（**不含商品快照**）。
 *
 * 管理端展示商品名/图片/价格时，需要复用 A2 的商品详情接口逐条补全。
 * 排序是全量覆盖语义：PUT sort 必须恰好覆盖该推荐位全部已加入商品。
 */
export interface RecommendPositionDTO {
  id: number;
  code: string;
  name: string;
  /** 1 启用 / 0 禁用 */
  status: 0 | 1;
  created_at: string | null;
  updated_at: string | null;
}

/** 推荐商品关系 */
export interface RecommendItemDTO {
  id: number;
  position_id: number;
  product_id: number;
  sort: number;
  created_at: string | null;
  updated_at: string | null;
}

/** 推荐位详情（含关系列表，按 sort,id 升序） */
export interface RecommendPositionDetailDTO extends RecommendPositionDTO {
  items: RecommendItemDTO[];
}

/** 创建推荐位请求 */
export interface CreatePositionPayload {
  code: string;
  name: string;
  status?: 0 | 1;
}

/** 更新推荐位请求（name/status，code 不可变） */
export interface UpdatePositionPayload {
  name?: string;
  status?: 0 | 1;
}

/** 添加推荐商品请求 */
export interface AddRecommendItemPayload {
  product_id: number;
  sort?: number;
}
