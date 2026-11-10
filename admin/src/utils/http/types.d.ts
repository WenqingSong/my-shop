import type {
  Method,
  AxiosError,
  AxiosResponse,
  AxiosRequestConfig
} from "axios";

export type resultType = {
  accessToken?: string;
};

export type RequestMethods = Extract<
  Method,
  "get" | "post" | "put" | "delete" | "patch" | "option" | "head"
>;

export interface PureHttpError extends AxiosError {
  isCancelRequest?: boolean;
  /**
   * HTTP 状态非 2xx 时，后端 envelope 中的业务码。
   *
   * 后端对商品/库存等业务错误会使用 400/404/409 等 HTTP 状态，
   * 真正的错误语义在 `{code,message}` 里，这里提取出来供 UI 展示与断言。
   */
  businessCode?: number;
  /** HTTP 状态非 2xx 时，后端 envelope 中的 message */
  businessMessage?: string;
}

export interface PureHttpResponse extends AxiosResponse {
  config: PureHttpRequestConfig;
}

export interface PureHttpRequestConfig extends AxiosRequestConfig {
  beforeRequestCallback?: (request: PureHttpRequestConfig) => void;
  beforeResponseCallback?: (response: PureHttpResponse) => void;
  /**
   * 静默模式：请求失败（业务错误 / HTTP 错误 / 网络异常）时不触发全局错误提示，
   * 由调用方自行渲染错误状态。
   *
   * 用于：登录表单（错误内联展示）、工作台分区（展示"权限不足/加载失败 + 重试"）。
   * 注意：静默模式**不影响** 401 的登录态失效处理。
   */
  silent?: boolean;
  /** 登录接口等场景跳过 Authorization 头注入 */
  skipAuth?: boolean;
  /**
   * 跳过全局 401 失效处理。
   *
   * 仅用于"登录请求本身"：登录失败（401）应作为登录错误由表单展示，
   * 不能被当成"已登录会话失效"而触发全局清理与跳转。
   */
  skipUnauthorizedHandling?: boolean;
}

export default class PureHttp {
  request<T>(
    method: RequestMethods,
    url: string,
    param?: AxiosRequestConfig,
    axiosConfig?: PureHttpRequestConfig
  ): Promise<T>;
  post<T, P>(
    url: string,
    params?: P,
    config?: PureHttpRequestConfig
  ): Promise<T>;
  get<T, P>(
    url: string,
    params?: P,
    config?: PureHttpRequestConfig
  ): Promise<T>;
}
