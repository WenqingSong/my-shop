/**
 * SKU / 商品规格 Adapter。
 *
 * ## 为什么这里需要适配层
 * 后端 SKU 是**平铺模型**：`{ id, product_id, name, price, status }`，
 * 数据库没有规格组、规格值、SKU 编码、SKU 图片字段。
 *
 * 前端按完整规格矩阵交互（规格组 → 规格值 → 笛卡尔组合 → SKU 行），
 * 再转换回后端能持久化的字段：
 * - **可持久化**：`name`（由规格值拼接）、`price`、`status`。
 * - **PROVISIONAL（不持久化）**：规格组名称、规格值结构、SKU 编码、SKU 图片。
 *
 * 规格结构的恢复方式是"解析 SKU 名称"（分隔符 ` / `），
 * 这在前端是可解释的，但**不是后端契约**，不能当成权威数据。
 */
import type { SkuDTO, SkuStatus } from "../types/sku";

/** 规格值在 SKU 名称中的分隔符（仅前端约定） */
export const SKU_NAME_SEPARATOR = " / ";

/** SKU 价格上限（后端约束，整数分） */
export const SKU_PRICE_MAX_CENTS = 99_999_999;
/** SKU 名称最大长度 */
export const SKU_NAME_MAX_LENGTH = 128;
/** 无规格商品的默认 SKU 名称 */
export const DEFAULT_SKU_NAME = "默认规格";

/** 规格组草稿（PROVISIONAL：名称与结构不会提交给后端） */
export interface SpecGroupDraft {
  /** 前端行标识，不提交 */
  key: string;
  /** 规格组名称，例如「颜色」（PROVISIONAL） */
  name: string;
  /** 规格值，例如 `["黑", "白"]` */
  values: string[];
}

/** SKU 草稿行 */
export interface SkuDraft {
  /** 前端行标识（组合键），不提交 */
  key: string;
  /** 规格值（与规格组顺序对应；简单模式为空数组） */
  values: string[];
  /** 将提交给后端的 SKU 名称 */
  name: string;
  /** 价格：整数分 */
  priceCents: number;
  status: SkuStatus;
}

let specGroupSeed = 0;

/** 新建一个规格组草稿 */
export function createSpecGroup(
  name = "",
  values: string[] = [""]
): SpecGroupDraft {
  specGroupSeed += 1;
  return { key: `spec-${specGroupSeed}`, name, values };
}

/** 清洗规格值：去空白、去重、去空 */
export function normalizeSpecValues(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const raw of values) {
    const value = raw.trim();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    result.push(value);
  }
  return result;
}

/**
 * 规格组 → 全部组合（笛卡尔积）。
 *
 * 没有任何有效规格组时返回空数组（表示"商品无多规格"）。
 */
export function generateCombinations(groups: SpecGroupDraft[]): string[][] {
  const usable = groups
    .map(group => ({
      name: group.name.trim(),
      values: normalizeSpecValues(group.values)
    }))
    .filter(group => group.values.length > 0);

  if (usable.length === 0) return [];

  return usable.reduce<string[][]>(
    (acc, group) => {
      const next: string[][] = [];
      for (const prefix of acc) {
        for (const value of group.values) next.push([...prefix, value]);
      }
      return next;
    },
    [[]]
  );
}

/** 规格值 → SKU 名称 */
export function buildSkuName(values: string[]): string {
  const cleaned = values.map(value => value.trim()).filter(Boolean);
  return cleaned.length > 0
    ? cleaned.join(SKU_NAME_SEPARATOR)
    : DEFAULT_SKU_NAME;
}

/** SKU 名称 → 规格值（无法解析时返回单元素数组） */
export function parseSkuName(name: string): string[] {
  const parts = name
    .split(SKU_NAME_SEPARATOR)
    .map(part => part.trim())
    .filter(Boolean);
  return parts.length > 0 ? parts : [name.trim()];
}

/** 由 SKU 名称反推规格组（PROVISIONAL，仅用于回显） */
export function inferSpecGroups(skus: SkuDTO[]): SpecGroupDraft[] {
  if (skus.length === 0) return [];

  const parsed = skus.map(sku => parseSkuName(sku.name));
  const dimensions = new Set(parsed.map(values => values.length));

  // 各 SKU 的规格段数不一致：无法还原矩阵，退化为单组（值 = 完整名称）
  if (dimensions.size > 1) {
    return [
      createSpecGroup(
        "规格",
        skus.map(sku => sku.name)
      )
    ];
  }

  const dimension = parsed[0].length;
  if (dimension <= 1) return [];

  return Array.from({ length: dimension }, (_, index) =>
    createSpecGroup(
      `规格 ${index + 1}`,
      parsed.map(values => values[index])
    )
  );
}

/** 空价格时的回退值 */
function resolvePrice(priceCents: number | undefined): number {
  if (typeof priceCents !== "number" || !Number.isFinite(priceCents)) return 0;
  return Math.min(Math.max(Math.round(priceCents), 0), SKU_PRICE_MAX_CENTS);
}

/**
 * 规格组 → SKU 草稿列表。
 *
 * 已存在同名 SKU 时沿用其价格与状态，避免重新生成后丢失人工填写。
 */
export function generateSkuDrafts(
  groups: SpecGroupDraft[],
  options: { defaultPriceCents?: number; existing?: SkuDTO[] } = {}
): SkuDraft[] {
  const combinations = generateCombinations(groups);
  const existingByName = new Map(
    (options.existing ?? []).map(sku => [sku.name.trim(), sku])
  );

  if (combinations.length === 0) {
    return [createDefaultSkuDraft(options.defaultPriceCents)];
  }

  return combinations.map(values => {
    const name = buildSkuName(values);
    const matched = existingByName.get(name);
    return {
      key: values.join("\u0001"),
      values,
      name,
      priceCents: matched
        ? matched.price
        : resolvePrice(options.defaultPriceCents),
      status: matched ? matched.status : "enabled"
    };
  });
}

/** 无规格商品的默认 SKU */
export function createDefaultSkuDraft(priceCents = 0): SkuDraft {
  return {
    key: "__default__",
    values: [],
    name: DEFAULT_SKU_NAME,
    priceCents: resolvePrice(priceCents),
    status: "enabled"
  };
}

/**
 * 商品详情中的 SKU → 编辑器初始状态。
 *
 * 关键点：**不重新生成组合**，直接把后端已有的 SKU 逐行映射，
 * 保证编辑回显时不凭空多出或丢失 SKU。
 */
export function buildSkuEditorState(
  skus: SkuDTO[],
  productPriceCents = 0
): { groups: SpecGroupDraft[]; drafts: SkuDraft[] } {
  if (skus.length === 0) {
    return {
      groups: [],
      drafts: [createDefaultSkuDraft(productPriceCents)]
    };
  }

  return {
    groups: inferSpecGroups(skus),
    drafts: skus.map(sku => ({
      key: sku.name.trim(),
      values: parseSkuName(sku.name),
      name: sku.name,
      priceCents: sku.price,
      status: sku.status
    }))
  };
}

/** SKU 草稿校验（纯函数） */
export function validateSkuDrafts(drafts: SkuDraft[]): string[] {
  const errors: string[] = [];

  if (drafts.length === 0) {
    errors.push("至少需要一行 SKU");
    return errors;
  }

  const seen = new Set<string>();
  drafts.forEach((draft, index) => {
    const rowLabel = `第 ${index + 1} 行 SKU`;
    const name = draft.name.trim();

    if (!name) {
      errors.push(`${rowLabel}：请输入 SKU 名称`);
    } else if ([...name].length > SKU_NAME_MAX_LENGTH) {
      errors.push(
        `${rowLabel}：SKU 名称不能超过 ${SKU_NAME_MAX_LENGTH} 个字符`
      );
    } else if (seen.has(name)) {
      errors.push(`SKU 名称重复：${name}`);
    }
    if (name) seen.add(name);

    if (
      !Number.isInteger(draft.priceCents) ||
      draft.priceCents < 0 ||
      draft.priceCents > SKU_PRICE_MAX_CENTS
    ) {
      errors.push(`${rowLabel}：SKU 价格需为 0 ~ 999,999.99 元`);
    }
  });

  return errors;
}

/** 同步计划 */
export interface SkuSyncPlan {
  create: SkuDraft[];
  update: Array<{
    draft: SkuDraft;
    existing: SkuDTO;
    /** 只包含真正变化的字段 */
    payload: { price?: number; status?: SkuStatus };
  }>;
  remove: SkuDTO[];
}

/**
 * 计算 SKU 同步计划。
 *
 * 以 `name`（去空白）作为匹配键：
 * - 草稿名不存在 → 创建；
 * - 已存在且价格/状态/名称有变化 → 更新；
 * - 后端有但草稿没有 → 删除（后端可能因库存记录拒绝，需展示错误）。
 */
export function planSkuSync(
  drafts: SkuDraft[],
  existing: SkuDTO[]
): SkuSyncPlan {
  const existingByName = new Map(existing.map(sku => [sku.name.trim(), sku]));
  const draftNames = new Set(drafts.map(draft => draft.name.trim()));

  const plan: SkuSyncPlan = { create: [], update: [], remove: [] };

  for (const draft of drafts) {
    const name = draft.name.trim();
    const matched = existingByName.get(name);
    if (!matched) {
      plan.create.push({ ...draft, name });
      continue;
    }

    const payload: { price?: number; status?: SkuStatus } = {};
    if (matched.price !== draft.priceCents) payload.price = draft.priceCents;
    if (matched.status !== draft.status) payload.status = draft.status;

    if (Object.keys(payload).length > 0) {
      plan.update.push({ draft, existing: matched, payload });
    }
  }

  for (const sku of existing) {
    if (!draftNames.has(sku.name.trim())) plan.remove.push(sku);
  }

  return plan;
}

/** SKU 状态文案 */
export function formatSkuStatus(status: SkuStatus): string {
  return status === "enabled" ? "启用" : "禁用";
}
