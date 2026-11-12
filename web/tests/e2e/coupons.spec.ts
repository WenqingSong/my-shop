import { expect, test, type Page } from '@playwright/test'

/**
 * 优惠券中心端到端用例（W4-04）。
 *
 * 运行环境：`VITE_ENABLE_MOCK=true`，接口由 MSW 拦截，**不是真实 Go 后端**
 * （真实联调结果见 `docs/reports/w4-04-coupon-center/DELIVERY_REPORT.md`）。
 *
 * 覆盖范围说明：后端没有面向用户的公开券模板列表接口，前端也没有领券入口，
 * 因此浏览器侧无法在纯 UI 流程中「造出一张券」。这里覆盖路由保护、空状态、
 * 结算页无券提示与下单金额；「有券时选择/换券/取消用券」由组件测试
 * （`src/pages/checkout-coupon.test.tsx`）与真实后端联调覆盖。
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

async function loginAndPrepareAddress(page: Page) {
  await page.goto('/account/addresses')
  await loginFromCurrentPage(page)
  await expect(page).toHaveURL(/\/account\/addresses$/)
  await createDefaultAddress(page)
}

test.describe('优惠券中心', () => {
  test('未登录访问我的优惠券跳转登录，登录后展示空状态与能力说明', async ({ page }) => {
    await page.goto('/account/coupons')

    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)

    await expect(page).toHaveURL(/\/account\/coupons$/)
    await expect(page.getByRole('heading', { name: '我的优惠券' })).toBeVisible()
    await expect(page.getByText('还没有优惠券')).toBeVisible()
    await expect(
      page.getByText(/本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口/),
    ).toBeVisible()

    // 状态筛选只包含后端真实存在的三种状态。
    const filters = page.getByRole('group', { name: '按优惠券状态筛选' })
    await expect(filters.getByRole('button', { name: /^全部/ })).toBeVisible()
    await expect(filters.getByRole('button', { name: /^可使用/ })).toBeVisible()
    await expect(filters.getByRole('button', { name: /^已锁定/ })).toBeVisible()
    await expect(filters.getByRole('button', { name: /^已使用/ })).toBeVisible()
    await expect(filters.getByRole('button', { name: /^已过期/ })).toHaveCount(0)
  })

  test('结算页无优惠券时提示并仍可下单，成功页展示服务端三项金额', async ({ page }) => {
    await loginAndPrepareAddress(page)

    // 加购 → 购物车 → 结算。
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

    const couponSection = page.getByRole('region', { name: '优惠券' })
    await expect(couponSection).toBeVisible()
    await expect(couponSection).toContainText('优惠券适用性及最终优惠金额以服务端下单校验结果为准')
    await expect(couponSection).toContainText('暂无优惠券')

    const amountPanel = page.getByRole('complementary', { name: '订单金额' })
    await expect(amountPanel).toContainText('未使用优惠券')

    await page.getByRole('button', { name: '提交订单' }).click()
    await expect(page).toHaveURL(/\/checkout\/success\?order_id=\d+$/)

    const info = page.getByRole('region', { name: '订单信息' })
    await expect(info).toContainText('商品总额')
    await expect(info).toContainText('优惠金额')
    await expect(info).toContainText('未使用优惠券')
    await expect(info).toContainText('应付金额')
  })

  test('个人中心导航与账户概览都提供我的优惠券入口', async ({ page }) => {
    await page.goto('/account/coupons')
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/account\/coupons$/)

    await page
      .getByRole('navigation', { name: '个人中心导航' })
      .getByRole('link', { name: '我的优惠券' })
      .click()
    await expect(page).toHaveURL(/\/account\/coupons$/)

    await page
      .getByRole('navigation', { name: '个人中心导航' })
      .getByRole('link', { name: '账户概览' })
      .click()
    await expect(page).toHaveURL(/\/account$/)
    await expect(page.getByRole('link', { name: /我的优惠券/ })).toBeVisible()
  })
})
