/**
 * A5 用户/管理员/角色/权限的视图转换与表单校验。
 *
 * 所有字段语义以 Go 后端源码为准：
 * - `internal/logic/admin/admin.go` 的校验规则；
 * - `internal/logic/iam/user_status.go` 的 reason 规则；
 * - `api/admin/v1`、`api/iam/v1` 的 DTO。
 *
 * 权限码一律来自后端，本文件**不生成**任何权限码；
 * `permissionDomain` 只是从真实 code 的前缀推导展示分组。
 */
import type { UserStatusDTO } from "../types/user";
import type { RoleDTO } from "../types/role";
import type { PermissionDTO } from "../types/permission";

/* -------------------------------------------------------------------------- */
/* 通用                                                                        */
/* -------------------------------------------------------------------------- */

/** 统计 Unicode 字符数（后端用 `utf8.RuneCountInString` 校验长度） */
function runeLength(value: string): number {
  return Array.from(value).length;
}

/** 解析正整数 id：非法（空、0、负数、小数、非数字）返回 null */
export function parsePositiveId(input: string | number | null | undefined) {
  if (input === null || input === undefined) return null;
  const raw = typeof input === "number" ? String(input) : input.trim();
  if (raw === "") return null;
  if (!/^\d+$/.test(raw)) return null;
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value <= 0) return null;
  return value;
}

/* -------------------------------------------------------------------------- */
/* 用户账号状态                                                                */
/* -------------------------------------------------------------------------- */

export interface UserStatusView {
  id: number;
  status: 0 | 1;
  statusLabel: string;
  statusTagType: "success" | "info";
}

/** 用户状态视图（后端只返回 id + status） */
export function toUserStatusView(dto: UserStatusDTO): UserStatusView {
  return {
    id: dto.id,
    status: dto.status,
    statusLabel: dto.status === 1 ? "启用" : "禁用",
    statusTagType: dto.status === 1 ? "success" : "info"
  };
}

/** 校验禁用/启用原因：后端要求 trim 后非空且 ≤255 字符 */
export function validateUserStatusReason(reason: string): string | null {
  const trimmed = reason.trim();
  if (trimmed === "") return "请输入变更原因";
  if (runeLength(trimmed) > 255) return "变更原因不能超过 255 个字符";
  return null;
}

/* -------------------------------------------------------------------------- */
/* 管理员                                                                      */
/* -------------------------------------------------------------------------- */

export interface CreateAdminForm {
  username: string;
  password: string;
}

export interface CreateAdminFormErrors {
  username?: string;
  password?: string;
}

/**
 * 创建管理员表单校验。
 *
 * 与后端 `validateUsername` / `validatePassword` 一致：
 * 用户名 3~24 位大小写字母或数字；密码 8~24 位（不 trim，密码按原样提交）。
 */
export function validateCreateAdminForm(
  form: CreateAdminForm
): CreateAdminFormErrors {
  const errors: CreateAdminFormErrors = {};

  if (form.username.trim() === "") {
    errors.username = "请输入管理员用户名";
  } else if (!/^[a-zA-Z0-9]{3,24}$/.test(form.username.trim())) {
    errors.username = "用户名为 3~24 位大小写字母或数字";
  }

  const passwordLength = form.password.length;
  if (passwordLength === 0) {
    errors.password = "请输入管理员密码";
  } else if (passwordLength < 8 || passwordLength > 24) {
    errors.password = "密码长度为 8~24 位";
  }

  return errors;
}

/** 创建管理员请求体（用户名 trim，密码原样） */
export function buildCreateAdminPayload(form: CreateAdminForm) {
  return { username: form.username.trim(), password: form.password };
}

/* -------------------------------------------------------------------------- */
/* 角色                                                                        */
/* -------------------------------------------------------------------------- */

export interface RoleForm {
  name: string;
  description: string;
}

export interface RoleFormErrors {
  name?: string;
  description?: string;
}

export interface RoleView extends RoleDTO {
  descriptionText: string;
}

/** 角色视图：后端没有 status，也没有权限数量，不伪造这些字段 */
export function toRoleView(dto: RoleDTO): RoleView {
  return {
    id: dto.id,
    name: dto.name,
    description: dto.description,
    descriptionText: dto.description || "—"
  };
}

/** 角色表单校验：名称 trim 后非空且 ≤64 字符；描述（可空）≤255 字符 */
export function validateRoleForm(form: RoleForm): RoleFormErrors {
  const errors: RoleFormErrors = {};

  const name = form.name.trim();
  if (name === "") {
    errors.name = "请输入角色名称";
  } else if (runeLength(name) > 64) {
    errors.name = "角色名称不能超过 64 个字符";
  }

  if (runeLength(form.description.trim()) > 255) {
    errors.description = "角色描述不能超过 255 个字符";
  }

  return errors;
}

/** 创建角色请求体（与后端一致：name/description 都做 trim） */
export function buildCreateRolePayload(form: RoleForm) {
  return { name: form.name.trim(), description: form.description.trim() };
}

/** 更新角色请求体（仅提交有变化的字段） */
export function buildUpdateRolePayload(
  original: RoleDTO,
  form: RoleForm
): { name?: string; description?: string } {
  const payload: { name?: string; description?: string } = {};
  const name = form.name.trim();
  const description = form.description.trim();
  if (name !== original.name) payload.name = name;
  if (description !== original.description) payload.description = description;
  return payload;
}

/* -------------------------------------------------------------------------- */
/* 权限                                                                        */
/* -------------------------------------------------------------------------- */

/** 已知业务域的展示名（仅前端展示用，键来自真实权限码前缀） */
const DOMAIN_LABELS: Readonly<Record<string, string>> = {
  dashboard: "运营数据",
  user: "用户",
  product: "商品",
  category: "分类",
  sku: "SKU",
  inventory: "库存",
  order: "订单",
  refund: "退款",
  coupon: "优惠券",
  flash_sale: "秒杀",
  banner: "轮播图",
  recommend: "推荐位",
  review: "评价",
  admin: "管理员",
  role: "角色",
  permission: "权限"
};

export interface PermissionView extends PermissionDTO {
  /** 由真实权限码前缀推导（后端没有该字段） */
  domain: string;
  domainLabel: string;
}

/**
 * 从权限码推导业务域。
 *
 * 只做字符串切分，不生成/改写权限码本身；未知前缀原样展示。
 */
export function permissionDomain(code: string): string {
  const index = code.indexOf(":");
  const domain = index > 0 ? code.slice(0, index) : code.trim();
  return domain === "" ? "unknown" : domain;
}

/** 权限视图：附带由 code 推导的业务域 */
export function toPermissionView(dto: PermissionDTO): PermissionView {
  const domain = permissionDomain(dto.code);
  return {
    id: dto.id,
    code: dto.code,
    name: dto.name,
    description: dto.description,
    domain,
    domainLabel: DOMAIN_LABELS[domain] ?? domain
  };
}

/** 按业务域分组（保持后端返回顺序，即 id 升序） */
export function groupPermissionsByDomain(views: PermissionView[]) {
  const groups = new Map<
    string,
    { domain: string; domainLabel: string; items: PermissionView[] }
  >();
  for (const view of views) {
    const group = groups.get(view.domain);
    if (group) {
      group.items.push(view);
    } else {
      groups.set(view.domain, {
        domain: view.domain,
        domainLabel: view.domainLabel,
        items: [view]
      });
    }
  }
  return [...groups.values()];
}

/** 权限列表的客户端筛选（后端一次返回全部定义，无分页/搜索参数） */
export function filterPermissions(
  views: PermissionView[],
  keyword: string,
  domain = ""
): PermissionView[] {
  const text = keyword.trim().toLowerCase();
  return views.filter(view => {
    if (domain && view.domain !== domain) return false;
    if (!text) return true;
    return (
      view.code.toLowerCase().includes(text) ||
      view.name.toLowerCase().includes(text)
    );
  });
}

export interface PermissionForm {
  code: string;
  name: string;
  description: string;
}

export interface PermissionFormErrors {
  code?: string;
  name?: string;
  description?: string;
}

/** 权限表单校验：code/name trim 后非空且 ≤64；描述（可空）≤255 */
export function validatePermissionForm(
  form: PermissionForm
): PermissionFormErrors {
  const errors: PermissionFormErrors = {};

  const code = form.code.trim();
  if (code === "") {
    errors.code = "请输入权限码";
  } else if (runeLength(code) > 64) {
    errors.code = "权限码不能超过 64 个字符";
  }

  const name = form.name.trim();
  if (name === "") {
    errors.name = "请输入权限名称";
  } else if (runeLength(name) > 64) {
    errors.name = "权限名称不能超过 64 个字符";
  }

  if (runeLength(form.description.trim()) > 255) {
    errors.description = "权限描述不能超过 255 个字符";
  }

  return errors;
}

/** 创建权限请求体 */
export function buildCreatePermissionPayload(form: PermissionForm) {
  return {
    code: form.code.trim(),
    name: form.name.trim(),
    description: form.description.trim()
  };
}

/** 更新权限请求体（仅提交有变化的字段） */
export function buildUpdatePermissionPayload(
  original: PermissionDTO,
  form: PermissionForm
): { code?: string; name?: string; description?: string } {
  const payload: { code?: string; name?: string; description?: string } = {};
  const code = form.code.trim();
  const name = form.name.trim();
  const description = form.description.trim();
  if (code !== original.code) payload.code = code;
  if (name !== original.name) payload.name = name;
  if (description !== original.description) payload.description = description;
  return payload;
}
