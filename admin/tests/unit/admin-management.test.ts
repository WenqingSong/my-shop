import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { mockDb, resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import {
  assignAdminRole,
  createAdmin,
  deleteAdmin,
  removeAdminRole,
  updateAdminStatus
} from "@/api/modules/admin";
import { listRoles } from "@/api/modules/role";
import {
  buildCreateAdminPayload,
  parsePositiveId,
  validateCreateAdminForm
} from "@/api/adapters/access-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { PERMISSION } from "@/utils/permission";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("管理员管理（后端真实接口）", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("创建管理员：返回 id 与用户名，不回传密码", async () => {
    const created = await createAdmin({
      username: "ops99",
      password: "password123"
    });

    expect(created).toEqual({ id: 10, username: "ops99" });
    expect(Object.keys(created).sort()).toEqual(["id", "username"]);

    const stored = mockDb.admins.find(admin => admin.id === created.id);
    expect(stored?.is_super).toBe(0);
    expect(stored?.status).toBe(1);
  });

  it("创建管理员时用户名重复返回 2004（409）", async () => {
    await expectBusinessCode(
      createAdmin({ username: "ops01", password: "password123" }),
      2004
    );
  });

  it("后端拒绝非法用户名与密码（1001）", async () => {
    await expectBusinessCode(
      createAdmin({ username: "a", password: "password123" }),
      1001
    );
    await expectBusinessCode(
      createAdmin({ username: "ops98", password: "short" }),
      1001
    );
  });

  it("禁用/启用普通管理员", async () => {
    await updateAdminStatus(2, { status: 0 });
    expect(mockDb.admins.find(admin => admin.id === 2)?.status).toBe(0);

    await updateAdminStatus(2, { status: 1 });
    expect(mockDb.admins.find(admin => admin.id === 2)?.status).toBe(1);
  });

  it("超级管理员受后端保护：禁用返回 2005（403）", async () => {
    await expectBusinessCode(updateAdminStatus(1, { status: 0 }), 2005);
  });

  it("删除超级管理员同样被拒绝（2005）：保护所有超级管理员，而不只是最后一个", async () => {
    await expectBusinessCode(deleteAdmin(1), 2005);
  });

  it("目标管理员不存在返回 2003（404）", async () => {
    await expectBusinessCode(updateAdminStatus(999999, { status: 0 }), 2003);
    await expectBusinessCode(deleteAdmin(999999), 2003);
  });

  it("删除普通管理员后再次删除返回 2003", async () => {
    await deleteAdmin(3);
    expect(mockDb.admins.some(admin => admin.id === 3)).toBe(false);

    await expectBusinessCode(deleteAdmin(3), 2003);
  });

  it("角色列表可用于角色分配（同 role:list 读权限）", async () => {
    const result = await listRoles();
    expect(result.items.map(role => role.id)).toEqual([501, 502]);
  });

  it("分配角色：幂等且角色必须存在（不存在 2007）", async () => {
    await assignAdminRole(2, { role_id: 501 });
    await assignAdminRole(2, { role_id: 501 });

    await expectBusinessCode(assignAdminRole(2, { role_id: 999999 }), 2007);
  });

  it("移除角色是幂等的", async () => {
    await removeAdminRole(2, 501);
    await removeAdminRole(2, 501);
  });

  it("超级管理员的角色不可被修改（2005）", async () => {
    await expectBusinessCode(assignAdminRole(1, { role_id: 501 }), 2005);
    await expectBusinessCode(removeAdminRole(1, 501), 2005);
  });

  it("权限不足时错误文案为统一提示", async () => {
    server.use(
      mswHttp.post("*/api/admin/admins", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await createAdmin({ username: "ops77", password: "password123" });
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("创建表单校验与后端规则一致（3~24 位字母数字 / 8~24 位密码）", () => {
    expect(validateCreateAdminForm({ username: "", password: "" })).toEqual({
      username: "请输入管理员用户名",
      password: "请输入管理员密码"
    });

    expect(
      validateCreateAdminForm({ username: "ab", password: "1234567" })
    ).toEqual({
      username: "用户名为 3~24 位大小写字母或数字",
      password: "密码长度为 8~24 位"
    });

    expect(
      validateCreateAdminForm({
        username: "ops01",
        password: "12345678"
      })
    ).toEqual({});

    expect(
      validateCreateAdminForm({
        username: "ops_01",
        password: "12345678"
      }).username
    ).toBeDefined();

    expect(
      buildCreateAdminPayload({ username: "  ops99 ", password: "p" })
    ).toEqual({ username: "ops99", password: "p" });
  });

  it("提交锁阻止并发重复提交", async () => {
    const guard = useSubmitGuard();
    let calls = 0;

    const task = async () => {
      calls += 1;
      await new Promise(resolve => setTimeout(resolve, 10));
      return calls;
    };

    const [first, second] = await Promise.all([
      guard.run(task),
      guard.run(task)
    ]);

    expect(calls).toBe(1);
    expect(first.executed).toBe(true);
    expect(second.executed).toBe(false);
  });

  it("目标 ID 解析复用正整数规则", () => {
    expect(parsePositiveId("2")).toBe(2);
    expect(parsePositiveId("2.0")).toBeNull();
    expect(parsePositiveId("")).toBeNull();
  });

  it("管理员权限码全部来自后端真实定义", () => {
    expect(PERMISSION.ADMIN_CREATE).toBe("admin:create");
    expect(PERMISSION.ADMIN_DISABLE).toBe("admin:disable");
    expect(PERMISSION.ADMIN_DELETE).toBe("admin:delete");
    expect(PERMISSION.ADMIN_ASSIGN_ROLE).toBe("admin:assign_role");
    // 后端没有 admin:list / admin:update，前端不得编造
    expect(Object.values(PERMISSION)).not.toContain("admin:list");
    expect(Object.values(PERMISSION)).not.toContain("admin:update");
  });
});
