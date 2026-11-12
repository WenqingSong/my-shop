/**
 * 优惠券 Adapter。
 *
 * 后端 V1 规则：满减固定金额（threshold + discount），没有折扣率、
 * 商品范围、每人限领、领取后有效期。页面不展示不存在的维度。
 *
 * 金额：DTO 为整数分，表单为元，提交时精确转回分。
 */
import { formatCentsAsCny, parseYuanToCents } from "@/utils/money";
import { formatServerDateTime, toServerDateTime } from "@/utils/datetime";
import type { CouponTemplateDTO, CreateCouponPayload } from "../types/coupon";

type TagType = "success" | "info" | "warning" | "danger";

/** 金额上限（与后端 maxAmount 一致） */
export const COUPON_MAX_CENTS = 99_999_999;
/** 模板名最大字符数 */
export const COUPON_NAME_MAX_LENGTH = 64;

export const COUPON_STATUS_OPTIONS: Array<{ value: 0 | 1; label: string }> = [
  { value: 1, label: "可领取" },
  { value: 0, label: "已停止领取" }
];

/** 模板状态文案 */
export function formatCouponStatus(status: number): string {
  if (status === 1) return "可领取";
  if (status === 0) return "已停止领取";
  return `未知状态（${status}）`;
}

/** 模板状态标签颜色 */
export function couponStatusTagType(status: number): TagType {
  if (status === 1) return "success";
  if (status === 0) return "info";
  return "danger";
}

/** 优惠券视图 */
export interface CouponView {
  id: number;
  name: string;
  thresholdCents: number;
  thresholdText: string;
  discountCents: number;
  discountText: string;
  totalQuantity: number;
  issuedQuantity: number;
  claimRangeText: string;
  useRangeText: string;
  status: 0 | 1;
  statusLabel: string;
  statusTagType: TagType;
  createdText: string;
}

/** 时间区间展示；缺失时显示 `—` */
function rangeText(start: string | null, end: string | null): string {
  if (!start || !end) return "—";
  return `${formatServerDateTime(start)} ~ ${formatServerDateTime(end)}`;
}

/** DTO → 视图 */
export function toCouponView(dto: CouponTemplateDTO): CouponView {
  return {
    id: dto.id,
    name: dto.name,
    thresholdCents: dto.threshold_amount,
    thresholdText:
      dto.threshold_amount === 0
        ? "无门槛"
        : `满 ${formatCentsAsCny(dto.threshold_amount)} 可用`,
    discountCents: dto.discount_amount,
    discountText: formatCentsAsCny(dto.discount_amount),
    totalQuantity: dto.total_quantity,
    issuedQuantity: dto.issued_quantity,
    claimRangeText: rangeText(dto.claim_start_time, dto.claim_end_time),
    useRangeText: rangeText(dto.use_start_time, dto.use_end_time),
    status: dto.status,
    statusLabel: formatCouponStatus(dto.status),
    statusTagType: couponStatusTagType(dto.status),
    createdText: formatServerDateTime(dto.created_at)
  };
}

/** 创建表单（时间为本地 Date 区间） */
export interface CouponForm {
  name: string;
  /** 单位：元 */
  thresholdYuan: number | null;
  /** 单位：元 */
  discountYuan: number | null;
  totalQuantity: number | null;
  claimRange?: [Date, Date];
  useRange?: [Date, Date];
}

/** 空表单 */
export function emptyCouponForm(): CouponForm {
  return {
    name: "",
    thresholdYuan: 0,
    discountYuan: null,
    totalQuantity: null,
    claimRange: undefined,
    useRange: undefined
  };
}

export type CouponFormErrors = Partial<
  Record<
    "name" | "threshold" | "discount" | "quantity" | "claimRange" | "useRange",
    string
  >
>;

function validateRange(
  range: [Date, Date] | undefined,
  label: string
): string | undefined {
  if (!range || range.length !== 2) return `请选择${label}`;
  const [start, end] = range;
  if (
    !(start instanceof Date) ||
    !(end instanceof Date) ||
    !Number.isFinite(start.getTime()) ||
    !Number.isFinite(end.getTime())
  ) {
    return `${label}不合法`;
  }
  if (end.getTime() <= start.getTime()) {
    return `${label}结束时间必须晚于开始时间`;
  }
  return undefined;
}

/** 表单校验（与后端 19002 的规则对齐） */
export function validateCouponForm(form: CouponForm): CouponFormErrors {
  const errors: CouponFormErrors = {};

  const name = form.name.trim();
  if (!name) {
    errors.name = "请输入优惠券名称";
  } else if ([...name].length > COUPON_NAME_MAX_LENGTH) {
    errors.name = `名称不能超过 ${COUPON_NAME_MAX_LENGTH} 个字符`;
  }

  if (form.thresholdYuan === null || form.thresholdYuan === undefined) {
    errors.threshold = "请输入使用门槛（0 表示无门槛）";
  } else if (parseYuanToCents(form.thresholdYuan) === null) {
    errors.threshold = "门槛需为 0 ~ 999,999.99 元，最多两位小数";
  }

  if (form.discountYuan === null || form.discountYuan === undefined) {
    errors.discount = "请输入减免金额";
  } else {
    const cents = parseYuanToCents(form.discountYuan);
    if (cents === null || cents <= 0) {
      errors.discount = "减免金额需大于 0 且不超过 999,999.99 元";
    }
  }

  if (
    form.totalQuantity === null ||
    form.totalQuantity === undefined ||
    !Number.isInteger(form.totalQuantity) ||
    form.totalQuantity <= 0
  ) {
    errors.quantity = "发放总量必须是正整数";
  }

  errors.claimRange = validateRange(form.claimRange, "领取时间");
  errors.useRange = validateRange(form.useRange, "使用时间");

  return errors;
}

/** 表单 → 创建请求 */
export function buildCreateCouponPayload(
  form: CouponForm
): CreateCouponPayload {
  if (!form.claimRange || !form.useRange) {
    throw new Error("领取/使用时间不能为空");
  }
  const threshold = parseYuanToCents(form.thresholdYuan);
  const discount = parseYuanToCents(form.discountYuan);
  if (threshold === null || discount === null || !form.totalQuantity) {
    throw new Error("优惠券参数非法");
  }

  return {
    name: form.name.trim(),
    threshold_amount: threshold,
    discount_amount: discount,
    total_quantity: form.totalQuantity,
    claim_start_time: toServerDateTime(form.claimRange[0]),
    claim_end_time: toServerDateTime(form.claimRange[1]),
    use_start_time: toServerDateTime(form.useRange[0]),
    use_end_time: toServerDateTime(form.useRange[1])
  };
}

/**
 * 客户端筛选。
 *
 * 后端列表接口没有 keyword/status 参数，但会一次性返回全部模板，
 * 因此在前端对**完整数据集**过滤是真实筛选，不是"只过滤当前页"。
 */
export function filterCoupons(
  items: CouponTemplateDTO[],
  keyword: string,
  status: 0 | 1 | ""
): CouponTemplateDTO[] {
  const search = keyword.trim().toLowerCase();
  return items.filter(item => {
    if (status !== "" && item.status !== status) return false;
    if (search && !item.name.toLowerCase().includes(search)) return false;
    return true;
  });
}

/** 内存分页（数据源是后端返回的全量列表） */
export function sliceCoupons<T>(items: T[], page: number, size: number): T[] {
  const start = Math.max(0, (page - 1) * size);
  return items.slice(start, start + size);
}
