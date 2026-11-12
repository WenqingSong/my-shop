# W2-01 用户认证与登录体系：交付报告

- 任务：SurgeCart 用户商城前端 W2-01（用户认证与登录体系）
- 分支：`feat/phase2-user-frontend`
- 模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W2-01-user-authentication.md`

---

## 0. 摘要

本期在 W1 商品浏览基础上实现完整用户认证闭环：注册、登录、退出、当前用户信息、
统一认证状态、Token 注入、401 自动刷新、登录/注册页、受保护账户页、Header 登录状态切换。

认证依据仓库内真实 Go IAM 实现，未修改后端、未新增后端不存在的接口或字段。
后端返回 Bearer 双 Token（无 HttpOnly Cookie），前端采用**纯内存 Token** 存储，
页面内通过真实 `POST /refresh` 续期；刷新页面后需要重新登录，这是本期明确的安全取舍。

已执行 `lint / typecheck / test / build / test:e2e`，并完成真实后端联调。

---

## 1. 前置核查

| 检查项 | 结果 |
| --- | --- |
| 当前分支 | `feat/phase2-user-frontend` |
| W0 `631dc03` | 在当前 HEAD 历史中 |
| W1-01 `5e40486` | 在当前 HEAD 历史中 |
| 初始工作区 | clean |
| `contracts/openapi.yaml` | 不存在 |
| 已有 W2-01 文档 | 不存在，未覆盖既有成果 |
| 现有 API Client / Query Client / Router | 沿用并扩展 |

后端认证源码：

- 契约：`api/iam/v1/iam.go`
- 路由：`internal/cmd/routes_frontend.go`
- 登录/注册/登出：`internal/logic/iam/iam.go`
- Refresh：`internal/logic/iam/refresh.go`
- JWT：`internal/auth/jwt.go`
- Redis 会话：`internal/auth/session.go`
- 鉴权中间件：`internal/middleware/auth.go`
- 错误码：`internal/codes/codes.go`
- 设计说明：`docs/design/iam.md`

---

## 2. 实现的认证功能

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 注册 | 完成 | 仅 username/password；重复用户名提示；成功后跳登录并预填 |
| 登录 | 完成 | 表单校验、错误提示、加载态；成功后回跳原目标 |
| 退出登录 | 完成 | 调 `/logout`，失败也清空本地；Header 恢复未登录 |
| 当前用户 | 完成 | TanStack Query 统一缓存 `/me`，多组件只请求一次 |
| Token 注入 | 完成 | 内存 Token Store 注册到现有 `setAuthTokenProvider` |
| 401 处理 | 完成 | 单飞 refresh + 重试一次；失败清空会话并跳登录 |
| 登录状态持久化 | 明确不使用 | 仅内存；刷新页面后重新登录（安全取舍，见第 5 节） |
| 路由保护 | 完成 | `/account` 受保护；未登录跳 `/login` 并记录 `from` |
| 登录后回跳 | 完成 | 只接受站内路径，拒绝外链/协议相对地址 |
| Header 状态切换 | 完成 | 未登录显示登录/注册；已登录显示用户名与退出 |
| 移动端入口 | 完成 | 导航抽屉同步展示登录/注册或账户/退出 |

页面路由：

| 路由 | 说明 |
| --- | --- |
| `/login` | 登录页（已登录访问自动回跳） |
| `/register` | 注册页（已登录访问自动回跳） |
| `/account` | 最小受保护账户页（仅用户名、ID、退出；不包含 W2-02 功能） |

---

## 3. 实际使用的 API（Method + Path）

| 功能 | Method + Path | 鉴权 |
| --- | --- | --- |
| 注册 | `POST /register` | 公开 |
| 登录 | `POST /login` | 公开 |
| 刷新 Token | `POST /refresh` | 公开（refresh token 即凭证） |
| 当前用户 | `GET /me` | `Authorization: Bearer <access>` |
| 退出 | `POST /logout` | `Authorization: Bearer <access>`（后端仅验签） |

前端统一通过 `/api` 前缀访问，转发层剥离前缀（开发环境 Vite rewrite）。

---

## 4. 请求与响应结构

### 4.1 `POST /register`

请求：

```json
{ "username": "realuser1", "password": "password123" }
```

成功 `200`：

```json
{ "code": 0, "message": "OK", "data": { "id": 1, "username": "realuser1" } }
```

失败：

- 参数格式错误：`400` / `1001`
- 用户名已存在：`409` / `2001`

说明：注册成功后端**不签发 Token**，前端回到登录页。

### 4.2 `POST /login`

请求：

```json
{ "username": "realuser1", "password": "password123" }
```

成功 `200`：

```json
{
  "code": 0,
  "message": "OK",
  "data": {
    "access_token": "<HS256 JWT，239 字符左右>",
    "refresh_token": "<64 位 hex>",
    "token_type": "Bearer",
    "expires_in": 3600
  }
}
```

失败：

- 用户名/密码格式错误：`400` / `1001`
- 用户名或密码错误：`401` / `2002`
- 账号被禁用：`401` / `1002`

### 4.3 `POST /refresh`

请求：

```json
{ "refresh_token": "<64 位 hex>" }
```

成功 `200`：返回新的 `access_token` / `refresh_token` / `token_type` / `expires_in`。

失败：

- 无效/已撤销：`401` / `2012`
- 已过期：`401` / `2013`
- reuse（旧轮换 token 再次提交）：`401` / `2014`，并撤销该用户全部会话

### 4.4 `GET /me`

请求头：`Authorization: Bearer <access_token>`

成功 `200`：

```json
{ "code": 0, "message": "OK", "data": { "id": 1, "username": "realuser1" } }
```

未认证/会话失效：`401` / `1002`。

### 4.5 `POST /logout`

请求头：`Authorization: Bearer <access_token>`；无请求体。

成功 `200`：

```json
{ "code": 0, "message": "OK", "data": null }
```

后端只验签（`AuthSignatureOnly`）：即使会话已被撤销，重复登出仍可幂等成功。

---

## 5. Token 实际存储与生命周期

### 5.1 存储

- access token 与 refresh token **仅保存在当前 JS 运行时内存**
  （`src/auth/token-store.ts`）。
- **不写** `localStorage`、`sessionStorage`、Cookie、URL。
- 通过现有 API Client 的 `setAuthTokenProvider` 注入
  `Authorization: Bearer <access_token>`。

### 5.2 生命周期

| 凭证 | 有效期 | 行为 |
| --- | --- | --- |
| access token | 1 小时 | 每次请求自动注入；过期后 401 触发刷新 |
| refresh token | 30 天（后端轮换） | 只在刷新时使用；轮换后旧 token 失效 |
| 内存会话 | 页面生命周期 | 刷新/关闭标签页即丢失，需要重新登录 |

### 5.3 刷新策略

- 仅在受保护请求返回 401 时刷新，不做定时预刷新；
- **单飞**：并发 401 共享同一个 refresh Promise，避免后端 reuse 全量撤销；
- 刷新成功：替换内存双 token，并用新 access token 重试原请求一次；
- 刷新失败：清空 Token，Auth 状态转为未登录，受保护路由跳登录；
- 刷新期间用户退出/重新登录：丢弃旧刷新结果，避免“登出后又被刷新回来”。

### 5.4 为什么不持久化

后端通过 JSON 返回 30 天有效的 refresh token，没有 HttpOnly Cookie。
写入 localStorage/sessionStorage 会让 XSS 直接窃取长期凭证，违反本期安全要求。
因此选择内存 Token，并明确记录限制。若后续需要持久会话，应由后端通过
`HttpOnly + Secure + SameSite` Cookie 下发 refresh token，前端不接触长期凭证。

---

## 6. 认证状态管理方案

- **Token 事实来源**：`src/auth/token-store.ts`（模块级内存 + 订阅）。
- **React 订阅**：`useSyncExternalStore`，只暴露“是否存在会话”的布尔快照。
- **服务端用户信息**：TanStack Query `['auth','me']`，`staleTime` 5 分钟。
- **状态聚合**：`useAuthStatus()` 返回 `loading | authenticated | unauthenticated | error`。
- **无 Token 不发请求**：`/me` 查询 `enabled=false`，匿名浏览零额外请求。
- **多组件共享**：Header、`RequireAuth`、账户页使用同一 Query Key，只请求一次。
- **Zustand**：未用于认证，仍只保存移动端导航开关等 UI 状态。
- **登录**：写 Token 后主动 `fetchQuery('/me')`，失败则回滚 Token。
- **退出**：服务端失败也清空本地 Token 并移除 `['auth']` 缓存。

---

## 7. 路由保护

- `/account`：`RequireAuth`
  - loading → RouteLoading；
  - unauthenticated → `/login`，state 记录完整来源路径；
  - `/me` 非 401 错误 → 错误态 + 重试，不误跳登录；
  - authenticated → 渲染页面。
- `/login`、`/register`：`GuestOnly`
  - 已登录访问 → 回到 `from` 或首页；
  - loading 时仍渲染表单，避免登录提交过程中被卸载。
- 回跳目标经 `resolveRedirectTarget`：
  - 只允许 `/` 开头的站内路径；
  - 拒绝 `//evil.com`、`https://...`、`/login`、`/register`。

---

## 8. 关键文件与职责

### 8.1 新增

| 文件 | 职责 |
| --- | --- |
| `web/src/auth/token-store.ts` | 内存 Token 存储、Token Provider 注册、订阅快照 |
| `web/src/auth/refresh-session.ts` | 单飞 refresh、轮换/登出竞态保护 |
| `web/src/auth/use-auth.ts` | Auth 状态、登录/注册/退出 hooks |
| `web/src/auth/require-auth.tsx` | 受保护路由守卫 |
| `web/src/auth/guest-only.tsx` | 登录/注册页已登录重定向 |
| `web/src/auth/redirect.ts` | 安全回跳目标解析 |
| `web/src/auth/auth-errors.ts` | 认证错误码文案映射 |
| `web/src/auth/validation.ts` | 与后端一致的用户名/密码 Zod 校验 |
| `web/src/api/auth.ts` | 登录/注册/refresh 公开 API |
| `web/src/api/account.ts` | `/me`、`/logout` |
| `web/src/api/auth-request.ts` | 401 → refresh → 重试一次 |
| `web/src/components/auth/auth-card.tsx` | 登录/注册卡片 |
| `web/src/components/auth/password-input.tsx` | 密码输入 + 可见性切换 |
| `web/src/components/layout/header-auth.tsx` | Header 认证入口 |
| `web/src/pages/login-page.tsx` | 登录页 |
| `web/src/pages/register-page.tsx` | 注册页 |
| `web/src/pages/account-page.tsx` | 最小受保护账户页 |
| `web/src/mocks/data/auth.ts` | MSW 认证状态（会话/轮换） |
| `web/src/auth/*.test.ts`、`src/api/auth.test.ts`、`src/api/auth-request.test.ts` | 认证单元/集成测试 |
| `web/src/pages/{login,register,account}-page.test.tsx` | 页面测试 |
| `web/tests/e2e/auth.spec.ts` | 认证 E2E |

### 8.2 修改

| 文件 | 变更 |
| --- | --- |
| `web/src/api/envelope.ts` | 新增 `postData`（公开 POST，不解包重试） |
| `web/src/api/query-keys.ts` | 新增 `auth.me` |
| `web/src/components/layout/site-header.tsx` | 用 `HeaderAuthControl` 替换禁用登录入口 |
| `web/src/components/layout/mobile-nav.tsx` | 登录/注册/账户/退出入口 |
| `web/src/router/index.tsx` | 新增登录/注册/账户路由与守卫 |
| `web/src/router/lazy-pages.tsx` | 认证页面路由级懒加载 |
| `web/src/test/setup.ts` | 每用例清理 Token/refresh/Mock 认证状态 |
| `web/src/mocks/handlers.ts` | 注册/登录/refresh/me/logout Mock |
| `web/README.md` | W2 使用说明与 Token 策略 |
| `web/src/api/client.ts` | 无接口变更；沿用 W1 的 Token Provider 扩展点 |

---

## 9. 真实后端联调与 Mock 验证

### 9.1 已真实联调

环境：临时 MySQL/Redis + 仓库 Go 后端（`migrate up` + `serve`）；
因本地无七牛凭据，使用仓库 `test/serveproc`（`-tags serve_lifecycle`）+
`test/container/qiniu_mock` 仅重定向七牛 UC endpoint，校验逻辑不变；
Vite 不启用 MSW，通过 `/api` 代理访问后端。

接口级结果：

| 场景 | 结果 |
| --- | --- |
| 注册新用户 | `200`，返回 id/username |
| 重复注册 | `409` / `2001` |
| 错误密码登录 | `401` / `2002` |
| 登录 | `200`，access 约 239 字符、refresh 64 位 hex、`expires_in=3600` |
| `/me` 携带 access | `200`，返回 id/username |
| `/me` 无 token | `401` / `1002` |
| `/refresh` | `200`，返回新双 token |
| 旧 refresh 再次提交 | `401` / `2014`，并触发全量撤销（新 access 再访问 `/me` 也为 401） |
| `/logout`（签名有效） | `200`，`data=null` |
| `/logout` 后 `/me` | `401` / `1002` |

浏览器级结果（Playwright 临时脚本，非仓库用例）：

| 场景 | 结果 |
| --- | --- |
| 注册成功 → 登录页预填用户名 | 通过 |
| 错误密码提示 | 通过 |
| 登录成功 → Header 显示用户名/退出 | 通过 |
| 登录后进入 `/account` | 通过 |
| 刷新页面 → 内存 Token 丢失 → 跳登录 | 通过（符合设计限制） |
| 重新登录 → 回到 `/account` | 通过 |
| 退出登录 → Header 恢复未登录 | 通过 |

验证结束后已停止后端/Vite/Mock 进程，并删除临时容器、数据卷与脚本。

### 9.2 仅 Mock 验证

- `npm run test`（Vitest + MSW）：登录/注册/退出、401 单飞刷新、refresh 轮换、
  重复 `/me` 去重、路由守卫、Header 切换等全部自动化用例；
- `npm run test:e2e`：登录失败、受保护回跳、注册成功/重复、Header 退出、移动端入口；
- 401 自动刷新的真实触发需要等待 access token 1 小时过期，本期未做时间旅行；
  后端 `/refresh` 与 reuse 语义已通过 curl 真实验证，前端 401 刷新流程由 MSW 覆盖。

**Mock 测试不能表述为真实后端联调通过。**

---

## 10. 后端尚未支持的能力

| 缺口 | 影响 |
| --- | --- |
| refresh token 通过 JSON 返回，无 HttpOnly Cookie | 无法安全持久化登录；刷新页面需重新登录 |
| 登录响应不含用户信息 | 登录后需要额外 `GET /me` |
| 无“记住我”/持久会话开关 | 前端无法提供长期登录 |
| 无邮箱/手机号/验证码注册 | 注册仅 username + password |
| 无找回密码/修改密码接口 | 本期不提供相关入口 |
| 登录禁用账号返回通用 1002 | 前端无法区分“禁用”与其它未授权 |
| 会话列表/撤销其他设备属于 W2-02 范围 | 本期未接入 |
| 无前端专用 refresh Cookie 轮换策略 | 长期凭证只能内存保存 |

---

## 11. 测试命令与真实执行结果

执行目录：`/root/projects/my-shop/web`

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过，0 error / 0 warning |
| `npm run typecheck` | 通过 |
| `npm run test` | 22 个测试文件、97 条用例全部通过 |
| `npm run build` | 通过；登录/注册/账户页各自独立 chunk |
| `npm run test:e2e` | 25 条 Playwright 用例全部通过（约 29s） |

构建产物（关键 chunk）：

- `login-page` 2.83 kB
- `register-page` 2.41 kB
- `account-page` 1.77 kB
- `products-page` 7.86 kB
- `product-detail-page` 12.79 kB

E2E 覆盖：商品浏览 19 条（W1 回归）+ 认证 6 条（登录校验/失败、受保护回跳、
Header 状态与退出、注册成功/重复、移动端登录入口）。

---

## 12. 交付文档路径

1. 开发知识文档：`.agent/tasks/dev/W2-01-user-authentication.md`
2. 正式交付报告：`docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md`（本文件）

两份文档职责不同：前者解释架构、原理与维护方式，后者记录范围、接口、验证与缺口。

---

## 13. Git

- 本任务代码与两份文档在同一提交中完成，提交信息：
  `feat(web): implement user authentication flow`。
- 提交 SHA 与最终工作区状态以本次交付汇报为准。
- 未修改 main、未修改后端 Go 代码、未修改 Docker/Nginx/K8s/Prometheus 配置、
  未修改其他前端工程、未合并其他分支、未 force push、未 push。

---

## 14. 风险与后续建议

1. **登录持久化**：需要后端提供 HttpOnly Cookie 方案；在此之前刷新页面必须重新登录。
2. **401 刷新**：仅对使用 `authGetData/authPostData` 的接口生效；W2-02 新接口必须沿用。
3. **refresh 并发**：任何新代码不得绕过 `refreshSession()`，否则可能触发 reuse 全量撤销。
4. **账号禁用**：后端对登录返回 1002，前端文案需保持“账号不可用或已被禁用”而非精确原因。
5. **Token 安全**：继续保持不落 Web Storage、不进日志/URL；如引入第三方脚本需评估 XSS。
6. **登录态与缓存**：登出时已移除 `['auth']`；后续用户私有数据（地址/收藏）也必须在登出时清理。
