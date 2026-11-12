/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** API 基础路径，默认 /api（代理层剥离前缀后转发到后端契约路径） */
  readonly VITE_API_BASE_URL?: string
  /** 首页推荐位 code；未配置时不请求推荐接口 */
  readonly VITE_HOME_RECOMMEND_CODE?: string
  /** 是否启用 MSW 浏览器 Mock："true" 时启用 */
  readonly VITE_ENABLE_MOCK?: string
  /** 请求超时（毫秒） */
  readonly VITE_API_TIMEOUT_MS?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
