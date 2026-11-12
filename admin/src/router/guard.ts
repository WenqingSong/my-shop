import { clearAccessToken, getAccessToken, isTokenExpired } from "@/utils/auth";
import { ensureAdminProfile, useUserStoreHook } from "@/store/modules/user";
import { usePermissionStoreHook } from "@/store/modules/permission";
import { hasMetaPermission } from "@/utils/permission";

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

/** 无权限落地页（全屏 403） */
export const ACCESS_DENIED_PATH = "/access-denied";

/** 权限守卫需要的最小路由信息（`vue-router` 的 `to` 满足该结构） */
export interface PermissionGuardTarget {
  path: string;
  fullPath: string;
  meta?: {
    permissions?: string[];
    permissionsMode?: "every" | "some";
  };
  matched?: Array<{
    meta?: {
      permissions?: string[];
      permissionsMode?: "every" | "some";
    };
  }>;
}

/**
 * 判断当前主体是否有权访问目标路由。
 *
 * - 逐级检查 `matched` 中每条记录的 `meta.permissions`（父级布局不声明则不限制）；
 * - 没有声明 `permissions` 的页面只要求登录（与后端"仅 AdminAuth"的读接口一致）；
 * - `is_super` 由 `hasPermission` 统一放行（界面展示语义，最终授权仍在后端）。
 */
export function hasRoutePermission(target: PermissionGuardTarget): boolean {
  const metas =
    target.matched && target.matched.length > 0
      ? target.matched.map(item => item.meta)
      : [target.meta];
  return metas.every(meta => hasMetaPermission(meta));
}

/**
 * 取当前权限下第一个可访问的菜单页面，作为无权限时的落地页。
 *
 * 只遍历已按权限过滤过的 `wholeMenus`，因此返回值本身一定可访问。
 */
export function getFirstAccessibleMenuPath(): string | null {
  const menus = usePermissionStoreHook().wholeMenus as Array<{
    path?: string;
    children?: unknown[];
  }>;

  const walk = (nodes: typeof menus): string | null => {
    for (const node of nodes) {
      const children = node.children as typeof menus | undefined;
      if (children && children.length > 0) {
        const found = walk(children);
        if (found) return found;
      } else if (typeof node.path === "string" && node.path.startsWith("/")) {
        return node.path;
      }
    }
    return null;
  };

  return walk(menus);
}

/**
 * 计算页面访问权限决策。
 *
 * 覆盖场景：
 * - 登录页 / 403 页：不参与权限判断；
 * - 有权限：放行；
 * - 无权限：跳转到第一个可访问菜单；若没有可访问菜单（或就是当前页），跳 403。
 *
 * 前端守卫只是体验层：即使绕过，后端 RBAC 仍会拒绝。
 */
export function resolvePermissionGuard(
  to: PermissionGuardTarget
): AuthGuardDecision {
  if (to.path === LOGIN_PATH || to.path === ACCESS_DENIED_PATH) {
    return { type: "allow" };
  }

  if (hasRoutePermission(to)) return { type: "allow" };

  const fallback = getFirstAccessibleMenuPath();
  if (fallback && fallback !== to.path) {
    return { type: "redirect", path: fallback, replace: true };
  }

  return { type: "redirect", path: ACCESS_DENIED_PATH, replace: true };
}
