import { describe, expect, it, vi } from "vitest";

// 路由定义只依赖 `$t` 做标题占位，单元测试中无需加载完整 i18n 资源
vi.mock("@/plugins/i18n", () => ({
  $t: (key: string) => key,
  transformI18n: (message: any) =>
    typeof message === "string" ? message : (message?.zh ?? ""),
  i18n: {
    global: { t: (key: string) => key, locale: { value: "zh" } }
  },
  localesConfigs: { zh: {}, en: {} }
}));

import { getHistoryMode } from "@/router/history";
import home from "@/router/modules/home";
import remaining from "@/router/modules/remaining";

describe("管理端路由", () => {
  it("首页重定向到 /dashboard", () => {
    expect(home.path).toBe("/");
    expect(home.redirect).toBe("/dashboard");
  });

  it("首页子路由包含 /dashboard 与 Dashboard 组件", () => {
    const dashboard = home.children.find(item => item.path === "/dashboard");

    expect(dashboard).toBeDefined();
    expect(dashboard?.name).toBe("Dashboard");
    expect(typeof dashboard?.component).toBe("function");
  });

  it("包含 /login 路由且不参与菜单展示", () => {
    const login = remaining.find(item => item.path === "/login");

    expect(login).toBeDefined();
    expect(login?.meta?.showLink).toBe(false);
  });

  it("保留 403 / 500 全屏页与 redirect 中转路由", () => {
    const paths = remaining.map(item => item.path);

    expect(paths).toContain("/access-denied");
    expect(paths).toContain("/server-error");
    expect(paths).toContain("/redirect");
  });

  it("路由使用与 /console/ 兼容的 history 模式并携带 base", () => {
    // vue-router 会去掉 base 末尾的斜杠
    expect(getHistoryMode("h5,/console/").base).toBe("/console");
  });

  it("历史模式配置缺失时回退为 h5（无 base）", () => {
    expect(getHistoryMode(undefined as unknown as string).base).toBe("");
  });
});
