/**
 * 分类 API 模块。
 *
 * 真实路由（`internal/cmd/routes_admin.go` + `routes_frontend.go`）：
 * - `GET    /categories`       树形列表，**前台公开**接口，仅返回启用项
 * - `GET    /categories/:id`   详情
 * - `POST   /categories`       创建（category:create）
 * - `PUT    /categories/:id`   更新（category:update）
 * - `DELETE /categories/:id`   删除（category:delete）
 *
 * 后端没有后台分类列表接口，也没有分页；禁用分类不会出现在树中。
 */
import { http } from "@/utils/http";
import type {
  CategoryDTO,
  CategoryTreeResult,
  CreateCategoryPayload,
  UpdateCategoryPayload
} from "../types/category";

/** 树形分类列表（仅启用项） */
export function listCategories() {
  return http.get<CategoryTreeResult, undefined>("/categories", undefined, {
    silent: true
  });
}

/** 分类详情 */
export function getCategory(id: number) {
  return http.get<CategoryDTO, undefined>(`/categories/${id}`, undefined, {
    silent: true
  });
}

/** 创建分类 */
export function createCategory(payload: CreateCategoryPayload) {
  return http.post<{ id: number }, typeof payload>(
    "/categories",
    { data: payload },
    { silent: true }
  );
}

/** 更新分类 */
export function updateCategory(id: number, payload: UpdateCategoryPayload) {
  return http.request<CategoryDTO>(
    "put",
    `/categories/${id}`,
    { data: payload },
    { silent: true }
  );
}

/** 删除分类（后端拒绝有子分类或被商品引用的分类，不做级联） */
export function deleteCategory(id: number) {
  return http.request<null>("delete", `/categories/${id}`, undefined, {
    silent: true
  });
}
