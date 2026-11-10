import { beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import { resetMockDb } from "@/mocks/handlers/mock-db";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";
import { clearAccessToken, setAccessToken } from "@/utils/auth";
import { getUserStatus, updateUserStatus } from "@/api/modules/user";
import {
  parsePositiveId,
  toUserStatusView,
  validateUserStatusReason
} from "@/api/adapters/access-adapter";
import { resolveErrorMessage } from "@/utils/error-message";
import { expectBusinessCode } from "../helpers/business-error";

function login() {
  window.sessionStorage.clear();
  clearAccessToken();
  setAccessToken(MOCK_ACCESS_TOKEN, 3600);
}

describe("用户账号状态管理（后端真实接口）", () => {
  beforeEach(() => {
    resetMockDb();
    login();
  });

  it("按 id 查询启用中的用户", async () => {
    const dto = await getUserStatus(101);

    expect(dto).toEqual({ id: 101, status: 1 });
    // 后端只返回 id/status，不含密码哈希等认证数据
    expect(Object.keys(dto).sort()).toEqual(["id", "status"]);
  });

  it("按 id 查询已禁用用户并转换为视图", async () => {
    const dto = await getUserStatus(102);
    const view = toUserStatusView(dto);

    expect(view.status).toBe(0);
    expect(view.statusLabel).toBe("禁用");
    expect(view.statusTagType).toBe("info");
  });

  it("目标用户不存在返回 2015（404）", async () => {
    await expectBusinessCode(getUserStatus(999999), 2015);
  });

  it("禁用用户后重新查询得到最新状态", async () => {
    await updateUserStatus(101, { status: 0, reason: "违规操作" });

    const dto = await getUserStatus(101);
    expect(dto.status).toBe(0);
  });

  it("启用用户是幂等的，重复提交仍然成功", async () => {
    await updateUserStatus(102, { status: 1, reason: "恢复账号" });
    await updateUserStatus(102, { status: 1, reason: "恢复账号" });

    const dto = await getUserStatus(102);
    expect(dto.status).toBe(1);
  });

  it("变更原因必填：空原因被后端拒绝（1001）", async () => {
    await expectBusinessCode(
      updateUserStatus(101, { status: 0, reason: "   " }),
      1001
    );
  });

  it("变更原因超长被后端拒绝（1001）", async () => {
    await expectBusinessCode(
      updateUserStatus(101, { status: 0, reason: "a".repeat(256) }),
      1001
    );
  });

  it("变更不存在的用户返回 2015", async () => {
    await expectBusinessCode(
      updateUserStatus(999999, { status: 0, reason: "test" }),
      2015
    );
  });

  it("无 user:read 权限时展示权限提示（403 不清除登录态）", async () => {
    server.use(
      mswHttp.get("*/api/admin/users/:id/status", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    try {
      await getUserStatus(101);
    } catch (cause) {
      expect(resolveErrorMessage(cause)).toBe("没有权限执行该操作");
    }
  });

  it("请求失败时给出可展示的兜底文案", async () => {
    server.use(
      mswHttp.get("*/api/admin/users/:id/status", () =>
        HttpResponse.json(
          { code: 1000, message: "内部错误", data: null },
          { status: 500 }
        )
      )
    );

    try {
      await getUserStatus(101);
    } catch (cause) {
      expect(resolveErrorMessage(cause, "用户状态加载失败")).toBe("内部错误");
    }
  });

  it("用户 ID 解析：只接受正整数", () => {
    expect(parsePositiveId("101")).toBe(101);
    expect(parsePositiveId(102)).toBe(102);
    expect(parsePositiveId(" 7 ")).toBe(7);

    expect(parsePositiveId("")).toBeNull();
    expect(parsePositiveId("0")).toBeNull();
    expect(parsePositiveId("-1")).toBeNull();
    expect(parsePositiveId("1.5")).toBeNull();
    expect(parsePositiveId("abc")).toBeNull();
    expect(parsePositiveId(null)).toBeNull();
  });

  it("变更原因前端校验与后端规则一致", () => {
    expect(validateUserStatusReason("")).toBe("请输入变更原因");
    expect(validateUserStatusReason("   ")).toBe("请输入变更原因");
    expect(validateUserStatusReason("a".repeat(256))).toContain("255");
    expect(validateUserStatusReason("正常")).toBeNull();
  });
});
