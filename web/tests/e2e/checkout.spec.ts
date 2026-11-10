import { expect, test, type Page } from '@playwright/test'

async function loginFromCurrentPage(page: Page) {
  await page.getByLabel('用户名').fill('demo')
  await page.getByLabel('密码', { exact: true }).fill('demo12345')
  await page.getByRole('button', { name: '登录' }).click()
}

async function createAddress(
  page: Page,
  input: { name: string; phone: string; detail: string; isDefault?: boolean },
) {
  await page.getByRole('button', { name: '新增地址' }).first().click()

  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('收货人姓名').fill(input.name)
  await dialog.getByLabel('手机号').fill(input.phone)
  await dialog.getByLabel('省份').fill('广东省')
  await dialog.getByLabel('城市').fill('深圳市')
  await dialog.getByLabel('区/县').fill('南山区')
  await dialog.getByLabel('详细地址').fill(input.detail)

  if (input.isDefault) {
    await dialog.getByLabel('设为默认地址').check()
  }

  await dialog.getByRole('button', { name: '保存地址' }).click()
  await expect(page.getByText('收货地址已新增')).toBeVisible()
}

test.describe('订单结算与创建', () => {
  test('未登录访问结算页跳转登录，登录后回到结算页', async ({ page }) => {
    await page.goto('/checkout')

    await expect(page).toHaveURL(/\/login$/)
    await loginFromCurrentPage(page)

    await expect(page).toHaveURL(/\/checkout$/)
    await expect(page.getByRole('heading', { level: 1, name: '确认订单' })).toBeVisible()
    await expect(page.getByText('没有勾选的商品')).toBeVisible()
    await expect(page.getByRole('link', { name: '去购物车勾选' })).toHaveAttribute(
      'href',
      '/cart',
    )
  })

  test('从地址到下单成功：默认地址、切换地址、服务端金额与购物车清理', async ({ page }) => {
    // 1. 登录并准备两个收货地址（第二条设为默认）。
    await page.goto('/account/addresses')
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/account\/addresses$/)
    await expect(page.getByText('还没有收货地址')).toBeVisible()

    await createAddress(page, { name: '张三', phone: '13800138000', detail: '科技园路 1 号' })
    await createAddress(page, {
      name: '李四',
      phone: '13900139000',
      detail: '深南大道 2 号',
      isDefault: true,
    })

    // 2. 回到商城，从首页进入商品详情并加入购物车。
    await page
      .getByRole('banner')
      .getByRole('link', { name: 'SurgeCart 商城首页' })
      .click()
    await page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' }).first().click()
    await expect(
      page.getByRole('heading', { level: 1, name: '云裳 女士通勤托特包' }),
    ).toBeVisible()

    await page.getByRole('button', { name: /标准款/ }).click()
    await page.getByRole('button', { name: '加入购物车' }).click()
    await expect(page.getByText(/已加入购物车/)).toBeVisible()

    // 3. 购物车 → 结算页。
    await page.getByRole('link', { name: '购物车，1 件商品' }).click()
    await expect(page).toHaveURL(/\/cart$/)
    await page.getByRole('link', { name: '去结算' }).click()
    await expect(page).toHaveURL(/\/checkout$/)
    await expect(page.getByRole('heading', { level: 1, name: '确认订单' })).toBeVisible()

    // 4. 默认地址优先，手动切换到张三。
    await expect(page.getByRole('radio', { name: '使用收货地址：李四' })).toBeChecked()
    await page.getByRole('radio', { name: '使用收货地址：张三' }).check()
    await expect(page.getByRole('radio', { name: '使用收货地址：张三' })).toBeChecked()

    const amountPanel = page.getByRole('complementary', { name: '订单金额' })
    await expect(amountPanel).toContainText('1 件')
    await expect(amountPanel).toContainText('459')
    await expect(amountPanel).toContainText('不是后端订单试算结果')

    // 5. 提交订单 → 成功页展示服务端返回的订单号/金额/状态。
    await page.getByRole('button', { name: '提交订单' }).click()
    await expect(page).toHaveURL(/\/checkout\/success\?order_id=\d+$/)
    await expect(page.getByRole('heading', { level: 1, name: '订单提交成功' })).toBeVisible()

    const orderInfo = page.getByRole('region', { name: '订单信息' })
    // 订单号：13 位毫秒时间戳 + 12 位随机 hex（与后端 generateOrderNo 一致）
    await expect(orderInfo).toContainText(/\d{13}[0-9a-f]{12}/)
    await expect(orderInfo).toContainText('待支付')
    await expect(orderInfo).toContainText('459')
    await expect(page.getByText('张三')).toBeVisible()
    await expect(page.getByText(/广东省深圳市南山区科技园路 1 号/)).toBeVisible()

    // 6. 已购条目被服务端清理：购物车为空，Header 徽标消失。
    await page.getByRole('link', { name: '查看购物车' }).click()
    await expect(page).toHaveURL(/\/cart$/)
    await expect(page.getByText('购物车还是空的')).toBeVisible()
    await expect(page.getByRole('link', { name: /购物车，\d+ 件商品/ })).toHaveCount(0)
  })

  test('空购物车不提供结算入口', async ({ page }) => {
    await page.goto('/cart')
    await loginFromCurrentPage(page)
    await expect(page).toHaveURL(/\/cart$/)

    await expect(page.getByText('购物车还是空的')).toBeVisible()
    await expect(page.getByRole('link', { name: '去结算' })).toHaveCount(0)
  })
})
