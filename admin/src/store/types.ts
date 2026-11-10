import type { RouteRecordName } from "vue-router";

export type cacheType = {
  mode: string;
  name?: RouteRecordName;
};

export type positionType = {
  startIndex?: number;
  length?: number;
};

export type appType = {
  sidebar: {
    opened: boolean;
    withoutAnimation: boolean;
    // 判断是否手动点击Collapse
    isClickCollapse: boolean;
  };
  layout: string;
  device: string;
  viewportSize: { width: number; height: number };
  sortSwap: boolean;
};

export type multiType = {
  path: string;
  name: string;
  meta: any;
  query?: object;
  params?: object;
};

export type setType = {
  title: string;
  fixedHeader: boolean;
  hiddenSideBar: boolean;
};

export type userType = {
  /** 管理员 id（后端 `/admin/me` 的 `id`） */
  adminId?: number | null;
  avatar?: string;
  username?: string;
  nickname?: string;
  /** 是否超级管理员（后端 `is_super`） */
  isSuper?: boolean;
  roles?: Array<string>;
  /**
   * 后端下发的权限码。
   * `null` 表示后端未提供该字段（当前 `/admin/me` 的真实状态）：
   * 非超级管理员按 fail-closed 处理，有效权限为空。
   */
  permissions?: Array<string> | null;
  /** 是否已通过 `/admin/me` 完成会话校验 */
  profileLoaded?: boolean;
  verifyCode?: string;
  currentPage?: number;
  isRemembered?: boolean;
  loginDay?: number;
};
