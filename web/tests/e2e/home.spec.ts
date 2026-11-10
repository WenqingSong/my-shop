import { expect, test } from '@playwright/test'

test.describe('商城公共布局', () => {
  test('首页渲染 Header、主内容与 Footer', async ({ page }) => {
    await page.goto('/')

    await expect(page).toHaveTitle(/SurgeCart/)
    await expect(page.getByRole('banner')).toBeVisible()
    await expect(page.getByRole('main')).toBeVisible()
    await expect(page.getByRole('contentinfo')).toBeVisible()
    await expect(page.getByRole('heading', { level: 1 })).toContainText('把好物')
    await expect(page.getByRole('navigation', { name: '主导航' })).toContainText('首页')
  })

  test('设计基线与组件区块可通过锚点到达', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('link', { name: /查看设计基线/ }).click()
    await expect(page.getByRole('heading', { name: '设计基线' })).toBeVisible()

    await page.getByRole('link', { name: '组件与状态' }).click()
    await expect(page.getByRole('heading', { name: '组件与交互状态' })).toBeVisible()
  })

  test('Dialog 可以打开并通过关闭按钮退出', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('button', { name: '打开 Dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByRole('heading', { name: '确认移除商品？' })).toBeVisible()

    await page.getByRole('button', { name: '关闭' }).click()
    await expect(page.getByRole('dialog')).toBeHidden()
  })

  test('分页组件可切换页码', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('button', { name: '第 4 页' }).click()
    await expect(page.getByText('当前第 4 页 / 共 12 页')).toBeVisible()
  })

  test('未知路由展示 404 页面', async ({ page }) => {
    await page.goto('/not-exist')

    await expect(page.getByRole('heading', { level: 1, name: '页面不存在' })).toBeVisible()
    await expect(page.getByRole('link', { name: '返回首页' })).toBeVisible()
  })
})

test.describe('移动端布局', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('导航抽屉可以打开并跳转首页', async ({ page }) => {
    await page.goto('/not-exist')

    await page.getByRole('button', { name: '打开商城导航' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await page.getByRole('dialog').getByRole('link', { name: '首页' }).click()
    await expect(page.getByRole('heading', { level: 1 })).toContainText('把好物')
  })
})
