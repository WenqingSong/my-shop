import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";

vi.mock("@/plugins/i18n", () => ({
  $t: (key: string) => key,
  transformI18n: (message: any) =>
    typeof message === "string" ? message : (message?.zh ?? ""),
  i18n: { global: { t: (key: string) => key, locale: { value: "zh" } } },
  localesConfigs: { zh: {}, en: {} }
}));

import { HOME_PATH, LOGIN_PATH, resolveAuthGuard } from "@/router/guard";
import {
  __resetProfileRequestCache,
  useUserStoreHook
} from "@/store/modules/user";
import { clearAccessToken, getAccessToken, setAccessToken } from "@/utils/auth";
import {
  getPermissionSubject,
  resetPermissionSubject
} from "@/utils/permission";
import { MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN } from "@/mocks/handlers/admin";

const dashboard = { path: "/dashboard", fullPath: "/dashboard" };
const login = { path: LOGIN_PATH, fullPath: LOGIN_PATH };
const root = { path: "/", fullPath: "/" };

describe("路由认证守卫决策", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    __resetProfileRequestCache();
    useUserStoreHook().RESET_AUTH_STATE();
    resetPermissionSubject();
    clearAccessToken();
    window.sessionStorage.clear();
  });

  it("未登录访问工作台时跳转登录页并保留 redirect", async () => {
    const decision = await resolveAuthGuard(dashboard);

    expect(decision).toEqual({
      type: "redirect",
      path: LOGIN_PATH,
      query: { redirect: "/dashboard" },
      replace: true
    });
  });

  it("访问根路径时不记录 redirect（避免登录后再被重定向一次）", async () => {
    const decision = await resolveAuthGuard(root);

    expect(decision.type).toBe("redirect");
    expect((decision as any).query).toBeUndefined();
  });

  it("已登录访问登录页时跳转工作台", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    const decision = await resolveAuthGuard(login);

    expect(decision).toEqual({
      type: "redirect",
      path: HOME_PATH,
      replace: true
    });
    expect(useUserStoreHook().profileLoaded).toBe(true);
  });

  it("token 存在但服务端拒绝时清理登录态并停留在登录页", async () => {
    setAccessToken("revoked-token", MOCK_EXPIRES_IN);

    const decision = await resolveAuthGuard(login);

    expect(decision.type).toBe("allow");
    expect(getAccessToken()).toBeNull();
    expect(useUserStoreHook().profileLoaded).toBe(false);
  });

  it("有效会话访问工作台时放行并同步权限快照", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    const decision = await resolveAuthGuard(dashboard);

    expect(decision.type).toBe("allow");
    expect(useUserStoreHook().profileLoaded).toBe(true);
    expect(getPermissionSubject().isSuper).toBe(true);
  });

  it("本地 token 已过期时不发请求直接回登录页", async () => {
    let meCalls = 0;
    server.use(
      mswHttp.get("*/api/admin/me", () => {
        meCalls += 1;
        return HttpResponse.json({ code: 0, message: "OK", data: {} });
      })
    );
    setAccessToken("expired", MOCK_EXPIRES_IN);
    window.sessionStorage.setItem(
      "surgecart-admin:expires_at",
      String(Date.now() - 1)
    );

    const decision = await resolveAuthGuard(dashboard);

    expect(decision.type).toBe("redirect");
    expect(meCalls).toBe(0);
    expect(getAccessToken()).toBeNull();
  });

  it("初始化阶段重复调用只请求一次 /admin/me", async () => {
    let meCalls = 0;
    server.use(
      mswHttp.get("*/api/admin/me", () => {
        meCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "OK",
          data: {
            id: 1,
            username: "admin",
            is_super: true,
            roles: ["super_admin"]
          }
        });
      })
    );
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    const [first, second] = await Promise.all([
      resolveAuthGuard(dashboard),
      resolveAuthGuard(dashboard)
    ]);

    expect(first.type).toBe("allow");
    expect(second.type).toBe("allow");
    expect(meCalls).toBe(1);
  });

  it("已加载身份后再次进入不再重复请求", async () => {
    let meCalls = 0;
    server.use(
      mswHttp.get("*/api/admin/me", () => {
        meCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "OK",
          data: { id: 1, username: "admin", is_super: true, roles: [] }
        });
      })
    );
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    await resolveAuthGuard(dashboard);
    await resolveAuthGuard(dashboard);

    expect(meCalls).toBe(1);
  });

  it("服务端 401 时清理登录态并跳转登录页", async () => {
    setAccessToken("invalid-token", MOCK_EXPIRES_IN);

    const decision = await resolveAuthGuard(dashboard);

    expect(decision).toEqual({
      type: "redirect",
      path: LOGIN_PATH,
      query: { redirect: "/dashboard" },
      replace: true
    });
    expect(useUserStoreHook().profileLoaded).toBe(false);
  });
});
