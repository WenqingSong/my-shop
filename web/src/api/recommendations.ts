import { getData } from '@/api/envelope'
import type { RecommendationResult } from '@/api/types'

/**
 * GET /recommendations/:code（公开，仅启用位中的 on_shelf 商品）
 * 后端：api/recommendation/v1/recommendation.go FrontendReq / FrontendRes。
 *
 * 注意：推荐位 code 由运营在后台创建，后端没有「列出可用 code」的公开接口，
 * 前端不能猜测 code，因此首页是否展示推荐区块由部署配置提供 code 决定。
 */
export function fetchRecommendation(code: string, signal?: AbortSignal): Promise<RecommendationResult> {
  return getData<RecommendationResult>(`/recommendations/${encodeURIComponent(code)}`, { signal })
}
