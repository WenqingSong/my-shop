/**
 * 管理端 Token 与登录态存储。
 *
 * 契约来源（后端初步设计，尚未冻结为 OpenAPI）：
 * - `POST /admin/login` 返回 `{ access_token, token_type, expires_in }`
 * - 认证方式 `Authorization: Bearer <access_token>`
 * - 当前**没有 Refresh Token**
 *
 * 存储策略（A1 明确要求）：
 * - 只使用 `sessionStorage`，不使用 Cookie、不使用 localStorage。
 * - 只保存 `access_token` 与 `expires_at`（绝对毫秒时间戳）。
 * - 不保存密码、不保存 Refresh Token、不保存后端返回的其它字段。
 * - 关闭标签页即失效，避免共享设备上的长期残留。
 */
import { hasPermission } from "@/utils/permission";

/** access token 存储键 */
export const ACCESS_TOKEN_KEY = "surgecart-admin:access_token";
/** token 过期时间（毫秒时间戳）存储键 */
export const EXPIRES_AT_KEY = "surgecart-admin:expires_at";

function getSession(): Storage | null {
  // SSR / 测试环境兜底：没有 sessionStorage 时退化为内存可用性判断
  if (typeof window === "undefined" || !window.sessionStorage) return null;
  return window.sessionStorage;
}

/** 读取 access token；不存在返回 `null` */
export function getAccessToken(): string | null {
  return getSession()?.getItem(ACCESS_TOKEN_KEY) ?? null;
}

/** 读取 token 过期时间（毫秒时间戳）；不存在或非法返回 `null` */
export function getTokenExpiresAt(): number | null {
  const raw = getSession()?.getItem(EXPIRES_AT_KEY);
  if (!raw) return null;
  const value = Number(raw);
  return Number.isFinite(value) ? value : null;
}

/**
 * 保存登录凭证。
 *
 * `expires_at = Date.now() + expires_in * 1000`。
 * `expires_in` 缺失或非正数时不写入过期时间（视为后端异常，交由调用方处理）。
 */
export function setAccessToken(accessToken: string, expiresIn: number) {
  const session = getSession();
  if (!session) return;
  session.setItem(ACCESS_TOKEN_KEY, accessToken);
  if (Number.isFinite(expiresIn) && expiresIn > 0) {
    session.setItem(EXPIRES_AT_KEY, String(Date.now() + expiresIn * 1000));
  } else {
    session.removeItem(EXPIRES_AT_KEY);
  }
}

/** 清理本地登录态（token + 过期时间） */
export function clearAccessToken() {
  const session = getSession();
  if (!session) return;
  session.removeItem(ACCESS_TOKEN_KEY);
  session.removeItem(EXPIRES_AT_KEY);
}

/**
 * 判断当前 token 是否可用。
 *
 * 判定顺序：无 token → 不可用；无过期时间 → 视为不可用（无法判断有效期时不放行）；
 * `expires_at <= now` → 已过期。
 */
export function isTokenExpired(now: number = Date.now()): boolean {
  const token = getAccessToken();
  if (!token) return true;
  const expiresAt = getTokenExpiresAt();
  if (expiresAt === null) return true;
  return expiresAt <= now;
}

/** 是否持有"看起来仍有效"的 token（不校验服务端签名，仅做本地时效判断） */
export function hasValidToken(): boolean {
  return !isTokenExpired();
}

/** 格式化 Authorization 头值 */
export const formatToken = (token: string): string => `Bearer ${token}`;

/**
 * 兼容别名：模板布局的"清空缓存并返回登录页"仍调用 `removeToken()`。
 * 实际清理逻辑与 `clearAccessToken` 完全一致。
 */
export const removeToken = clearAccessToken;

/**
 * 按钮级权限判断（兼容模板 `RePerms` 组件与 `v-perms` 指令）。
 *
 * 统一委托给 `@/utils/permission` 的 `hasPermission`，保证与页面级权限判断
 * 使用同一套角色映射与快照，避免出现两份互相矛盾的权限逻辑。
 */
export function hasPerms(value: string | Array<string>): boolean {
  return hasPermission(value);
}
