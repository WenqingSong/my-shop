import { expect, test, type Page } from '@playwright/test'

/**
 * 退款中心端到端用例（W4-03）。
 *
 * 运行环境说明：本套 e2e 通过 `VITE_ENABLE_MOCK=true` 启动，接口由 MSW 拦截，
 * **不是真实 Go 后端**（真实后端联调结果见 `docs/reports/w4-03-refund-center/DELIVERY_REPORT.md`）。
 *
 * 覆盖：路由保护、空状态、付款后申请退款、申请后跳转详情、
 * 撤销退款、退款状态在列表与订单卡片上的同步。
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

/**
 * 完整链路：登录 → 建地址 → 下单 → 模拟支付，结束时停留在「已支付」订单详情。
 *
 * 全程使用 SPA 导航（不做 page.goto），否则 MSW 的模块级 Mock 状态会被重置。
 */
async function createPaidOrder(page: Page) {
  await loginAndPrepareAddress(page)
  await fillCartAndGoCheckout(page)
  await page.getByRole('button', { name: '提交订单' }).click()
  await expect(page).toHaveURL(/\/checkout\/success\?order_id=\d+$/)

  await page.getByRole('link', { name: '查看订单' }).click()
  await expect(page.getByRole('heading', { name: '订单详情' })).toBeVisible()

  await page.getByRole('button', { name: '模拟支付' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: '确认模拟支付' }).click()
  await expect(page.locator('[data-status="paid"]')).toBeVisible()
}

test.describe('退款中心', () => {
  test('未登录访问退款中心跳转登录，登录后回到退款列表并展示空状态', async ({ page }) => {
    await page.goto('/account/refunds')

    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)

    await expect(page).toHaveURL(/\/account\/refunds$/)
    await expect(page.getByRole('heading', { name: '我的退款' })).toBeVisible()
    await expect(page.getByText('还没有退款记录')).toBeVisible()
    await expect(page.getByRole('link', { name: '查看我的订单' })).toBeVisible()
  })

  test('已支付订单申请整单退款后跳转退款详情，并可在退款列表查看', async ({ page }) => {
    await createPaidOrder(page)

    // 订单详情出现退款区与申请入口。
    await expect(page.getByRole('region', { name: '退款' })).toBeVisible()
    await expect(page.getByText(/订单已支付且未发货，可以申请整单退款/)).toBeVisible()
    await page.getByRole('button', { name: '申请退款' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('仅支持该订单整单退款')
    await expect(dialog).toContainText('申请成功不代表退款完成')
    await expect(dialog).toContainText('也不代表真实资金已经退回')
    await expect(dialog).not.toContainText(/支付宝|微信支付|银联/)

    await dialog.getByLabel('退款原因').fill('E2E 退款原因')
    await dialog.getByRole('button', { name: '提交退款申请' }).click()

    // 成功后跳转退款详情：状态申请中、金额来自服务端、原因正确。
    await expect(page).toHaveURL(/\/account\/refunds\/\d+$/)
    await expect(page.getByRole('heading', { name: '退款详情' })).toBeVisible()
    await expect(page.locator('[data-refund-status="applying"]')).toBeVisible()
    await expect(page.getByText('E2E 退款原因')).toBeVisible()
    await expect(page.getByText(/不代表真实资金已经退回/)).toBeVisible()
    await expect(page.getByText(/不提供状态时间轴/)).toBeVisible()

    // 退款列表：记录可见、状态可筛选。
    await page.getByRole('link', { name: '返回退款列表' }).click()
    await expect(page).toHaveURL(/\/account\/refunds$/)
    await expect(page.getByText('共 1 条退款记录')).toBeVisible()
    await expect(page.getByText('E2E 退款原因')).toBeVisible()

    await page.getByRole('button', { name: /^申请中/ }).click()
    await expect(page.getByText('E2E 退款原因')).toBeVisible()

    await page.getByRole('button', { name: /^退款成功/ }).click()
    await expect(page.getByText('该状态下暂无退款记录')).toBeVisible()
  })

  test('申请中可撤销，撤销后状态与订单列表退款徽标同步', async ({ page }) => {
    await createPaidOrder(page)

    await page.getByRole('button', { name: '申请退款' }).click()
    const applyDialog = page.getByRole('dialog')
    await applyDialog.getByLabel('退款原因').fill('撤销流程测试')
    await applyDialog.getByRole('button', { name: '提交退款申请' }).click()
    await expect(page).toHaveURL(/\/account\/refunds\/\d+$/)

    // 撤销确认框说明撤销不影响订单状态。
    await page.getByRole('button', { name: '撤销退款申请' }).click()
    const revokeDialog = page.getByRole('dialog')
    await expect(revokeDialog).toContainText('撤销后本次退款申请将变为「已撤销」')
    await expect(revokeDialog).toContainText('订单状态保持「已支付」')
    await revokeDialog.getByRole('button', { name: '确认撤销' }).click()

    await expect(page.getByText(/退款申请已撤销，订单保持已支付状态/)).toBeVisible()
    await expect(page.locator('[data-refund-status="revoked"]')).toBeVisible()
    await expect(page.getByRole('button', { name: '撤销退款申请' })).toHaveCount(0)

    // 订单列表的退款徽标同步为「已撤销」，订单本身仍为「已支付」。
    await page
      .getByRole('navigation', { name: '个人中心导航' })
      .getByRole('link', { name: '我的订单' })
      .click()
    await expect(page.getByRole('heading', { name: '我的订单' })).toBeVisible()
    await expect(page.getByText('共 1 笔订单')).toBeVisible()
    await expect(page.locator('[data-refund-status="revoked"]')).toBeVisible()
    await expect(page.locator('[data-status="paid"]')).toBeVisible()
  })
})
