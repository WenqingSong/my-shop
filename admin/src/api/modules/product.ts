/**
 * 商品 API 模块。
 *
 * 全部路径为 Go 后端真实路由（`internal/cmd/routes_admin.go`）：
 * - `GET    /admin/products`             列表（AdminAuth，无独立读权限码）
 * - `GET    /admin/products/:id`         详情（含全部状态 SKU）
 * - `POST   /admin/products`             创建（product:create）
 * - `PUT    /admin/products/:id`         更新（product:update）
 * - `POST   /admin/products/:id/on-shelf`  上架（product:on_shelf）
 * - `POST   /admin/products/:id/off-shelf` 下架（product:off_shelf）
 *
 * 后端**没有**删除商品的接口，因此本模块不提供 delete。
 *
 * 失败策略：统一 `silent: true`，由页面渲染错误（避免全局弹窗与页面内提示重复）。
 */
import { http } from "@/utils/http";
import type {
  CreateProductPayload,
  ProductDetailDTO,
  ProductDTO,
  ProductListQuery,
  ProductListResult,
  UpdateProductPayload
} from "../types/product";

/** 后台商品列表（全部状态） */
export function listProducts(query: ProductListQuery = {}) {
  return http.get<ProductListResult, undefined>(
    "/admin/products",
    { params: query },
    { silent: true }
  );
}

/** 后台商品详情（含 SKU） */
export function getProduct(id: number) {
  return http.get<ProductDetailDTO, undefined>(
    `/admin/products/${id}`,
    undefined,
    { silent: true }
  );
}

/** 创建商品（后端强制草稿状态） */
export function createProduct(payload: CreateProductPayload) {
  return http.post<ProductDTO, typeof payload>(
    "/admin/products",
    { data: payload },
    { silent: true }
  );
}

/** 更新商品基本信息（不接受 status） */
export function updateProduct(id: number, payload: UpdateProductPayload) {
  return http.request<ProductDTO>(
    "put",
    `/admin/products/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 上架（仅 draft / off_shelf → on_shelf） */
export function onShelfProduct(id: number) {
  return http.post<ProductDTO, undefined>(
    `/admin/products/${id}/on-shelf`,
    undefined,
    { silent: true }
  );
}

/** 下架（仅 on_shelf → off_shelf） */
export function offShelfProduct(id: number) {
  return http.post<ProductDTO, undefined>(
    `/admin/products/${id}/off-shelf`,
    undefined,
    { silent: true }
  );
}

/** 单次跨页拉取的最大页数（防止状态筛选失控地遍历全表） */
export const PRODUCT_SCAN_MAX_PAGES = 5;
/** 跨页拉取使用的每页条数（后端上限 100） */
export const PRODUCT_SCAN_PAGE_SIZE = 100;

/**
 * 跨页拉取商品的原始 DTO。
 *
 * 用途：后端列表接口**不支持 status 参数**，但运营需要按状态筛选。
 * 这里用真实接口逐页拉取后由前端过滤，属于"真实数据 + 前端过滤"，
 * 不是假数据；`truncated = true` 表示达到页数上限、结果可能不完整。
 */
export async function scanProducts(
  query: Omit<ProductListQuery, "page" | "size"> = {}
): Promise<{ items: ProductDTO[]; truncated: boolean }> {
  const items: ProductDTO[] = [];
  let truncated = false;

  for (let page = 1; page <= PRODUCT_SCAN_MAX_PAGES; page += 1) {
    const result = await listProducts({
      ...query,
      page,
      size: PRODUCT_SCAN_PAGE_SIZE
    });
    items.push(...result.items);

    if (items.length >= result.total || result.items.length === 0) {
      return { items, truncated: false };
    }
    if (page === PRODUCT_SCAN_MAX_PAGES) truncated = true;
  }

  return { items, truncated };
}
