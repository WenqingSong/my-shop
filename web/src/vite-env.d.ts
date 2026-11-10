/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** API 基础路径，默认 /api */
  readonly VITE_API_BASE_URL?: string
  /** 是否启用 MSW 浏览器 Mock："true" 时启用 */
  readonly VITE_ENABLE_MOCK?: string
  /** 请求超时（毫秒） */
  readonly VITE_API_TIMEOUT_MS?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
