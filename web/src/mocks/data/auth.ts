import type { AuthTokenResult, RegisterResult } from '@/api/auth'
import type { CurrentUser } from '@/api/account'

/**
 * MSW 认证状态（仅测试；不参与生产代码）。
 *
 * 模拟后端的有状态会话 + refresh 轮换：
 * - 登录成功创建一个会话，access/refresh token 成对生成；
 * - /me、/logout 只接受当前 access token；
 * - /refresh 只接受当前 refresh token，轮换后旧 token 失效（接近后端 reuse 语义）。
 */

export interface MockAuthUser {
  id: number
  username: string
  password: string
}

interface MockTokens {
  tokens: AuthTokenResult
  user: MockAuthUser
}

const initialUsers: MockAuthUser[] = [{ id: 1, username: 'demo', password: 'demo12345' }]

let users: MockAuthUser[] = initialUsers.map((user) => ({ ...user }))
let sessionUserId: number | null = null
let accessToken = ''
let refreshToken = ''
let generation = 0

export function resetMockAuthState(): void {
  users = initialUsers.map((user) => ({ ...user }))
  sessionUserId = null
  accessToken = ''
  refreshToken = ''
  generation = 0
}

export function findMockUserByUsername(username: string): MockAuthUser | undefined {
  return users.find((user) => user.username === username)
}

export function createMockUser(username: string, password: string): RegisterResult {
  const user: MockAuthUser = {
    id: users.length + 1,
    username,
    password,
  }
  users = [...users, user]
  return { id: user.id, username: user.username }
}

function issueTokens(user: MockAuthUser): AuthTokenResult {
  generation += 1
  accessToken = `mock-access-${generation}`
  refreshToken = `mock-refresh-${generation}`
  sessionUserId = user.id

  return {
    access_token: accessToken,
    refresh_token: refreshToken,
    token_type: 'Bearer',
    expires_in: 3600,
  }
}

/** 登录：创建新会话并返回双 token。 */
export function startMockSession(username: string): MockTokens | null {
  const user = findMockUserByUsername(username)

  if (!user) {
    return null
  }

  return { tokens: issueTokens(user), user }
}

/** /me、/logout 的 access token 校验。 */
export function findMockUserByAccessToken(token: string): CurrentUser | null {
  if (!token || token !== accessToken || sessionUserId === null) {
    return null
  }

  const user = users.find((item) => item.id === sessionUserId)

  return user ? { id: user.id, username: user.username } : null
}

/** refresh 轮换：旧 refresh token 立即失效。 */
export function rotateMockRefreshToken(token: string): MockTokens | null {
  if (!token || token !== refreshToken || sessionUserId === null) {
    return null
  }

  const user = users.find((item) => item.id === sessionUserId)

  if (!user) {
    return null
  }

  return { tokens: issueTokens(user), user }
}

/** 登出：撤销当前会话；无效 token 返回 false。 */
export function revokeMockSession(token: string): boolean {
  if (!token || token !== accessToken) {
    return false
  }

  sessionUserId = null
  accessToken = ''
  refreshToken = ''
  return true
}
