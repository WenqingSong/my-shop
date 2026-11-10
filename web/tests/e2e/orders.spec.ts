import { expect, test, type Page } from '@playwright/test'

/**
 * 订单中心端到端用例。
 *
 * 运行环境说明：本套 e2e 通过 `VITE_ENABLE_MOCK=true` 启动，接口由 MSW 拦截，
 * **不是真实 Go 后端**（真实后端联调见交付报告「真实联调」一节）。
 * 用例覆盖：路由保护、列表渲染、本地状态筛选、详情渲染、成功页跳转。
 */

async function loginFromCurrentPage(page: Page) {
  await page.getByLabel('用户名').fill('demo')
  await page.getByLabel('密码', { exact: true }).fill('demo12345')
  await page.getByRole('button', { name: '登录' }).click()
}

async function createDefaultAddress(page: Page) {
  await page.getByRole('button', { name: '新增地址' }).first().click()

  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('收货人姓名').fill('张三')
  await dialog.getByLabel('手机号').fill('13800138000')
  await dialog.getByLabel('省份').fill('广东省')
  await dialog.getByLabel('城市').fill('深圳市')
  await dialog.getByLabel('区/县').fill('南山区')
  await dialog.getByLabel('详细地址').fill('科技园路 1 号')
  await dialog.getByLabel('设为默认地址').check()
  await dialog.getByRole('button', { name: '保存地址' }).click()
  await expect(page.getByText('收货地址已新增')).toBeVisible()
}

test.describe('订单中心', () => {
  test('未登录访问订单列表跳转登录，登录后回到订单列表', async ({ page }) => {
    await page.goto('/account/orders')

    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)

    await expect(page).toHaveURL(/\/account\/orders$/)
    await expect(page.getByRole('heading', { name: '我的订单' })).toBeVisible()
    await expect(page.getByText('还没有订单')).toBeVisible()
  })

  test('下单后可查看订单列表、切换状态筛选并进入详情', async ({ page }) => {
    // 1. 登录并准备默认地址。
    await page.goto('/account/addresses')
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/account\/addresses$/)
    await createDefaultAddress(page)

    // 2. 进入商品详情并加入购物车。
    await page
      .getByRole('banner')
      .getByRole('link', { name: 'SurgeCart 商城首页' })
      .click()
    await page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' }).first().click()
    await page.getByRole('button', { name: /标准款/ }).click()
    await page.getByRole('button', { name: '加入购物车' }).click()
    await expect(page.getByText(/已加入购物车/)).toBeVisible()

    // 3. 购物车 → 结算 → 提交订单。
    await page.getByRole('link', { name: '购物车，1 件商品' }).click()
    await page.getByRole('link', { name: '去结算' }).click()
    await expect(page).toHaveURL(/\/checkout$/)
    await page.getByRole('button', { name: '提交订单' }).click()
    await expect(page).toHaveURL(/\/checkout\/success\?order_id=\d+$/)

    // 4. 成功页 → 查看订单（W4-01 新增入口）。
    await page.getByRole('link', { name: '查看订单' }).click()
    await expect(page).toHaveURL(/\/account\/orders\/\d+$/)
    await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()

    const orderNoLocator = page.getByText(/^\d{13}[0-9a-f]{12}$/)
    await expect(orderNoLocator).toBeVisible()
    const detailOrderNo = await orderNoLocator.innerText()
    expect(detailOrderNo).toMatch(/^\d{13}[0-9a-f]{12}$/)

    // 详情：状态、商品、金额、收货快照与时间信息。
    await expect(page.locator('[data-status="pending_payment"]')).toBeVisible()
    const itemsSection = page.getByRole('region', { name: '商品明细' })
    await expect(itemsSection).toContainText('云裳 女士通勤托特包')
    await expect(itemsSection).toContainText('规格：标准款')

    const amountSection = page.getByRole('region', { name: '金额' })
    await expect(amountSection).toContainText('商品总金额')
    await expect(amountSection).toContainText('应付金额')

    const addressSection = page.getByRole('region', { name: '收货信息' })
    await expect(addressSection).toContainText('张三')
    await expect(addressSection).toContainText('13800138000')
    await expect(addressSection).toContainText('广东省深圳市南山区科技园路 1 号')

    const timeSection = page.getByRole('region', { name: '时间信息' })
    await expect(timeSection).toContainText('下单时间')

    // 5. 返回订单列表：应能看到刚创建的订单。
    await page.getByRole('link', { name: '返回订单列表' }).click()
    await expect(page).toHaveURL(/\/account\/orders$/)
    await expect(page.getByRole('heading', { name: '我的订单' })).toBeVisible()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await expect(page.getByText(detailOrderNo)).toBeVisible()
    await expect(page.getByText('云裳 女士通勤托特包')).toBeVisible()

    // 6. 本地状态筛选：待支付可见、已完成为空。
    await page.getByRole('button', { name: /^待支付/ }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()

    await page.getByRole('button', { name: /^已完成/ }).click()
    await expect(page.getByText('该状态下暂无订单')).toBeVisible()

    await page.getByRole('button', { name: '查看全部订单' }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()

    // 7. 从列表进入详情。
    await page.getByRole('link', { name: /^查看订单 .* 详情$/ }).click()
    await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()
  })
})
