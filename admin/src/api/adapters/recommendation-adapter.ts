/**
 * 推荐位 Adapter。
 *
 * 后端推荐商品关系只有 product_id + sort，没有商品快照。
 * 页面展示商品名/图片/价格时需要复用 A2 `getProduct` 补全；
 * 补全失败时只展示 `商品 #id`，不伪造商品信息。
 *
 * 排序是**全量覆盖**语义：必须提交该推荐位全部已加入商品的有序 id 列表。
 */
import { formatCentsAsCny } from "@/utils/money";
import { formatServerDateTime } from "@/utils/datetime";
import { formatProductStatus, productStatusTagType } from "./product-adapter";
import type { ProductDTO } from "../types/product";
import type {
  CreatePositionPayload,
  RecommendItemDTO,
  RecommendPositionDTO,
  UpdatePositionPayload
} from "../types/recommendation";

type TagType = "success" | "info" | "danger";

export const RECOMMEND_STATUS_OPTIONS: Array<{
  value: 0 | 1;
  label: string;
}> = [
  { value: 1, label: "启用" },
  { value: 0, label: "禁用" }
];

export const POSITION_NAME_MAX_LENGTH = 64;
/** 后端 code 规则：[a-z0-9][a-z0-9-]{0,63} */
export const POSITION_CODE_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;

/** 状态文案 */
export function formatPositionStatus(status: number): string {
  if (status === 1) return "启用";
  if (status === 0) return "禁用";
  return `未知状态（${status}）`;
}

/** 状态标签颜色 */
export function positionStatusTagType(status: number): TagType {
  if (status === 1) return "success";
  if (status === 0) return "info";
  return "danger";
}

/** 推荐位视图 */
export interface PositionView {
  id: number;
  code: string;
  name: string;
  status: 0 | 1;
  statusLabel: string;
  statusTagType: TagType;
  createdText: string;
  updatedText: string;
}

/** DTO → 视图 */
export function toPositionView(dto: RecommendPositionDTO): PositionView {
  return {
    id: dto.id,
    code: dto.code,
    name: dto.name,
    status: dto.status,
    statusLabel: formatPositionStatus(dto.status),
    statusTagType: positionStatusTagType(dto.status),
    createdText: formatServerDateTime(dto.created_at),
    updatedText: formatServerDateTime(dto.updated_at)
  };
}

/** 推荐商品视图（商品信息可能为空） */
export interface RecommendItemView {
  relationId: number;
  productId: number;
  sort: number;
  productName: string;
  productImage: string;
  productPriceText: string;
  productStatusLabel: string;
  productStatusTagType: TagType;
  /** 商品详情是否加载成功；false 时仅展示 product_id */
  productLoaded: boolean;
}

/** 关系 + 商品详情 → 视图 */
export function toRecommendItemView(
  item: RecommendItemDTO,
  product?: ProductDTO | null
): RecommendItemView {
  return {
    relationId: item.id,
    productId: item.product_id,
    sort: item.sort,
    productName: product?.name ?? `商品 #${item.product_id}`,
    productImage: product?.main_image ?? "",
    productPriceText: product ? formatCentsAsCny(product.price) : "—",
    productStatusLabel: product ? formatProductStatus(product.status) : "未知",
    productStatusTagType: product
      ? (productStatusTagType(product.status) as TagType)
      : "info",
    productLoaded: Boolean(product)
  };
}

/** 推荐位表单 */
export interface PositionForm {
  code: string;
  name: string;
  status: 0 | 1;
}

/** 空表单 */
export function emptyPositionForm(): PositionForm {
  return { code: "", name: "", status: 1 };
}

export type PositionFormErrors = Partial<
  Record<"code" | "name" | "status", string>
>;

/**
 * 表单校验。
 *
 * @param mode create 时校验 code；edit 时 code 不可变，忽略该字段
 */
export function validatePositionForm(
  form: PositionForm,
  mode: "create" | "edit" = "create"
): PositionFormErrors {
  const errors: PositionFormErrors = {};

  if (mode === "create") {
    const code = form.code.trim();
    if (!code) {
      errors.code = "请输入推荐位 code";
    } else if (!POSITION_CODE_PATTERN.test(code)) {
      errors.code = "code 仅支持小写字母/数字/连字符，且以字母或数字开头";
    }
  }

  const name = form.name.trim();
  if (!name) {
    errors.name = "请输入推荐位名称";
  } else if ([...name].length > POSITION_NAME_MAX_LENGTH) {
    errors.name = `名称不能超过 ${POSITION_NAME_MAX_LENGTH} 个字符`;
  }

  if (form.status !== 0 && form.status !== 1) {
    errors.status = "状态只能是启用或禁用";
  }

  return errors;
}

/** 是否有校验错误 */
export function hasPositionErrors(errors: PositionFormErrors): boolean {
  return Object.values(errors).some(Boolean);
}

/** 表单 → 创建请求 */
export function buildCreatePositionPayload(
  form: PositionForm
): CreatePositionPayload {
  return {
    code: form.code.trim(),
    name: form.name.trim(),
    status: form.status
  };
}

/** 表单 → 更新请求（code 不可变） */
export function buildUpdatePositionPayload(
  form: PositionForm
): UpdatePositionPayload {
  return {
    name: form.name.trim(),
    status: form.status
  };
}

/**
 * 调整顺序：把第 index 项上移/下移一位。
 *
 * 返回值总是新数组；越界时原样返回副本。
 */
export function moveItem<T>(items: T[], index: number, direction: -1 | 1): T[] {
  const next = [...items];
  const target = index + direction;
  if (
    index < 0 ||
    index >= next.length ||
    target < 0 ||
    target >= next.length
  ) {
    return next;
  }
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

/**
 * 构造全量排序请求的 product_ids。
 *
 * 后端要求"恰好覆盖全部已加入商品"，因此这里直接按当前顺序全量提交。
 */
export function buildSortPayload(items: RecommendItemView[]): number[] {
  return items.map(item => item.productId);
}
