import { expect, test } from '@playwright/test'

test.describe('商品详情与 SKU 选择', () => {
  test('展示商品信息、规格、库存说明与未开放的购物车入口', async ({ page }) => {
    await page.goto('/products/1')

    await expect(page).toHaveTitle(/星海 X1 Pro/)
    await expect(
      page.getByRole('heading', { level: 1, name: '星海 X1 Pro 5G 手机 12GB+256GB' }),
    ).toBeVisible()

    const pricePanel = page.getByText('当前价格').locator('..')
    await expect(pricePanel).toContainText('4,299')

    await expect(page.getByRole('group', { name: '商品规格' })).toBeVisible()
    await expect(page.getByText('已选规格：标准款')).toBeVisible()
    await expect(page.getByText('库存状态')).toBeVisible()
    await expect(page.getByRole('button', { name: '加入购物车' })).toBeDisabled()
    await expect(page.getByText(/购物车功能尚未开放/)).toBeVisible()
  })

  test('切换 SKU 后价格与已选规格同步更新', async ({ page }) => {
    await page.goto('/products/1')

    const pricePanel = page.getByText('当前价格').locator('..')
    await expect(pricePanel).toContainText('4,299')

    await page.getByRole('button', { name: /升级款/ }).click()

    await expect(page.getByText('已选规格：升级款')).toBeVisible()
    await expect(pricePanel).toContainText('4,349')
  })

  test('商品图册支持切换缩略图', async ({ page }) => {
    await page.goto('/products/1')

    const secondImage = page.getByRole('button', { name: '查看第 2 张商品图片' })
    await expect(secondImage).toBeVisible()
    await secondImage.click()
    await expect(secondImage).toHaveAttribute('aria-pressed', 'true')
  })

  test('展示评价汇总并支持翻页', async ({ page }) => {
    await page.goto('/products/1')

    await expect(page.getByText('共 7 条已发布评价')).toBeVisible()
    await expect(page.getByText('客服回复及时，发票开具顺利。')).toBeVisible()

    await page.getByRole('button', { name: '第 2 页' }).click()

    await expect(page.getByText('包装完好，物流很快，手机手感不错。')).toBeVisible()
  })

  test('商品不存在时展示 404 状态并可返回列表', async ({ page }) => {
    await page.goto('/products/999999')

    await expect(page.getByRole('heading', { level: 1, name: '商品不存在或已下架' })).toBeVisible()
    await page.getByRole('link', { name: '返回商品列表' }).click()
    await expect(page).toHaveURL(/\/products$/)
  })

  test('面包屑返回全部商品', async ({ page }) => {
    await page.goto('/products/1')

    await page.getByRole('navigation', { name: '面包屑' }).getByRole('link', { name: '全部商品' }).click()

    await expect(page).toHaveURL(/\/products$/)
    await expect(page.getByRole('heading', { level: 1, name: '全部商品' })).toBeVisible()
  })
})
