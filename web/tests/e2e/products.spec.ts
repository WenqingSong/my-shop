import { expect, test } from '@playwright/test'

test.describe('商品列表与搜索', () => {
  test('展示商品列表、总数与分页', async ({ page }) => {
    await page.goto('/products')

    await expect(page.getByRole('heading', { level: 1, name: '全部商品' })).toBeVisible()
    await expect(page.getByText('第 1 / 3 页')).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' })).toBeVisible()
    await expect(page.getByRole('navigation', { name: '商品分类筛选' })).toBeVisible()
  })

  test('Header 搜索框提交后展示搜索结果', async ({ page }) => {
    await page.goto('/products')

    await page.getByRole('searchbox', { name: '搜索关键词' }).first().fill('星海')
    await page.getByRole('searchbox', { name: '搜索关键词' }).first().press('Enter')

    await expect(page).toHaveURL(/q=%E6%98%9F%E6%B5%B7/)
    await expect(page.getByRole('heading', { level: 1, name: '“星海”的搜索结果' })).toBeVisible()
    await expect(page.getByRole('link', { name: /查看商品：星海/ })).toHaveCount(4)
  })

  test('分页切换加载下一页并保持列表可用', async ({ page }) => {
    await page.goto('/products')

    await page.getByRole('button', { name: '第 2 页' }).click()

    await expect(page).toHaveURL(/page=2/)
    await expect(page.getByRole('link', { name: '查看商品：云裳 女士针织开衫' })).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' })).toHaveCount(0)
  })

  test('分类筛选只展示该分类商品', async ({ page }) => {
    await page.goto('/products')

    await page.getByRole('navigation', { name: '商品分类筛选' }).getByRole('link', { name: '耳机音箱' }).click()

    await expect(page).toHaveURL(/category_id=12/)
    await expect(page.getByRole('heading', { level: 1, name: '耳机音箱' })).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云听 Air 3 主动降噪耳机' })).toBeVisible()
    await expect(page.getByRole('link', { name: /查看商品：云裳/ })).toHaveCount(0)
  })

  test('价格排序使用后端 sort/order 参数', async ({ page }) => {
    await page.goto('/products?sort=price&order=asc')

    const cards = page.getByRole('link', { name: /查看商品：/ })
    await expect(cards.first()).toHaveAccessibleName('查看商品：暖居 布艺收纳箱 45L')
  })

  test('无匹配结果时展示空状态并可清除筛选', async ({ page }) => {
    await page.goto('/products?q=%E4%B8%8D%E5%AD%98%E5%9C%A8%E7%9A%84%E5%95%86%E5%93%81')

    await expect(page.getByText('没有找到符合条件的商品')).toBeVisible()
    // 筛选栏与空状态各有一个「清除筛选」入口，点击任意一个都应回到全部商品。
    await page.getByRole('button', { name: '清除筛选' }).first().click()

    await expect(page.getByRole('heading', { level: 1, name: '全部商品' })).toBeVisible()
    await expect(page.getByRole('link', { name: '查看商品：云裳 女士通勤托特包' })).toBeVisible()
  })

  test('接口错误时展示错误状态与重试入口', async ({ page }) => {
    // __error__ 是 MSW Mock 专用测试钩子（真实后端不存在），见 src/mocks/handlers.ts。
    await page.goto('/products?q=__error__')

    await expect(page.getByText('商品加载失败')).toBeVisible()
    await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible()
  })
})
