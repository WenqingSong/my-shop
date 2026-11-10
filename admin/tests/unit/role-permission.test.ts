import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { mockDb, resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  assignRolePermission,
  createRole,
  deleteRole,
  listRoles,
  removeRolePermission,
  updateRole
} from "@/api/modules/role";
import {
  createPermission,
  deletePermission,
  listPermissions,
  updatePermission
} from "@/api/modules/permission";
import {
  buildCreatePermissionPayload,
  buildCreateRolePayload,
  buildUpdatePermissionPayload,
  buildUpdateRolePayload,
  filterPermissions,
  groupPermissionsByDomain,
  permissionDomain,
  toPermissionView,
  toRoleView,
  validatePermissionForm,
  validateRoleForm
} from "@/api/adapters/access-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("角色与权限管理（后端真实接口）", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("角色列表只有 id/name/description（后端无状态字段）", async () => {
    const result = await listRoles();

    expect(result.items.map(role => role.id)).toEqual([501, 502]);
    expect(Object.keys(result.items[0]).sort()).toEqual([
      "description",
      "id",
      "name"
    ]);
  });

  it("创建角色并返回新 id；重名返回 2008（409）", async () => {
    const created = await createRole({
      name: "support",
      description: "客服"
    });
    expect(created.id).toBe(600);

    const list = await listRoles();
    expect(list.items.map(role => role.name)).toContain("support");

    await expectBusinessCode(
      createRole({ name: "support", description: "" }),
      2008
    );
  });

  it("后端拒绝空角色名与超长描述（1001）", async () => {
    await expectBusinessCode(createRole({ name: "  ", description: "" }), 1001);
    await expectBusinessCode(
      createRole({ name: "too-long", description: "a".repeat(256) }),
      1001
    );
  });

  it("更新角色名称与描述；不存在返回 2007", async () => {
    const updated = await updateRole(501, {
      name: "operator-v2",
      description: "日常运营（更新）"
    });
    expect(updated).toEqual({
      id: 501,
      name: "operator-v2",
      description: "日常运营（更新）"
    });

    await expectBusinessCode(updateRole(999999, { name: "x" }), 2007);
  });

  it("删除角色会级联清理角色权限关联；再次删除返回 2007", async () => {
    expect(
      [...mockDb.rolePermissions].some(key => key.startsWith("501:"))
    ).toBe(true);

    await deleteRole(501);

    expect(mockDb.roles.some(role => role.id === 501)).toBe(false);
    expect(
      [...mockDb.rolePermissions].some(key => key.startsWith("501:"))
    ).toBe(false);

    await expectBusinessCode(deleteRole(501), 2007);
  });

  it("权限列表返回系统全部定义（49 条种子权限），字段与后端一致", async () => {
    const result = await listPermissions();

    expect(result.items).toHaveLength(49);
    expect(Object.keys(result.items[0]).sort()).toEqual([
      "code",
      "description",
      "id",
      "name"
    ]);

    const codes = result.items.map(item => item.code);
    expect(codes).toContain("dashboard:view");
    expect(codes).toContain("user:read");
    expect(codes).toContain("coupon:stop");
    expect(codes).toContain("role:assign_permission");
  });

  it("创建/更新/删除权限；重复 code 返回 2010，缺失返回 2009", async () => {
    const created = await createPermission({
      code: "report:export",
      name: "导出报表",
      description: ""
    });
    expect(created.id).toBe(100);

    await expectBusinessCode(
      createPermission({
        code: "report:export",
        name: "重复",
        description: ""
      }),
      2010
    );

    const updated = await updatePermission(created.id, {
      name: "导出运营报表"
    });
    expect(updated.name).toBe("导出运营报表");
    expect(updated.code).toBe("report:export");

    await deletePermission(created.id);
    expect(mockDb.permissions.some(item => item.id === created.id)).toBe(false);

    await expectBusinessCode(deletePermission(created.id), 2009);
    await expectBusinessCode(updatePermission(created.id, { name: "x" }), 2009);
  });

  it("角色权限授予/移除均为幂等写操作", async () => {
    await assignRolePermission(502, { permission_id: 27 });
    expect(mockDb.rolePermissions.has("502:27")).toBe(true);

    await assignRolePermission(502, { permission_id: 27 });
    expect(mockDb.rolePermissions.has("502:27")).toBe(true);

    await removeRolePermission(502, 27);
    expect(mockDb.rolePermissions.has("502:27")).toBe(false);

    await removeRolePermission(502, 27);
    expect(mockDb.rolePermissions.has("502:27")).toBe(false);
  });

  it("角色/权限不存在时授予返回 2007 / 2009", async () => {
    await expectBusinessCode(
      assignRolePermission(999999, { permission_id: 1 }),
      2007
    );
    await expectBusinessCode(
      assignRolePermission(501, { permission_id: 999999 }),
      2009
    );
  });

  it("权限不足时展示统一提示", async () => {
    server.use(
      mswHttp.get("*/api/admin/roles", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await listRoles();
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("权限码前缀推导业务域（不生成新权限码）", () => {
    expect(permissionDomain("product:create")).toBe("product");
    expect(permissionDomain("flash_sale:update")).toBe("flash_sale");
    expect(permissionDomain("permission:list")).toBe("permission");
    expect(permissionDomain("standalone")).toBe("standalone");

    const view = toPermissionView({
      id: 1,
      code: "user:read",
      name: "查看用户状态",
      description: ""
    });
    expect(view.domain).toBe("user");
    expect(view.domainLabel).toBe("用户");
    expect(view.code).toBe("user:read");
  });

  it("权限按业务域分组与筛选基于完整数据集", async () => {
    const result = await listPermissions();
    const views = result.items.map(toPermissionView);

    const groups = groupPermissionsByDomain(views);
    expect(groups.map(group => group.domain)).toContain("dashboard");
    expect(
      groups
        .find(group => group.domain === "user")
        ?.items.map(item => item.code)
    ).toEqual(["user:read", "user:status"]);

    expect(
      filterPermissions(views, "coupon", "").map(item => item.code)
    ).toEqual(["coupon:create", "coupon:list", "coupon:stop"]);

    expect(
      filterPermissions(views, "", "admin").map(item => item.code)
    ).toEqual([
      "admin:create",
      "admin:disable",
      "admin:delete",
      "admin:assign_role"
    ]);

    expect(filterPermissions(views, "不存在的权限", "").length).toBe(0);
  });

  it("角色表单校验：名称必填且 ≤64，描述 ≤255", () => {
    expect(validateRoleForm({ name: " ", description: "" }).name).toBe(
      "请输入角色名称"
    );
    expect(
      validateRoleForm({ name: "a".repeat(65), description: "" }).name
    ).toContain("64");
    expect(
      validateRoleForm({ name: "ok", description: "a".repeat(256) }).description
    ).toContain("255");
    expect(validateRoleForm({ name: "ok", description: "" })).toEqual({});
  });

  it("权限表单校验与后端规则一致", () => {
    const errors = validatePermissionForm({
      code: "",
      name: "",
      description: ""
    });
    expect(errors.code).toBe("请输入权限码");
    expect(errors.name).toBe("请输入权限名称");

    expect(
      validatePermissionForm({
        code: "a".repeat(65),
        name: "ok",
        description: ""
      }).code
    ).toContain("64");

    expect(
      validatePermissionForm({
        code: "report:view",
        name: "查看报表",
        description: "a".repeat(256)
      }).description
    ).toContain("255");
  });

  it("请求体构造：trim 且只提交变化字段", () => {
    expect(
      buildCreateRolePayload({ name: " ops ", description: " 描述 " })
    ).toEqual({ name: "ops", description: "描述" });

    expect(
      buildUpdateRolePayload(
        { id: 1, name: "ops", description: "旧" },
        { name: " ops ", description: "新" }
      )
    ).toEqual({ description: "新" });

    expect(
      buildCreatePermissionPayload({
        code: " report:view ",
        name: " 查看报表 ",
        description: ""
      })
    ).toEqual({ code: "report:view", name: "查看报表", description: "" });

    expect(
      buildUpdatePermissionPayload(
        { id: 1, code: "report:view", name: "查看报表", description: "" },
        { code: "report:view", name: "导出报表", description: "" }
      )
    ).toEqual({ name: "导出报表" });
  });

  it("角色视图不伪造后端不存在的字段", () => {
    const view = toRoleView({ id: 501, name: "operator", description: "" });

    expect(view.descriptionText).toBe("—");
    expect(Object.keys(view).sort()).toEqual([
      "description",
      "descriptionText",
      "id",
      "name"
    ]);
  });

  it("角色与权限权限码全部来自后端真实定义", () => {
    expect(PERMISSION.ROLE_LIST).toBe("role:list");
    expect(PERMISSION.ROLE_CREATE).toBe("role:create");
    expect(PERMISSION.ROLE_UPDATE).toBe("role:update");
    expect(PERMISSION.ROLE_DELETE).toBe("role:delete");
    expect(PERMISSION.ROLE_ASSIGN_PERMISSION).toBe("role:assign_permission");
    expect(PERMISSION.PERMISSION_LIST).toBe("permission:list");
    expect(PERMISSION.PERMISSION_CREATE).toBe("permission:create");
    expect(PERMISSION.PERMISSION_UPDATE).toBe("permission:update");
    expect(PERMISSION.PERMISSION_DELETE).toBe("permission:delete");
  });
});
