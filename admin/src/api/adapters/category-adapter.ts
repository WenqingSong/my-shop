/**
 * 分类 Adapter：树结构操作与表单映射。
 *
 * 后端事实：
 * - `GET /categories` 返回**仅启用项**的树，同级按 sort、id 升序；
 * - 层级上限 3 级（`maxLevel`），`parent_id = 0` 为顶级；
 * - 删除不做级联，有子分类 3003、有商品引用 3005。
 */
import type {
  CategoryDTO,
  CreateCategoryPayload,
  UpdateCategoryPayload
} from "../types/category";

/** 后端允许的最大分类层级 */
export const CATEGORY_MAX_LEVEL = 3;
/** 分类名称最大长度 */
export const CATEGORY_NAME_MAX_LENGTH = 64;

/** 扁平化后的分类行（用于表格/下拉） */
export interface CategoryRow extends CategoryDTO {
  /** 层级，顶级为 0 */
  depth: number;
  /** 父分类名称，顶级为空串 */
  parentName: string;
}

/** 先序扁平化，保留后端顺序 */
export function flattenCategoryTree(
  nodes: CategoryDTO[] = [],
  depth = 0,
  parentName = ""
): CategoryRow[] {
  const rows: CategoryRow[] = [];
  for (const node of nodes) {
    rows.push({ ...node, depth, parentName });
    if (node.children?.length) {
      rows.push(...flattenCategoryTree(node.children, depth + 1, node.name));
    }
  }
  return rows;
}

/** id → 名称映射（商品列表展示分类名用） */
export function toCategoryNameMap(
  nodes: CategoryDTO[] = []
): Map<number, string> {
  const map = new Map<number, string>();
  for (const row of flattenCategoryTree(nodes)) map.set(row.id, row.name);
  return map;
}

/** 按 id 查找节点 */
export function findCategory(
  nodes: CategoryDTO[] = [],
  id: number
): CategoryDTO | null {
  for (const node of nodes) {
    if (node.id === id) return node;
    const matched = findCategory(node.children ?? [], id);
    if (matched) return matched;
  }
  return null;
}

/** 收集自身及全部后代 id */
export function collectCategoryIds(
  nodes: CategoryDTO[] = [],
  id: number
): number[] {
  const target = findCategory(nodes, id);
  if (!target) return [id];

  const ids: number[] = [];
  const walk = (node: CategoryDTO) => {
    ids.push(node.id);
    (node.children ?? []).forEach(walk);
  };
  walk(target);
  return ids;
}

/** 分类节点是否为叶子（决定商品能否挂载；后端会再校验一次） */
export function isLeafCategory(node: CategoryDTO): boolean {
  return !node.children || node.children.length === 0;
}

export interface CategoryParentOption {
  value: number;
  label: string;
  depth: number;
  disabled: boolean;
}

/**
 * 构建父级下拉选项。
 *
 * - `excludeId` 及其后代会被禁用，防止把分类挂到自己的子树下；
 * - 达到层级上限的节点禁用（后端 maxLevel = 3）。
 */
export function buildCategoryParentOptions(
  nodes: CategoryDTO[] = [],
  excludeId?: number | null
): CategoryParentOption[] {
  const excluded = new Set(
    typeof excludeId === "number" ? collectCategoryIds(nodes, excludeId) : []
  );

  const options: CategoryParentOption[] = [
    { value: 0, label: "顶级分类", depth: 0, disabled: false }
  ];

  const walk = (list: CategoryDTO[], depth: number) => {
    for (const node of list) {
      options.push({
        value: node.id,
        label: node.name,
        depth,
        disabled: excluded.has(node.id) || depth >= CATEGORY_MAX_LEVEL
      });
      if (node.children?.length) walk(node.children, depth + 1);
    }
  };
  walk(nodes, 0);

  return options;
}

/** 分类表单 */
export interface CategoryForm {
  name: string;
  /** 0 表示顶级 */
  parentId: number;
  sort: number;
  /** 1 启用 / 0 禁用 */
  status: number;
}

/** 空表单 */
export function emptyCategoryForm(parentId = 0): CategoryForm {
  return { name: "", parentId, sort: 0, status: 1 };
}

/** DTO → 表单（编辑回显） */
export function toCategoryForm(dto: CategoryDTO): CategoryForm {
  return {
    name: dto.name,
    parentId: dto.parent_id,
    sort: dto.sort,
    status: dto.status
  };
}

/** 表单校验 */
export function validateCategoryForm(
  form: CategoryForm
): Partial<Record<"name" | "sort" | "status", string>> {
  const errors: Partial<Record<"name" | "sort" | "status", string>> = {};

  const name = form.name.trim();
  if (!name) {
    errors.name = "请输入分类名称";
  } else if ([...name].length > CATEGORY_NAME_MAX_LENGTH) {
    errors.name = `分类名称不能超过 ${CATEGORY_NAME_MAX_LENGTH} 个字符`;
  }

  if (!Number.isInteger(form.sort)) {
    errors.sort = "排序值必须是整数";
  }

  if (form.status !== 0 && form.status !== 1) {
    errors.status = "状态只能是启用或禁用";
  }

  return errors;
}

/** 表单 → 创建请求体 */
export function buildCreateCategoryPayload(
  form: CategoryForm
): CreateCategoryPayload {
  return {
    parent_id: form.parentId,
    name: form.name.trim(),
    sort: form.sort,
    status: form.status
  };
}

/** 表单 → 更新请求体 */
export function buildUpdateCategoryPayload(
  form: CategoryForm
): UpdateCategoryPayload {
  return {
    parent_id: form.parentId,
    name: form.name.trim(),
    sort: form.sort,
    status: form.status
  };
}
