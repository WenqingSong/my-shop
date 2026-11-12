/**
 * 分类领域 DTO。
 *
 * 依据：`api/categories/v1/categories.go`。
 *
 * 已确认的后端事实：
 * - 只有 `GET /categories`（前台公开路由）提供列表，返回**仅启用项**的树。
 * - 后台**没有**分类列表接口，禁用分类不会出现在树里。
 * - 层级上限 3 级；`parent_id = 0` 表示顶级；同级重名返回 3002。
 * - 删除：有子分类 → 3003；被商品引用 → 3005；后端不做级联删除。
 */

/** 分类 DTO（树节点） */
export interface CategoryDTO {
  id: number;
  /** 父分类 id，0 表示顶级 */
  parent_id: number;
  name: string;
  /** 同级排序值，越小越靠前 */
  sort: number;
  /** 1 启用 / 0 禁用 */
  status: number;
  created_at: string | null;
  updated_at: string | null;
  /** 树形返回时填充 */
  children?: CategoryDTO[];
}

/** `GET /categories` 响应 */
export interface CategoryTreeResult {
  items: CategoryDTO[];
}

/** 创建分类请求体 */
export interface CreateCategoryPayload {
  parent_id?: number;
  name: string;
  sort?: number;
  status?: number;
}

/** 更新分类请求体（仅提交需要变更的字段） */
export interface UpdateCategoryPayload {
  parent_id?: number;
  name?: string;
  sort?: number;
  status?: number;
}
