import { expect, test, type Page } from '@playwright/test'

const DEMO_USERNAME = 'demo'
const DEMO_PASSWORD = 'demo12345'

async function login(page: Page, username: string, password: string) {
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '登录' }).click()
}

test.describe('用户认证', () => {
  test('登录表单校验与失败提示', async ({ page }) => {
    await page.goto('/login')

    await page.getByRole('button', { name: '登录' }).click()
    await expect(page.getByText('请输入用户名')).toBeVisible()
    await expect(page.getByText('请输入密码')).toBeVisible()

    await login(page, DEMO_USERNAME, 'wrongpass')
    await expect(page.getByRole('alert')).toContainText('用户名或密码错误')
    await expect(page).toHaveURL(/\/login$/)
  })

  test('未登录访问受保护页面，登录后返回原目标', async ({ page }) => {
    await page.goto('/account')

    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByRole('heading', { level: 1, name: '登录' })).toBeVisible()

    await login(page, DEMO_USERNAME, DEMO_PASSWORD)

    await expect(page).toHaveURL(/\/account$/)
    await expect(page.getByRole('heading', { level: 1, name: '账户' })).toBeVisible()

    const summary = page.getByRole('region', { name: '当前登录用户' })
    await expect(summary).toContainText(DEMO_USERNAME)
  })

  test('Header 登录态切换与退出登录', async ({ page }) => {
    await page.goto('/login')
    await login(page, DEMO_USERNAME, DEMO_PASSWORD)

    await expect(page.getByRole('link', { name: `账户：${DEMO_USERNAME}` })).toBeVisible()
    await expect(page.getByRole('button', { name: '退出登录' })).toBeVisible()

    await page.getByRole('link', { name: `账户：${DEMO_USERNAME}` }).click()
    await expect(page.getByRole('heading', { level: 1, name: '账户' })).toBeVisible()

    const summary = page.getByRole('region', { name: '当前登录用户' })
    await summary.getByRole('button', { name: '退出登录' }).click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByRole('link', { name: '登录 / 注册' })).toBeVisible()
    await expect(page.getByRole('link', { name: `账户：${DEMO_USERNAME}` })).toHaveCount(0)
  })

  test('注册成功后回到登录页并预填用户名', async ({ page }) => {
    await page.goto('/register')

    await page.getByLabel('用户名').fill('e2euser1')
    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '注册' }).click()

    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByText('注册成功，请使用新账号登录。')).toBeVisible()
    await expect(page.getByLabel('用户名')).toHaveValue('e2euser1')

    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '登录' }).click()

    await expect(page.getByRole('link', { name: '账户：e2euser1' })).toBeVisible()
  })

  test('重复用户名注册失败', async ({ page }) => {
    await page.goto('/register')

    await page.getByLabel('用户名').fill(DEMO_USERNAME)
    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '注册' }).click()

    await expect(page.getByRole('alert')).toContainText('用户名已存在')
    await expect(page).toHaveURL(/\/register$/)
  })
})

test.describe('移动端认证入口', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('导航抽屉展示登录入口并可进入登录页', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('button', { name: '打开商城导航' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByRole('link', { name: '登录' }).click()
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByRole('heading', { level: 1, name: '登录' })).toBeVisible()
  })
})
