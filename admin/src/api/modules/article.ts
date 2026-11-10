/**
 * 文章 API 模块（**只读**）。
 *
 * 后端没有管理端文章接口：写接口要求用户 token 且校验作者本人，
 * 管理员 token 无法调用；文章表也没有 status/封面/分类字段。
 *
 * 这里只封装两个公开只读接口：
 * - `GET /articles`      分页列表（不含正文）
 * - `GET /articles/:id`  详情（含正文，纯文本）
 */
import { http } from "@/utils/http";
import type {
  ArticleDTO,
  ArticleListQuery,
  ArticleListResult
} from "../types/article";

/** 公开文章列表（分页，按 id 倒序） */
export function listArticles(query: ArticleListQuery = {}) {
  return http.get<ArticleListResult, undefined>(
    "/articles",
    { params: query },
    { silent: true }
  );
}

/** 公开文章详情 */
export function getArticle(id: number) {
  return http.get<ArticleDTO, undefined>(`/articles/${id}`, undefined, {
    silent: true
  });
}
