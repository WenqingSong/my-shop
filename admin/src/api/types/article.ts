/**
 * 文章 DTO（`api/article/v1/article.go`）。
 *
 * 重要：后端**没有管理端文章接口**，也没有 status / 封面 / 分类字段。
 * 现有接口均为用户侧（作者本人）或公开只读：
 * - `GET /articles`（公开，分页）
 * - `GET /articles/:id`（公开，含正文）
 * - 写接口需要用户 token 且校验作者归属，管理员 token 不适用。
 *
 * 因此前台只做只读浏览；正文为 TEXT 纯文本，不做 HTML 渲染。
 */
export interface ArticleItemDTO {
  id: number;
  author_id: number;
  author_username: string;
  title: string;
  created_at: string | null;
  updated_at: string | null;
}

/** 文章详情（含正文） */
export interface ArticleDTO extends ArticleItemDTO {
  content: string;
}

/** 公开文章列表查询 */
export interface ArticleListQuery {
  page?: number;
  size?: number;
}

/** 公开文章列表响应 */
export interface ArticleListResult {
  items: ArticleItemDTO[];
  total: number;
  page: number;
  size: number;
}
