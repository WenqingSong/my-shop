/**
 * 前端权限判断（RBAC 适配层）。
 *
 * 背景：后端 `/admin/me` 当前只返回 `is_super` 与 `roles`，**没有** `permissions[]`。
 * 因此前端采用"角色 → 权限码"的本地映射，作为契约冻结前的过渡方案。
 *
 * 硬性约束：
 * 1. 只配置**已经明确知道**的角色（当前仅 `super_admin`），不为未知角色赋权。
 * 2. `permissions` 一旦由后端下发，就以它为准（即使为空数组），不再回退到角色映射。
 * 3. 前端权限判断只用于界面呈现，**不是安全边界**；真正的授权由后端
 *    `RequirePermission(...)` 中间件执行，前端最多做到"不展示无权入口"。
 * 4. 未知角色默认拒绝（白名单语义）。
 *
 * 设计说明：本模块保持**零依赖**（不 import store、不 import router），
 * 通过 `setPermissionSubject()` 由 Pinia 在登录/恢复会话时同步一份快照。
 * 这样可以避免 `router → router/utils → store → router` 的循环依赖。
 */

/**
 * 权限码（**全部来自 Go 后端真实定义**）。
 *
 * 来源：
 * - `internal/boot/seed.go` 的权限种子；
 * - `internal/cmd/routes_admin.go` 中 `RequirePermission(code)` 的实际参数。
 *
 * 注意：后端**没有** `product:read` / `category:read` / `sku:read` /
 * `inventory:read` 这类读权限码。商品、库存的查询接口只要求 AdminAuth，
 * 分类树更是公开接口。因此前端**不能**为页面访问编造读权限码，
 * 页面访问由登录态保证，写操作按钮按下列真实码控制。
 */
export const PERMISSION = {
  DASHBOARD_VIEW: "dashboard:view",
  PRODUCT_CREATE: "product:create",
  PRODUCT_UPDATE: "product:update",
  PRODUCT_ON_SHELF: "product:on_shelf",
  PRODUCT_OFF_SHELF: "product:off_shelf",
  CATEGORY_CREATE: "category:create",
  CATEGORY_UPDATE: "category:update",
  CATEGORY_DELETE: "category:delete",
  SKU_CREATE: "sku:create",
  SKU_UPDATE: "sku:update",
  SKU_DELETE: "sku:delete",
  INVENTORY_INCREASE: "inventory:increase",
  INVENTORY_DEDUCT: "inventory:deduct"
} as const;

/** 权限码：工作台只读（保留 A1 的命名，避免调用方改动） */
export const PERMISSION_DASHBOARD_VIEW = PERMISSION.DASHBOARD_VIEW;

/**
 * 角色 → 权限码映射（集中配置）。
 *
 * 仅登记当前已知角色 `super_admin`。其余角色在后端契约明确前不得推测，
 * 遇到未知角色时一律按"无权限"处理。
 */
export const ROLE_PERMISSION_MAP: Readonly<Record<string, readonly string[]>> =
  {
    super_admin: [PERMISSION_DASHBOARD_VIEW]
  };

/** 权限判断主体 */
export interface PermissionSubject {
  /** 是否超级管理员（后端 `is_super`） */
  isSuper: boolean;
  /** 角色名列表（后端 `roles`） */
  roles: string[];
  /**
   * 后端下发的权限码。
   * `null` 表示后端**未提供**该字段（当前状态），此时回退到角色映射；
   * 空数组表示后端明确表示"没有任何权限"，此时不做回退。
   */
  permissions: string[] | null;
}

/** 未登录时的默认主体（无任何权限） */
const anonymousSubject: PermissionSubject = {
  isSuper: false,
  roles: [],
  permissions: null
};

let currentSubject: PermissionSubject = { ...anonymousSubject };

/**
 * 同步当前权限主体（由 user store 在登录成功、刷新恢复、登出时调用）。
 */
export function setPermissionSubject(subject: PermissionSubject) {
  currentSubject = {
    isSuper: Boolean(subject.isSuper),
    roles: [...(subject.roles ?? [])],
    permissions: subject.permissions ?? null
  };
}

/** 读取当前权限主体（只读快照） */
export function getPermissionSubject(): PermissionSubject {
  return currentSubject;
}

/** 重置为匿名主体（登出、认证失效时调用） */
export function resetPermissionSubject() {
  currentSubject = { ...anonymousSubject };
}

/**
 * 计算主体的有效权限集合。
 *
 * 优先级：`is_super` → `*`；后端 `permissions` → 直接使用；
 * 否则按 `roles` 查映射表（未知角色不贡献任何权限）。
 */
export function resolvePermissions(
  subject: PermissionSubject = currentSubject
): string[] {
  if (subject.isSuper) return ["*"];

  if (subject.permissions !== null) return [...subject.permissions];

  const result = new Set<string>();
  for (const role of subject.roles ?? []) {
    const granted = ROLE_PERMISSION_MAP[role];
    if (!granted) continue;
    for (const permission of granted) result.add(permission);
  }
  return [...result];
}

/**
 * 统一权限判断入口。
 *
 * @param required 需要的权限码；数组表示"全部满足"（`every`）
 * @param options.mode `every`（默认，全部满足）或 `some`（任一满足）
 * @param options.subject 指定主体；缺省使用当前登录管理员的快照
 */
export function hasPermission(
  required: string | string[],
  options: { mode?: "every" | "some"; subject?: PermissionSubject } = {}
): boolean {
  const { mode = "every", subject = currentSubject } = options;

  const requiredList = Array.isArray(required) ? required : [required];
  if (requiredList.length === 0) return true;

  if (subject.isSuper) return true;

  const granted = resolvePermissions(subject);
  const match = (permission: string) => granted.includes(permission);
  return mode === "some" ? requiredList.some(match) : requiredList.every(match);
}
