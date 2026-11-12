import { router, syncMenusWithPermissions } from "@/router";
import { ensureAdminProfile } from "@/store/modules/user";
import { resolvePermissionGuard } from "@/router/guard";

/**
 * 权限变更后的统一刷新流程。
 *
 * 触发场景：角色权限分配/移除、角色删除、管理员角色分配/移除等
 * "可能改变当前管理员有效权限"的写操作成功后。
 *
 * 顺序（与需求 7.4 一致）：
 * 1. 强制重新请求 `GET /admin/me`，更新 Pinia 身份与权限快照；
 * 2. 按新权限重建菜单；
 * 3. 重新检查当前页面是否仍有访问权限，失去权限则跳转。
 *
 * 注意：后端没有"查询当前管理员有效权限"接口，`/admin/me` 也不返回
 * `permissions[]`，因此这里的刷新只能反映 `is_super` / `roles` 的变化；
 * 非超级管理员仍按 fail-closed 处理（详见 `@/utils/permission`）。
 */
export async function refreshAccess(
  options: { forceProfile?: boolean } = {}
): Promise<void> {
  await ensureAdminProfile({ force: options.forceProfile ?? true });
  syncMenusWithPermissions();

  const current = router.currentRoute.value;
  const decision = resolvePermissionGuard({
    path: current.path,
    fullPath: current.fullPath,
    meta: current.meta,
    matched: current.matched.map(record => ({ meta: record.meta }))
  });

  if (decision.type === "redirect") {
    await router.replace(decision.path);
  }
}
