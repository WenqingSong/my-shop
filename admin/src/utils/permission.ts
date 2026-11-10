/**
 * 前端权限判断（RBAC 适配层）。
 *
 * 事实来源（A5 复核后的真实后端能力）：
 * - `GET /admin/me` 返回 `{ id, username, is_super, roles }`，**不返回** `permissions[]`；
 * - 后端**没有**"查询当前管理员有效权限"接口；
 * - 后端**没有**"查询角色已有权限"接口（只有授予/移除权限的写接口）。
 *
 * 因此 A5 的授权优先级是：
 * 1. `is_super === true` → 视为拥有全部权限（仅影响界面展示）；
 * 2. 后端下发了 `permissions[]`（当前未实现，保留兼容）→ 以其为准；
 * 3. 以上都不具备 → **fail-closed**，非超级管理员的有效权限集合为空。
 *
 * 明确禁止：把角色名当作权限集合。角色名只用于展示（`/admin/me` 的 `roles`），
 * 不参与授权计算。
 *
 * 前端权限判断只用于界面呈现，**不是安全边界**：真正的授权由后端
 * `RequirePermission(...)` 中间件执行。
 *
 * 设计说明：本模块保持**零依赖**（不 import store、不 import router），
 * 通过 `setPermissionSubject()` 由 Pinia 在登录/恢复会话时同步一份快照，
 * 避免 `router → router/utils → store → router` 的循环依赖。
 */

/**
 * 权限码（**全部来自 Go 后端真实定义**）。
 *
 * 来源：
 * - `internal/boot/seed.go` 的权限种子；
 * - `internal/cmd/routes_admin.go` 中 `RequirePermission(code)` 的实际参数。
 *
 * 注意：
 * - 商品/分类/SKU/库存/轮播图/推荐位的**读接口没有权限码**，仅要求 AdminAuth，
 *   因此前端不为这些页面的"读"编造权限码；
 * - 订单/退款/优惠券/工作台有真实读权限码（`order:list`、`refund:list`、
 *   `coupon:list`、`dashboard:view`）；
 * - 用户域只有 `user:read` / `user:status`，**没有**用户列表权限码；
 * - 管理员域只有写权限码，**没有** `admin:list`。
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
  INVENTORY_DEDUCT: "inventory:deduct",
  ORDER_LIST: "order:list",
  ORDER_VIEW: "order:view",
  ORDER_SHIP: "order:ship",
  REFUND_LIST: "refund:list",
  REFUND_VIEW: "refund:view",
  REFUND_REVIEW: "refund:review",
  REFUND_EXECUTE: "refund:execute",
  COUPON_CREATE: "coupon:create",
  COUPON_LIST: "coupon:list",
  COUPON_STOP: "coupon:stop",
  FLASH_SALE_CREATE: "flash_sale:create",
  FLASH_SALE_UPDATE: "flash_sale:update",
  BANNER_CREATE: "banner:create",
  BANNER_UPDATE: "banner:update",
  BANNER_DELETE: "banner:delete",
  RECOMMEND_CREATE: "recommend:create",
  RECOMMEND_UPDATE: "recommend:update",
  RECOMMEND_DELETE: "recommend:delete",
  RECOMMEND_ITEM: "recommend:item",
  USER_READ: "user:read",
  USER_STATUS: "user:status",
  ADMIN_CREATE: "admin:create",
  ADMIN_DISABLE: "admin:disable",
  ADMIN_DELETE: "admin:delete",
  ADMIN_ASSIGN_ROLE: "admin:assign_role",
  ROLE_CREATE: "role:create",
  ROLE_LIST: "role:list",
  ROLE_UPDATE: "role:update",
  ROLE_DELETE: "role:delete",
  ROLE_ASSIGN_PERMISSION: "role:assign_permission",
  PERMISSION_CREATE: "permission:create",
  PERMISSION_LIST: "permission:list",
  PERMISSION_UPDATE: "permission:update",
  PERMISSION_DELETE: "permission:delete"
} as const;

/** 权限码：工作台只读（保留 A1 的命名，避免调用方改动） */
export const PERMISSION_DASHBOARD_VIEW = PERMISSION.DASHBOARD_VIEW;

/** 权限判断主体 */
export interface PermissionSubject {
  /** 是否超级管理员（后端 `is_super`） */
  isSuper: boolean;
  /** 角色名列表（后端 `roles`），**不参与授权计算**，仅用于展示 */
  roles: string[];
  /**
   * 后端下发的权限码。
   * `null` 表示后端**未提供**该字段（当前真实状态）→ 非超级管理员按 fail-closed 处理；
   * 空数组表示后端明确表示"没有任何权限"。
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
    permissions: subject.permissions ? [...subject.permissions] : null
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
 * 顺序：`is_super` → `["*"]`；后端 `permissions` → 直接使用；
 * 否则返回空数组（fail-closed）。角色名不参与计算。
 */
export function resolvePermissions(
  subject: PermissionSubject = currentSubject
): string[] {
  if (subject.isSuper) return ["*"];
  if (subject.permissions !== null) return [...subject.permissions];
  return [];
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

/**
 * 路由 `meta` 声明的页面级权限判断（菜单过滤与路由守卫共用）。
 *
 * - `meta.permissions` 缺省/空数组 → 页面只要求登录（后端读接口仅 AdminAuth）；
 * - `meta.permissionsMode` 缺省为 `every`。
 */
export function hasMetaPermission(
  meta?: {
    permissions?: string[];
    permissionsMode?: "every" | "some";
  } | null
): boolean {
  const required = meta?.permissions;
  if (!required || required.length === 0) return true;
  return hasPermission(required, { mode: meta?.permissionsMode ?? "every" });
}

/**
 * 当前权限主体的签名（用于"是否需要重算菜单"的比较）。
 *
 * 只依赖有效权限集合：权限变化 → 签名变化 → 重建菜单。
 */
export function getPermissionSignature(
  subject: PermissionSubject = currentSubject
): string {
  return resolvePermissions(subject).slice().sort().join(",");
}
