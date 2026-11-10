/**
 * 秒杀活动 Adapter。
 *
 * 后端只有创建/更新，没有查询接口，因此：
 * - 表单模型只覆盖后端 DTO 字段；
 * - 更新支持部分字段（name/status/时间/SKU 绑定）；
 * - SKU 绑定更新只允许已绑定项，新增绑定只能在创建时完成。
 *
 * 该模块**不触碰**秒杀交易链路：不修改 Redis 库存、不写 Kafka/Outbox、
 * 不修改请求状态，只构造活动配置请求。
 */
import { formatCentsAsCny, parseYuanToCents } from "@/utils/money";
import { formatServerDateTime, toServerDateTime } from "@/utils/datetime";
import type {
  ActivityDTO,
  ActivitySkuInput,
  CreateActivityPayload,
  FlashSaleStatus,
  UpdateActivityPayload
} from "../types/flash-sale";

type TagType = "success" | "info" | "warning" | "danger";

export const FLASH_SALE_STATUS_OPTIONS: Array<{
  value: FlashSaleStatus;
  label: string;
}> = [
  { value: "enabled", label: "启用" },
  { value: "disabled", label: "禁用" }
];

/** 活动名最大字符数 */
export const FLASH_SALE_NAME_MAX_LENGTH = 64;
/** 秒杀价上限（整数分） */
export const FLASH_SALE_PRICE_MAX_CENTS = 99_999_999;

/** 状态文案 */
export function formatFlashSaleStatus(status: string): string {
  if (status === "enabled") return "启用";
  if (status === "disabled") return "禁用";
  return `未知状态（${status}）`;
}

/** 状态标签颜色 */
export function flashSaleStatusTagType(status: string): TagType {
  if (status === "enabled") return "success";
  if (status === "disabled") return "info";
  return "danger";
}

/** SKU 绑定视图 */
export interface ActivitySkuView {
  id: number;
  skuId: number;
  flashPriceCents: number;
  flashPriceText: string;
  totalStock: number;
  sold: number;
}

/** 活动视图 */
export interface ActivityView {
  id: number;
  name: string;
  status: FlashSaleStatus;
  statusLabel: string;
  statusTagType: TagType;
  startText: string;
  endText: string;
  createdText: string;
  updatedText: string;
  skus: ActivitySkuView[];
  /** Σ total_stock（真实绑定字段求和，仅展示） */
  totalStock: number;
  /** Σ sold（真实绑定字段求和，仅展示） */
  totalSold: number;
}

/** DTO → 视图 */
export function toActivityView(dto: ActivityDTO): ActivityView {
  const skus = (dto.skus ?? []).map(sku => ({
    id: sku.id,
    skuId: sku.sku_id,
    flashPriceCents: sku.flash_price,
    flashPriceText: formatCentsAsCny(sku.flash_price),
    totalStock: sku.total_stock,
    sold: sku.sold
  }));

  return {
    id: dto.id,
    name: dto.name,
    status: dto.status,
    statusLabel: formatFlashSaleStatus(dto.status),
    statusTagType: flashSaleStatusTagType(dto.status),
    startText: formatServerDateTime(dto.start_time),
    endText: formatServerDateTime(dto.end_time),
    createdText: formatServerDateTime(dto.created_at),
    updatedText: formatServerDateTime(dto.updated_at),
    skus,
    totalStock: skus.reduce((sum, item) => sum + item.totalStock, 0),
    totalSold: skus.reduce((sum, item) => sum + item.sold, 0)
  };
}

/** SKU 绑定草稿 */
export interface FlashSaleBindingForm {
  key: string;
  /** 仅展示用，不提交 */
  productId: number | null;
  productName: string;
  skuId: number | null;
  skuName: string;
  /** 单位：元 */
  flashPriceYuan: number | null;
  totalStock: number | null;
}

/** 创建表单 */
export interface FlashSaleCreateForm {
  name: string;
  range?: [Date, Date];
  bindings: FlashSaleBindingForm[];
}

/** 更新表单（部分更新） */
export interface FlashSaleUpdateForm {
  id: number | null;
  name: string;
  status: FlashSaleStatus | "";
  range?: [Date, Date];
  /** 仅用于更新已绑定 SKU 的秒杀价/库存 */
  bindings: FlashSaleBindingForm[];
}

let bindingSeed = 0;

/** 新建一行绑定草稿 */
export function createBindingForm(): FlashSaleBindingForm {
  bindingSeed += 1;
  return {
    key: `binding-${bindingSeed}`,
    productId: null,
    productName: "",
    skuId: null,
    skuName: "",
    flashPriceYuan: null,
    totalStock: null
  };
}

function validateTimeRange(range?: [Date, Date]): string | undefined {
  if (!range || range.length !== 2) return "请选择活动开始/结束时间";
  const [start, end] = range;
  if (
    !(start instanceof Date) ||
    !(end instanceof Date) ||
    !Number.isFinite(start.getTime()) ||
    !Number.isFinite(end.getTime())
  ) {
    return "活动时间不合法";
  }
  if (end.getTime() <= start.getTime()) {
    return "活动结束时间必须晚于开始时间";
  }
  return undefined;
}

/**
 * 校验 SKU 绑定列表。
 *
 * 后端规则：非空、SKU 存在、无重复、秒杀价 >0、库存正整数。
 * "SKU 存在"由后端校验，前端只做格式与重复校验。
 */
export function validateBindings(bindings: FlashSaleBindingForm[]): string[] {
  const errors: string[] = [];
  if (bindings.length === 0) {
    errors.push("至少需要绑定一个 SKU");
    return errors;
  }

  const seen = new Set<number>();
  bindings.forEach((binding, index) => {
    const row = `第 ${index + 1} 个绑定`;
    if (!binding.skuId || binding.skuId <= 0) {
      errors.push(`${row}：请选择 SKU`);
    } else if (seen.has(binding.skuId)) {
      errors.push(`${row}：SKU ID ${binding.skuId} 重复`);
    } else {
      seen.add(binding.skuId);
    }

    const price = parseYuanToCents(binding.flashPriceYuan);
    if (price === null || price <= 0) {
      errors.push(`${row}：秒杀价需大于 0 且最多两位小数`);
    }

    if (
      binding.totalStock === null ||
      binding.totalStock === undefined ||
      !Number.isInteger(binding.totalStock) ||
      binding.totalStock <= 0
    ) {
      errors.push(`${row}：秒杀库存必须是正整数`);
    }
  });

  return errors;
}

/** 绑定草稿 → 请求输入 */
export function buildSkuInputs(
  bindings: FlashSaleBindingForm[]
): ActivitySkuInput[] {
  return bindings.map(binding => {
    const price = parseYuanToCents(binding.flashPriceYuan);
    if (price === null || !binding.skuId || !binding.totalStock) {
      throw new Error("SKU 绑定参数非法");
    }
    return {
      sku_id: binding.skuId,
      flash_price: price,
      total_stock: binding.totalStock
    };
  });
}

/** 创建表单校验 */
export function validateCreateForm(form: FlashSaleCreateForm): string[] {
  const errors: string[] = [];

  const name = form.name.trim();
  if (!name) {
    errors.push("请输入活动名称");
  } else if ([...name].length > FLASH_SALE_NAME_MAX_LENGTH) {
    errors.push(`活动名称不能超过 ${FLASH_SALE_NAME_MAX_LENGTH} 个字符`);
  }

  const timeError = validateTimeRange(form.range);
  if (timeError) errors.push(timeError);

  errors.push(...validateBindings(form.bindings));
  return errors;
}

/** 创建表单 → 请求 */
export function buildCreatePayload(
  form: FlashSaleCreateForm
): CreateActivityPayload {
  if (!form.range) throw new Error("活动时间不能为空");
  return {
    name: form.name.trim(),
    start_time: toServerDateTime(form.range[0]),
    end_time: toServerDateTime(form.range[1]),
    skus: buildSkuInputs(form.bindings)
  };
}

/** 更新表单校验（至少提交一项变更） */
export function validateUpdateForm(form: FlashSaleUpdateForm): string[] {
  const errors: string[] = [];

  if (!form.id || form.id <= 0) errors.push("请输入有效的活动 ID");

  const name = form.name.trim();
  if (name && [...name].length > FLASH_SALE_NAME_MAX_LENGTH) {
    errors.push(`活动名称不能超过 ${FLASH_SALE_NAME_MAX_LENGTH} 个字符`);
  }

  if (form.range) {
    const timeError = validateTimeRange(form.range);
    if (timeError) errors.push(timeError);
  }

  if (form.bindings.length > 0) {
    errors.push(...validateBindings(form.bindings));
  }

  const hasChange =
    Boolean(name) ||
    form.status !== "" ||
    Boolean(form.range) ||
    form.bindings.length > 0;
  if (!hasChange) errors.push("请至少填写一项要更新的内容");

  return errors;
}

/**
 * 更新表单 → 部分更新请求。
 *
 * 时间要么都不传（保持原值），要么同时传开始与结束，避免只改一端
 * 造成后端使用旧值拼出非法区间。
 */
export function buildUpdatePayload(
  form: FlashSaleUpdateForm
): UpdateActivityPayload {
  const payload: UpdateActivityPayload = {};

  const name = form.name.trim();
  if (name) payload.name = name;
  if (form.status) payload.status = form.status;
  if (form.range) {
    payload.start_time = toServerDateTime(form.range[0]);
    payload.end_time = toServerDateTime(form.range[1]);
  }
  if (form.bindings.length > 0) {
    payload.skus = buildSkuInputs(form.bindings);
  }

  return payload;
}
