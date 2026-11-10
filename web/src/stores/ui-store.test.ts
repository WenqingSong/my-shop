import { beforeEach, describe, expect, it } from 'vitest'

import { useUiStore } from '@/stores/ui-store'

describe('useUiStore', () => {
  beforeEach(() => {
    useUiStore.setState({ isMobileNavOpen: false })
  })

  it('默认关闭移动端导航', () => {
    expect(useUiStore.getState().isMobileNavOpen).toBe(false)
  })

  it('支持切换与显式关闭', () => {
    useUiStore.getState().toggleMobileNav()
    expect(useUiStore.getState().isMobileNavOpen).toBe(true)

    useUiStore.getState().setMobileNavOpen(true)
    expect(useUiStore.getState().isMobileNavOpen).toBe(true)

    useUiStore.getState().closeMobileNav()
    expect(useUiStore.getState().isMobileNavOpen).toBe(false)
  })
})
