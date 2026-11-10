import { beforeEach, describe, expect, it } from "vitest";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import * as articleModule from "@/api/modules/article";
import { getArticle, listArticles } from "@/api/modules/article";
import {
  buildExcerpt,
  toArticleDetailView,
  toArticleItemView
} from "@/api/adapters/article-adapter";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("文章内容模块（只读）", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("公开列表按 id 倒序并支持服务端分页", async () => {
    const first = await listArticles({ page: 1, size: 2 });
    const second = await listArticles({ page: 2, size: 2 });

    expect(first.total).toBe(4);
    expect(first.items.map(item => item.id)).toEqual([8404, 8403]);
    expect(second.items.map(item => item.id)).toEqual([8402, 8401]);
    expect(first.items[0]).not.toHaveProperty("content");
  });

  it("详情包含纯文本正文", async () => {
    const detail = await getArticle(8401);
    const view = toArticleDetailView(detail);

    expect(view.title).toBe("SurgeCart 选购指南");
    expect(view.authorName).toBe("u101");
    expect(view.content).toContain("\n");
    expect(view.excerpt).toContain("第一步");
  });

  it("文章不存在返回 16001", async () => {
    await expectBusinessCode(getArticle(999999), 16001);
  });

  it("列表视图映射作者与时间", () => {
    const view = toArticleItemView({
      id: 1,
      author_id: 101,
      author_username: "u101",
      title: "标题",
      created_at: "2026-11-01 10:00:00",
      updated_at: "2026-11-02 11:00:00"
    });

    expect(view.authorName).toBe("u101");
    expect(view.createdText).toBe("2026-11-01 10:00:00");
  });

  it("作者名为空时回退为用户 id", () => {
    const view = toArticleItemView({
      id: 1,
      author_id: 102,
      author_username: "",
      title: "标题",
      created_at: null,
      updated_at: null
    });

    expect(view.authorName).toBe("用户 #102");
    expect(view.createdText).toBe("—");
  });

  it("摘要压缩空白并截断，不改变正文", () => {
    expect(buildExcerpt("a\n\n  b   c")).toBe("a b c");
    expect(buildExcerpt("x".repeat(80), 10)).toBe(`${"x".repeat(10)}…`);
    expect(buildExcerpt("")).toBe("（无正文）");
  });

  it("模块不提供任何文章写接口（后端无管理端能力）", () => {
    expect("createArticle" in articleModule).toBe(false);
    expect("updateArticle" in articleModule).toBe(false);
    expect("deleteArticle" in articleModule).toBe(false);
    expect("publishArticle" in articleModule).toBe(false);
  });
});
