import { startRouteLoading, stopRouteLoading } from "@/directives/brandLoading";
import NProgress from "@/utils/progress";
import { getConfig } from "@/config";
import { transformI18n } from "@/plugins/i18n";
import { buildHierarchyTree } from "@/utils/tree";
import remainingRouter from "./modules/remaining";
import { usePermissionStoreHook } from "@/store/modules/permission";
import { isUrl, openLink, cloneDeep } from "@pureadmin/utils";
import {
  ascending,
  getHistoryMode,
  handleAliveRoute,
  formatTwoStageRoutes,
  formatFlatteningRoutes
} from "./utils";
import {
  type Router,
  type RouteRecordRaw,
  type RouteComponent,
  createRouter
} from "vue-router";
import { resolveAuthGuard, resolvePermissionGuard } from "./guard";
import { getPermissionSignature } from "@/utils/permission";

/**
 * 自动导入全部静态路由，无需再手动引入！
 * 匹配 `src/router/modules` 目录（任何嵌套级别）中具有 `.ts` 扩展名的所有文件，
 * 除了 `remaining.ts` 文件（该文件不参与菜单生成）。
 */
const modules: Record<string, any> = import.meta.glob(
  ["./modules/**/*.ts", "!./modules/**/remaining.ts"],
  {
    eager: true
  }
);

/** 原始静态路由（未做任何处理） */
const routes: Array<RouteRecordRaw> = [];

Object.keys(modules).forEach(key => {
  routes.push(modules[key].default);
});

/** 导出处理后的静态路由（三级及以上的路由全部拍成二级） */
export const constantRoutes: Array<RouteRecordRaw> = formatTwoStageRoutes(
  formatFlatteningRoutes(buildHierarchyTree(ascending(routes.flat(Infinity))))
);

/** 初始的静态路由，用于退出登录时重置路由 */
const initConstantRoutes: Array<RouteRecordRaw> = cloneDeep(constantRoutes);

/** 用于渲染菜单，保持原始层级 */
export const constantMenus: Array<RouteComponent> = ascending(
  routes.flat(Infinity)
).concat(...remainingRouter);

/** 不参与菜单的路由 */
export const remainingPaths = Object.keys(remainingRouter).map(v => {
  return (remainingRouter as any)[v].path;
});

/** 创建路由实例 */
export const router: Router = createRouter({
  history: getHistoryMode(import.meta.env.VITE_ROUTER_HISTORY),
  routes: constantRoutes.concat(...(remainingRouter as any)),
  strict: true,
  scrollBehavior(to, from, savedPosition) {
    return new Promise(resolve => {
      if (savedPosition) {
        return savedPosition;
      } else {
        if (from.meta.saveSrollTop) {
          const top: number =
            document.documentElement.scrollTop || document.body.scrollTop;
          resolve({ left: 0, top });
        }
      }
    });
  }
});

/** 记录已经加载的页面路径 */
const loadedPaths = new Set<string>();

/**
 * 上次构建菜单时的权限签名。
 *
 * 菜单在应用启动时（尚未登录）就会构建一次，登录成功或权限变化后必须重建，
 * 否则侧边栏会停留在"匿名权限"的结果上。用签名比较避免每次导航都深拷贝路由树。
 */
let lastMenuSignature: string | null = null;

/**
 * 按当前权限主体重建菜单（仅在签名变化时执行）。
 *
 * 供路由守卫与 `refreshAccess()`（权限变更后）共用。
 */
export function syncMenusWithPermissions() {
  const signature = getPermissionSignature();
  if (signature === lastMenuSignature) return;
  lastMenuSignature = signature;
  usePermissionStoreHook().handleWholeMenus([]);
}

/** 重置已加载页面记录 */
export function resetLoadedPaths() {
  loadedPaths.clear();
}

/** 重置路由 */
export function resetRouter() {
  router.clearRoutes();
  for (const route of initConstantRoutes.concat(...(remainingRouter as any))) {
    router.addRoute(route);
  }
  router.options.routes = formatTwoStageRoutes(
    formatFlatteningRoutes(buildHierarchyTree(ascending(routes.flat(Infinity))))
  );
  usePermissionStoreHook().clearAllCachePage();
  resetLoadedPaths();
  // 登出/重置后重新按匿名权限构建菜单，避免残留上一个账号的菜单
  lastMenuSignature = null;
}

router.beforeEach(async (to: ToRouteType, _from) => {
  startRouteLoading(to.fullPath);
  to.meta.loaded = loadedPaths.has(to.path);

  if (!to.meta.loaded) {
    NProgress.start();
  }

  if (to.meta?.keepAlive) {
    handleAliveRoute(to, "add");
    /** 页面整体刷新和点击标签页刷新 */
    if (_from.name === undefined || _from.name === "Redirect") {
      handleAliveRoute(to);
    }
  }

  if (!isUrl(to?.name as string)) {
    to.matched.some(item => {
      if (!item.meta.title) return "";
      const Title = getConfig().Title;
      if (Title)
        document.title = `${transformI18n(item.meta.title)} | ${Title}`;
      else document.title = transformI18n(item.meta.title);
    });
  }

  /** 外部链接：直接打开，不参与路由渲染 */
  if (isUrl(to?.name as string)) {
    openLink(to?.name as string);
    NProgress.done();
    return false;
  }

  /** 认证守卫：决策逻辑见 `./guard.ts`（可单测） */
  const decision = await resolveAuthGuard(to);
  if (decision.type === "redirect") {
    NProgress.done();
    return {
      path: decision.path,
      query: decision.query,
      replace: true
    };
  }

  /**
   * 认证通过后按当前权限重建菜单，再做页面级权限判断。
   *
   * 顺序不能颠倒：无权限时的兜底落地页来自"已过滤的菜单"。
   */
  syncMenusWithPermissions();

  const access = resolvePermissionGuard(to);
  if (access.type === "redirect") {
    NProgress.done();
    return { path: access.path, replace: true };
  }

  return true;
});

router.onError(() => stopRouteLoading());

router.afterEach(to => {
  stopRouteLoading(to.fullPath);
  loadedPaths.add(to.path);
  NProgress.done();
});

export default router;
