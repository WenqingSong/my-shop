import { clearAccessToken, getAccessToken, isTokenExpired } from "@/utils/auth";
import { ensureAdminProfile, useUserStoreHook } from "@/store/modules/user";

/**
 * 路由认证守卫的**决策逻辑**。
 *
 * 独立于 `vue-router` 的导航回调，原因是：
 * 1. 决策本身是可枚举的（放行 / 跳登录 / 跳工作台），适合直接单元测试；
 * 2. `router/index.ts` 只负责把决策翻译成路由返回值，避免守卫内部塞满分支。
 *
 * 认证事实来源：
 * - "是否可能已登录"：只看 sessionStorage 中 token 是否存在且未过期（本地时效，不校验签名）。
 * - "到底是谁"：必须由 `GET /admin/me` 确认，**不解析 JWT 内容**作为身份依据。
 */

/** 登录页路径 */
export const LOGIN_PATH = "/login";
/** 登录后的默认落地页 */
export const HOME_PATH = "/dashboard";

export type AuthGuardDecision =
  | { type: "allow" }
  | {
      type: "redirect";
      path: string;
      query?: Record<string, string>;
      replace: true;
    };

/** 本地是否持有"看起来仍有效"的 token */
export function hasLocalToken(): boolean {
  return getAccessToken() !== null && !isTokenExpired();
}

/** 清理本地登录态：token + Pinia 管理员信息 + 权限快照 */
export function clearLocalSession() {
  clearAccessToken();
  useUserStoreHook().RESET_AUTH_STATE();
}

/**
 * 计算认证守卫决策。
 *
 * 覆盖场景：
 * - 未登录访问受保护页面 → `/login?redirect=<原路径>`
 * - 已登录访问 `/login` → `/dashboard`（**先校验会话**，避免失效 token 触发重定向循环）
 * - 有 token 但 `/admin/me` 失败 → 清理本地状态并回登录页
 * - 会话有效 → 放行
 */
export async function resolveAuthGuard(to: {
  path: string;
  fullPath: string;
}): Promise<AuthGuardDecision> {
  const isLoginPage = to.path === LOGIN_PATH;

  if (isLoginPage) {
    // 未登录直接进入登录页，不发起任何请求
    if (!hasLocalToken()) return { type: "allow" };

    try {
      await ensureAdminProfile();
      return { type: "redirect", path: HOME_PATH, replace: true };
    } catch {
      // token 存在但服务端不认（过期/被撤销）：清理后停留在登录页
      clearLocalSession();
      return { type: "allow" };
    }
  }

  if (!hasLocalToken()) {
    // 存在但已过期的 token 需要清掉，避免"过期凭证"一直留在 sessionStorage
    if (getAccessToken()) clearLocalSession();

    return {
      type: "redirect",
      path: LOGIN_PATH,
      // 根路径不记录 redirect，避免登录后跳到 "/" 再被重定向一次
      query: to.fullPath === "/" ? undefined : { redirect: to.fullPath },
      replace: true
    };
  }

  try {
    await ensureAdminProfile();
    return { type: "allow" };
  } catch {
    clearLocalSession();
    return {
      type: "redirect",
      path: LOGIN_PATH,
      query: { redirect: to.fullPath },
      replace: true
    };
  }
}
