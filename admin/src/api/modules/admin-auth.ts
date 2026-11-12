/**
 * 管理员认证 API 适配层。
 *
 * URI 约定：所有路径都是**后端真实路由**（`/admin/*`），不带 `/api` 前缀。
 * Axios 实例的 `baseURL` 已是 `/api`，由 Vite Proxy（开发）或网关（生产）
 * 去掉前缀后转发到 Go 后端。
 *
 * 类型状态：PROVISIONAL。
 * `contracts/openapi.yaml` 尚未交付，以下 DTO 依据后端初步设计手写，
 * 一旦契约冻结，应用 `pnpm api:types` 生成的类型替换本文件中的
 * `Provisional*` 类型，请求/响应结构与函数签名保持不变即可。
 *
 * 不要在本文件中重复添加 `/api` 前缀，也不要在此处实现业务逻辑。
 */
import { http } from "@/utils/http";

/** 【PROVISIONAL】登录请求体 */
export interface ProvisionalAdminLoginPayload {
  username: string;
  password: string;
}

/** 【PROVISIONAL】登录响应 `data` */
export interface ProvisionalAdminLoginResult {
  /** JWT access token */
  access_token: string;
  /** token 类型，后端恒为 `Bearer` */
  token_type: string;
  /** 有效期（秒），后端当前恒为 3600 */
  expires_in: number;
}

/** 【PROVISIONAL】当前管理员信息 `data` */
export interface ProvisionalAdminProfile {
  id: number;
  username: string;
  /** 是否超级管理员 */
  is_super: boolean;
  /** 角色名列表，例如 `["super_admin"]` */
  roles: string[];
  /**
   * 后端当前**不返回**该字段。
   * 保留为可选：将来若契约新增 `permissions[]`，无需改动调用方即可直接使用。
   */
  permissions?: string[];
}

/**
 * 管理员登录。
 *
 * 使用 `silent`：登录失败由登录表单内联展示，避免全局错误弹窗与表单提示重复，
 * 也避免登录阶段的失败被误判为"已登录会话失效"。
 */
export function adminLogin(payload: ProvisionalAdminLoginPayload) {
  return http.post<ProvisionalAdminLoginResult, typeof payload>(
    "/admin/login",
    { data: payload },
    { silent: true, skipAuth: true, skipUnauthorizedHandling: true }
  );
}

/** 获取当前登录管理员（每次刷新页面都用于校验会话） */
export function getCurrentAdmin(config?: { silent?: boolean }) {
  return http.get<ProvisionalAdminProfile, undefined>("/admin/me", undefined, {
    silent: config?.silent ?? false
  });
}

/**
 * 管理员退出登录。
 *
 * 后端仅验签（幂等撤销），因此本地登录态无论如何都要清理，调用方需自行保证。
 * 使用 `silent`：退出失败不应打扰用户，也不应阻止本地登出。
 */
export function adminLogout() {
  return http.post<null, undefined>("/admin/logout", undefined, {
    silent: true
  });
}
