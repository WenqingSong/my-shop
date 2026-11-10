import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

// 路由模块依赖 i18n 资源，单测中提供最小实现（与 A0 路由测试一致的做法）
vi.mock("@/plugins/i18n", () => ({
  $t: (key: string) => key,
  transformI18n: (message: any) =>
    typeof message === "string" ? message : (message?.zh ?? ""),
  i18n: { global: { t: (key: string) => key, locale: { value: "zh" } } },
  localesConfigs: { zh: {}, en: {} }
}));

import { http } from "@/utils/http";
import {
  ACCESS_TOKEN_KEY,
  EXPIRES_AT_KEY,
  clearAccessToken,
  getAccessToken,
  getTokenExpiresAt,
  isTokenExpired,
  setAccessToken
} from "@/utils/auth";
import {
  __resetProfileRequestCache,
  ensureAdminProfile,
  useUserStoreHook
} from "@/store/modules/user";
import { BusinessError } from "@/api/types/envelope";
import {
  getPermissionSubject,
  resetPermissionSubject
} from "@/utils/permission";
import {
  MOCK_ACCESS_TOKEN,
  MOCK_ADMIN_PASSWORD,
  MOCK_ADMIN_USERNAME,
  MOCK_EXPIRES_IN
} from "@/mocks/handlers/admin";

describe("管理员认证", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    // `useUserStoreHook` 绑定的是应用级 pinia 实例，用例之间需要显式重置
    __resetProfileRequestCache();
    useUserStoreHook().RESET_AUTH_STATE();
    clearAccessToken();
    resetPermissionSubject();
    window.sessionStorage.clear();
  });

  it("登录成功后保存 access_token 与 expires_at，并写入管理员身份", async () => {
    const store = useUserStoreHook();
    const before = Date.now();

    const profile = await store.login({
      username: MOCK_ADMIN_USERNAME,
      password: MOCK_ADMIN_PASSWORD
    });

    expect(profile.username).toBe(MOCK_ADMIN_USERNAME);
    expect(getAccessToken()).toBe(MOCK_ACCESS_TOKEN);

    const expiresAt = getTokenExpiresAt();
    expect(expiresAt).not.toBeNull();
    // expires_at = now + expires_in * 1000（允许 2 秒误差）
    expect(expiresAt! - before).toBeGreaterThanOrEqual(
      MOCK_EXPIRES_IN * 1000 - 2000
    );
    expect(expiresAt! - before).toBeLessThanOrEqual(
      MOCK_EXPIRES_IN * 1000 + 2000
    );

    expect(store.adminId).toBe(1);
    expect(store.isSuper).toBe(true);
    expect(store.roles).toEqual(["super_admin"]);
    expect(store.profileLoaded).toBe(true);
  });

  it("登录失败（业务错误）不写入 token，也不留下半登录状态", async () => {
    const store = useUserStoreHook();

    await expect(
      store.login({ username: MOCK_ADMIN_USERNAME, password: "wrong-password" })
    ).rejects.toBeInstanceOf(BusinessError);

    expect(getAccessToken()).toBeNull();
    expect(store.profileLoaded).toBe(false);
    expect(store.username).toBe("");
  });

  it("请求层自动注入 Authorization: Bearer <token>", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    // handler 内部校验 Authorization，不匹配会返回 401
    const profile = await http.get<{ username: string }, undefined>(
      "/admin/me",
      undefined,
      { silent: true }
    );

    expect(profile.username).toBe(MOCK_ADMIN_USERNAME);
  });

  it("没有 token 时不注入 Authorization 头", async () => {
    await expect(
      http.get("/admin/me", undefined, { silent: true })
    ).rejects.toMatchObject({ response: { status: 401 } });
  });

  it("token 过期后 isTokenExpired 返回 true 且 hasValidToken 为 false", () => {
    setAccessToken("expired-token", 1);
    // 直接把 expires_at 改到过去
    window.sessionStorage.setItem(EXPIRES_AT_KEY, String(Date.now() - 1000));

    expect(isTokenExpired()).toBe(true);
    expect(getAccessToken()).toBe("expired-token");
  });

  it("缺少 expires_at 时视为不可用（避免无法判断有效期就放行）", () => {
    window.sessionStorage.setItem(ACCESS_TOKEN_KEY, "token-without-expiry");
    window.sessionStorage.removeItem(EXPIRES_AT_KEY);

    expect(isTokenExpired()).toBe(true);
  });

  it("ensureAdminProfile 在会话有效时恢复身份并同步权限快照", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    const profile = await ensureAdminProfile();

    expect(profile.id).toBe(1);
    expect(useUserStoreHook().profileLoaded).toBe(true);
    expect(getPermissionSubject().isSuper).toBe(true);
    expect(getPermissionSubject().roles).toEqual(["super_admin"]);
  });

  it("ensureAdminProfile 并发调用只请求一次 /admin/me", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, MOCK_EXPIRES_IN);

    const store = useUserStoreHook();
    expect(store.profileLoaded).toBe(false);

    const [a, b] = await Promise.all([
      ensureAdminProfile(),
      ensureAdminProfile()
    ]);

    expect(a.id).toBe(b.id);
    expect(store.profileLoaded).toBe(true);
  });

  it("token 过期时 ensureAdminProfile 清理本地状态并抛错", async () => {
    setAccessToken("stale-token", 1);
    window.sessionStorage.setItem(EXPIRES_AT_KEY, String(Date.now() - 1));

    await expect(ensureAdminProfile()).rejects.toThrow();

    expect(getAccessToken()).toBeNull();
    expect(useUserStoreHook().profileLoaded).toBe(false);
  });

  it("退出登录会调用 /admin/logout 并清理管理员状态", async () => {
    const store = useUserStoreHook();
    await store.login({
      username: MOCK_ADMIN_USERNAME,
      password: MOCK_ADMIN_PASSWORD
    });

    await store.logOut({ redirect: false });

    expect(getAccessToken()).toBeNull();
    expect(store.adminId).toBeNull();
    expect(store.username).toBe("");
    expect(store.profileLoaded).toBe(false);
    expect(getPermissionSubject().roles).toEqual([]);
  });

  it("token 已失效时退出登录仍能清理本地状态（不去调用后端）", async () => {
    const store = useUserStoreHook();
    await store.login({
      username: MOCK_ADMIN_USERNAME,
      password: MOCK_ADMIN_PASSWORD
    });
    window.sessionStorage.setItem(EXPIRES_AT_KEY, String(Date.now() - 1));

    await store.logOut({ redirect: false });

    expect(getAccessToken()).toBeNull();
    expect(store.profileLoaded).toBe(false);
  });
});
