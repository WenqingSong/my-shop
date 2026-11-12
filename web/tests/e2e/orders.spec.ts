import { expect, test, type Page } from '@playwright/test'

/**
 * 订单中心端到端用例。
 *
 * 运行环境说明：本套 e2e 通过 `VITE_ENABLE_MOCK=true` 启动，接口由 MSW 拦截，
 * **不是真实 Go 后端**（真实后端联调见 W4-02 交付报告「真实联调」一节）。
 * 用例覆盖：路由保护、列表渲染、本地状态筛选、详情渲染、成功页跳转、
 * 模拟支付、取消订单、操作后的列表状态同步。
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

/** 登录 + 准备默认地址（从登录页回跳到地址页）。 */
async function loginAndPrepareAddress(page: Page) {
  await page.goto('/account/addresses')
  await loginFromCurrentPage(page)
  await expect(page).toHaveURL(/\/account\/addresses$/)
  await createDefaultAddress(page)
}

/** 首页 → 商品详情 → 加购 → 购物车 → 结算页。 */
async function fillCartAndGoCheckout(page: Page) {
  await page
    .getByRole('banner')
    .getByRole('link', { name: 'SurgeCart 商城首页' })
    .click()
  await page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' }).first().click()
  await page.getByRole('button', { name: /标准款/ }).click()
  await page.getByRole('button', { name: '加入购物车' }).click()
  await expect(page.getByText(/已加入购物车/)).toBeVisible()

  await page.getByRole('link', { name: '购物车，1 件商品' }).click()
  await page.getByRole('link', { name: '去结算' }).click()
  await expect(page).toHaveURL(/\/checkout$/)
}

/** 完整下单链路，结束时停留在下单成功页。 */
async function createOrder(page: Page) {
  await loginAndPrepareAddress(page)
  await fillCartAndGoCheckout(page)
  await page.getByRole('button', { name: '提交订单' }).click()
  await expect(page).toHaveURL(/\/checkout\/success\?order_id=\d+$/)
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
    await createOrder(page)

    // 成功页 → 查看订单（W4-01 新增入口）。
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

    // 返回订单列表：应能看到刚创建的订单。
    await page.getByRole('link', { name: '返回订单列表' }).click()
    await expect(page).toHaveURL(/\/account\/orders$/)
    await expect(page.getByRole('heading', { name: '我的订单' })).toBeVisible()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await expect(page.getByText(detailOrderNo)).toBeVisible()
    await expect(page.getByText('云裳 女士通勤托特包')).toBeVisible()

    // 本地状态筛选：待支付可见、已完成为空。
    await page.getByRole('button', { name: /^待支付/ }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()

    await page.getByRole('button', { name: /^已完成/ }).click()
    await expect(page.getByText('该状态下暂无订单')).toBeVisible()

    await page.getByRole('button', { name: '查看全部订单' }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()

    // 从列表进入详情。
    await page.getByRole('link', { name: /^查看订单 .* 详情$/ }).click()
    await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()
  })

  test('待支付订单可完成模拟支付，状态与列表同步更新', async ({ page }) => {
    await createOrder(page)
    await page.getByRole('link', { name: '查看订单' }).click()
    await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()

    // 支付入口与模拟支付说明。
    await expect(page.getByRole('button', { name: '模拟支付' })).toBeVisible()
    await page.getByRole('button', { name: '模拟支付' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('当前为项目模拟支付，不会产生真实资金扣款')
    await expect(dialog).not.toContainText(/支付宝|微信支付/)
    await dialog.getByRole('button', { name: '确认模拟支付' }).click()

    await expect(page.getByText(/模拟支付已完成/)).toBeVisible()
    await expect(page.locator('[data-status="paid"]')).toBeVisible()
    await expect(page.getByRole('button', { name: '模拟支付' })).toHaveCount(0)

    // 列表缓存同步：返回列表后状态应为已支付，且分组计数更新。
    await page.getByRole('link', { name: '返回订单列表' }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await expect(page.locator('[data-status="paid"]')).toBeVisible()
    await page.getByRole('button', { name: /^待发货/ }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await page.getByRole('button', { name: /^待支付/ }).click()
    await expect(page.getByText('该状态下暂无订单')).toBeVisible()
  })

  test('待支付订单可取消，取消后展示取消原因且不可再操作', async ({ page }) => {
    await createOrder(page)
    await page.getByRole('link', { name: '查看订单' }).click()
    await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()

    await page.getByRole('button', { name: '取消订单' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('服务端会在同一事务内恢复该订单占用的库存')
    await dialog.getByRole('button', { name: '确认取消订单' }).click()

    await expect(page.getByText(/订单已取消，服务端已在同一事务内恢复库存/)).toBeVisible()
    await expect(page.locator('[data-status="cancelled"]')).toBeVisible()
    await expect(page.getByText(/取消原因：用户主动取消/)).toBeVisible()
    await expect(page.getByRole('button', { name: '模拟支付' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '取消订单' })).toHaveCount(0)

    // 列表同步：已关闭分组可见。
    await page.getByRole('link', { name: '返回订单列表' }).click()
    await page.getByRole('button', { name: /^已关闭/ }).click()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await expect(page.locator('[data-status="cancelled"]')).toBeVisible()
  })
})
