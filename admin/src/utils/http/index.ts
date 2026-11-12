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
import { formatToken, getAccessToken } from "@/utils/auth";
import {
  API_SUCCESS_CODE,
  BusinessError,
  isApiEnvelope
} from "@/api/types/envelope";

/**
 * 统一 `Axios` 实例。
 *
 * 职责边界（A1 起）：
 * - 传输层：超时、JSON 请求头、HTTP 错误与网络异常归一化。
 * - 认证层：注入 `Authorization: Bearer <token>`；识别 401 并触发统一的登录态失效处理。
 * - 契约层：解析后端统一 envelope `{code,message,data}`，`code === 0` 时只返回 `data`。
 *
 * 不负责：业务语义判断、权限菜单、路由跳转（由 store / router 承担）。
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
export type HttpErrorHandler = (text: string, error?: unknown) => void;

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

/**
 * 登录态失效（HTTP 401）处理器。
 *
 * 由应用启动时注册（见 `src/main.ts`），内部完成：清理本地 token、重置 Pinia
 * 管理员状态、跳转登录页。请求层不直接依赖 store / router，避免循环依赖。
 */
export type UnauthorizedHandler = (
  error?: PureHttpError
) => void | Promise<void>;

let unauthorizedHandler: UnauthorizedHandler | null = null;
/** 进行中的失效处理，保证并发 401 只清理一次 */
let unauthorizedTask: Promise<void> | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler;
  // 更换处理器时重置进行中标记：新处理器需要能重新接管下一次 401
  unauthorizedTask = null;
}

/** 是否存在正在执行的 401 处理流程（测试与调试用） */
export function isHandlingUnauthorized(): boolean {
  return unauthorizedTask !== null;
}

/**
 * 触发登录态失效处理（幂等）。
 *
 * 多个并发请求同时 401 时只会执行一次清理与跳转；处理结束后短暂保持，
 * 避免同一批请求的尾随 401 再次触发重复清理。
 */
function handleUnauthorized(error?: PureHttpError): Promise<void> {
  if (unauthorizedTask) return unauthorizedTask;
  unauthorizedTask = Promise.resolve(unauthorizedHandler?.(error))
    .catch(() => {
      // 处理器自身异常不应影响请求链
    })
    .then(() => {
      setTimeout(() => {
        unauthorizedTask = null;
      }, 500);
    });
  return unauthorizedTask;
}

/**
 * 规范化错误文案：优先超时，其次后端 envelope message，最后 HTTP 状态码。
 *
 * 为什么优先后端 message：商品/库存等接口在业务失败时会同时返回
 * HTTP 400/404/409 与 `{code,message}`，例如「库存不足」「有子分类不能删除」。
 * 只按状态码展示会把这类可操作的信息抹掉。
 */
function resolveErrorText(error: PureHttpError): string {
  if (error.code === "ECONNABORTED" || error.code === "ETIMEDOUT") {
    return "请求超时，请稍后重试";
  }
  if (error.businessMessage) {
    return error.businessMessage;
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

  /** 请求拦截：注入 Bearer Token */
  private httpInterceptorsRequest(): void {
    PureHttp.axiosInstance.interceptors.request.use(
      (config: PureHttpRequestConfig): any => {
        // 登录接口等显式声明 skipAuth 的请求不注入 token
        const token = getAccessToken();
        if (token && !config.skipAuth) {
          config.headers = config.headers ?? {};
          config.headers.Authorization = formatToken(token);
        }

        if (typeof config.beforeRequestCallback === "function") {
          config.beforeRequestCallback(config);
          return config;
        }
        if (PureHttp.initConfig.beforeRequestCallback) {
          PureHttp.initConfig.beforeRequestCallback(config);
          return config;
        }
        return config;
      },
      error => Promise.reject(error)
    );
  }

  /** 响应拦截：解析 envelope + 认证失效处理 */
  private httpInterceptorsResponse(): void {
    const instance = PureHttp.axiosInstance;
    instance.interceptors.response.use(
      (response: PureHttpResponse) => {
        const $config = response.config;

        if (typeof $config.beforeResponseCallback === "function") {
          $config.beforeResponseCallback(response);
        } else if (PureHttp.initConfig.beforeResponseCallback) {
          PureHttp.initConfig.beforeResponseCallback(response);
        }

        const payload = response.data;

        // 非 envelope 响应（文件流、纯文本、第三方静态 JSON）原样返回
        if (!isApiEnvelope(payload)) return payload;

        if (payload.code === API_SUCCESS_CODE) return payload.data;

        /**
         * 业务失败：HTTP 状态可能是 200。
         * 这里在成功拦截器中抛出，不会再进入下方 error 分支，避免重复提示。
         */
        const businessError = new BusinessError(payload.code, payload.message);
        if (!$config.silent) {
          errorHandler(businessError.message, businessError);
        }
        return Promise.reject(businessError);
      },
      (error: PureHttpError) => {
        const $error = error;
        // Axios 的 error.config 类型是 InternalAxiosRequestConfig，
        // 这里需要读回自定义字段（silent / skipUnauthorizedHandling）
        const requestConfig = $error.config as
          | PureHttpRequestConfig
          | undefined;
        $error.isCancelRequest = Axios.isCancel($error);

        // 主动取消的请求（如路由切换）不提示、不触发登录态处理
        if ($error.isCancelRequest) return Promise.reject($error);

        const status = $error.response?.status;

        /**
         * 非 2xx 响应也可能带统一 envelope（后端错误码映射到 400/404/409）。
         * 这里只做**附加信息提取**，不改变 reject 类型与原有提示逻辑。
         */
        const failurePayload = $error.response?.data;
        if (
          isApiEnvelope(failurePayload) &&
          failurePayload.code !== API_SUCCESS_CODE
        ) {
          $error.businessCode = failurePayload.code;
          $error.businessMessage = failurePayload.message;
        }

        /**
         * 401：登录态失效。
         * 只触发统一的失效处理（清理 + 跳转），不在这里重复弹错误提示，
         * 也**不**因为 403 而清理登录态（403 由业务层渲染"权限不足"）。
         */
        if (status === 401 && !requestConfig?.skipUnauthorizedHandling) {
          void handleUnauthorized($error);
          return Promise.reject($error);
        }

        if (!requestConfig?.silent) {
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
