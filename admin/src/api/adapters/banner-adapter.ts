/**
 * 轮播图 Adapter。
 *
 * 后端字段：title（≤64）、image_url（≤255）、link_url（≤512，可空）、
 * sort、status（1/0）。没有展示位置与有效期。
 *
 * 图片上传复用 A2 的七牛直传流程，本 adapter 只处理 URL 字符串。
 */
import { formatServerDateTime } from "@/utils/datetime";
import type {
  BannerDTO,
  CreateBannerPayload,
  UpdateBannerPayload
} from "../types/banner";

type TagType = "success" | "info" | "danger";

export const BANNER_STATUS_OPTIONS: Array<{ value: 0 | 1; label: string }> = [
  { value: 1, label: "启用" },
  { value: 0, label: "禁用" }
];

export const BANNER_TITLE_MAX_LENGTH = 64;
export const BANNER_IMAGE_MAX_LENGTH = 255;
export const BANNER_LINK_MAX_LENGTH = 512;

/** 状态文案 */
export function formatBannerStatus(status: number): string {
  if (status === 1) return "启用";
  if (status === 0) return "禁用";
  return `未知状态（${status}）`;
}

/** 状态标签颜色 */
export function bannerStatusTagType(status: number): TagType {
  if (status === 1) return "success";
  if (status === 0) return "info";
  return "danger";
}

/** 轮播图视图 */
export interface BannerView {
  id: number;
  title: string;
  imageUrl: string;
  linkUrl: string;
  sort: number;
  status: 0 | 1;
  statusLabel: string;
  statusTagType: TagType;
  createdText: string;
  updatedText: string;
}

/** DTO → 视图 */
export function toBannerView(dto: BannerDTO): BannerView {
  return {
    id: dto.id,
    title: dto.title,
    imageUrl: dto.image_url,
    linkUrl: dto.link_url || "",
    sort: dto.sort,
    status: dto.status,
    statusLabel: formatBannerStatus(dto.status),
    statusTagType: bannerStatusTagType(dto.status),
    createdText: formatServerDateTime(dto.created_at),
    updatedText: formatServerDateTime(dto.updated_at)
  };
}

/** 表单 */
export interface BannerForm {
  title: string;
  imageUrl: string;
  linkUrl: string;
  sort: number;
  status: 0 | 1;
}

/** 空表单 */
export function emptyBannerForm(): BannerForm {
  return { title: "", imageUrl: "", linkUrl: "", sort: 0, status: 1 };
}

/** DTO → 表单 */
export function toBannerForm(dto: BannerDTO): BannerForm {
  return {
    title: dto.title,
    imageUrl: dto.image_url,
    linkUrl: dto.link_url || "",
    sort: dto.sort,
    status: dto.status
  };
}

export type BannerFormErrors = Partial<
  Record<"title" | "imageUrl" | "linkUrl" | "sort" | "status", string>
>;

/** 表单校验（与后端 14002 对齐） */
export function validateBannerForm(form: BannerForm): BannerFormErrors {
  const errors: BannerFormErrors = {};

  const title = form.title.trim();
  if (!title) {
    errors.title = "请输入轮播图标题";
  } else if ([...title].length > BANNER_TITLE_MAX_LENGTH) {
    errors.title = `标题不能超过 ${BANNER_TITLE_MAX_LENGTH} 个字符`;
  }

  const imageUrl = form.imageUrl.trim();
  if (!imageUrl) {
    errors.imageUrl = "请上传轮播图片";
  } else if ([...imageUrl].length > BANNER_IMAGE_MAX_LENGTH) {
    errors.imageUrl = `图片地址不能超过 ${BANNER_IMAGE_MAX_LENGTH} 个字符`;
  }

  if ([...form.linkUrl.trim()].length > BANNER_LINK_MAX_LENGTH) {
    errors.linkUrl = `跳转链接不能超过 ${BANNER_LINK_MAX_LENGTH} 个字符`;
  }

  if (!Number.isInteger(form.sort)) {
    errors.sort = "排序必须是整数";
  }

  if (form.status !== 0 && form.status !== 1) {
    errors.status = "状态只能是启用或禁用";
  }

  return errors;
}

/** 是否有校验错误 */
export function hasBannerErrors(errors: BannerFormErrors): boolean {
  return Object.values(errors).some(Boolean);
}

/** 表单 → 创建请求 */
export function buildCreateBannerPayload(
  form: BannerForm
): CreateBannerPayload {
  return {
    title: form.title.trim(),
    image_url: form.imageUrl.trim(),
    link_url: form.linkUrl.trim(),
    sort: form.sort,
    status: form.status
  };
}

/**
 * 表单 → 更新请求。
 *
 * 提交全量可编辑字段（后端支持部分更新，全量提交语义等价且不会遗漏）。
 */
export function buildUpdateBannerPayload(
  form: BannerForm
): UpdateBannerPayload {
  return {
    title: form.title.trim(),
    image_url: form.imageUrl.trim(),
    link_url: form.linkUrl.trim(),
    sort: form.sort,
    status: form.status
  };
}
