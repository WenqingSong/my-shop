import { expect, test, type Page } from '@playwright/test'

const PRODUCT_NAME = '星海 X1 Pro 5G 手机 12GB+256GB'

async function loginFromCurrentPage(page: Page) {
  await page.getByLabel('用户名').fill('demo')
  await page.getByLabel('密码', { exact: true }).fill('demo12345')
  await page.getByRole('button', { name: '登录' }).click()
}

test.describe('购物车', () => {
  test('未登录访问购物车跳转登录，登录后返回购物车', async ({ page }) => {
    await page.goto('/cart')

    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)

    await expect(page).toHaveURL(/\/cart$/)
    await expect(page.getByRole('heading', { level: 1, name: '购物车' })).toBeVisible()
    await expect(page.getByText('购物车还是空的')).toBeVisible()
    await expect(page.getByRole('link', { name: '去挑选商品' })).toHaveAttribute(
      'href',
      '/products',
    )
  })

  test('选择规格加入购物车，并在购物车页面修改数量、勾选、全选与删除', async ({ page }) => {
    await page.goto('/products/1')
    await expect(page.getByRole('heading', { level: 1, name: PRODUCT_NAME })).toBeVisible()

    // 未登录：加购入口引导登录，登录后回到商品详情。
    await page.getByRole('button', { name: '登录后加入购物车' }).click()
    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/products\/1$/)

    // 登录后重新选择规格（页面重挂载，选择状态不保留）。
    await page.getByRole('button', { name: /标准款/ }).click()
    await page.getByRole('button', { name: `增加数量（${PRODUCT_NAME}）` }).click()
    await page.getByRole('button', { name: '加入购物车' }).click()

    await expect(page.getByText(/已加入购物车（该规格共 2 件）/)).toBeVisible()
    const cartLink = page.getByRole('link', { name: '购物车，2 件商品' })
    await expect(cartLink).toBeVisible()

    await cartLink.click()
    await expect(page).toHaveURL(/\/cart$/)

    const row = page.getByRole('article', { name: `购物车商品：${PRODUCT_NAME}` })
    const summary = page.getByRole('complementary', { name: '购物车金额汇总' })

    await expect(row).toBeVisible()
    await expect(row).toContainText('规格：标准款')
    await expect(row).toContainText('4,299')
    // 2 × ¥4,299.00
    await expect(row).toContainText('8,598')
    await expect(summary).toContainText('2 件')
    await expect(summary).toContainText('8,598')
    await expect(page.getByRole('button', { name: '去结算' })).toBeDisabled()

    // 数量 +1 → 3 × ¥4,299.00
    await row.getByRole('button', { name: `增加数量（${PRODUCT_NAME}）` }).click()
    await expect(summary).toContainText('12,897')
    await expect(summary).toContainText('3 件')

    // 取消勾选：不计入预估金额
    await row.getByRole('checkbox', { name: `选择商品：${PRODUCT_NAME}` }).uncheck()
    await expect(summary).toContainText('0 件')

    // 全选恢复
    await page.getByRole('checkbox', { name: '全选商品' }).check()
    await expect(summary).toContainText('12,897')

    // 删除后进入空购物车
    await row.getByRole('button', { name: `删除商品（${PRODUCT_NAME}）` }).click()
    await expect(page.getByText('已将商品移出购物车')).toBeVisible()
    await expect(page.getByText('购物车还是空的')).toBeVisible()
    await expect(page.getByRole('link', { name: '购物车，3 件商品' })).toHaveCount(0)
  })

  test('退出登录后 Header 购物车数量清空', async ({ page }) => {
    await page.goto('/products/1')
    await page.getByRole('button', { name: '登录后加入购物车' }).click()
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/products\/1$/)

    await page.getByRole('button', { name: /标准款/ }).click()
    await page.getByRole('button', { name: '加入购物车' }).click()
    await expect(page.getByRole('link', { name: '购物车，1 件商品' })).toBeVisible()

    await page.getByRole('button', { name: '退出登录' }).click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByRole('link', { name: '购物车' })).toBeVisible()
    await expect(page.getByRole('link', { name: /购物车，\d+ 件商品/ })).toHaveCount(0)
  })

  test('移动端导航提供购物车入口', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/cart')
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/cart$/)

    await page.getByRole('button', { name: '打开商城导航' }).click()

    const drawer = page.getByRole('dialog')
    const cartLink = drawer.getByRole('link', { name: '购物车' })
    await expect(cartLink).toBeVisible()
    await cartLink.click()

    await expect(page).toHaveURL(/\/cart$/)
    await expect(page.getByRole('heading', { level: 1, name: '购物车' })).toBeVisible()
  })
})
