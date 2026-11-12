import { http } from "msw";
import { mockDb } from "./mock-db";
import { fail, isAuthorized, ok, unauthorized } from "./shared";

/**
 * A5 Mock：用户账号状态、管理员、角色、权限。
 *
 * 对照的真实后端：
 * - `internal/cmd/routes_admin.go` 的路由与 `RequirePermission` 权限码；
 * - `internal/logic/admin/admin.go` 的业务规则（保护超级管理员/禁止操作自身/幂等）；
 * - `internal/logic/iam/user_status.go`（reason 必填、状态幂等）。
 *
 * **仅用于本地演示与单元测试**，不是生产数据，也不代表后端已有列表接口：
 * 后端没有 `GET /admin/users`、`GET /admin/admins` 等列表 API，
 * 这里也**不会**提供这些 mock 路由。
 */

/** Mock 中的"当前登录管理员"（与 `MOCK_ADMIN_PROFILE.id` 一致） */
const CURRENT_ADMIN_ID = 1;

/** 校验管理员用户名（后端 `usernamePattern`：3~24 位字母数字） */
function isValidUsername(username: string): boolean {
  return /^[a-zA-Z0-9]{3,24}$/.test(username);
}

/** 后端 `validatePassword`：8~24 位 */
function isValidPassword(password: string): boolean {
  return password.length >= 8 && password.length <= 24;
}

/** 后端 `validateDescription`：trim 后 ≤255 字符 */
function isValidDescription(description: string): boolean {
  return Array.from(description.trim()).length <= 255;
}

/** 后端 `validateRoleName` / `validatePermissionName/Code`：trim 后非空且 ≤64 */
function isNonEmptyWithin64(value: string): boolean {
  return value.trim() !== "" && Array.from(value.trim()).length <= 64;
}

/** 目标管理员的保护规则：不存在 2003；超级管理员 2005；操作自身 2006 */
function checkTargetAdmin(id: number) {
  const target = mockDb.admins.find(admin => admin.id === id);
  if (!target) return fail(2003, "管理员不存在", 404);
  if (target.is_super === 1) return fail(2005, "超级管理员受保护", 403);
  if (target.id === CURRENT_ADMIN_ID) return fail(2006, "不能操作自身", 403);
  return null;
}

export const accessHandlers = [
  /** GET /admin/users/:id/status */
  http.get("*/api/admin/users/:id/status", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const user = mockDb.users.find(item => item.id === id);
    if (!user) return fail(2015, "用户不存在", 404);
    return ok({ id: user.id, status: user.status });
  }),

  /** PUT /admin/users/:id/status */
  http.put("*/api/admin/users/:id/status", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as {
      status?: number;
      reason?: string;
    };
    const id = Number(params.id);

    if (body?.status !== 0 && body?.status !== 1) {
      return fail(1001, "参数校验失败", 400);
    }
    const reason = (body?.reason ?? "").trim();
    if (reason === "" || Array.from(reason).length > 255) {
      return fail(1001, "参数校验失败", 400);
    }

    const user = mockDb.users.find(item => item.id === id);
    if (!user) return fail(2015, "用户不存在", 404);

    user.status = body.status;
    return ok(null);
  }),

  /** POST /admin/admins */
  http.post("*/api/admin/admins", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as {
      username?: string;
      password?: string;
    };
    const username = (body?.username ?? "").trim();
    const password = body?.password ?? "";

    if (!isValidUsername(username) || !isValidPassword(password)) {
      return fail(1001, "参数校验失败", 400);
    }
    if (mockDb.admins.some(admin => admin.username === username)) {
      return fail(2004, "管理员用户名已存在", 409);
    }

    const id = mockDb.nextAdminId++;
    mockDb.admins.push({ id, username, status: 1, is_super: 0 });
    return ok({ id, username });
  }),

  /** PUT /admin/admins/:id/status */
  http.put("*/api/admin/admins/:id/status", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    // 后端顺序：先校验 status 合法性，再检查目标管理员保护规则
    const body = (await request.json()) as { status?: number };
    if (body?.status !== 0 && body?.status !== 1) {
      return fail(1001, "参数校验失败", 400);
    }

    const id = Number(params.id);
    const protection = checkTargetAdmin(id);
    if (protection) return protection;

    const admin = mockDb.admins.find(item => item.id === id)!;
    admin.status = body.status;
    return ok(null);
  }),

  /** DELETE /admin/admins/:id */
  http.delete("*/api/admin/admins/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const protection = checkTargetAdmin(id);
    if (protection) return protection;

    mockDb.admins = mockDb.admins.filter(item => item.id !== id);
    return ok(null);
  }),

  /** POST /admin/admins/:id/roles */
  http.post("*/api/admin/admins/:id/roles", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const protection = checkTargetAdmin(id);
    if (protection) return protection;

    const body = (await request.json()) as { role_id?: number };
    const roleId = Number(body?.role_id);
    if (!mockDb.roles.some(role => role.id === roleId)) {
      return fail(2007, "角色不存在", 404);
    }
    return ok(null);
  }),

  /** DELETE /admin/admins/:id/roles/:role_id */
  http.delete(
    "*/api/admin/admins/:id/roles/:role_id",
    ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const id = Number(params.id);
      const protection = checkTargetAdmin(id);
      if (protection) return protection;

      // 后端移除角色是幂等的：不存在的关联同样返回成功
      return ok(null);
    }
  ),

  /** GET /admin/roles */
  http.get("*/api/admin/roles", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    return ok({
      items: mockDb.roles
        .slice()
        .sort((a, b) => a.id - b.id)
        .map(role => ({
          id: role.id,
          name: role.name,
          description: role.description
        }))
    });
  }),

  /** POST /admin/roles */
  http.post("*/api/admin/roles", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as {
      name?: string;
      description?: string;
    };
    const name = (body?.name ?? "").trim();
    const description = (body?.description ?? "").trim();

    if (!isNonEmptyWithin64(name) || !isValidDescription(description)) {
      return fail(1001, "参数校验失败", 400);
    }
    if (mockDb.roles.some(role => role.name === name)) {
      return fail(2008, "角色名已存在", 409);
    }

    const id = mockDb.nextRoleId++;
    mockDb.roles.push({ id, name, description });
    return ok({ id });
  }),

  /** PUT /admin/roles/:id */
  http.put("*/api/admin/roles/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const role = mockDb.roles.find(item => item.id === id);
    if (!role) return fail(2007, "角色不存在", 404);

    const body = (await request.json()) as {
      name?: string;
      description?: string;
    };
    const name = body?.name === undefined ? role.name : body.name.trim();
    const description =
      body?.description === undefined
        ? role.description
        : body.description.trim();

    if (!isNonEmptyWithin64(name) || !isValidDescription(description)) {
      return fail(1001, "参数校验失败", 400);
    }
    if (mockDb.roles.some(item => item.id !== id && item.name === name)) {
      return fail(2008, "角色名已存在", 409);
    }

    role.name = name;
    role.description = description;
    return ok({ id: role.id, name: role.name, description: role.description });
  }),

  /** DELETE /admin/roles/:id */
  http.delete("*/api/admin/roles/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    if (!mockDb.roles.some(item => item.id === id)) {
      return fail(2007, "角色不存在", 404);
    }

    mockDb.roles = mockDb.roles.filter(item => item.id !== id);
    for (const key of [...mockDb.rolePermissions]) {
      if (key.startsWith(`${id}:`)) mockDb.rolePermissions.delete(key);
    }
    return ok(null);
  }),

  /** POST /admin/roles/:id/permissions */
  http.post(
    "*/api/admin/roles/:id/permissions",
    async ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const roleId = Number(params.id);
      if (!mockDb.roles.some(role => role.id === roleId)) {
        return fail(2007, "角色不存在", 404);
      }

      const body = (await request.json()) as { permission_id?: number };
      const permissionId = Number(body?.permission_id);
      if (!mockDb.permissions.some(item => item.id === permissionId)) {
        return fail(2009, "权限不存在", 404);
      }

      mockDb.rolePermissions.add(`${roleId}:${permissionId}`);
      return ok(null);
    }
  ),

  /** DELETE /admin/roles/:id/permissions/:permission_id */
  http.delete(
    "*/api/admin/roles/:id/permissions/:permission_id",
    ({ request, params }) => {
      if (!isAuthorized(request)) return unauthorized();

      const roleId = Number(params.id);
      const permissionId = Number(params.permission_id);
      mockDb.rolePermissions.delete(`${roleId}:${permissionId}`);
      return ok(null);
    }
  ),

  /** GET /admin/permissions */
  http.get("*/api/admin/permissions", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    return ok({
      items: mockDb.permissions
        .slice()
        .sort((a, b) => a.id - b.id)
        .map(item => ({
          id: item.id,
          code: item.code,
          name: item.name,
          description: item.description
        }))
    });
  }),

  /** POST /admin/permissions */
  http.post("*/api/admin/permissions", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const body = (await request.json()) as {
      code?: string;
      name?: string;
      description?: string;
    };
    const code = (body?.code ?? "").trim();
    const name = (body?.name ?? "").trim();
    const description = (body?.description ?? "").trim();

    if (
      !isNonEmptyWithin64(code) ||
      !isNonEmptyWithin64(name) ||
      !isValidDescription(description)
    ) {
      return fail(1001, "参数校验失败", 400);
    }
    if (mockDb.permissions.some(item => item.code === code)) {
      return fail(2010, "权限 code 已存在", 409);
    }

    const id = mockDb.nextPermissionId++;
    mockDb.permissions.push({ id, code, name, description });
    return ok({ id });
  }),

  /** PUT /admin/permissions/:id */
  http.put("*/api/admin/permissions/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    const permission = mockDb.permissions.find(item => item.id === id);
    if (!permission) return fail(2009, "权限不存在", 404);

    const body = (await request.json()) as {
      code?: string;
      name?: string;
      description?: string;
    };
    const code = body?.code === undefined ? permission.code : body.code.trim();
    const name = body?.name === undefined ? permission.name : body.name.trim();
    const description =
      body?.description === undefined
        ? permission.description
        : body.description.trim();

    if (
      !isNonEmptyWithin64(code) ||
      !isNonEmptyWithin64(name) ||
      !isValidDescription(description)
    ) {
      return fail(1001, "参数校验失败", 400);
    }
    if (mockDb.permissions.some(item => item.id !== id && item.code === code)) {
      return fail(2010, "权限 code 已存在", 409);
    }

    permission.code = code;
    permission.name = name;
    permission.description = description;
    return ok({
      id: permission.id,
      code: permission.code,
      name: permission.name,
      description: permission.description
    });
  }),

  /** DELETE /admin/permissions/:id */
  http.delete("*/api/admin/permissions/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();

    const id = Number(params.id);
    if (!mockDb.permissions.some(item => item.id === id)) {
      return fail(2009, "权限不存在", 404);
    }

    mockDb.permissions = mockDb.permissions.filter(item => item.id !== id);
    for (const key of [...mockDb.rolePermissions]) {
      if (key.endsWith(`:${id}`)) mockDb.rolePermissions.delete(key);
    }
    return ok(null);
  })
];

/** 供测试引用：Mock 中的当前管理员 id */
export const MOCK_CURRENT_ADMIN_ID = CURRENT_ADMIN_ID;
