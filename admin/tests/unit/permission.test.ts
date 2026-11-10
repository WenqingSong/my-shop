import { beforeEach, describe, expect, it } from "vitest";
import {
  PERMISSION,
  PERMISSION_DASHBOARD_VIEW,
  getPermissionSignature,
  getPermissionSubject,
  hasMetaPermission,
  hasPermission,
  resetPermissionSubject,
  resolvePermissions,
  setPermissionSubject,
  type PermissionSubject
} from "@/utils/permission";

function subject(partial: Partial<PermissionSubject>): PermissionSubject {
  return {
    isSuper: false,
    roles: [],
    permissions: null,
    ...partial
  };
}

describe("前端权限映射", () => {
  beforeEach(() => {
    resetPermissionSubject();
  });

  it("超级管理员通过任意前端权限检查", () => {
    const admin = subject({ isSuper: true, roles: ["super_admin"] });

    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: admin })).toBe(
      true
    );
    expect(hasPermission("any:other:permission", { subject: admin })).toBe(
      true
    );
    expect(resolvePermissions(admin)).toEqual(["*"]);
  });

  it("非超级管理员且后端未下发 permissions 时 fail-closed（即使角色是 super_admin）", () => {
    const admin = subject({ roles: ["super_admin"] });

    expect(resolvePermissions(admin)).toEqual([]);
    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: admin })).toBe(
      false
    );
    expect(hasPermission(PERMISSION.ROLE_LIST, { subject: admin })).toBe(false);
  });

  it("未知角色不会被自动赋予任何权限", () => {
    const unknown = subject({ roles: ["operator", "auditor"] });

    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: unknown })).toBe(
      false
    );
    expect(resolvePermissions(unknown)).toEqual([]);
  });

  it("没有任何角色且没有 permissions 时不授予权限", () => {
    const anonymous = subject({});

    expect(
      hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: anonymous })
    ).toBe(false);
    expect(getPermissionSignature(anonymous)).toBe("");
  });

  it("后端下发 permissions 时以它为准（角色名不参与计算）", () => {
    const fromBackend = subject({
      roles: ["super_admin"],
      permissions: ["order:read"]
    });

    expect(resolvePermissions(fromBackend)).toEqual(["order:read"]);
    expect(hasPermission("order:read", { subject: fromBackend })).toBe(true);
    expect(
      hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: fromBackend })
    ).toBe(false);
  });

  it("后端下发空 permissions 表示明确无权限", () => {
    const noPermission = subject({ roles: ["super_admin"], permissions: [] });

    expect(resolvePermissions(noPermission)).toEqual([]);
    expect(
      hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: noPermission })
    ).toBe(false);
  });

  it("支持 some / every 两种匹配模式", () => {
    const admin = subject({
      permissions: [PERMISSION_DASHBOARD_VIEW]
    });

    expect(
      hasPermission([PERMISSION_DASHBOARD_VIEW, "order:read"], {
        subject: admin,
        mode: "some"
      })
    ).toBe(true);
    expect(
      hasPermission([PERMISSION_DASHBOARD_VIEW, "order:read"], {
        subject: admin,
        mode: "every"
      })
    ).toBe(false);
  });

  it("空权限要求视为通过（不需要额外权限）", () => {
    expect(hasPermission([], { subject: subject({}) })).toBe(true);
  });

  it("setPermissionSubject 同步当前快照，供组件直接调用 hasPermission", () => {
    setPermissionSubject({
      isSuper: false,
      roles: [],
      permissions: [PERMISSION.ROLE_LIST]
    });

    expect(getPermissionSubject().permissions).toEqual([PERMISSION.ROLE_LIST]);
    expect(hasPermission(PERMISSION.ROLE_LIST)).toBe(true);
    expect(hasPermission(PERMISSION.PERMISSION_LIST)).toBe(false);

    resetPermissionSubject();
    expect(hasPermission(PERMISSION.ROLE_LIST)).toBe(false);
  });

  it("快照是副本，外部修改 roles / permissions 数组不会污染内部状态", () => {
    const roles: string[] = ["super_admin"];
    const permissions: string[] = [PERMISSION.USER_READ];
    setPermissionSubject({ isSuper: false, roles, permissions });
    roles.push("operator");
    permissions.push(PERMISSION.USER_STATUS);

    expect(getPermissionSubject().roles).toEqual(["super_admin"]);
    expect(getPermissionSubject().permissions).toEqual([PERMISSION.USER_READ]);
  });

  it("路由 meta 权限：未声明即只要求登录", () => {
    expect(hasMetaPermission(undefined)).toBe(true);
    expect(hasMetaPermission({})).toBe(true);
    expect(hasMetaPermission({ permissions: [] })).toBe(true);
  });

  it("路由 meta 权限：every 模式需要全部命中真实权限码", () => {
    setPermissionSubject({
      isSuper: false,
      roles: [],
      permissions: [PERMISSION.ROLE_LIST]
    });

    expect(
      hasMetaPermission({
        permissions: [PERMISSION.ROLE_LIST, PERMISSION.PERMISSION_LIST]
      })
    ).toBe(false);
    expect(hasMetaPermission({ permissions: [PERMISSION.ROLE_LIST] })).toBe(
      true
    );
  });

  it("路由 meta 权限：some 模式只需命中一个真实权限码", () => {
    setPermissionSubject({
      isSuper: false,
      roles: [],
      permissions: [PERMISSION.ADMIN_DISABLE]
    });

    expect(
      hasMetaPermission({
        permissions: [
          PERMISSION.ADMIN_CREATE,
          PERMISSION.ADMIN_DISABLE,
          PERMISSION.ADMIN_DELETE,
          PERMISSION.ADMIN_ASSIGN_ROLE
        ],
        permissionsMode: "some"
      })
    ).toBe(true);

    expect(hasMetaPermission({ permissions: [PERMISSION.ADMIN_CREATE] })).toBe(
      false
    );
  });

  it("权限签名用于菜单重建：与权限集合顺序无关，权限变化则变化", () => {
    const a = getPermissionSignature(
      subject({ permissions: [PERMISSION.USER_READ, PERMISSION.ROLE_LIST] })
    );
    const b = getPermissionSignature(
      subject({ permissions: [PERMISSION.ROLE_LIST, PERMISSION.USER_READ] })
    );
    const c = getPermissionSignature(
      subject({ permissions: [PERMISSION.USER_READ] })
    );

    expect(a).toBe(b);
    expect(a).not.toBe(c);
    expect(getPermissionSignature(subject({ isSuper: true }))).toBe("*");
  });

  it("A5 权限码全部来自后端真实定义", () => {
    expect(PERMISSION.USER_READ).toBe("user:read");
    expect(PERMISSION.USER_STATUS).toBe("user:status");
    expect(PERMISSION.ADMIN_CREATE).toBe("admin:create");
    expect(PERMISSION.ADMIN_DISABLE).toBe("admin:disable");
    expect(PERMISSION.ADMIN_DELETE).toBe("admin:delete");
    expect(PERMISSION.ADMIN_ASSIGN_ROLE).toBe("admin:assign_role");
    expect(PERMISSION.ROLE_LIST).toBe("role:list");
    expect(PERMISSION.ROLE_ASSIGN_PERMISSION).toBe("role:assign_permission");
    expect(PERMISSION.PERMISSION_LIST).toBe("permission:list");

    // 后端没有读权限码的能力，前端不得编造
    expect(Object.values(PERMISSION)).not.toContain("admin:list");
    expect(Object.values(PERMISSION)).not.toContain("user:list");
    expect(Object.values(PERMISSION)).not.toContain("role:view");
  });
});
