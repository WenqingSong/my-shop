import { HttpResponse, http } from "msw";

/**
 * 管理员认证 Mock。
 *
 * 用途：本地演示（`VITE_MOCK_ENABLED=true`）与 Vitest 单元测试夹具。
 * **不是生产数据，也不是真实凭据**；账号密码仅用于让 Mock 流程跑通，
 * 任何真实环境都不应依赖这里的值。
 *
 * 响应结构与后端 `api/admin/v1` 一致：
 * 成功 `{code:0,message:"OK",data:{...}}`，业务失败 `{code:非0,...}`（HTTP 仍可能是 200），
 * 未认证返回 HTTP 401 + `{code:1001,...}`。
 */
export const MOCK_ADMIN_USERNAME = "admin";
export const MOCK_ADMIN_PASSWORD = "mock-password";
export const MOCK_ACCESS_TOKEN = "mock-access-token";
export const MOCK_EXPIRES_IN = 3600;

/** 演示/测试用管理员身份 */
export const MOCK_ADMIN_PROFILE = {
  id: 1,
  username: "admin",
  is_super: true,
  roles: ["super_admin"]
};

function envelopeError(code: number, message: string, status = 200) {
  return HttpResponse.json({ code, message, data: null }, { status });
}

export const adminHandlers = [
  /** POST /admin/login */
  http.post("*/api/admin/login", async ({ request }) => {
    const body = (await request.json()) as {
      username?: string;
      password?: string;
    };

    if (
      body?.username !== MOCK_ADMIN_USERNAME ||
      body?.password !== MOCK_ADMIN_PASSWORD
    ) {
      return envelopeError(1002, "用户名或密码错误");
    }

    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: {
        access_token: MOCK_ACCESS_TOKEN,
        token_type: "Bearer",
        expires_in: MOCK_EXPIRES_IN
      }
    });
  }),

  /** GET /admin/me */
  http.get("*/api/admin/me", ({ request }) => {
    const authorization = request.headers.get("Authorization");
    if (authorization !== `Bearer ${MOCK_ACCESS_TOKEN}`) {
      return envelopeError(1001, "未认证或登录已过期", 401);
    }
    return HttpResponse.json({
      code: 0,
      message: "OK",
      data: MOCK_ADMIN_PROFILE
    });
  }),

  /** POST /admin/logout */
  http.post("*/api/admin/logout", ({ request }) => {
    const authorization = request.headers.get("Authorization");
    if (authorization !== `Bearer ${MOCK_ACCESS_TOKEN}`) {
      return envelopeError(1001, "未认证或登录已过期", 401);
    }
    return HttpResponse.json({ code: 0, message: "OK", data: null });
  })
];
