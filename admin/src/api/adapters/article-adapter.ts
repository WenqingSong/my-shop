/**
 * 文章 Adapter（只读）。
 *
 * 正文在 DB 为 `TEXT`，接口原样返回，没有 HTML 标记处理。
 * 页面按纯文本 + `white-space: pre-wrap` 展示，不注入 HTML。
 */
import { formatServerDateTime } from "@/utils/datetime";
import type { ArticleDTO, ArticleItemDTO } from "../types/article";

/** 列表行视图 */
export interface ArticleItemView {
  id: number;
  title: string;
  authorId: number;
  authorName: string;
  createdText: string;
  updatedText: string;
}

/** 详情视图 */
export interface ArticleDetailView extends ArticleItemView {
  content: string;
  /** 列表摘要（纯文本截断） */
  excerpt: string;
}

/** 列表 DTO → 视图 */
export function toArticleItemView(dto: ArticleItemDTO): ArticleItemView {
  return {
    id: dto.id,
    title: dto.title,
    authorId: dto.author_id,
    authorName: dto.author_username || `用户 #${dto.author_id}`,
    createdText: formatServerDateTime(dto.created_at),
    updatedText: formatServerDateTime(dto.updated_at)
  };
}

/** 正文摘要：去掉多余空白后截断，不改变原文 */
export function buildExcerpt(content: string, maxLength = 60): string {
  const text = (content ?? "").replace(/\s+/g, " ").trim();
  if (!text) return "（无正文）";
  return [...text].length > maxLength
    ? `${[...text].slice(0, maxLength).join("")}…`
    : text;
}

/** 详情 DTO → 视图 */
export function toArticleDetailView(dto: ArticleDTO): ArticleDetailView {
  return {
    ...toArticleItemView(dto),
    content: dto.content ?? "",
    excerpt: buildExcerpt(dto.content ?? "")
  };
}
