/**
 * 运行时前端配置。
 *
 * 只读取 Vite 注入的公开变量（VITE_*），不包含任何密钥。
 */

function readOptional(value: string | undefined): string {
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * 首页推荐位 code。
 *
 * 后端 GET /recommendations/:code 需要运营在后台创建的具体 code，
 * 且没有公开接口可以枚举；未配置时首页不发起推荐请求、也不渲染该区块，
 * 避免猜测业务 code 或展示空壳栏目。
 */
export const homeRecommendCode = readOptional(import.meta.env.VITE_HOME_RECOMMEND_CODE)
