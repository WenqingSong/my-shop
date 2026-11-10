/**
 * 商品领域 DTO。
 *
 * 依据：`api/product/v1/product.go`（Go 结构体已实现，字段名与后端 JSON 一致）。
 * 状态：CONTRACT_BACKED —— 字段可以直接对照后端结构体验证；
 * 仍未生成 OpenAPI 类型，等 `contracts/openapi.yaml` 冻结后由 `pnpm api:types` 替换。
 *
 * 已确认的后端事实：
 * - `price` 为**整数分**（int64），前端展示必须 /100，提交必须 *100。
 * - 创建商品时后端强制 `draft`，请求体**没有** status 字段。
 * - 更新商品时即使传 status，合法值也会被**忽略**；状态只能通过
 *   `POST /admin/products/:id/on-shelf|off-shelf` 变更。
 * - 后端**没有** subtitle 字段，也没有商品删除接口。
 */

import type { SkuDTO } from "./sku";

/** 商品状态（后端字符串枚举） */
export type ProductStatus = "draft" | "on_shelf" | "off_shelf";

/** 商品 DTO（列表项与详情公共字段） */
export interface ProductDTO {
  id: number;
  name: string;
  /** 品牌，可能为空字符串 */
  brand: string;
  category_id: number;
  /** 价格：整数分 */
  price: number;
  /** 主图 URL，可能为空字符串 */
  main_image: string;
  /** 商品详情（富文本/纯文本由运营侧约定，前端按纯文本编辑） */
  detail: string;
  status: ProductStatus;
  /** 累计浏览量（只读） */
  view_count: number;
  /** 轮播图 URL 列表（按 sort 升序） */
  images: string[];
  /** 后端返回 `gtime` 字符串（`YYYY-MM-DD HH:mm:ss`），可能为 null */
  created_at: string | null;
  updated_at: string | null;
}

/** 后台商品列表查询参数（AdminListReq） */
export interface ProductListQuery {
  page?: number;
  size?: number;
  category_id?: number;
  /** 模糊匹配 name / brand */
  keyword?: string;
  sort?: ProductSortField;
  order?: "asc" | "desc";
}

/** 后端仅支持这四种排序字段 */
export type ProductSortField = "id" | "price" | "created_at" | "updated_at";

/** 商品列表响应（AdminListRes） */
export interface ProductListResult {
  items: ProductDTO[];
  total: number;
  page: number;
  size: number;
}

/** 商品详情响应（AdminDetailRes = Product + skus） */
export interface ProductDetailDTO extends ProductDTO {
  skus: SkuDTO[];
}

/**
 * 创建商品请求体（CreateReq）。
 *
 * 注意：**没有** `status` 字段——后端固定创建为 draft。
 */
export interface CreateProductPayload {
  name: string;
  brand?: string;
  /** 必须是存在且启用的叶子分类 */
  category_id: number;
  /** 整数分，0 ~ 99_999_999 */
  price: number;
  /** 主图 URL，≤512 字符 */
  main_image?: string;
  detail?: string;
  /** 提供则全量替换轮播图 */
  images?: string[];
}

/**
 * 更新商品请求体（UpdateReq）。
 *
 * 后端使用指针字段，只有出现的字段才会被更新。
 * `status` 故意不暴露：合法值会被忽略，传了只会误导调用方。
 */
export interface UpdateProductPayload {
  name?: string;
  brand?: string;
  category_id?: number;
  price?: number;
  main_image?: string;
  detail?: string;
  images?: string[];
}
