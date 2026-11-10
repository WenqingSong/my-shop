import { getData } from '@/api/envelope'
import type { BannerListResult } from '@/api/types'

/**
 * GET /banners（公开，仅启用项，按 sort、id 升序）
 * 后端：api/banner/v1/banner.go ListReq / ListRes。
 */
export function fetchBanners(signal?: AbortSignal): Promise<BannerListResult> {
  return getData<BannerListResult>('/banners', { signal })
}
