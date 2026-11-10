/**
 * 登录回跳目标解析。
 *
 * 只接受站内绝对路径（以单个 / 开头），拒绝：
 * - 非字符串 / 空值；
 * - `//evil.com` 这类协议相对地址（会被浏览器当成外站）；
 * - `/login`、`/register` 自身，避免登录成功后再次跳回认证页形成循环。
 */
export interface AuthLocationState {
  /** 受保护页面记录的原目标地址。 */
  from?: string
  /** 注册成功后带到登录页、用于预填的用户名。 */
  registeredUsername?: string
}

export function readAuthLocationState(state: unknown): AuthLocationState {
  if (!state || typeof state !== 'object') {
    return {}
  }

  const record = state as Record<string, unknown>

  return {
    ...(typeof record.from === 'string' ? { from: record.from } : {}),
    ...(typeof record.registeredUsername === 'string'
      ? { registeredUsername: record.registeredUsername }
      : {}),
  }
}

function isAuthPage(path: string): boolean {
  return (
    path === '/login' ||
    path.startsWith('/login?') ||
    path === '/register' ||
    path.startsWith('/register?')
  )
}

export function resolveRedirectTarget(from: unknown, fallback = '/'): string {
  if (typeof from !== 'string' || from === '' || !from.startsWith('/') || from.startsWith('//')) {
    return fallback
  }

  if (isAuthPage(from)) {
    return fallback
  }

  return from
}
