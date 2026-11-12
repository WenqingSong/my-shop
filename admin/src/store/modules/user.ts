import { defineStore } from "pinia";
import {
  type userType,
  store,
  router,
  resetRouter,
  routerArrays
} from "../utils";
import { useMultiTagsStoreHook } from "./multiTags";
import {
  clearAccessToken,
  getAccessToken,
  isTokenExpired,
  setAccessToken
} from "@/utils/auth";
import {
  resetPermissionSubject,
  setPermissionSubject,
  hasPermission as checkPermission
} from "@/utils/permission";
import {
  adminLogin,
  adminLogout,
  getCurrentAdmin,
  type ProvisionalAdminLoginPayload,
  type ProvisionalAdminProfile
} from "@/api/modules/admin-auth";

/**
 * 管理员状态。
 *
 * 存储策略：
 * - **只**把 `access_token` / `expires_at` 放进 sessionStorage（见 `@/utils/auth`）。
 * - 管理员身份信息（id / username / is_super / roles）只存在 Pinia 运行时状态，
 *   页面刷新后必须重新调用 `GET /admin/me` 恢复，避免把可被篡改的本地数据当成身份依据。
 * - 不保存密码、不保存 Refresh Token。
 *
 * 权限：
 * - 每次身份变化都会把快照同步到 `@/utils/permission`，
 *   保证 `hasPermission()` 与界面渲染读取同一份数据。
 */
export const useUserStore = defineStore("pure-user", {
  state: (): userType => ({
    adminId: null,
    avatar: "",
    username: "",
    nickname: "",
    isSuper: false,
    roles: [],
    // `null` = 后端未提供 permissions 字段（当前 `/admin/me` 的真实状态，非超级管理员 fail-closed）
    permissions: null,
    profileLoaded: false,
    verifyCode: "",
    currentPage: 0,
    isRemembered: false,
    loginDay: 7
  }),
  actions: {
    /** 写入管理员身份（登录成功或 `/admin/me` 成功后调用） */
    SET_ADMIN_PROFILE(profile: ProvisionalAdminProfile) {
      this.adminId = profile.id;
      this.username = profile.username;
      this.nickname = profile.username;
      this.isSuper = Boolean(profile.is_super);
      this.roles = [...(profile.roles ?? [])];
      this.permissions = Array.isArray(profile.permissions)
        ? [...profile.permissions]
        : null;
      this.profileLoaded = true;
      setPermissionSubject({
        isSuper: this.isSuper,
        roles: this.roles,
        permissions: this.permissions
      });
    },

    /** 清空管理员身份与权限快照（登出、401、会话校验失败时调用） */
    RESET_AUTH_STATE() {
      this.adminId = null;
      this.avatar = "";
      this.username = "";
      this.nickname = "";
      this.isSuper = false;
      this.roles = [];
      this.permissions = null;
      this.profileLoaded = false;
      resetPermissionSubject();
    },

    /** 权限判断（委托 `@/utils/permission`） */
    hasPermission(
      required: string | Array<string>,
      mode: "every" | "some" = "every"
    ) {
      return checkPermission(required, {
        mode,
        subject: {
          isSuper: Boolean(this.isSuper),
          roles: this.roles ?? [],
          permissions: this.permissions ?? null
        }
      });
    },

    /** 存储登录页面显示哪个组件（模板遗留，未使用） */
    SET_CURRENTPAGE(value: number) {
      this.currentPage = value;
    },

    /** 存储是否勾选了登录页的免登录（模板遗留，A1 不使用） */
    SET_ISREMEMBERED(bool: boolean) {
      this.isRemembered = bool;
    },

    /** 设置登录页的免登录存储几天（模板遗留，A1 不使用） */
    SET_LOGINDAY(value: number) {
      this.loginDay = Number(value);
    },

    /**
     * 管理员登录（完整流程）。
     *
     * 顺序：`POST /admin/login` → 保存 token → `GET /admin/me` 校验身份。
     * 任何一步失败都会清理本地登录态，不留下"有 token 但没有身份"的半登录状态。
     */
    async login(payload: ProvisionalAdminLoginPayload) {
      const result = await adminLogin(payload);
      setAccessToken(result.access_token, result.expires_in);

      try {
        const profile = await getCurrentAdmin({ silent: true });
        this.SET_ADMIN_PROFILE(profile);
        return profile;
      } catch (error) {
        clearAccessToken();
        this.RESET_AUTH_STATE();
        throw error;
      }
    },

    /**
     * 退出登录。
     *
     * 无论 `/admin/logout` 成功还是失败（含 token 已失效），都必须清理本地状态。
     */
    async logOut(options: { redirect?: boolean } = {}) {
      const { redirect = true } = options;
      try {
        if (getAccessToken() && !isTokenExpired()) {
          await adminLogout();
        }
      } catch {
        // 服务端撤销失败不影响本地登出
      } finally {
        clearAccessToken();
        this.RESET_AUTH_STATE();
        useMultiTagsStoreHook().handleTags("equal", [...routerArrays]);
        resetRouter();
        // 导航被中止（例如已在登录页）时不应让登出流程抛出未处理异常
        if (redirect) await router.replace("/login").catch(() => {});
      }
    }
  }
});

export function useUserStoreHook() {
  return useUserStore(store);
}

/**
 * 进行中的 `/admin/me` 请求。
 *
 * 用于避免初始化阶段（路由守卫 + 页面组件）重复请求同一接口，
 * 也避免并发 401 造成多次清理。
 */
let profileRequest: Promise<ProvisionalAdminProfile> | null = null;

/**
 * 确保管理员身份已加载。
 *
 * - 已有有效身份时直接返回（`force = true` 可强制刷新）。
 * - 没有 token 或 token 已过期时，清理状态并抛错，由调用方决定跳转。
 * - 并发调用共享同一个请求。
 */
export async function ensureAdminProfile(
  options: { force?: boolean } = {}
): Promise<ProvisionalAdminProfile> {
  const userStore = useUserStoreHook();
  if (!options.force && userStore.profileLoaded && userStore.adminId !== null) {
    return {
      id: userStore.adminId as number,
      username: userStore.username ?? "",
      is_super: Boolean(userStore.isSuper),
      roles: [...(userStore.roles ?? [])],
      ...(userStore.permissions ? { permissions: userStore.permissions } : {})
    };
  }

  if (!getAccessToken() || isTokenExpired()) {
    clearAccessToken();
    userStore.RESET_AUTH_STATE();
    throw new Error("登录已过期，请重新登录");
  }

  if (profileRequest) return profileRequest;

  profileRequest = getCurrentAdmin({ silent: true })
    .then(profile => {
      userStore.SET_ADMIN_PROFILE(profile);
      return profile;
    })
    .finally(() => {
      profileRequest = null;
    });

  return profileRequest;
}

/** 仅测试使用：重置进行中的请求缓存 */
export function __resetProfileRequestCache() {
  profileRequest = null;
}
