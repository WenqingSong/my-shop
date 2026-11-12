import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HttpResponse, http as mswHttp } from "msw";
import { server } from "../setup";
import {
  http,
  isHandlingUnauthorized,
  setHttpErrorHandler,
  setUnauthorizedHandler
} from "@/utils/http";
import { clearAccessToken, getAccessToken, setAccessToken } from "@/utils/auth";
import { BusinessError } from "@/api/types/envelope";
import { MOCK_ACCESS_TOKEN } from "@/mocks/handlers/admin";

/** 等待 401 处理链完成（handleUnauthorized 内部是微任务 + 定时复位） */
const flush = () => new Promise(resolve => setTimeout(resolve, 50));

describe("请求层认证行为", () => {
  beforeEach(() => {
    clearAccessToken();
    window.sessionStorage.clear();
  });

  afterEach(() => {
    setHttpErrorHandler(() => {});
    setUnauthorizedHandler(null);
  });

  it("HTTP 401 触发统一登录态失效处理", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    setAccessToken("invalid-token", 3600);

    await expect(
      http.get("/admin/me", undefined, { silent: true })
    ).rejects.toMatchObject({ response: { status: 401 } });

    await flush();
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("并发 401 只触发一次登录态清理", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    setAccessToken("invalid-token", 3600);

    const results = await Promise.allSettled([
      http.get("/admin/me", undefined, { silent: true }),
      http.get("/admin/me", undefined, { silent: true }),
      http.get("/admin/logout", undefined, { silent: true })
    ]);

    await flush();
    expect(results.every(item => item.status === "rejected")).toBe(true);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(isHandlingUnauthorized()).toBe(true);
  });

  it("HTTP 403 不清理登录态，也不触发 401 处理", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    setAccessToken(MOCK_ACCESS_TOKEN, 3600);

    server.use(
      mswHttp.get("*/api/admin/me", () =>
        HttpResponse.json(
          { code: 1003, message: "没有访问权限", data: null },
          { status: 403 }
        )
      )
    );

    await expect(
      http.get("/admin/me", undefined, { silent: true })
    ).rejects.toMatchObject({ response: { status: 403 } });

    await flush();
    expect(onUnauthorized).not.toHaveBeenCalled();
    // 登录态保留，避免"无权限"被误处理成"未登录"
    expect(getAccessToken()).toBe(MOCK_ACCESS_TOKEN);
  });

  it("登录接口返回 401 时不会触发全局退出（skipUnauthorizedHandling）", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);

    server.use(
      mswHttp.post("*/api/admin/login", () =>
        HttpResponse.json(
          { code: 1001, message: "用户名或密码错误", data: null },
          { status: 401 }
        )
      )
    );

    await expect(
      http.post(
        "/admin/login",
        { data: { username: "admin", password: "bad" } },
        { silent: true, skipAuth: true, skipUnauthorizedHandling: true }
      )
    ).rejects.toMatchObject({ response: { status: 401 } });

    await flush();
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("业务失败（HTTP 200 + code !== 0）抛出 BusinessError 并保留 code/message", async () => {
    server.use(
      mswHttp.get("*/api/admin/dashboard/overview", () =>
        HttpResponse.json({ code: 18001, message: "参数不合法", data: null })
      )
    );

    const error = (await http
      .get("/admin/dashboard/overview", undefined, { silent: true })
      .catch(e => e)) as BusinessError;

    expect(error).toBeInstanceOf(BusinessError);
    expect(error.code).toBe(18001);
    expect(error.serverMessage).toBe("参数不合法");
    expect(error.message).toBe("参数不合法");
  });

  it("业务失败在非静默模式下会走统一错误提示，且只提示一次", async () => {
    const onError = vi.fn();
    setHttpErrorHandler(onError);

    server.use(
      mswHttp.get("*/api/admin/dashboard/overview", () =>
        HttpResponse.json({ code: 18001, message: "参数不合法", data: null })
      )
    );

    await expect(
      http.get("/admin/dashboard/overview", undefined, {})
    ).rejects.toBeInstanceOf(BusinessError);

    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0][0]).toBe("参数不合法");
  });

  it("静默模式下业务失败不触发全局提示", async () => {
    const onError = vi.fn();
    setHttpErrorHandler(onError);

    server.use(
      mswHttp.get("*/api/admin/dashboard/flash-sales", () =>
        HttpResponse.json({ code: 18001, message: "参数不合法", data: null })
      )
    );

    await expect(
      http.get("/admin/dashboard/flash-sales", undefined, { silent: true })
    ).rejects.toBeInstanceOf(BusinessError);

    expect(onError).not.toHaveBeenCalled();
  });

  it("静默模式不会吞掉 401 的登录态处理", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    setAccessToken("invalid-token", 3600);

    await expect(
      http.get("/admin/me", undefined, { silent: true })
    ).rejects.toMatchObject({ response: { status: 401 } });

    await flush();
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("envelope 成功时只返回 data 部分", async () => {
    setAccessToken(MOCK_ACCESS_TOKEN, 3600);

    const data = await http.get<{ user_total: number }, undefined>(
      "/admin/dashboard/overview",
      undefined,
      { silent: true }
    );

    expect(data.user_total).toBe(1280);
    expect((data as Record<string, unknown>).code).toBeUndefined();
  });
});
