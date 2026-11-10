# W2-01 用户认证与登录体系：开发知识文档

- 任务：SurgeCart 用户商城前端 W2-01（用户认证与登录体系）
- 分支：`feat/phase2-user-frontend`
- 代码位置：`web/`
- 对应正式报告：`docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md`
- 本文目标：解释认证架构、JWT 与 refresh token 原理、Token 生命周期、认证状态管理、
  路由保护、API 调用链与安全取舍，供 W2-02 及后续任务接手时直接复用。

本文只描述仓库中真实存在并可运行的代码；接口结论均来自 Go 源码与真实请求验证。

---

## 1. 任务边界与开始前核查

### 1.1 边界

本期实现：

- 注册、登录、退出登录；
- 当前用户信息获取（`/me`）；
- 统一认证状态与 Token 注入；
- HTTP 401 处理（单飞 refresh + 重试）；
- 登录/注册路由与受保护路由；
- Header 登录状态切换。

不实现：个人中心资料编辑、收货地址、收藏、订单、购物车。
`/account` 只是用于验证认证状态与路由保护的最小页面，不展开 W2-02 功能。

### 1.2 核查结果

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| W0 `631dc03`、W1-01 `5e40486` | 均在当前 HEAD 历史中 |
| 初始工作区 | clean |
| `contracts/openapi.yaml` | 不存在 |
| 已有同名 W2 文档 | 不存在，本次新建 |
| 现有 API Client / Query Client / 路由 | 已具备，本期在其上扩展 |

---

## 2. 后端认证事实（源码依据）

### 2.1 接口

| 接口 | 鉴权 | 说明 |
| --- | --- | --- |
| `POST /register` | 公开 | 只接受 `username` + `password`，返回 `id`/`username`，**不签发 Token** |
| `POST /login` | 公开 | 返回 access + refresh 双 token（JSON） |
| `POST /refresh` | 公开 | refresh token 自身即凭证；轮换后返回新双 token |
| `GET /me` | `Auth` | 验签 + Redis 会话 + 用户状态/认证版本 |
| `POST /logout` | `AuthSignatureOnly` | 仅验签；按 `sid` 撤销会话 + refresh family，幂等 |

源码：

- 契约结构：`api/iam/v1/iam.go`
- 路由注册：`internal/cmd/routes_frontend.go`
- 登录/注册/登出：`internal/logic/iam/iam.go`
- Refresh 轮换与 reuse：`internal/logic/iam/refresh.go`
- JWT：`internal/auth/jwt.go`
- Redis 会话：`internal/auth/session.go`
- 鉴权中间件：`internal/middleware/auth.go`
- 设计说明：`docs/design/iam.md`

### 2.2 JWT

- 算法：HS256，密钥 `AUTH_JWT_SECRET`（≥32 字节）。
- Claims：`sub`（用户 id）、`type="user"`、`iss="surgecart"`、`iat`/`exp`、`sid`。
- `exp = iat + 3600`，即 access token 固定 1 小时。
- JWT 本身不可撤销；服务端通过 Redis `iam:session:{sid}` 判断会话是否仍有效。
- 每请求还会校验 `users.status=1` 且 `session.auth_epoch == users.auth_epoch`；
  账号被禁用后旧凭证立即失效。

### 2.3 Refresh Token

- 登录生成 32 字节随机 refresh token（64 位 hex），明文**只在登录/刷新响应出现一次**。
- MySQL `refresh_tokens` 只存 SHA-256 哈希，30 天绝对有效期（`AUTH_REFRESH_TTL`）。
- 每次 `/refresh` 轮换：旧 token 标记 `rotated`，签发新 token；
  同一 refresh token 再次提交会被判定 reuse，撤销该用户全部会话与 family。
- 因此前端刷新必须**单飞**，不能并发调用 `/refresh`。

### 2.4 Token 返回方式

后端通过 JSON body 返回：

```json
{
  "access_token": "<JWT>",
  "refresh_token": "<64 hex>",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

**没有** `Set-Cookie`、没有 HttpOnly Cookie、没有 Cookie 会话。
`apiClient` 通过 `Authorization: Bearer <access_token>` 使用 access token。

### 2.5 错误码

| code | HTTP | 语义 |
| --- | --- | --- |
| 1001 | 400 | 参数错误（用户名/密码格式） |
| 1002 | 401 | 未授权（登录时通常为账号禁用） |
| 2001 | 409 | 用户名已存在 |
| 2002 | 401 | 用户名或密码错误（不区分不存在/密码错） |
| 2012 | 401 | refresh token 无效/已撤销 |
| 2013 | 401 | refresh token 过期 |
| 2014 | 401 | refresh token reuse（触发全量撤销） |

登录与注册的字段规则（`internal/logic/iam/iam.go`）：

- username：`^[a-zA-Z0-9]{3,24}$`
- password：`len(password)`（UTF-8 字节）8-24

后端没有确认密码、短信验证码、邮箱、手机号等字段，前端不增加。

---

## 3. 前端认证架构

```text
登录页
  └─ useLogin()
       ├─ POST /login（api/auth.ts，无 401 重试，避免递归）
       ├─ setAuthTokens()  // 仅内存
       └─ queryClient.fetchQuery(['auth','me']) // 跳转前确认用户信息

Header / RequireAuth / AccountPage
  └─ useAuthStatus()
       ├─ useSyncExternalStore(token-store)  // 是否有会话
       └─ useQuery(['auth','me'])            // 共享缓存，天然去重

需要登录的 API
  └─ authGetData / authPostData（api/auth-request.ts）
       ├─ 401 → refreshSession()  // 单飞
       │        └─ POST /refresh → setAuthTokens()
       └─ 用新 access token 重试一次

退出
  └─ useLogout()
       ├─ POST /logout（失败也继续本地登出）
       └─ clearAuthTokens() + 移除 ['auth','me'] 缓存
```

### 3.1 模块职责

| 文件 | 职责 |
| --- | --- |
| `src/auth/token-store.ts` | 内存 Token 事实来源；注册 `setAuthTokenProvider`；`useSyncExternalStore` 快照 |
| `src/auth/refresh-session.ts` | 单飞 refresh；轮换/登出竞态保护 |
| `src/api/auth.ts` | 登录/注册/refresh 公开接口 |
| `src/api/account.ts` | `/me`、`/logout` 登录态接口 |
| `src/api/auth-request.ts` | 401 → refresh → 重试一次；失败清空 Token |
| `src/auth/use-auth.ts` | `useAuthStatus`、`useLogin`、`useRegister`、`useLogout` |
| `src/auth/require-auth.tsx` | 受保护路由 |
| `src/auth/guest-only.tsx` | 登录/注册页已登录重定向 |
| `src/auth/redirect.ts` | 安全回跳目标解析 |
| `src/auth/auth-errors.ts` | 认证业务码 → 用户文案 |
| `src/auth/validation.ts` | 与后端一致的 Zod 校验 |

### 3.2 Token 存储决策（仅内存）

后端支持 refresh token，但没有 HttpOnly Cookie。把 30 天有效的 refresh token 写入
`localStorage`/`sessionStorage` 会被任意 XSS 读取，等同长期账号接管风险；
任务也明确要求不要擅自把长期 JWT 放进 localStorage。

因此本期选择：

- access token + refresh token **都只存在 JS 内存**；
- 不写任何 Web Storage；
- 刷新页面/新标签页后 Token 丢失，**必须重新登录**；
- 页面内 access token 过期时通过真实的 `POST /refresh` 续期，不强制每小时重登；
- 若未来需要“刷新后保持登录”，正确方案是后端通过
  `HttpOnly + Secure + SameSite` Cookie 下发 refresh token（或 BFF 代理），
  前端不接触长期凭证；这需要后端契约变更，不属于本期。

### 3.3 Token 生命周期

```text
登录
  access 1h / refresh 30d（轮换）
    │
    ├─ access 有效：正常请求
    │
    ├─ access 过期 → 401
    │     ├─ refresh 有效 → 新 access + 新 refresh → 重试原请求
    │     └─ refresh 无效/过期/reuse → 清空 Token → 跳登录
    │
    ├─ 用户主动退出 → POST /logout → 清空 Token
    │
    └─ 页面刷新 → 内存清空 → 重新登录
```

- 不做定时预刷新；只在 401 时刷新，避免无意义请求。
- 单飞：并发 401 共享同一个 `refreshSession()` Promise。
- 竞态：刷新返回后如果 refresh token 已被替换或清空（用户重新登录/退出），丢弃旧结果。
- refresh 失败：只清空当前会话的 Token，并让路由守卫跳转登录。

### 3.4 认证状态

`useAuthStatus()` 返回：

- `unauthenticated`：内存无 Token；
- `loading`：有 Token，`/me` 请求中；
- `authenticated`：`/me` 成功；
- `error`：`/me` 非 401 失败（网络/500），保留 Token 并允许重试，不误判为未登录。

多个组件（Header、RequireAuth、AccountPage）使用同一个 Query Key，`/me` 只会请求一次。
无 Token 时 query `enabled=false`，匿名浏览不会产生 `/me` 请求。

### 3.5 登录与注册

- 登录成功：写 Token → `fetchQuery('/me')` → `GuestOnly` 读取 `location.state.from` 回跳。
- 注册成功：后端不签发 Token，前端跳转 `/login`，通过 history state 预填用户名并提示成功。
- 表单只包含后端支持的 `username` + `password`；密码显示/隐藏只是 UI 切换。
- 前端校验与后端一致（用户名正则、密码 UTF-8 字节长度）。

### 3.6 路由保护

- `/account`：`RequireAuth` 包裹；未登录跳 `/login` 并携带 `from`；
- `/login`、`/register`：`GuestOnly`；已登录访问时跳回 `from` 或首页；
- 回跳目标只允许站内单斜杠路径，拒绝 `//evil.com`、外链、登录/注册页自身；
- 认证加载中展示 RouteLoading，401 清空后由守卫跳转，`/me` 非 401 错误展示重试。

### 3.7 Header

- 未登录：`登录 / 注册` 链接；
- 已登录：`账户：<username>` + `退出登录` 按钮；
- 加载中：`账户加载中`，不闪错误的未登录入口；
- 加载失败：`账户重试`；
- 移动端导航抽屉同步展示登录/注册或账户/退出入口。

---

## 4. 安全决策汇总

1. **不持久化 Token**：内存存储，规避 XSS 窃取长期 refresh token。
2. **不新增后端能力**：refresh 是后端已有接口；没有实现 Cookie、验证码、找回密码等不存在的能力。
3. **refresh 单飞**：防止并发轮换触发 reuse 全量撤销。
4. **401 重试一次**：避免 refresh 失败后无限循环；重试仍 401 立即清空会话。
5. **本地登出优先**：服务端 logout 失败也要清空本地 Token；服务端会话最多存活一个 session TTL。
6. **回跳白名单**：只允许站内路径，防开放重定向。
7. **错误文案不枚举用户**：登录失败统一“用户名或密码错误”；注册重复才提示用户名已存在（后端语义）。
8. **不在 URL/日志中放 Token**；不 `console.log` Token 或密码。
9. **表单**：`autocomplete="username" / current-password / new-password`，密码可切换可见性。
10. **受保护 API 统一走 `authGetData/authPostData`**，组件不得自行拼 Authorization。

---

## 5. 遇到的问题与解决

### 5.1 登录成功时登录页被卸载

最初 `GuestOnly` 在 `status=loading` 时返回 Loading；登录写入 Token 后，
`/me` 加载会让 GuestOnly 卸载正在提交的登录表单，错误提示与表单状态丢失。
解决：GuestOnly 只在 `authenticated` 时重定向，loading 时继续渲染表单，
由 Header/守卫处理加载态。

### 5.2 refresh 轮换与并发

后端 refresh 是一次性轮换，并发刷新会被判 reuse。解决：模块级单飞 Promise +
刷新结果与当前 refresh token 的一致性校验。

### 5.3 测试环境的 Token 状态隔离

Token Store 是模块级内存，用例间会泄漏。解决：`src/test/setup.ts` 在每个用例后
`clearAuthTokens()`、`resetRefreshStateForTests()`、`resetMockAuthState()`。

### 5.4 E2E 的 label 歧义

密码输入框旁有 `显示密码` 按钮，`getByLabel('密码')` 会同时匹配输入框与按钮。
解决：E2E 使用 `getByLabel('密码', { exact: true })`。

### 5.5 真实后端启动依赖七牛检查

本地联调沿用仓库 `test/serveproc`（`-tags serve_lifecycle`）+
`test/container/qiniu_mock`，只重定向七牛 UC endpoint，不修改校验逻辑；
生产二进制不读取该变量。该手段只用于本地验证，不是产品功能。

### 5.6 密码长度按字节

后端 `len(password)` 是 UTF-8 字节数；前端用 `TextEncoder` 计算，保证
“4 个中文字符（12 字节）”这类输入与后端一致。

---

## 6. 测试验证

### 6.1 自动化

```bash
cd web
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
```

覆盖：

- Token Store 保存/清空/订阅；
- 登录成功/失败、注册成功/重复用户名、refresh 轮换与旧 token 失效；
- 401 → refresh → 重试、并发 401 只刷新一次、refresh 失败清空；
- 未登录守卫跳转、登录后回跳、Header 状态切换、退出登录；
- `/me` 多组件共享一次请求；
- 表单校验（用户名格式、密码字节长度、空值）；
- E2E：登录失败、受保护路由回跳、Header 退出、注册成功/重复、移动端入口。

### 6.2 真实后端联调

临时启动 MySQL/Redis + Go 后端，Vite 不启用 MSW：

- 注册 → 200；重复注册 → 409/2001；
- 错误密码 → 401/2002；
- 登录返回双 token；
- `/me` 携带 access → 200；无 token → 401/1002；
- `/refresh` 返回新双 token；旧 refresh 再次提交 → 401/2014，
  且后端全量撤销会话（新 access 访问 `/me` 也变为 401）；
- 登出（AuthSignatureOnly）→ 200；登出后 `/me` → 401；
- 浏览器：注册→登录→账户页→刷新丢失登录→重新登录回跳→退出，全部通过。

Mock 验证与真实联调分开记录，详见交付报告。

---

## 7. 维护注意事项

1. **W2-02 受保护接口**必须使用 `authGetData/authPostData`，否则 401 不会自动刷新。
2. 任何新的 refresh 调用都必须经过 `refreshSession()`，禁止直接 `refreshTokens()` 并发调用。
3. 不要为“保持登录”私自把 refresh token 写入 Web Storage；需先推动后端 HttpOnly Cookie 方案。
4. `/me` 的 Query Key 固定为 `['auth','me']`；登出时移除 `['auth']` 缓存。
5. 新增认证错误码时，在 `auth-errors.ts` 补充用户文案，避免直接暴露后端 message。
6. 路由回跳必须经过 `resolveRedirectTarget`，禁止直接把 `location.state.from` 传给 `navigate`。
7. 登录页/注册页字段必须与后端契约一致；不要增加验证码、手机号等不存在字段。
8. 若后端将 refresh token 改为 Cookie，需要同步移除内存 refresh token 与 `Authorization` 注入逻辑。
9. `expires_in` 目前只用于记录 `expiresAt`，未做预刷新；如未来需要，必须保持单飞语义。
10. 退出登录失败时前端已本地登出，服务端会话依赖 TTL 自然过期；若要求强一致，需要后端提供幂等撤销保障（当前 `/logout` 已是幂等）。
