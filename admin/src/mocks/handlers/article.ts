import { http } from "msw";
import { mockDb } from "./mock-db";
import { fail, ok, queryInt } from "./shared";

/**
 * 文章 Mock（只读）。
 *
 * 后端只有公开 GET 列表/详情；没有管理端写接口。
 * 列表不含正文，详情含正文（纯文本）。
 */
export const articleHandlers = [
  /** GET /articles */
  http.get("*/api/articles", ({ request }) => {
    const url = new URL(request.url);
    const page = queryInt(url.searchParams, "page", 1);
    const size = Math.min(queryInt(url.searchParams, "size", 20), 100);

    const sorted = [...mockDb.articles].sort(
      (left, right) => right.id - left.id
    );
    const total = sorted.length;
    const start = (page - 1) * size;

    const items = sorted.slice(start, start + size).map(item => ({
      id: item.id,
      author_id: item.author_id,
      author_username: item.author_username,
      title: item.title,
      created_at: item.created_at,
      updated_at: item.updated_at
    }));

    return ok({ items, total, page, size });
  }),

  /** GET /articles/:id */
  http.get("*/api/articles/:id", ({ params }) => {
    const article = mockDb.articles.find(item => item.id === Number(params.id));
    if (!article) return fail(16001, "文章不存在", 404);
    return ok({ ...article });
  })
];
