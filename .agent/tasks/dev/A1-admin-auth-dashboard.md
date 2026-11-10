# A1 开发知识文档：管理员认证与运营工作台

| 项 | 内容 |
|---|---|
| 任务 | A1 — 管理员认证、Token 管理、路由守卫、权限映射、运营工作台（五个 Dashboard 接口） |
| 分支 | `feat/phase2-admin-frontend` |
| 基线提交 | `339b513c4a3460d15c555a24a8900296b72f5aed`（A0 文档交付完成） |
| 代码位置 | 仓库根目录 `admin/` |
| 正式交付报告 | `docs/reports/A1-admin-auth-dashboard.md` |
| 契约状态 | `contracts/openapi.yaml` **尚未交付**，全部 DTO 为 PROVISIONAL |

> 本文记录的是**最终代码**的实现事实与取舍，用于后续对话与交接。
> 未经验证的内容会显式标注，不作为"已可用"的结论。

---

## 1. 管理员认证流程与 Token 生命周期

### 1.1 登录链路

```
登录页表单（admin/src/views/login/index.vue）
  └─ el-form 校验（用户名/密码必填）
       └─ userStore.login({ username, password })        ← store/modules/user.ts
            ├─ adminLogin()                              ← api/modules/admin-auth.ts
            │    └─ POST /api/admin/login                ← http baseURL=/api + 真实路由 /admin/login
            │         └─ envelope: code===0 → 返回 data
            ├─ setAccessToken(access_token, expires_in)   ← utils/auth.ts
            │    └─ sessionStorage: access_token / expires_at(= now + expires_in*1000)
            ├─ getCurrentAdmin({ silent: true })
            │    └─ GET /api/admin/me
            │         └─ SET_ADMIN_PROFILE(profile)
            │              ├─ Pinia: adminId / username / is_super / roles / profileLoaded
            │              └─ setPermissionSubject(...)   ← utils/permission.ts 同步快照
            └─ router.replace(redirect || "/dashboard")
```

失败路径：任一步失败都会 `clearAccessToken()` + `RESET_AUTH_STATE()`，不留下
"有 token 没有身份"的半登录状态。

### 1.2 Token 存储与生命周期

| 项 | 实现 |
|---|---|
| 存储介质 | `sessionStorage`（关闭标签页即失效） |
| 存储键 | `surgecart-admin:access_token`、`surgecart-admin:expires_at` |
| 写入时机 | 登录成功、`expires_in > 0` 时 |
| 过期判定 | `expires_at <= Date.now()`；**缺少 expires_at 也视为不可用** |
| 清理时机 | 登出、401、会话校验失败、路由守卫发现本地 token 已过期、设置面板"清空缓存" |
| 不存储 | 密码、Refresh Token、JWT 解码后的身份信息 |

关键设计：**不解析 JWT 内容作为身份依据**。JWT payload 可被客户端任意篡改，只能作为不可信提示；
真正身份一律以 `GET /admin/me` 的返回为准。

`expires_at` 而非"剩余秒数"的原因：剩余秒数在页面长时间停留后会失真，
绝对时间戳可以直接与 `Date.now()` 比较。

### 1.3 后端契约（依据 `api/admin/v1` 实际代码）

| 接口 | 方法 | 请求 | 成功 data |
|---|---|---|---|
| `/admin/login` | POST | `{username, password}` | `{access_token, token_type, expires_in}` |
| `/admin/me` | GET | — | `{id, username, is_super, roles[]}` |
| `/admin/logout` | POST | — | `null` |

注意：`/admin/me` 当前**没有** `permissions[]` 字段。前端 DTO 中该字段为可选，
Pinia 中存 `null` 表示"后端未提供"，用于区分"未提供"与"明确为空"。

---

## 2. Pinia 管理员状态与页面刷新恢复

### 2.1 状态结构（`src/store/modules/user.ts`）

| 字段 | 类型 | 说明 |
|---|---|---|
| `adminId` | `number \| null` | 后端 `id`，`null` 表示未登录 |
| `username` / `nickname` | `string` | 布局导航栏展示用（`nickname` 与 username 同值） |
| `isSuper` | `boolean` | 后端 `is_super` |
| `roles` | `string[]` | 后端 `roles` |
| `permissions` | `string[] \| null` | `null` = 后端未下发；数组 = 后端权威权限 |
| `profileLoaded` | `boolean` | 是否已通过 `/admin/me` 校验 |

### 2.2 刷新恢复机制

```
浏览器刷新 → main.ts 挂载前 → router.isReady()
  → 守卫 beforeEach → resolveAuthGuard(to)
      → hasLocalToken()? 否 → 跳 /login
      → 是 → ensureAdminProfile() → GET /admin/me → SET_ADMIN_PROFILE → 放行
```

`ensureAdminProfile()` 的三层保护：

1. **缓存**：`profileLoaded && adminId !== null` 直接返回内存中的资料，不发请求。
2. **并发合并**：模块级 `profileRequest` Promise，多个调用方共享同一次请求。
3. **时效预检**：无 token 或 token 已过期时直接清理并抛错，不浪费一次网络请求。

身份信息**不落盘**：刷新后必定重新请求 `/admin/me`。这样即使 sessionStorage 被写入伪造的
"super_admin"，也无法通过后端校验。

### 2.3 登出流程

```ts
async logOut({ redirect = true } = {}) {
  try {
    if (getAccessToken() && !isTokenExpired()) await adminLogout();  // 服务端撤销（幂等）
  } catch { /* 忽略 */ }
  finally {
    clearAccessToken();
    RESET_AUTH_STATE();
    handleTags("equal", [...routerArrays]);   // 清空多标签
    resetRouter();                            // 重置路由表
    if (redirect) await router.replace("/login").catch(() => {});
  }
}
```

"无论服务端成功与否都清本地"是硬要求：token 已失效时 `/admin/logout` 会返回 401，
若在此时中断清理，用户会被困在一个"看起来已登录但所有请求都失败"的界面里。

---

## 3. Vue Router 认证守卫

### 3.1 为什么把决策逻辑抽到 `src/router/guard.ts`

守卫内联写法（A0 的形态）难以单元测试：要测"未登录跳登录页"必须真的驱动一次导航，
还要处理全局 router 单例的状态残留。

抽出 `resolveAuthGuard(to)` 后：

- 决策是可枚举的纯数据：`{ type: "allow" }` 或 `{ type: "redirect", path, query }`。
- `router/index.ts` 只负责把决策翻译成 vue-router 的返回值。
- 测试直接调用函数，覆盖 9 种场景，不需要挂载应用。

### 3.2 决策表

| 场景 | 决策 |
|---|---|
| 无 token 访问受保护页 | `redirect /login?redirect=<fullPath>`（根路径不带 redirect） |
| token 存在但已过期 | 先清理本地状态，再 `redirect /login` |
| 有 token 访问 `/login`，`/admin/me` 成功 | `redirect /dashboard` |
| 有 token 访问 `/login`，`/admin/me` 失败 | 清理后 `allow`（留在登录页） |
| 有 token 访问受保护页，会话有效 | `allow` |
| 有 token 访问受保护页，`/admin/me` 失败 | 清理后 `redirect /login?redirect=...` |

### 3.3 防循环与防重复的关键点

- **防循环**：已登录访问 `/login` 时**先校验会话**再跳工作台。若直接跳，服务端已失效的 token
  会在工作台守卫处失败 → 又跳回登录页 → 形成重定向循环。
- **防重复请求**：`ensureAdminProfile()` 的缓存 + 并发合并，保证一次页面加载只请求一次 `/admin/me`。
- **根路径特例**：`/` 不带 `redirect` 参数，避免登录后先跳 `/` 再被首页重定向一次。

---

## 4. Axios Bearer 注入与 401 / 403 处理

### 4.1 请求拦截

```ts
const token = getAccessToken();
if (token && !config.skipAuth) {
  config.headers = config.headers ?? {};
  config.headers.Authorization = formatToken(token);  // `Bearer <token>`
}
```

`skipAuth` 用于登录请求（虽然此时通常没有 token，但显式声明可避免历史残留 token 被带上）。

### 4.2 401：登录态失效

- 判定：`error.response.status === 401` **且** `!requestConfig.skipUnauthorizedHandling`。
- 动作：调用统一的 `unauthorizedHandler`（在 `main.ts` 注册），完成
  "清 token → 重置 Pinia → 重置多标签 → `resetRouter()` → `router.replace('/login')` → 提示一次"。
- 去重：模块级 `unauthorizedTask` Promise。并发 401 共用同一个 task，处理结束后保留 500ms
  再复位，避免同一批请求的尾随 401 重复清理。
- 提示：401 本身不弹全局错误（由 handler 统一提示一次），避免"清一次 + 弹多次"。

### 4.3 403：权限不足，不清理登录态

403 与 401 语义完全不同：**401 = 你是谁不知道；403 = 知道你是谁，但你不能做这件事**。

所以 403 分支只走普通错误提示（或静默），绝不清 token。工作台各分区把 403 渲染成
"权限不足，无法查看该数据"，而不是把用户踢回登录页。

### 4.4 登录失败不触发全局退出

登录请求带 `skipUnauthorizedHandling: true`：

```ts
export function adminLogin(payload) {
  return http.post("/admin/login", { data: payload },
    { silent: true, skipAuth: true, skipUnauthorizedHandling: true });
}
```

原因：真实后端可能用 HTTP 401 表示"账号密码错误"。若不隔离，用户输错密码会被当成
"已登录会话失效"，触发一次莫名其妙的清理和跳转。

### 4.5 错误提示不重复

- 业务错误（HTTP 200 + `code !== 0`）在**响应成功拦截器**中 `return Promise.reject(businessError)`。
  axios 的链式拦截器不会让同一拦截器的 rejected 回调再处理这个 rejection，因此只提示一次。
- HTTP 错误在 rejected 回调中提示一次。
- `silent: true` 的请求（登录、工作台）不触发全局提示，由界面自行渲染错误态。

---

## 5. 后端响应 envelope 的解析机制

### 5.1 结构

```ts
interface ApiEnvelope<T> { code: number; message: string; data: T }
```

成功判定：`code === 0`（**不是** HTTP 200）。

### 5.2 拦截器中的处理

```ts
const payload = response.data;
if (!isApiEnvelope(payload)) return payload;        // 非 envelope 响应原样返回
if (payload.code === API_SUCCESS_CODE) return payload.data;   // 解包，只给业务 data
throw new BusinessError(payload.code, payload.message);       // 保留 code + message
```

三个设计点：

1. **保留非 envelope 兼容**：文件流、纯文本、第三方静态 JSON（如 `platform-config.json` 走的
   是裸 axios，不经此实例）不应被误判为业务错误。
2. **解包位置集中在拦截器**：业务层 `http.get<T>()` 的 `T` 直接是 `data` 的类型，
   不需要到处写 `res.data.data`。
3. **BusinessError 携带 `code` 与 `serverMessage`**：界面既能展示后端文案，也能按业务码分支处理。

---

## 6. RBAC 与前端权限映射的关系

### 6.1 后端授权 vs 前端判断

- 后端：`RequirePermission("dashboard:view")` 中间件，是**唯一的安全边界**。
- 前端：`hasPermission()` 只决定"要不要展示入口/区块"，绕过它最多看到空白区域，
  数据仍然拿不到（后端 403）。

这条边界必须写进文档，否则后续容易把前端判断当成授权手段。

### 6.2 三级优先级

```
is_super === true            → 全部权限（["*"]）
permissions 字段存在（含空数组）→ 以它为权威，不再查角色映射
permissions === null         → 按 roles 查 ROLE_PERMISSION_MAP
```

`permissions === null` 与 `[]` 的区别很重要：

- `null`：后端根本没提供该字段（当前现状）→ 必须回退到角色映射，否则所有人都没权限。
- `[]`：后端明确表示"此人无任何权限"→ 不能回退，否则会越权。

### 6.3 当前映射

```ts
export const ROLE_PERMISSION_MAP = {
  super_admin: ["dashboard:view"]
};
```

只登记后端已经明确的角色。**未知角色返回空集合**（白名单语义），不推测权限关系。

### 6.4 为什么用"快照"而不是在 `hasPermission` 里读 store

`utils/permission.ts` 保持零依赖（不 import store、不 import router），由 store 在
`SET_ADMIN_PROFILE` / `RESET_AUTH_STATE` 时调用 `setPermissionSubject()` 同步一份快照。

原因：`store → router → router/utils → store` 是既有的循环依赖链，再让 permission 反向依赖 store
会加剧 TDZ 风险（A0 曾因 `router/utils ↔ router/index` 循环在测试中报
`Cannot access '__vite_ssr_import_4__' before initialization`）。

零依赖 + 快照的代价是"两份数据"，因此同步点必须收敛到 store 的两个 action 里。

### 6.5 工作台为什么不做页面级权限拦截

任务要求：工作台导航允许**所有已认证管理员**访问，实际数据由后端 `dashboard:view` 控制。
因此路由 `meta` 上不加 `roles`/`auths`，进入页面后由各分区根据 403 展示"权限不足"。
这比"进不去页面"更符合运营场景（能看到页面结构，只是没数据）。

---

## 7. ECharts 数据映射与工作台调用链

### 7.1 调用链

```
views/dashboard/index.vue
  ├─ MetricCards.vue        → getDashboardOverview()
  ├─ OrderTrendPanel.vue    → getDashboardOrderTrend()   → buildOrderTrendOption()
  ├─ OrderStatusPanel.vue   → getDashboardOrderStatus({start_time,end_time}) → buildOrderStatusOption()
  ├─ TopProductsPanel.vue   → getDashboardTopProducts(10)
  └─ FlashSalePanel.vue     → getDashboardFlashSales()
```

每个面板：

```
useAsyncSection(loader) → { data, loading, error, loaded, load }
  ├─ onMounted(load)
  └─ defineExpose({ reload })  ← 供主页面"刷新全部"调用
```

### 7.2 为什么要抽"配置构建纯函数"

ECharts 渲染依赖 DOM 与真实 canvas，jsdom 里既慢又容易产生噪音失败。
真正值得测的是"接口数据 → 图表配置"的映射：

```ts
buildOrderTrendOption(days) → { xAxis.data: [...日期], series[0].data: [...订单数] }
buildOrderStatusOption(items) → { series[0].data: [{name:"已支付", value:140}, ...] }
```

因此 `views/dashboard/utils/chart-options.ts` 只依赖 DTO 类型与状态映射，可直接断言。

### 7.3 图表实例管理

`ChartPanel.vue` 使用 `@pureadmin/utils` 的 `useECharts(chartRef)`：

- 渲染前 `clear()` 再 `setOptions(option)`，避免切换数据源时残留旧系列。
- `v-show="canRender"` 而非 `v-if`：容器必须始终存在，ECharts 才能正确测量尺寸；
  错误/空数据时隐藏而不是销毁实例。
- 只在 `!loading && !error && !empty` 时渲染。

---

## 8. 五个 Dashboard API 的实际实现

| 函数 | 路径 | 查询参数 | 关键字段 |
|---|---|---|---|
| `getDashboardOverview()` | `/admin/dashboard/overview` | — | `user_total` `today_new_users` `product_total` `order_total` `today_new_orders` `today_order_amount`(分) |
| `getDashboardOrderTrend()` | `/admin/dashboard/orders/trend` | — | `days[].date` / `days[].order_count` |
| `getDashboardOrderStatus(query)` | `/admin/dashboard/orders/status` | `start_time` `end_time`（RFC3339，必须同传） | `total`、`status_distribution[].status/count` |
| `getDashboardTopProducts(limit)` | `/admin/dashboard/products/top` | `limit`（默认 10，上限 50） | `items[].rank/product_id/product_name/sales` |
| `getDashboardFlashSales()` | `/admin/dashboard/flash-sales` | — | `activity_total`、`activity_status{enabled,disabled}`、`request_status{queued,success,failed,dead}`、`current_queued`、`success_orders` |

全部使用 `silent: true`，错误由分区自行渲染。

### 8.1 金额口径

`today_order_amount` 单位是**分**，标题固定为「今日已支付订单金额」。

- 转换：`formatCentsAsYuan(2153650) === "21,536.50"`（保留两位小数、带千分位）。
- **不能**描述成"退款后实收"：后端口径是"已支付订单金额"，退款不在此字段扣减。

### 8.2 订单状态整数枚举

后端仓库内的真实定义（不是猜测）：

- `internal/logic/order/order.go`：`statusPendingPayment=10`、`statusPaid=20`、`statusShipped=30`、
  `statusReceived=40`、`statusCompleted=50`、`statusCancelled=60`、`statusRefunded=70`。
- `internal/logic/dashboard/dashboard.go`：`allOrderStatuses = {10,20,30,40,50,60,70}`；
  金额/销量口径只统计 `{20,30,40,50}`。

前端 `src/constants/order-status.ts` 按上述定义建立映射；未登记的状态码显示为
`状态 <code>`，不做语义猜测。

### 8.3 时间窗

`getDefaultDashboardStatusQuery()` 生成最近 7 天（左闭右开 `[今天-6天 00:00, 明天 00:00)`），
格式为带本地偏移的 RFC3339，例如 `2026-11-04T00:00:00+08:00`。

已知限制：以**浏览器本地时区**计算，若与后端业务时区（`dashboard.timezone`，默认
`Asia/Shanghai`）不一致，边界会有偏差（见第 12 节）。

---

## 9. 核心技术决策与取舍

| 决策 | 选择 | 备选与放弃原因 |
|---|---|---|
| Token 存储 | `sessionStorage` | Cookie：任务明确禁止；localStorage：共享设备长期残留风险高 |
| 身份恢复 | 每次刷新调 `/admin/me` | 缓存身份到 localStorage：可被篡改，且与后端状态可能不一致 |
| 401 去重 | 模块级 Promise + 500ms 复位窗口 | 布尔标志：并发下可能漏处理或重复处理；无限期标志：后续失效不再处理 |
| 403 处理 | 保留登录态 + 分区渲染"权限不足" | 全局踢出：会把"无权限"误当"未登录" |
| 登录 401 | `skipUnauthorizedHandling` | 在 handler 里判断当前路由：脆弱，依赖路由时序 |
| 权限判断 | 零依赖纯函数 + store 同步快照 | 直接读 store：加剧既有循环依赖 |
| 守卫逻辑 | 抽 `resolveAuthGuard()` 返回决策 | 内联在 beforeEach：只能通过驱动真实导航来测 |
| 图表测试 | 抽 option 构建纯函数 | 挂载组件 + canvas：慢且脆 |
| Dashboard 加载 | 每分区独立 section + 页面级 `Promise.allSettled` | `Promise.all`：一个失败全盘皆输 |
| DTO | `Provisional*` 前缀 + 集中文件 | 直接用 `any`：失去字段契约；自己造 OpenAPI：越界 |

---

## 10. 实际遇到的问题与排查

### 10.1 测试用例间 store 状态互相污染

现象：连续用例中 `profileLoaded` 断言时真时假；"登录失败"用例断言 `profileLoaded === false` 失败。

根因：`useUserStoreHook()` 内部是 `useUserStore(store)`，`store` 是应用级 pinia 实例，
**不受 `setActivePinia()` 影响**。因此 `beforeEach` 里新建 pinia 并不能重置状态。

解决：在 `beforeEach` 显式重置：

```ts
setActivePinia(createPinia());
__resetProfileRequestCache();
useUserStoreHook().RESET_AUTH_STATE();
```

顺带暴露了真实语义：测试里"新建 pinia"不等于"重置应用状态"。

### 10.2 401 处理器在用例间残留

现象："并发 401 只触发一次"用例通过后，后续用例的 `unauthorizedHandler` 不再被调用。

根因：`handleUnauthorized()` 结束后保留 500ms 才复位 `unauthorizedTask`，
而测试间隔远小于 500ms。

解决：`setUnauthorizedHandler()` 里同时把 `unauthorizedTask = null`。
语义上也更合理——更换处理器意味着需要重新接管下一次 401。

### 10.3 过期 token 没有被清理

现象：守卫用例断言"过期后 token 为 null"失败，实际仍残留 `"expired"`。

根因：`resolveAuthGuard` 的 `!hasLocalToken()` 分支直接返回 redirect，没有清理。

解决：该分支内先判断 `getAccessToken()` 是否存在（存在即说明是"过期"而非"从未登录"），
再调用 `clearLocalSession()`。

### 10.4 ECharts `setOptions` 第二参数类型不符

现象：`setOptions(option, true)` 报 `Argument of type 'boolean' is not assignable to parameter of type 'OptionsParams'`。

根因：`@pureadmin/utils` 的 `setOptions(options, ...params: OptionsParams[])` 中
`OptionsParams` 是**事件绑定参数**（`{name, callback}`），不是 echarts 的 `notMerge`。

解决：改为 `clear()` + `setOptions(option)`，语义更明确（整体替换）。

### 10.5 `useECharts` 的 ref 类型不匹配

现象：`Argument of type 'Ref<HTMLDivElement | undefined>' is not assignable to 'ElementRef<HTMLDivElement>'`。

解决：`useECharts(chartRef as unknown as ElementRef<HTMLDivElement>)`，
`ElementRef` 从 `@pureadmin/utils` 导入。

### 10.6 axios 自定义配置字段在 error 上读不到

现象：`$error.config?.silent` / `skipUnauthorizedHandling` 报"属性不存在"。

根因：`error.config` 的静态类型是 `InternalAxiosRequestConfig`，不含自定义字段。

解决：显式断言 `const requestConfig = $error.config as PureHttpRequestConfig | undefined`，
集中在一处读取，避免每处都写 as any。

### 10.7 MSW 在 Vitest（jsdom）下的可行性

结论：**可行**。MSW v3 的 `setupServer` 会拦截 jsdom 的 `XMLHttpRequest`，
而 axios 在 jsdom 下默认使用 xhr adapter，因此 `http.get("/admin/me")` 能被 handler 捕获。

配套要求：

- `tests/setup.ts` 里 `server.listen({ onUnhandledRequest: "error" })`，未匹配请求直接失败，
  防止测试偷偷打到真实网络。
- 测试与浏览器共用 `src/mocks/handlers`，保证夹具一致。

### 10.8 路由模块在测试中的 i18n 依赖

现象（沿用 A0 结论）：导入 `@/router` 会拉起 `@/plugins/i18n`，而 i18n 插件依赖 YAML 资源，
Vitest 未配置该插件时解析失败。

解决：测试文件顶部 `vi.mock("@/plugins/i18n", ...)` 提供最小 `$t/transformI18n`。

### 10.9 登出/清缓存路径遗漏认证状态

- `useNav.logout()` 直接调用异步 `logOut()` 未处理返回值 → 改为 `void logOut()`，
  并在 `logOut` 内对 `router.replace` 加 `.catch()`。
- 设置面板"清空缓存"（`useDataThemeChange.onReset`）原本只清 token 与 localStorage，
  不重置 Pinia → 刷新后守卫会因 `profileLoaded === true` 跳过 `/admin/me` 校验。
  已补 `useUserStoreHook().RESET_AUTH_STATE()`。

---

## 11. 测试覆盖与未验证事项

### 11.1 实际执行结果

| 命令 | 结果 |
|---|---|
| `pnpm typecheck` | 通过（0 error，`strict: true`） |
| `pnpm lint:eslint` | 通过（`--max-warnings 0`） |
| `pnpm test` | **7 个测试文件 / 70 个用例全部通过** |
| `pnpm build` | 成功，`✓ built in 4.24s`，产物约 3.07 MB |

### 11.2 测试文件与覆盖点

| 文件 | 用例 | 覆盖 |
|---|---|---|
| `tests/unit/admin-auth.test.ts` | 10 | 登录成功/失败、token 保存、注入 Header、过期判定、`ensureAdminProfile` 恢复与并发合并、登出清理、失效 token 登出 |
| `tests/unit/http-auth.test.ts` | 9 | 401 触发处理、并发 401 去重、403 不清登录态、登录 401 不触发全局退出、业务错误保留 code/message、静默模式、envelope 解包 |
| `tests/unit/auth-guard.test.ts` | 9 | 未登录跳转、根路径不带 redirect、已登录跳工作台、服务端拒绝清态、过期 token 不发请求、并发只请求一次、401 跳登录 |
| `tests/unit/permission.test.ts` | 12 | 超管全通过、映射角色、未知角色拒绝、`permissions` 权威性（含空数组）、`null` 回退、some/every、快照同步与隔离 |
| `tests/unit/dashboard.test.ts` | 17 | 五个 API 解析、RFC3339 时间窗、金额分转元、千分位、状态映射与未知回退、图表 option 映射、403 隔离、空数据、业务错误、网络异常、重试恢复 |
| `tests/unit/http.test.ts`（A0） | 5 | 实例基础配置与错误归一化（回归） |
| `tests/unit/router.test.ts`（A0） | 6 | 路由表与 history base（回归） |

### 11.3 明确未验证的部分

| 项 | 状态 | 说明 |
|---|---|---|
| 与真实 Go 后端联调 | **NOT_VERIFIED** | 本机未启动后端（`:8000` 无进程），全部接口经 MSW 验证 |
| 浏览器内真实交互（点击登录、图表渲染） | **NOT_VERIFIED** | 环境无 headless 浏览器，未做端到端 UI 验证 |
| MSW 在浏览器 DevTools 中的实际拦截 | **NOT_VERIFIED** | 仅验证了 `mockServiceWorker.js` 可访问（HTTP 200）与单测中的 node 拦截 |
| OpenAPI 生成类型替换 | **NOT_VERIFIED** | `contracts/openapi.yaml` 不存在 |
| 生产网关 `/api` 反代 | **NOT_IMPLEMENTED** | 未修改 Nginx/部署配置 |

因此不能声称"已完成端到端联调"或"浏览器端已验证"。

---

## 12. 后续维护需要注意的设计约束

1. **`permissions` 语义不能改**：`null` = 后端未提供（回退角色映射）；`[]` = 明确无权限（不回退）。
   改这个语义会直接造成越权或全员失权。
2. **401 与 403 必须保持区分**：401 清登录态，403 只提示。任何"403 也踢出"的改动都是错的。
3. **登录接口必须保留 `skipUnauthorizedHandling`**，否则密码错误会被当成会话失效。
4. **`ensureAdminProfile` 是唯一的身份恢复入口**：新增入口会绕过并发合并，产生重复请求与竞态。
5. **权限快照同步点只有两处**（`SET_ADMIN_PROFILE` / `RESET_AUTH_STATE`）。
   新增修改身份的 action 时必须同步调用 `setPermissionSubject`，否则权限判断会读到旧数据。
6. **DTO 替换契约时改文件不改签名**：契约冻结后把 `Provisional*` 换成生成类型，
   `api/modules/*` 的导出函数名与参数形状保持不变，页面无需改动。
7. **工作台分区不要合并成一个 loading**：否则单接口失败会拖垮整页，违背 6.7 的要求。
8. **金额字段永远按"分"处理**：新增金额字段时先确认后端单位，再决定是否走 `centsToYuan`。
9. **订单状态码以 `src/constants/order-status.ts` 为唯一来源**：不要在组件里硬编码 `10/20/30`。
10. **时间窗时区**：当前以浏览器本地时区计算 7 天窗口；若需严格对齐业务时区，
    应改为从后端获取时区或增加 `VITE_BUSINESS_TIMEZONE` 配置（未实现）。

---

## 13. 值得后续学习与面试复习的知识点

> 以下为"应掌握清单"，不代表已经掌握。建议按"问题 → 打开对应文件 → 讲清因果"自测。

### 13.1 知识要点

| 主题 | 核心结论 | 对应文件 |
|---|---|---|
| Token 存储选型 | sessionStorage 的失效边界随标签页；不落密码/Refresh Token | `utils/auth.ts` |
| 身份可信来源 | 只信 `/admin/me`，不信 JWT payload | `store/modules/user.ts` |
| 会话恢复 | 缓存 + 并发合并 + 时效预检，三者缺一不可 | `ensureAdminProfile()` |
| 401/403 语义 | 401 未认证（清态）、403 已认证但无权（不清态） | `utils/http/index.ts` |
| envelope | HTTP 200 ≠ 业务成功；`code === 0` 才是成功 | `api/types/envelope.ts` |
| 拦截器错误传播 | 成功拦截器抛错不会再进同拦截器的 error 分支，故只提示一次 | `utils/http/index.ts` |
| 权限模型 | `is_super` > 后端 permissions > 角色映射；未知角色拒绝 | `utils/permission.ts` |
| 循环依赖 | 零依赖模块 + 快照注入，避免 `store ↔ router` 环 | `utils/permission.ts`、`router/guard.ts` |
| 守卫可测性 | 决策与导航分离，决策返回纯数据 | `router/guard.ts` |
| 图表测试策略 | 测 option 纯函数，不测 canvas | `views/dashboard/utils/chart-options.ts` |
| 局部失败隔离 | 每分区自有状态 + `Promise.allSettled` | `useAsyncSection.ts`、`views/dashboard/index.vue` |
| MSW 测试 | node 侧 `setupServer` 拦截 xhr，与浏览器共用 handlers | `tests/setup.ts` |

### 13.2 面试问题（结合 SurgeCart 作答）

1. **为什么 token 放 sessionStorage 而不是 localStorage/Cookie？各自的风险是什么？**
   → `utils/auth.ts`、任务边界（禁用 Cookie）。
2. **页面刷新后如何恢复登录态？为什么不能把用户信息缓存在本地直接信任？**
   → `ensureAdminProfile()`。
3. **多个并发请求同时返回 401，如何保证只清理一次、只跳转一次？**
   → `handleUnauthorized()` 的 Promise 去重 + 500ms 复位。
4. **401 和 403 的处理差异体现在哪几行代码？为什么 403 不能清 token？**
   → `utils/http/index.ts` 错误分支。
5. **HTTP 200 但 `code !== 0`，你的请求层如何让业务层感知？**
   → `BusinessError` + 成功拦截器抛错。
6. **为什么登录失败不能走全局 401 处理？怎么实现隔离？**
   → `skipUnauthorizedHandling`。
7. **已登录用户访问 `/login` 时，直接跳 `/dashboard` 会有什么问题？**
   → 失效 token 导致重定向循环；guard 先校验。
8. **`permissions` 为 `null` 和为 `[]` 的区别，为什么要区分？**
   → `resolvePermissions()`。
9. **前端权限判断能当安全边界吗？绕过它会怎样？**
   → 后端 `RequirePermission`、403 渲染。
10. **为什么把守卫逻辑抽成函数？它返回什么？怎么测？**
    → `router/guard.ts`、`auth-guard.test.ts`。
11. **ECharts 组件怎么测？为什么不挂载组件？**
    → `chart-options.ts`、`dashboard.test.ts`。
12. **`Promise.all` 与 `Promise.allSettled` 在工作台场景的差别？**
    → `views/dashboard/index.vue`。
13. **如果后端明天交付 OpenAPI，A1 的哪些文件要改、哪些不用改？**
    → `api/modules/*` 的 DTO 替换；页面与 store 不动。
14. **`today_order_amount` 为什么不能叫"收入"或"实收"？**
    → 后端口径：已支付订单金额，退款不扣减。

### 13.3 自检清单

- [ ] 能画出"登录 → 存 token → 拉 me → 进工作台"的完整时序，并指出每步失败后的清理动作。
- [ ] 能解释为什么"过期 token 存在但不清理"会导致守卫反复跳转。
- [ ] 能说明测试中 `setActivePinia` 为什么不足以重置 `useUserStoreHook()`。
- [ ] 能指出 401 去重窗口设计在什么情况下会漏处理下一次 401。
- [ ] 能说出工作台五个接口各自的口径（数量类 vs 金额类）。
- [ ] 能解释"权限快照"为什么要由 store 主动同步，而不是 permission 模块自己去读。
- [ ] 能区分本任务中 IMPLEMENTED / VERIFIED / NOT_VERIFIED 的边界，尤其"未联调"这件事。
