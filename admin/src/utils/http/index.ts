import Axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type CustomParamsSerializer
} from "axios";
import type {
  PureHttpError,
  RequestMethods,
  PureHttpResponse,
  PureHttpRequestConfig
} from "./types.d";
import { stringify } from "qs";
import { message } from "@/utils/message";

/**
 * 统一 `Axios` 实例。
 *
 * 约定（A0）：
 * - 基础地址取自 `VITE_API_BASE_URL`，默认 `/api`，由 Vite 代理或网关转发到 Go 后端。
 * - 只负责「传输层」：超时、JSON 请求头、HTTP 错误与网络异常归一化。
 * - 不假设后端响应 envelope、业务错误码、Token 字段，这些等待后端 OpenAPI 契约确认；
 *   业务层通过 `src/api/modules` 自行解析并声明类型。
 */

/** HTTP 状态码 → 默认提示文案 */
const httpStatusMessages: Record<number, string> = {
  400: "请求参数有误",
  401: "未认证或登录已过期",
  403: "没有访问权限",
  404: "请求的资源不存在",
  408: "请求超时",
  409: "请求冲突",
  413: "请求体过大",
  422: "请求参数校验失败",
  429: "请求过于频繁",
  500: "服务器内部错误",
  502: "网关错误",
  503: "服务不可用",
  504: "网关超时"
};

/** 统一错误提示入口，默认使用 Element Plus `Message` 弹出错误提示 */
export type HttpErrorHandler = (text: string, error: PureHttpError) => void;

let errorHandler: HttpErrorHandler = (text: string) => {
  message(text, { type: "error" });
};

/**
 * 替换统一错误提示入口。
 * 后续如需接入监控上报、国际化文案或全局弹窗，在此处替换即可，无需改动业务代码。
 */
export function setHttpErrorHandler(handler: HttpErrorHandler) {
  errorHandler = handler;
}

/** 规范化错误文案：优先 HTTP 状态码映射，其次超时/网络异常 */
function resolveErrorText(error: PureHttpError): string {
  if (error.code === "ECONNABORTED" || error.code === "ETIMEDOUT") {
    return "请求超时，请稍后重试";
  }
  if (error.response) {
    const status = error.response.status;
    return httpStatusMessages[status] ?? `请求失败（HTTP ${status}）`;
  }
  return "网络异常，请检查网络连接后重试";
}

// 相关配置请参考：www.axios-js.com/zh-cn/docs/#axios-request-config-1
const defaultConfig: AxiosRequestConfig = {
  // 接口基础地址，见 .env.*  中的 VITE_API_BASE_URL
  baseURL: import.meta.env.VITE_API_BASE_URL || "/api",
  // 请求超时时间
  timeout: 10000,
  headers: {
    Accept: "application/json, text/plain, */*",
    "Content-Type": "application/json",
    "X-Requested-With": "XMLHttpRequest"
  },
  // 数组格式参数序列化（https://github.com/axios/axios/issues/5142）
  paramsSerializer: {
    serialize: stringify as unknown as CustomParamsSerializer
  }
};

class PureHttp {
  constructor() {
    this.httpInterceptorsRequest();
    this.httpInterceptorsResponse();
  }

  /** 初始化配置对象 */
  private static initConfig: PureHttpRequestConfig = {};

  /** 保存当前`Axios`实例对象 */
  private static axiosInstance: AxiosInstance = Axios.create(defaultConfig);

  /** 暴露原始实例，供后续需要自定义拦截器的场景使用 */
  public get instance(): AxiosInstance {
    return PureHttp.axiosInstance;
  }

  /** 请求拦截 */
  private httpInterceptorsRequest(): void {
    PureHttp.axiosInstance.interceptors.request.use(
      (config: PureHttpRequestConfig): any => {
        if (typeof config.beforeRequestCallback === "function") {
          config.beforeRequestCallback(config);
          return config;
        }
        if (PureHttp.initConfig.beforeRequestCallback) {
          PureHttp.initConfig.beforeRequestCallback(config);
          return config;
        }
        /**
         * 认证相关（注入 Token、刷新 Token）属于 A1。
         * 届时在此处统一附加请求头，避免业务代码重复处理。
         */
        return config;
      },
      error => Promise.reject(error)
    );
  }

  /** 响应拦截 */
  private httpInterceptorsResponse(): void {
    const instance = PureHttp.axiosInstance;
    instance.interceptors.response.use(
      (response: PureHttpResponse) => {
        const $config = response.config;
        if (typeof $config.beforeResponseCallback === "function") {
          $config.beforeResponseCallback(response);
          return response.data;
        }
        if (PureHttp.initConfig.beforeResponseCallback) {
          PureHttp.initConfig.beforeResponseCallback(response);
          return response.data;
        }
        return response.data;
      },
      (error: PureHttpError) => {
        const $error = error;
        $error.isCancelRequest = Axios.isCancel($error);

        // 主动取消的请求（如路由切换）不提示
        if (!$error.isCancelRequest) {
          errorHandler(resolveErrorText($error), $error);
        }

        return Promise.reject($error);
      }
    );
  }

  /** 通用请求工具函数 */
  public request<T>(
    method: RequestMethods,
    url: string,
    param?: AxiosRequestConfig,
    axiosConfig?: PureHttpRequestConfig
  ): Promise<T> {
    const config = {
      method,
      url,
      ...param,
      ...axiosConfig
    } as PureHttpRequestConfig;

    // 单独处理自定义请求/响应回调
    return new Promise((resolve, reject) => {
      PureHttp.axiosInstance
        .request<T>(config)
        .then(response => {
          resolve(response as T);
        })
        .catch(error => {
          reject(error);
        });
    });
  }

  /** 单独抽离的`post`工具函数 */
  public post<T, P>(
    url: string,
    params?: AxiosRequestConfig<P>,
    config?: PureHttpRequestConfig
  ): Promise<T> {
    return this.request<T>("post", url, params, config);
  }

  /** 单独抽离的`get`工具函数 */
  public get<T, P>(
    url: string,
    params?: AxiosRequestConfig<P>,
    config?: PureHttpRequestConfig
  ): Promise<T> {
    return this.request<T>("get", url, params, config);
  }
}

export const http = new PureHttp();
