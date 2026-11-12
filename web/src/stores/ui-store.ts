import { create } from 'zustand'

/**
 * 纯客户端 UI 状态。后端数据（商品、库存、订单）一律走 TanStack Query，
 * 不允许在这里建立第二套权威数据源。
 */
export interface UiState {
  /** 移动端导航抽屉是否展开 */
  isMobileNavOpen: boolean
  setMobileNavOpen: (open: boolean) => void
  toggleMobileNav: () => void
  closeMobileNav: () => void
}

export const useUiStore = create<UiState>()((set) => ({
  isMobileNavOpen: false,
  setMobileNavOpen: (open) => set({ isMobileNavOpen: open }),
  toggleMobileNav: () => set((state) => ({ isMobileNavOpen: !state.isMobileNavOpen })),
  closeMobileNav: () => set({ isMobileNavOpen: false }),
}))
