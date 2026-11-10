import { expect, test } from '@playwright/test'

test.describe('商城首页', () => {
  test('展示首屏、分类入口与最新上架商品', async ({ page }) => {
    await page.goto('/')

    await expect(page).toHaveTitle(/SurgeCart/)
    await expect(page.getByRole('banner')).toBeVisible()
    await expect(page.getByRole('main')).toBeVisible()
    await expect(page.getByRole('contentinfo')).toBeVisible()
    await expect(page.getByRole('heading', { level: 1 })).toContainText('把好物')
    await expect(page.getByRole('navigation', { name: '主导航' })).toContainText('全部商品')

    await expect(page.getByRole('heading', { name: '商品分类' })).toBeVisible()
    await expect(page.getByRole('link', { name: '手机' })).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' })).toBeVisible()
  })

  test('轮播图与推荐位使用后端数据渲染', async ({ page }) => {
    await page.goto('/')

    await expect(page.getByRole('link', { name: /新品首发/ })).toBeVisible()
    await expect(page.getByRole('heading', { name: '编辑推荐' })).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云听 Air 3 主动降噪耳机' })).toBeVisible()
  })

  test('点击商品卡片进入详情页', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' }).click()

    await expect(page).toHaveURL(/\/products\/26$/)
    await expect(
      page.getByRole('heading', { level: 1, name: '云裳 女士通勤托特包' }),
    ).toBeVisible()
  })

  test('分类入口跳转到对应商品列表', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('link', { name: '手机' }).click()

    await expect(page).toHaveURL(/\/products\?category_id=11$/)
    await expect(page.getByRole('heading', { level: 1, name: '手机' })).toBeVisible()
  })

  test('未知路由展示 404 页面', async ({ page }) => {
    await page.goto('/not-exist')

    await expect(page.getByRole('heading', { level: 1, name: '页面不存在' })).toBeVisible()
    await expect(page.getByRole('link', { name: '返回首页' })).toBeVisible()
  })
})

test.describe('移动端布局', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('导航抽屉可以打开并跳转商品列表', async ({ page }) => {
    await page.goto('/not-exist')

    await page.getByRole('button', { name: '打开商城导航' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await page.getByRole('dialog').getByRole('link', { name: '全部商品' }).click()
    await expect(page).toHaveURL(/\/products$/)
    await expect(page.getByRole('heading', { level: 1, name: '全部商品' })).toBeVisible()
  })
})
