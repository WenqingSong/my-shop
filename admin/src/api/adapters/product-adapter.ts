/**
 * 商品 Adapter：后端 DTO ⇄ 页面视图模型 / 表单模型。
 *
 * 所有"分 ↔ 元"的换算、状态文案、请求参数拼装都集中在这里，
 * 页面组件不得自己 `* 100` 或拼 query。
 */
import { formatCentsAsCny } from "@/utils/money";
import type {
  CreateProductPayload,
  ProductDTO,
  ProductListQuery,
  ProductSortField,
  ProductStatus,
  ProductDetailDTO,
  UpdateProductPayload
} from "../types/product";

/** 商品价格上限（后端约束：0 ~ 99,999,999 分） */
export const PRODUCT_PRICE_MAX_CENTS = 99_999_999;
/** 商品名称最大长度（后端 utf8 字符数） */
export const PRODUCT_NAME_MAX_LENGTH = 128;
/** 品牌最大长度 */
export const PRODUCT_BRAND_MAX_LENGTH = 64;
/** 主图 / 图片 URL 最大长度（DB VARCHAR(512)） */
export const PRODUCT_IMAGE_URL_MAX_LENGTH = 512;

/** 状态筛选项（后端不支持 status 参数，仅供前端跨页过滤） */
export const PRODUCT_STATUS_OPTIONS: Array<{
  value: ProductStatus;
  label: string;
}> = [
  { value: "draft", label: "草稿" },
  { value: "on_shelf", label: "已上架" },
  { value: "off_shelf", label: "已下架" }
];

const STATUS_LABELS: Record<ProductStatus, string> = {
  draft: "草稿",
  on_shelf: "已上架",
  off_shelf: "已下架"
};

const STATUS_TAG_TYPES: Record<
  ProductStatus,
  "success" | "info" | "warning" | "danger"
> = {
  draft: "info",
  on_shelf: "success",
  off_shelf: "warning"
};

/** 状态文案；未知值不猜测语义 */
export function formatProductStatus(status: string): string {
  return STATUS_LABELS[status as ProductStatus] ?? `未知状态（${status}）`;
}

/** 状态标签颜色；未知值用 danger 提醒后端枚举发生变化 */
export function productStatusTagType(
  status: string
): "success" | "info" | "warning" | "danger" {
  return STATUS_TAG_TYPES[status as ProductStatus] ?? "danger";
}

/** 页面展示用的商品视图模型 */
export interface ProductView {
  id: number;
  name: string;
  brand: string;
  categoryId: number;
  /** 分类名由页面用已加载的分类树补齐，缺省为空串 */
  categoryName: string;
  priceCents: number;
  /** 例：`¥129.00` */
  priceText: string;
  status: ProductStatus;
  statusLabel: string;
  statusTagType: "success" | "info" | "warning" | "danger";
  mainImage: string;
  images: string[];
  detail: string;
  viewCount: number;
  createdAt: string | null;
  updatedAt: string | null;
}

/** DTO → 视图模型（不发起请求） */
export function toProductView(dto: ProductDTO): ProductView {
  return {
    id: dto.id,
    name: dto.name,
    brand: dto.brand ?? "",
    categoryId: dto.category_id,
    categoryName: "",
    priceCents: dto.price,
    priceText: formatCentsAsCny(dto.price),
    status: dto.status,
    statusLabel: formatProductStatus(dto.status),
    statusTagType: productStatusTagType(dto.status),
    mainImage: dto.main_image ?? "",
    images: Array.isArray(dto.images) ? [...dto.images] : [],
    detail: dto.detail ?? "",
    viewCount: dto.view_count ?? 0,
    createdAt: dto.created_at,
    updatedAt: dto.updated_at
  };
}

/** 列表查询输入（页面状态，含前端过滤用的 status） */
export interface ProductListInput {
  page: number;
  size: number;
  keyword?: string;
  categoryId?: number | null;
  /** 仅前端过滤；不会发送给后端 */
  status?: ProductStatus | "";
  sort?: ProductSortField;
  order?: "asc" | "desc";
}

/**
 * 页面筛选 → 后端查询参数。
 *
 * **不会**输出 status：`AdminListReq` 没有该字段，传了后端也不会处理。
 */
export function toProductQuery(input: ProductListInput): ProductListQuery {
  const query: ProductListQuery = {
    page: input.page,
    size: input.size
  };

  const keyword = input.keyword?.trim();
  if (keyword) query.keyword = keyword;
  if (typeof input.categoryId === "number" && input.categoryId > 0) {
    query.category_id = input.categoryId;
  }
  if (input.sort) query.sort = input.sort;
  if (input.order) query.order = input.order;

  return query;
}

/** 元 → 分；非法输入返回 `null`（调用方负责提示） */
export function yuanToCents(value: number | string | null | undefined) {
  if (value === null || value === undefined || value === "") return null;
  const num = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(num) || num < 0) return null;
  const cents = Math.round(num * 100);
  if (!Number.isFinite(cents) || cents > PRODUCT_PRICE_MAX_CENTS) return null;
  // 防止 12.345 这类输入被静默四舍五入成 1235
  if (Math.abs(num * 100 - cents) > 1e-6) return null;
  return cents;
}

/** 分 → 元（表单回显，保留两位小数） */
export function centsToYuanInput(cents: number): number {
  return Math.round(cents) / 100;
}

/** 商品基本信息表单（与 SKU 编辑解耦） */
export interface ProductBasicForm {
  name: string;
  brand: string;
  categoryId: number | null;
  /** 单位：元 */
  priceYuan: number | null;
  mainImage: string;
  images: string[];
  detail: string;
}

/** 空表单（新建商品） */
export function emptyProductForm(): ProductBasicForm {
  return {
    name: "",
    brand: "",
    categoryId: null,
    priceYuan: null,
    mainImage: "",
    images: [],
    detail: ""
  };
}

/** 详情 DTO → 表单（编辑回显） */
export function toProductForm(dto: ProductDetailDTO): ProductBasicForm {
  return {
    name: dto.name,
    brand: dto.brand ?? "",
    categoryId: dto.category_id,
    priceYuan: centsToYuanInput(dto.price),
    mainImage: dto.main_image ?? "",
    images: Array.isArray(dto.images) ? [...dto.images] : [],
    detail: dto.detail ?? ""
  };
}

/** 表单校验结果：字段名 → 错误文案 */
export type ProductFormErrors = Partial<
  Record<
    "name" | "brand" | "categoryId" | "price" | "mainImage" | "images",
    string
  >
>;

/** 商品基本信息校验（纯函数，便于单测） */
export function validateProductForm(form: ProductBasicForm): ProductFormErrors {
  const errors: ProductFormErrors = {};

  const name = form.name.trim();
  if (!name) {
    errors.name = "请输入商品名称";
  } else if ([...name].length > PRODUCT_NAME_MAX_LENGTH) {
    errors.name = `商品名称不能超过 ${PRODUCT_NAME_MAX_LENGTH} 个字符`;
  }

  if (
    form.brand.trim() &&
    [...form.brand.trim()].length > PRODUCT_BRAND_MAX_LENGTH
  ) {
    errors.brand = `品牌不能超过 ${PRODUCT_BRAND_MAX_LENGTH} 个字符`;
  }

  if (!form.categoryId || form.categoryId <= 0) {
    errors.categoryId = "请选择商品分类";
  }

  if (form.priceYuan === null || form.priceYuan === undefined) {
    errors.price = "请输入商品价格";
  } else if (yuanToCents(form.priceYuan) === null) {
    errors.price = "价格需为 0 ~ 999,999.99 元，最多两位小数";
  }

  if (form.mainImage && form.mainImage.length > PRODUCT_IMAGE_URL_MAX_LENGTH) {
    errors.mainImage = `主图地址不能超过 ${PRODUCT_IMAGE_URL_MAX_LENGTH} 个字符`;
  }

  const invalidImage = form.images.find(
    url => !url || url.length > PRODUCT_IMAGE_URL_MAX_LENGTH
  );
  if (invalidImage !== undefined) {
    errors.images = `轮播图地址不能为空且不能超过 ${PRODUCT_IMAGE_URL_MAX_LENGTH} 个字符`;
  }

  return errors;
}

/** 是否有校验错误 */
export function hasFormErrors(errors: Record<string, string | undefined>) {
  return Object.values(errors).some(Boolean);
}

/** 表单 → 创建请求体 */
export function buildCreatePayload(
  form: ProductBasicForm
): CreateProductPayload {
  const price = yuanToCents(form.priceYuan);
  if (price === null) throw new Error("商品价格非法");

  return {
    name: form.name.trim(),
    brand: form.brand.trim(),
    category_id: form.categoryId as number,
    price,
    main_image: form.mainImage.trim(),
    detail: form.detail,
    images: form.images.map(url => url.trim()).filter(Boolean)
  };
}

/**
 * 表单 → 更新请求体。
 *
 * 不发送 status：后端会忽略它，状态只能走上下架接口。
 */
export function buildUpdatePayload(
  form: ProductBasicForm
): UpdateProductPayload {
  const price = yuanToCents(form.priceYuan);
  if (price === null) throw new Error("商品价格非法");

  return {
    name: form.name.trim(),
    brand: form.brand.trim(),
    category_id: form.categoryId as number,
    price,
    main_image: form.mainImage.trim(),
    detail: form.detail,
    images: form.images.map(url => url.trim()).filter(Boolean)
  };
}

/**
 * 按状态过滤 DTO。
 *
 * 后端列表接口没有 status 参数，这是"跨页拉取后前端过滤"的实现基础。
 */
export function filterProductsByStatus(
  items: ProductDTO[],
  status: ProductStatus | ""
): ProductDTO[] {
  if (!status) return items;
  return items.filter(item => item.status === status);
}

/** 按后端支持的字段排序（用于跨页拉取后的全局排序） */
export function sortProducts(
  items: ProductDTO[],
  sort: ProductSortField = "id",
  order: "asc" | "desc" = "desc"
): ProductDTO[] {
  const direction = order === "asc" ? 1 : -1;
  return [...items].sort((left, right) => {
    if (sort === "price") return (left.price - right.price) * direction;
    if (sort === "created_at" || sort === "updated_at") {
      const leftTime = Date.parse(String(left[sort] ?? "").replace(" ", "T"));
      const rightTime = Date.parse(String(right[sort] ?? "").replace(" ", "T"));
      const safeLeft = Number.isFinite(leftTime) ? leftTime : 0;
      const safeRight = Number.isFinite(rightTime) ? rightTime : 0;
      return (safeLeft - safeRight) * direction;
    }
    return (left.id - right.id) * direction;
  });
}

/** 内存分页（用于跨页拉取后的展示） */
export function slicePage<T>(items: T[], page: number, size: number): T[] {
  const start = Math.max(0, (page - 1) * size);
  return items.slice(start, start + size);
}
