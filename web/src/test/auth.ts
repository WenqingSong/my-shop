import { loginRequest } from '@/api/auth'
import { setAuthTokens } from '@/auth/token-store'

/** 测试辅助：登录 mock 用户 demo 并写入内存 Token。 */
export async function signInAsDemo(): Promise<void> {
  const tokens = await loginRequest({ username: 'demo', password: 'demo12345' })
  setAuthTokens(tokens)
}
