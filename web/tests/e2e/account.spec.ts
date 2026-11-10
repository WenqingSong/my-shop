import { expect, test, type Page } from '@playwright/test'

async function loginAsDemo(page: Page) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill('demo')
  await page.getByLabel('密码', { exact: true }).fill('demo12345')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('link', { name: '账户：demo' })).toBeVisible()
}

async function openAccount(page: Page, section: string) {
  await page.getByRole('link', { name: '账户：demo' }).click()
  await expect(page.getByRole('heading', { level: 1, name: '账户' })).toBeVisible()
  const nav = page.getByRole('navigation', { name: '个人中心导航' })
  await nav.getByRole('link', { name: section }).click()
}

test.describe('个人中心', () => {
  test('登录后可查看只读个人资料', async ({ page }) => {
    await loginAsDemo(page)
    await openAccount(page, '个人资料')

    await expect(page.getByRole('heading', { name: '个人资料' })).toBeVisible()
    await expect(page.getByRole('region', { name: '账户信息' })).toContainText('demo')
    await expect(page.getByText('资料修改暂未开放')).toBeVisible()
    await expect(page.getByRole('textbox')).toHaveCount(0)
  })

  test('收货地址新增、默认切换、编辑与删除', async ({ page }) => {
    await loginAsDemo(page)
    await openAccount(page, '收货地址')

    await expect(page.getByText('还没有收货地址')).toBeVisible()

    // 新增首条地址（后端自动默认）
    await page.getByRole('button', { name: '新增地址' }).first().click()
    const createDialog = page.getByRole('dialog')
    await createDialog.getByLabel('收货人姓名').fill('张三')
    await createDialog.getByLabel('手机号').fill('13800138000')
    await createDialog.getByLabel('省份').fill('广东省')
    await createDialog.getByLabel('城市').fill('深圳市')
    await createDialog.getByLabel('区/县').fill('南山区')
    await createDialog.getByLabel('详细地址').fill('科技园路 1 号 101 室')
    await createDialog.getByRole('button', { name: '保存地址' }).click()

    await expect(page.getByText('收货地址已新增')).toBeVisible()
    const firstCard = page.getByRole('article', { name: '收货地址：张三' })
    await expect(firstCard).toContainText('默认')

    // 新增第二条并设为默认
    await page.getByRole('button', { name: '新增地址' }).click()
    const secondDialog = page.getByRole('dialog')
    await secondDialog.getByLabel('收货人姓名').fill('李四')
    await secondDialog.getByLabel('手机号').fill('13900139000')
    await secondDialog.getByLabel('省份').fill('广东省')
    await secondDialog.getByLabel('城市').fill('深圳市')
    await secondDialog.getByLabel('区/县').fill('福田区')
    await secondDialog.getByLabel('详细地址').fill('深南大道 2 号')
    await secondDialog.getByLabel('设为默认地址').check()
    await secondDialog.getByRole('button', { name: '保存地址' }).click()

    const secondCard = page.getByRole('article', { name: '收货地址：李四' })
    await expect(secondCard).toContainText('默认')

    // 把张三设回默认
    await page.getByRole('button', { name: '设为默认地址（张三）' }).click()
    await expect(page.getByText('已设为默认地址')).toBeVisible()
    await expect(firstCard).toContainText('默认')

    // 编辑张三的详细地址
    await page.getByRole('button', { name: '编辑地址（张三）' }).click()
    const editDialog = page.getByRole('dialog')
    await editDialog.getByLabel('详细地址').fill('科技园路 3 号 303 室')
    await editDialog.getByRole('button', { name: '保存地址' }).click()
    await expect(page.getByText('收货地址已更新')).toBeVisible()
    await expect(firstCard).toContainText('科技园路 3 号 303 室')

    // 删除李四
    await page.getByRole('button', { name: '删除地址（李四）' }).click()
    const deleteDialog = page.getByRole('dialog')
    await deleteDialog.getByRole('button', { name: '删除' }).click()
    await expect(page.getByText('收货地址已删除')).toBeVisible()
    await expect(secondCard).toHaveCount(0)
  })

  test('商品收藏、收藏列表与取消收藏同步', async ({ page }) => {
    await loginAsDemo(page)

    // 从首页进入商品详情
    await page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' }).click()
    await expect(page.getByRole('heading', { level: 1, name: '云裳 女士通勤托特包' })).toBeVisible()

    await page.getByRole('button', { name: '收藏' }).click()
    await expect(page.getByRole('button', { name: '已收藏' })).toBeVisible()

    await openAccount(page, '我的收藏')

    const favoriteLink = page.getByRole('link', {
      name: '查看商品：云裳 女士通勤托特包',
    })
    await expect(favoriteLink).toBeVisible()

    await page.getByRole('button', { name: '取消收藏（云裳 女士通勤托特包）' }).click()

    await expect(page.getByText('还没有收藏商品')).toBeVisible()
    await expect(favoriteLink).toHaveCount(0)
  })

  test('未登录访问收藏页会跳登录，登录后返回原页面', async ({ page }) => {
    await page.goto('/account/favorites')

    await expect(page).toHaveURL(/\/login$/)
    await page.getByLabel('用户名').fill('demo')
    await page.getByLabel('密码', { exact: true }).fill('demo12345')
    await page.getByRole('button', { name: '登录' }).click()

    await expect(page).toHaveURL(/\/account\/favorites$/)
    await expect(page.getByRole('heading', { name: '我的收藏' })).toBeVisible()
  })
})
