import { apiClient, ApiError, type RequestOptions } from '@/api/client'

/**
 * 后端统一响应包装（internal/middleware/response.go）：
 * 成功 {code:0,message:"OK",data:T}；失败 {code:<业务码>,message:"...",data:null}。
 *
 * apiClient 只负责 HTTP 传输，业务模块统一通过 getData 取得 data，
 * 避免每个调用点各写一遍解包与判错逻辑。
 */
export interface ApiEnvelope<T> {
  code: number
  message: string
  data: T
}

function isEnvelope(value: unknown): value is ApiEnvelope<unknown> {
  return (
    typeof value === 'object' &&
    value !== null &&
    'code' in value &&
    typeof (value as { code: unknown }).code === 'number' &&
    'data' in value
  )
}

/**
 * 校验并取出 data。
 * - 结构不符（例如网关返回 HTML 错误页）：parse 类型错误，避免把错误对象当业务数据渲染。
 * - code !== 0：理论上后端会同时返回非 2xx，这里作为兜底，防止 2xx + 业务错误的组合被吞掉。
 */
export function unwrapEnvelope<T>(payload: unknown): T {
  if (!isEnvelope(payload)) {
    throw new ApiError('响应结构不符合约定', { kind: 'parse', details: payload })
  }

  if (payload.code !== 0) {
    throw new ApiError(payload.message || '请求失败', {
      kind: 'http',
      code: String(payload.code),
      details: payload.data,
    })
  }

  return payload.data as T
}

/** GET 业务接口并解包统一响应。 */
export async function getData<T>(
  path: string,
  options?: Omit<RequestOptions, 'method' | 'body'>,
): Promise<T> {
  const payload = await apiClient.get<unknown>(path, options)
  return unwrapEnvelope<T>(payload)
}
