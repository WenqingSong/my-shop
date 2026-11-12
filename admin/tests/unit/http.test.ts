import { afterEach, describe, expect, it, vi } from "vitest";
import { AxiosError, type AxiosRequestConfig } from "axios";
import { http, setHttpErrorHandler } from "@/utils/http";

const instance = http.instance;
const originalAdapter = instance.defaults.adapter;

function useAdapter(adapter: (config: AxiosRequestConfig) => Promise<any>) {
  instance.defaults.adapter = adapter as any;
}

afterEach(() => {
  instance.defaults.adapter = originalAdapter;
  setHttpErrorHandler(() => {});
});

describe("统一 Axios 实例", () => {
  it("使用 /api 作为默认基础地址并开启超时与 JSON 请求头", () => {
    expect(instance.defaults.baseURL).toBe("/api");
    expect(instance.defaults.timeout).toBe(10000);
    expect(instance.defaults.headers["Content-Type"]).toBe("application/json");
    expect(instance.defaults.headers.Accept).toContain("application/json");
  });

  it("响应拦截直接返回响应体，不假设业务 envelope", async () => {
    useAdapter(async config => ({
      data: { ok: true, echo: config.url },
      status: 200,
      statusText: "OK",
      headers: {},
      config
    }));

    const result = await http.get<{ ok: boolean; echo: string }, any>("/ping");

    expect(result).toEqual({ ok: true, echo: "/ping" });
  });

  it("网络异常通过统一错误处理入口提示", async () => {
    const onError = vi.fn();
    setHttpErrorHandler(onError);
    useAdapter(async () => {
      throw new AxiosError("Network Error", "ERR_NETWORK");
    });

    await expect(http.get("/boom")).rejects.toBeTruthy();

    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0][0]).toContain("网络异常");
  });

  it("HTTP 错误按状态码归一化为提示文案", async () => {
    const onError = vi.fn();
    setHttpErrorHandler(onError);
    useAdapter(async config => {
      throw new AxiosError(
        "Request failed",
        "ERR_BAD_RESPONSE",
        config as any,
        null,
        {
          data: null,
          status: 502,
          statusText: "Bad Gateway",
          headers: {},
          config: config as any
        }
      );
    });

    await expect(http.get("/broken")).rejects.toBeTruthy();

    expect(onError.mock.calls[0][0]).toBe("网关错误");
  });

  it("请求超时会被识别为超时提示", async () => {
    const onError = vi.fn();
    setHttpErrorHandler(onError);
    useAdapter(async () => {
      throw new AxiosError("timeout of 10000ms exceeded", "ECONNABORTED");
    });

    await expect(http.get("/slow")).rejects.toBeTruthy();

    expect(onError.mock.calls[0][0]).toContain("请求超时");
  });
});
