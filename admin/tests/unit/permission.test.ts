import { beforeEach, describe, expect, it } from "vitest";
import {
  PERMISSION_DASHBOARD_VIEW,
  ROLE_PERMISSION_MAP,
  getPermissionSubject,
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

  it("已配置角色按映射表授权（super_admin → dashboard:view）", () => {
    const admin = subject({ roles: ["super_admin"] });

    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: admin })).toBe(
      true
    );
    expect(resolvePermissions(admin)).toEqual([PERMISSION_DASHBOARD_VIEW]);
  });

  it("未知角色默认拒绝，不会被自动赋予权限", () => {
    const unknown = subject({ roles: ["operator", "auditor"] });

    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: unknown })).toBe(
      false
    );
    expect(resolvePermissions(unknown)).toEqual([]);
    // 映射表只登记了已明确知道的角色
    expect(Object.keys(ROLE_PERMISSION_MAP)).toEqual(["super_admin"]);
  });

  it("没有任何角色时不授予权限", () => {
    const anonymous = subject({});

    expect(
      hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: anonymous })
    ).toBe(false);
  });

  it("后端下发 permissions 时以它为准（不再回退角色映射）", () => {
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

  it("后端下发空 permissions 表示明确无权限，不回退到角色映射", () => {
    const noPermission = subject({ roles: ["super_admin"], permissions: [] });

    expect(resolvePermissions(noPermission)).toEqual([]);
    expect(
      hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: noPermission })
    ).toBe(false);
  });

  it("permissions 为 null（后端未提供该字段）时仍按角色映射正常工作", () => {
    const legacy = subject({ roles: ["super_admin"], permissions: null });

    expect(hasPermission(PERMISSION_DASHBOARD_VIEW, { subject: legacy })).toBe(
      true
    );
  });

  it("支持 some / every 两种匹配模式", () => {
    const admin = subject({ roles: ["super_admin"] });

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
      roles: ["super_admin"],
      permissions: null
    });

    expect(getPermissionSubject().roles).toEqual(["super_admin"]);
    expect(hasPermission(PERMISSION_DASHBOARD_VIEW)).toBe(true);

    resetPermissionSubject();
    expect(hasPermission(PERMISSION_DASHBOARD_VIEW)).toBe(false);
  });

  it("快照是副本，外部修改 roles 数组不会污染内部状态", () => {
    const roles = ["super_admin"];
    setPermissionSubject({ isSuper: false, roles, permissions: null });
    roles.push("operator");

    expect(getPermissionSubject().roles).toEqual(["super_admin"]);
  });
});
