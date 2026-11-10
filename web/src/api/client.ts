export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export type QueryValue = string | number | boolean | null | undefined
export type QueryParams = Record<string, QueryValue | QueryValue[]>

export type ApiErrorKind = 'http' | 'network' | 'timeout' | 'aborted' | 'parse'

export interface ApiErrorOptions {
  kind: ApiErrorKind
  status?: number
  code?: string
  details?: unknown
  cause?: unknown
}

/** 统一的 API 错误类型：业务层只需判断 kind / status，不必关心 fetch 细节。 */
export class ApiError extends Error {
  readonly kind: ApiErrorKind
  readonly status?: number
  readonly code?: string
  readonly details?: unknown

  constructor(message: string, options: ApiErrorOptions) {
    super(message, { cause: options.cause })
    this.name = 'ApiError'
    this.kind = options.kind
    this.status = options.status
    this.code = options.code
    this.details = options.details
  }

  get isUnauthorized(): boolean {
    return this.status === 401
  }

  get isNotFound(): boolean {
    return this.status === 404
  }
}

export type TokenProvider = () => string | null | undefined

export interface ApiClientConfig {
  /** 基础路径，浏览器内默认 /api，由 Nginx 或 Vite 代理转发。 */
  baseUrl: string
  /** 单次请求默认超时，单位毫秒。 */
  timeoutMs?: number
  /**
   * 用户 Token 扩展点。W0 不决定 Token 存储策略，
   * 后续由认证模块通过 setAuthTokenProvider 注入。
   */
  getToken?: TokenProvider
  /** 便于测试注入，默认使用全局 fetch。 */
  fetchImpl?: typeof fetch
}

export interface RequestOptions {
  method?: HttpMethod
  query?: QueryParams
  body?: unknown
  headers?: Record<string, string | undefined>
  signal?: AbortSignal
  timeoutMs?: number
}

export interface ApiClient {
  request: <T>(path: string, options?: RequestOptions) => Promise<T>
  get: <T>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>) => Promise<T>
  post: <T>(path: string, body?: unknown, options?: Omit<RequestOptions, 'method' | 'body'>) => Promise<T>
  put: <T>(path: string, body?: unknown, options?: Omit<RequestOptions, 'method' | 'body'>) => Promise<T>
  patch: <T>(path: string, body?: unknown, options?: Omit<RequestOptions, 'method' | 'body'>) => Promise<T>
  delete: <T>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>) => Promise<T>
}

const DEFAULT_TIMEOUT_MS = 15_000

function resolveUrl(baseUrl: string, path: string, query?: QueryParams): string {
  const base = baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  const search = new URLSearchParams()

  for (const [key, rawValue] of Object.entries(query ?? {})) {
    const values = Array.isArray(rawValue) ? rawValue : [rawValue]

    for (const value of values) {
      if (value === undefined || value === null) {
        continue
      }
      search.append(key, String(value))
    }
  }

  const queryString = search.toString()
  return queryString ? `${base}${normalizedPath}?${queryString}` : `${base}${normalizedPath}`
}

async function readErrorPayload(
  response: Response,
): Promise<{ message?: string; code?: string; details?: unknown }> {
  const contentType = response.headers.get('content-type') ?? ''

  if (contentType.includes('json')) {
    try {
      const payload: unknown = await response.json()

      if (payload && typeof payload === 'object') {
        const record = payload as Record<string, unknown>
        return {
          message: typeof record.message === 'string' ? record.message : undefined,
          code: typeof record.code === 'string' ? record.code : undefined,
          details: record.details,
        }
      }
    } catch {
      // 错误响应体不是合法 JSON 时，退回通用 HTTP 提示。
    }
  }

  return {}
}

/**
 * 创建 API Client。默认实例通过 VITE_API_BASE_URL 指向 /api，
 * 测试可注入自定义 baseUrl / fetchImpl。
 */
export function createApiClient(config: ApiClientConfig): ApiClient {
  const defaultTimeout = config.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const fetchImpl = config.fetchImpl ?? globalThis.fetch.bind(globalThis)

  async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const {
      method = 'GET',
      query,
      body,
      headers,
      signal,
      timeoutMs = defaultTimeout,
    } = options

    const controller = new AbortController()
    let timedOut = false

    const timer = setTimeout(() => {
      timedOut = true
      controller.abort()
    }, timeoutMs)

    const handleExternalAbort = () => {
      controller.abort(signal?.reason)
    }

    if (signal) {
      if (signal.aborted) {
        handleExternalAbort()
      } else {
        signal.addEventListener('abort', handleExternalAbort, { once: true })
      }
    }

    try {
      const requestHeaders = new Headers({ Accept: 'application/json' })

      if (body !== undefined) {
        requestHeaders.set('Content-Type', 'application/json')
      }

      const token = config.getToken?.()

      if (token) {
        requestHeaders.set('Authorization', `Bearer ${token}`)
      }

      for (const [key, value] of Object.entries(headers ?? {})) {
        if (value !== undefined) {
          requestHeaders.set(key, value)
        }
      }

      const response = await fetchImpl(resolveUrl(config.baseUrl, path, query), {
        method,
        headers: requestHeaders,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      })

      if (!response.ok) {
        const payload = await readErrorPayload(response)
        throw new ApiError(payload.message ?? `请求失败（HTTP ${response.status}）`, {
          kind: 'http',
          status: response.status,
          code: payload.code,
          details: payload.details,
        })
      }

      if (response.status === 204) {
        return undefined as T
      }

      const contentType = response.headers.get('content-type') ?? ''

      if (!contentType.includes('json')) {
        const text = await response.text()
        return (text === '' ? undefined : text) as T
      }

      try {
        return (await response.json()) as T
      } catch (cause) {
        throw new ApiError('响应不是合法的 JSON', {
          kind: 'parse',
          status: response.status,
          cause,
        })
      }
    } catch (error) {
      if (error instanceof ApiError) {
        throw error
      }

      if (timedOut) {
        throw new ApiError(`请求超时（${timeoutMs}ms）`, { kind: 'timeout', cause: error })
      }

      if (controller.signal.aborted) {
        throw new ApiError('请求已取消', { kind: 'aborted', cause: error })
      }

      throw new ApiError('网络异常，请检查网络后重试', { kind: 'network', cause: error })
    } finally {
      clearTimeout(timer)
      signal?.removeEventListener('abort', handleExternalAbort)
    }
  }

  return {
    request,
    get: (path, options) => request(path, { ...options, method: 'GET' }),
    post: (path, body, options) => request(path, { ...options, method: 'POST', body }),
    put: (path, body, options) => request(path, { ...options, method: 'PUT', body }),
    patch: (path, body, options) => request(path, { ...options, method: 'PATCH', body }),
    delete: (path, options) => request(path, { ...options, method: 'DELETE' }),
  }
}

let authTokenProvider: TokenProvider | null = null

/** 认证模块接入后注册 Token 提供者即可，无需改动 API Client 内部实现。 */
export function setAuthTokenProvider(provider: TokenProvider | null): void {
  authTokenProvider = provider
}

function resolveBaseUrl(): string {
  const configured = import.meta.env.VITE_API_BASE_URL
  return configured && configured.trim() !== '' ? configured.trim() : '/api'
}

function resolveTimeout(): number {
  const configured = Number(import.meta.env.VITE_API_TIMEOUT_MS)
  return Number.isFinite(configured) && configured > 0 ? configured : DEFAULT_TIMEOUT_MS
}

export const apiClient = createApiClient({
  baseUrl: resolveBaseUrl(),
  timeoutMs: resolveTimeout(),
  getToken: () => authTokenProvider?.(),
})
