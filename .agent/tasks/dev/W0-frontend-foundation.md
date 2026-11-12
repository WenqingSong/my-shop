# W0 前端工程基础：开发知识文档

- 任务：SurgeCart 用户商城前端 W0（工程初始化与设计基线）
- 分支：`feat/phase2-user-frontend`
- 代码位置：仓库根目录 `web/`
- 对应正式报告：`docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md`
- 本文目标：解释 W0 为什么这样实现、关键机制如何运作、边界在哪里，便于后续 W1–W5 接手时不必重新逆向工程。

本文只描述本仓库中真实存在并可运行的代码，所有结论均可在对应文件中核对。

---

## 1. W0 背景与工程目标

SurgeCart 用户商城最终要交付一个可上线的 PC 电商站点。W0 不做商品、购物车、订单等业务，
而是先解决四类**会导致后续反复返工**的问题：

1. **视觉没有基线**：如果先写页面再定颜色/间距/圆角，后面会出现几十种蓝色和十几种卡片样式。
2. **请求没有收口**：如果每个组件自己 `fetch`，错误处理、超时、Token、加载状态会各写一套。
3. **状态职责混乱**：把后端数据复制进客户端 store，会出现两套"真相"，刷新与失效无法收敛。
4. **没有测试与 Mock 地基**：没有契约前无法联调，测试也无法稳定运行。

因此 W0 的目标是建立可复用的工程底座：

- 可启动、可构建、类型严格的 React 工程；
- 统一设计 Token 与公共布局；
- 统一 API Client 与状态管理边界；
- Vitest / Testing Library / MSW / Playwright 四件套可运行；
- 明确"不做什么"：不猜后端接口、不提前写业务页面、不伪造数据契约。

验收标准不是"页面多"，而是 `lint / typecheck / test / build / test:e2e` 全部真实通过，
并且修改范围只在 `web/**`。

---

## 2. 最终技术栈及版本选择

实际安装版本（取自 `node_modules/*/package.json`，锁定于 `web/package-lock.json`）：

| 领域 | 包 | 版本 |
| --- | --- | --- |
| 视图 | react / react-dom | 19.3.1 |
| 语言 | typescript | 5.9.3（`~5.9.0`） |
| 构建 | vite / @vitejs/plugin-react | 8.3.7 / 6.3.0 |
| 路由 | react-router | 8.5.1 |
| 服务端状态 | @tanstack/react-query | 5.107.1 |
| 客户端状态 | zustand | 5.1.0 |
| 样式 | tailwindcss / @tailwindcss/vite | 4.3.5 / 4.3.5 |
| UI 原语 | @radix-ui/react-dialog / react-slot | 1.3.1 / 1.5.0 |
| 样式工具 | class-variance-authority / clsx / tailwind-merge | 0.7.1 / 2.1.1 / 3.7.0 |
| 图标 | lucide-react | 1.70.0 |
| 表单 | react-hook-form / zod / @hookform/resolvers | 7.91.0 / 4.6.7 / 7.0.5 |
| 单元测试 | vitest / jsdom | 5.1.0 / 31.0.0 |
| 组件测试 | @testing-library/react / jest-dom / user-event | 16.3.3 / 7.0.1 / 14.6.8 |
| API Mock | msw | 3.0.4 |
| E2E | @playwright/test | 1.65.0 |
| 契约 | openapi-typescript | 7.13.0 |
| 静态检查 | eslint / typescript-eslint | 10.14.0 / 8.72.2 |

### 版本选择的三个真实约束

1. **TypeScript 没有跟随 latest。** npm 上最新是 7.0.2，但 `typescript-eslint@8.72.2` 的
   peer 范围是 `>=4.8.4 <6.1.0`。安装时 npm 直接给出 ERESOLVE 冲突。最终固定 `~5.9.0`，
   没有使用 `--force` 或 `--legacy-peer-deps` 掩盖不兼容。
2. **Tailwind 4 采用 CSS-first 配置。** 没有 `tailwind.config.js`，主题写在
   `src/styles/globals.css` 的 `:root` 与 `@theme inline` 中，构建插件是 `@tailwindcss/vite`。
3. **shadcn/ui 不是运行时依赖。** 它是一套"把组件源码复制进项目"的约定：
   `components.json` 描述别名与 CSS 入口，`cn()` 合并类名，Radix 提供无样式行为，
   Tailwind + cva 提供外观。因此依赖里看不到 `shadcn` 包。

---

## 3. 工程目录与模块职责

```text
web/
├── src/
│   ├── api/            client.ts（请求/错误/超时/Token）、query-client.ts（缓存默认值）
│   ├── components/
│   │   ├── common/     与业务无关的展示组件：容器、金额、空/错/加载、分页、页面占位
│   │   ├── layout/     Header、Footer、Logo、搜索框、移动导航
│   │   └── ui/         shadcn 风格基础件：Button、Input、Dialog
│   ├── hooks/          useDocumentTitle
│   ├── layouts/        RootLayout（跳过导航 + Header + main + Footer）
│   ├── mocks/          MSW 的 handlers / browser worker / node server
│   ├── pages/          首页基线、404、首页各展示区块
│   ├── providers/      AppProviders（QueryClientProvider）
│   ├── router/         路由表与 browser router
│   ├── stores/         Zustand 的 UI 状态
│   ├── styles/         设计 Token、基础样式、Dialog 动效
│   ├── test/           Vitest setup（jest-dom + MSW 生命周期）
│   ├── utils/          cn、整数分格式化、分页序列计算
│   ├── App.tsx         组合 Provider 与 Router
│   └── main.tsx        挂载入口、按需启动 MSW
├── tests/e2e/          Playwright 用例
├── DESIGN_SYSTEM.md    设计规范（与 CSS Token 对应）
├── components.json     shadcn 配置
├── vite.config.ts      Vite + Vitest 配置
└── playwright.config.ts
```

**刻意不创建的目录**：`src/features/`、`src/types/`、`src/api/modules/`、`src/api/generated/`。
目前没有真实内容，先建空目录会制造"看起来有架构、实际为空"的假象。
W1 接入契约后再按域建立，避免无意义抽象。

---

## 4. React 应用启动过程

调用链：

```text
index.html (#root)
  └─ <script type="module" src="/src/main.tsx">
       └─ main.tsx: bootstrap()
            ├─ startMocking()            // 仅 VITE_ENABLE_MOCK=true
            ├─ document.getElementById('root')
            └─ createRoot(...).render(<StrictMode><App /></StrictMode>)
                 └─ App
                      └─ AppProviders (QueryClientProvider)
                           └─ RouterProvider(router)
```

关键点：

- `src/main.tsx:11` 的 `startMocking()` 在挂载前 `await`。MSW 的 Service Worker 必须先就绪，
  否则首屏请求会漏过拦截。未开启 Mock 时函数立即返回，不产生额外请求。
- `src/main.tsx:20` 的 `bootstrap()` 先取 `#root`，取不到直接抛错，避免 `createRoot(null!)` 的
  运行时崩溃。
- `src/main.tsx:29` 使用 `createRoot(...).render`，没有使用已废弃的 `ReactDOM.render`。
- `src/main.tsx:30` 的 `StrictMode` 只在开发环境重复执行渲染与 Effect，用于暴露副作用问题；
  Vitest 中渲染 `AppProviders` 时不加 StrictMode，避免同一断言被执行两次。
- 全局样式只在 `main.tsx` 引入一次：`import '@/styles/globals.css'`。组件测试不引入 CSS，
  因此 jsdom 不做样式计算，测试关注结构与可访问性而非像素。

`App.tsx` 本身非常薄（`src/App.tsx:6-12`）：只负责把 Provider 与 Router 组合起来。
这样测试可以绕过 `App`，直接用 `AppProviders` + `createMemoryRouter` 复用同一套路由表。

---

## 5. React Router 路由机制

实现：`src/router/index.tsx`

```ts
export const routes: RouteObject[] = [
  {
    path: '/',
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]

export const router = createBrowserRouter(routes)
```

设计与原理：

1. **路由表与实例分离。** `routes` 是纯配置（`src/router/index.tsx:11`），`router` 是浏览器实例
   （`:22`）。浏览器实例依赖 `window.history`，无法在 jsdom/Node 中直接创建；
   测试改用 `createMemoryRouter(routes)`（`src/__tests__/app.test.tsx:9-17`），
   从而保证"测试的路由"和"线上的路由"是同一份配置，不会漂移。
2. **布局路由。** `/` 的 element 是 `RootLayout`，子路由通过 `<Outlet />` 渲染
   （`src/layouts/root-layout.tsx:17`）。Header/Footer 因此只挂载一次，页面切换不重建。
3. **404 用通配子路由。** `path: '*'` 放在 `/` 的 children 里，未匹配路径仍会命中 RootLayout，
   所以 404 页面保留完整 Header/Footer，而不是白屏。
4. **数据路由（Data Router）。** 使用 `createBrowserRouter` 而非 `<BrowserRouter>`，
   为后续 W1 的 `loader`、`action`、`errorElement` 留出能力；当前只用最基础的 element 渲染。
5. **导航与激活态。** Header 用 `NavLink` 的 `isActive` 回调（`site-header.tsx` 的 `NavItem`），
   并设置 `end`，避免 `/` 在后续子路径中一直高亮。
6. **部署前提。** SPA 使用 History API，服务器必须把未知路径回退到 `index.html`
   （Nginx `try_files`）。本任务未修改 Nginx，部署接入时需确认；否则直接访问 `/xxx` 会 404。

已知缺口：当前路由没有配置 `errorElement`。若某个页面渲染抛错，会落到 React Router 默认错误页。
W1 增加业务页面时建议补统一的错误边界组件。

---

## 6. TanStack Query 与 Zustand 的职责边界

### TanStack Query：服务端状态的唯一权威

`src/api/query-client.ts:7-23`：

```ts
staleTime: 30_000,        // 30s 内认为数据新鲜，不重复请求
gcTime: 5 * 60_000,       // 缓存 5 分钟后回收
retry: 1,                 // 失败自动重试一次
refetchOnWindowFocus: false,
```

取值理由：

- 商城列表/详情在 30 秒内重复进入很常见，`staleTime` 减少无意义请求；
- 失败重试 1 次可以覆盖瞬时网络抖动，又不至于在真实故障时放大流量；
- 窗口聚焦自动刷新会造成列表闪动，商城场景默认关闭，改为显式失效。

`AppProviders`（`src/providers/app-providers.tsx:12`）通过 props 允许注入 `QueryClient`。
测试里每次 `createQueryClient()`（`src/__tests__/app.test.tsx:13`），避免用例间共享缓存。

W0 尚未有真实查询，因此没有 `queryKey`、`useQuery` 调用；W1 的约定是：
请求函数放 `src/api/modules/`，key 统一在 `src/api/query-keys.ts` 生成，
页面只通过 `useQuery` / `useMutation` 读写，不直接调用 `apiClient`。

### Zustand：只放轻量客户端状态

`src/stores/ui-store.ts:15`：

```ts
export const useUiStore = create<UiState>()((set) => ({
  isMobileNavOpen: false,
  setMobileNavOpen: (open) => set({ isMobileNavOpen: open }),
  toggleMobileNav: () => set((state) => ({ isMobileNavOpen: !state.isMobileNavOpen })),
  closeMobileNav: () => set({ isMobileNavOpen: false }),
}))
```

唯一职责是移动端导航抽屉开关。组件通过选择器订阅（`mobile-nav.tsx:19-20`），
只有该字段变化才重渲染，避免整仓状态变化导致 Header 抖动。

**边界规则**：商品、库存、订单等后端数据禁止进入 Zustand。否则会出现
"Query 缓存一份、Store 一份"，刷新/失效时无法保证一致。购物车若未来需要本地草稿，
只能存"UI 偏好/临时选择"，最终金额与库存仍以服务端返回为准。

---

## 7. Tailwind CSS 4 主题机制

实现：`src/styles/globals.css`

```css
@import 'tailwindcss';

:root {
  --brand-600: #1e5ae8;
  --primary: var(--brand-600);
  --price: #d92d3a;
  /* ... */
}

@theme inline {
  --color-primary: var(--primary);
  --color-price: var(--price);
  --shadow-card: var(--shadow-card);
  --font-sans: 'PingFang SC', ...;
}
```

原理：

1. **CSS-first 配置。** Tailwind 4 不再要求 JS 配置文件。`@import 'tailwindcss'` 载入框架，
   构建时扫描 `src` 下的类名并生成对应 CSS。
2. **三层 Token。** `原始色板（brand-* / gray-*） → 语义 Token（primary/border/price） →
   @theme 映射（--color-*）`。组件只写 `bg-primary`、`text-price`，不写十六进制值。
   换品牌色时只改 `:root` 一处。
3. **`@theme inline` 的作用。** 它告诉 Tailwind 不要在生成的 `:root` 里再复制一份变量，
   而是把值直接内联到工具类中。由于值是 `var(--primary)`，运行时仍然读 CSS 变量，
   既支持运行时主题切换，又避免多一层变量间接。
4. **透明度修饰符。** 例如 `bg-primary/10`，Tailwind 4 用 `color-mix()` 计算，
   对十六进制与 `var()` 都成立，所以语义色可以直接加 `/10`、`/25`。
5. **基础层与组件层。** `@layer base`（`:145`）设置全局边框色、body 背景/字体、
   `::selection`、`:focus-visible` 与 `prefers-reduced-motion` 降级；
   `@layer components`（`:207`）只放 Dialog 的遮罩/内容动效类。
6. **为什么不用 `dark:`。** 当前没有暗色 Token，若直接使用 `dark:` 会出现未定义颜色。
   README 与 DESIGN_SYSTEM 都明确：先补 Token 再启用暗色，避免半成品。

新增 Token 的固定流程：改 `:root` → 在 `@theme inline` 映射 → 组件使用语义类 →
同步 `DESIGN_SYSTEM.md`。四步缺一不可。

---

## 8. shadcn/ui 与 Radix 的组合方式

`components.json` 定义别名：

```json
{
  "tailwind": { "config": "", "css": "src/styles/globals.css", "cssVariables": true },
  "aliases": { "components": "@/components", "ui": "@/components/ui", "utils": "@/utils/cn" }
}
```

组合关系：

- **Radix** 提供行为与可访问性：`@radix-ui/react-dialog` 负责 Portal、遮罩、焦点陷阱、
  Esc 关闭、`aria-labelledby` / `aria-describedby` 关联；`@radix-ui/react-slot` 负责 `asChild`。
- **cva** 负责变体：`src/components/ui/button-variants.ts` 用 `cva` 描述 variant/size，
  生成类名字符串，组件只负责组合。
- **cn** 负责合并：`src/utils/cn.ts` 先 `clsx` 处理条件类名，再 `tailwind-merge` 消解冲突
  （例如外部传入 `px-6` 覆盖默认 `px-4`）。
- **Dialog 封装**（`src/components/ui/dialog.tsx:22`）：`DialogContent` 内部渲染
  `Portal → Overlay → Content`；内容用 `left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2` 居中。
  动效只做透明度（`globals.css:207-232`），因为 `transform` 已被用于定位，
  若动画再写 `transform` 会覆盖居中位移，导致弹层从左上角飞入。

`asChild` 的意义：把样式与行为"传给子元素"，而不是多包一层 `<button>`。
例如首页 CTA 用 `<Button asChild><a href="#design-baseline">…</a></Button>`，
最终 DOM 是 `<a class="...按钮样式">`，语义正确且可被键盘/爬虫识别为链接。

---

## 9. API Client 的设计、错误处理与请求流程

实现：`src/api/client.ts`

### 结构

- `ApiError`（`:17`）：统一错误类型，字段 `kind`、`status`、`code`、`details`、`cause`，
  并提供 `isUnauthorized` / `isNotFound`。
- `createApiClient(config)`（`:126`）：工厂函数，接受 `baseUrl`、`timeoutMs`、`getToken`、
  `fetchImpl`。工厂模式让测试可以注入独立实例与假 `fetch`，不需要改全局。
- `resolveUrl`（`:77`）：拼接 base 与 path，序列化 query；跳过 `null/undefined`，
  数组用 `append` 生成重复 key。
- `readErrorPayload`（`:97`）：只在 `content-type` 含 `json` 时解析，读取 `message` / `code` /
  `details`，解析失败则静默退回通用文案。
- `setAuthTokenProvider`（`:249`）与默认实例 `apiClient`（`:263`）。

### 一次请求的完整流程

```text
request(path, options)
 ├─ 创建 AbortController + 定时器（超时置 timedOut = true 后 abort）
 ├─ 合并外部 signal（若已 abort 立即取消，否则监听一次）
 ├─ 组装 headers：Accept / Content-Type / Authorization
 ├─ fetchImpl(resolveUrl(...), { method, headers, body: JSON.stringify(body), signal })
 ├─ !response.ok → readErrorPayload → throw ApiError(kind: 'http')
 ├─ 204 → undefined
 ├─ content-type 非 JSON → 返回 text（空串返回 undefined）
 ├─ JSON 解析失败 → throw ApiError(kind: 'parse')
 └─ catch:
      ApiError 原样抛出
      timedOut → kind: 'timeout'
      signal 已取消 → kind: 'aborted'
      其他 → kind: 'network'
    finally: clearTimeout + 移除 abort 监听
```

### 为什么错误要分 kind

调用方需要不同反应：

- `timeout` / `network`：可重试，展示"网络异常"并给重试按钮；
- `http 401`：跳转登录（认证接入后）；
- `http 404`：展示空/不存在，不重试；
- `parse`：说明契约或网关异常，属于需要上报的 bug。

如果只抛 `Error('请求失败')`，页面无法区分这四种情况，最终会写出"所有错误都弹同一个 toast"的体验。

### 超时与取消的细节

`timedOut` 标记（`:144`）是区分"用户主动取消"和"超时"的关键：
两者最终都会让 `controller.signal.aborted === true`，只看 aborted 无法区分。
此外，外部 `AbortSignal` 的监听在 `finally` 中移除，避免长列表反复请求造成监听泄漏。

### 当前不包含

- 不封装任何业务接口；
- 不做重试退避（交给 TanStack Query 的 `retry`）；
- 不处理 `FormData`（当前统一 JSON，上传场景后续单独设计）。

---

## 10. Token Provider 扩展点的实际作用

```ts
type TokenProvider = () => string | null | undefined

let authTokenProvider: TokenProvider | null = null

export function setAuthTokenProvider(provider: TokenProvider | null): void {
  authTokenProvider = provider
}

export const apiClient = createApiClient({
  baseUrl: resolveBaseUrl(),
  timeoutMs: resolveTimeout(),
  getToken: () => authTokenProvider?.(),
})
```

作用：

- API Client 在每次请求时**实时读取** token，而不是初始化时快照。登录/刷新后不需要重建 client。
- 具体 Token 存在哪里由认证模块决定：`localStorage`、内存变量、或由 BFF 写入 HttpOnly Cookie。
  W0 不替认证做决定，因为不同方案的安全模型完全不同（XSS vs CSRF）。
- 测试可以注入 `getToken: () => 'test-token'`，验证请求头（`client.test.ts:18-45` 断言
  `authorization === 'Bearer test-token'`）。
- 如果后端最终采用 Cookie 会话，则 `setAuthTokenProvider` 可以完全不使用，
  只需要 `credentials: 'include'`；这也是不提前绑定存储策略的原因之一。

它解决的是**解耦**：请求层只依赖"能拿到 token 的函数"这一稳定接口，
认证实现可以在 W2/W3 独立演进。

---

## 11. OpenAPI 类型生成原理与当前未接入契约的原因

命令（`package.json`）：

```bash
openapi-typescript ../contracts/openapi.yaml -o src/api/generated/schema.d.ts
```

原理：

1. `openapi-typescript` 读取 OpenAPI 3.x 文档（YAML/JSON）；
2. 把 `components.schemas` 转成 TypeScript 类型，把 `paths` 转成 `paths['/xxx']['get']`
   形式的请求/响应类型；
3. 产物是**纯类型声明**（`.d.ts`），没有运行时代码，不增加 bundle 体积；
4. 业务层用 `import type { paths } from '@/api/generated/schema'` 取类型，
   再在 `src/api/modules/` 写薄封装函数。

为什么现在不生成：

- 仓库根目录**没有** `contracts/openapi.yaml`。实测执行 `npm run typegen` 报
  `ENOENT: no such file or directory ... /contracts/openapi.yaml`，且不会创建输出目录。
- 若手写一份"看起来像"的 schema，会让前端类型与后端真实响应脱节，
  比没有类型更危险——编译通过但运行时字段对不上。
- 因此 W0 只保留命令与目录约定，等后端 B0 提供正式契约后再生成。

接入后的纪律：`src/api/generated/**` 是生成物，禁止手改；ESLint 已将其忽略
（`eslint.config.js` 的 `globalIgnores`）。

---

## 12. MSW 的请求拦截机制

MSW 有两套运行形态，本仓库都配置了：

| 形态 | 文件 | 使用场景 | 拦截原理 |
| --- | --- | --- | --- |
| Node Server | `src/mocks/server.ts` | Vitest | 通过 `@mswjs/interceptors` 拦截 Node 的 `http`/`fetch` |
| Browser Worker | `src/mocks/browser.ts` | 本地开发 | 注册 Service Worker，在网络层拦截页面发出的请求 |

关键代码：

- `src/mocks/handlers.ts`：`export const handlers: RequestHandler[] = []`。
  当前**故意为空**：没有正式契约，不伪造业务接口。
- `src/mocks/server.ts`：`setupServer(...handlers)`。
- `src/test/setup.ts:8`：`server.listen({ onUnhandledFrame: 'error' })`。
  未匹配的请求直接报错，防止测试悄悄打到真实网络。
- `src/test/setup.ts:11-14`：每个用例后 `cleanup()` + `server.resetHandlers()`，
  保证 `server.use()` 的临时 handler 不污染下一个用例。
- `src/mocks/browser.ts`：`worker.start({ onUnhandledFrame: 'bypass' })`，未匹配请求放行到真实后端。
- `src/main.tsx:11-18`：只有 `VITE_ENABLE_MOCK === 'true'` 才动态 `import('@/mocks/browser')`。
  动态导入保证 Mock 代码不进入生产首屏 chunk。

测试中的用法（`src/api/client.test.ts`）：用 `/api/__test__/echo`、`/api/__test__/missing`
这类**测试专用路径**注册 handler，不模拟任何真实业务接口。

MSW 3 与 2 的差异：`onUnhandledRequest` 已改名为 `onUnhandledFrame`。这是实际踩到的
类型错误（TS2353），不是文档抄写。

---

## 13. Vitest、Testing Library、Playwright 的职责区别

| 工具 | 是什么 | 在本仓库负责 | 不负责 |
| --- | --- | --- | --- |
| Vitest | 测试运行器（复用 Vite 转译） | 执行用例、断言、Mock、jsdom 环境 | 真实浏览器布局、网络栈 |
| Testing Library | DOM 查询与交互工具库 | 按角色/文本查询、`user-event` 模拟点击输入 | 启动测试、浏览器渲染 |
| Playwright | 浏览器自动化 | 真实 Chromium、真实 dev server、导航/弹层/响应式 | 细粒度纯函数断言（成本过高） |

本仓库的分工：

- **纯函数**：`src/utils/price.test.ts`、`src/utils/pagination.test.ts` —— 直接断言输入输出，
  不需要 DOM。
- **组件行为**：`src/components/common/price-display.test.tsx`、`pagination.test.tsx` ——
  用 Testing Library 断言渲染文本、禁用态、`aria-current`、点击回调。
- **应用集成**：`src/__tests__/app.test.tsx` —— 用 `createMemoryRouter` 渲染整棵路由树，
  验证布局、首页区块、404。
- **端到端**：`tests/e2e/home.spec.ts` —— 真实浏览器里验证锚点滚动、Dialog 打开/关闭、
  分页点击、移动端抽屉。这些依赖布局与浏览器行为，jsdom 无法可靠覆盖。

Testing Library 的查询优先级（Role > Label > Text > TestId）在这里落实为：
按钮用 `getByRole('button', { name })`，导航用 `getByRole('navigation', { name: '主导航' })`，
避免依赖 class 或 DOM 结构。

---

## 14. PriceDisplay 的整数分展示机制

### 为什么用整数分

浮点数无法精确表示 0.1、0.2 这类十进制小数：

```js
0.1 + 0.2 // 0.30000000000000004
```

金额一旦用浮点累加/折扣，就会出现"1 分钱对不上"。行业做法是后端以**最小货币单位**
（人民币为分）传输整数，前端只做展示，不做金额运算。

### 格式化实现

`src/utils/price.ts:28`：

```ts
if (!Number.isFinite(cents) || !Number.isInteger(cents)) {
  throw new TypeError(`价格必须是整数分，收到：${String(cents)}`)
}

const negative = cents < 0
const absolute = Math.abs(cents)
const yuan = Math.floor(absolute / 100)
const fen = absolute % 100
const integer = integerFormatter.format(yuan)          // zh-CN 千分位
const decimal = String(fen).padStart(2, '0')          // 5 分 -> "05"
```

要点：

- 用整数除法与取模拆出"元/分"，避免 `cents / 100` 的浮点误差；
- `Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 })` 只格式化整数元部分，
  千分位规则交给运行时的 ICU 数据；
- 负数单独处理（退款/抵扣场景），输出 `-¥19.90`；
- 非法入参直接抛 `TypeError`，让错误在开发期暴露，而不是在页面上显示 `¥NaN`。

### 组件拆分

`src/components/common/price-display.tsx:38-62` 把金额拆成三段渲染：

```text
¥      1,299      .00
符号    整数部分    小数部分
小字号   主字号      小字号
```

- `inline-flex items-baseline` 保证三段基线对齐；
- `tabular-nums` 让数字等宽，列表价格右对齐时不跳动；
- `size` 控制三档（sm/md/lg），`tone` 控制价格色/中性色；
- 组件**不接收**字符串价格、不做除法、不做折扣计算。

测试：`price-display.test.tsx` 断言 `textContent === '¥1,299.00'`、零元、小额补零；
`price.test.ts` 覆盖 0 / 5 / 1990 / 129900 / 123456789 / 负数 / 非整数异常。

---

## 15. Header 响应式设计与 768px 问题的实际修复

Header 结构（`src/components/layout/site-header.tsx`）：

```text
<header sticky top-0 z-40 border-b bg-surface/95 backdrop-blur>
  ├─ 主行 h-16 lg:h-20
  │   ├─ MobileNav（lg 以下显示汉堡按钮）
  │   ├─ BrandLogo
  │   ├─ SearchBox（md 起显示，flex-1 + max-w-560）
  │   └─ 操作区（购物车 / 登录，禁用占位）
  ├─ 导航行 h-12（桌面）
  │   ├─ 全部商品分类（禁用占位）
  │   ├─ 主导航：首页
  │   └─ W0 标记
  └─ 移动端搜索结果行（md:hidden）
```

### 768px 的真实问题

在 768px 宽度截图时发现：右侧「购物车」「登录 / 注册」被压成**逐字竖排**。

根因有两个：

1. 操作文案的显示断点是 `sm`（640px），768px 时文案已经出现；
2. 搜索表单的类名是 `mx-auto hidden max-w-[560px] md:flex`，它没有 `flex-1` 与 `min-w-0`。
   Flex 子项默认 `min-width: auto`，内容（搜索框 + 按钮）撑住宽度，导致右侧操作区
   只剩下很窄的空间；中文没有空格，浏览器便逐字换行。

### 修复（最终代码）

- `search-box.tsx:27`：表单加 `min-w-0`，允许在 flex 中收缩；
- `site-header.tsx:56`：搜索区改为 `min-w-0 flex-1 md:max-w-[560px]`，占据剩余空间但有上限；
- `site-header.tsx:28`：操作文案改为 `hidden lg:inline`，1024px 前只显示图标；
- `site-header.tsx` 的 `HeaderAction` 加 `shrink-0 whitespace-nowrap`，禁止被压缩与换行；
- 操作区容器加 `shrink-0`。

验证方式：Playwright 在 768×900、1024×800、1440×900、1920×1080、390×844 五种视口截图，
并用 `document.documentElement.scrollWidth > clientWidth` 检查横向溢出，结果均为 false。

### 另一个响应式细节

移动端搜索框不挤在主行，而是单独一行（`site-header.tsx:94` 的 `md:hidden`）。
手机上主行只保留汉堡、Logo、两个图标，避免输入框被压到不可用宽度。

---

## 16. 关键文件及其调用关系

```text
index.html
 └─ src/main.tsx
     ├─ src/mocks/browser.ts（按需）
     ├─ src/App.tsx
     │   └─ src/providers/app-providers.tsx
     │       ├─ src/api/query-client.ts
     │       └─ src/router/index.tsx
     │           ├─ src/layouts/root-layout.tsx
     │           │   ├─ src/components/layout/site-header.tsx
     │           │   │   ├─ brand-logo / search-box / mobile-nav
     │           │   │   └─ src/components/ui/{button,input,dialog}
     │           │   ├─ <Outlet/> → src/pages/home-page.tsx
     │           │   │   ├─ pages/home/design-baseline-section.tsx
     │           │   │   ├─ pages/home/component-showcase-section.tsx
     │           │   │   └─ components/common/{price-display,pagination,...}
     │           │   └─ src/components/layout/site-footer.tsx
     │           └─ src/pages/not-found-page.tsx
     └─ src/styles/globals.css
```

| 文件 | 职责 | 被谁使用 |
| --- | --- | --- |
| `src/main.tsx` | 挂载、按需启动 Mock | 浏览器入口 |
| `src/App.tsx` | 组合 Provider 与 Router | `main.tsx` |
| `src/router/index.tsx` | 路由表 + browser router | `App`、测试 |
| `src/layouts/root-layout.tsx` | 全局骨架、skip link | 路由 `/` |
| `src/api/client.ts` | 请求/错误/超时/Token | 业务模块（W1） |
| `src/api/query-client.ts` | 缓存默认值 | `AppProviders`、测试 |
| `src/stores/ui-store.ts` | 移动导航开关 | `mobile-nav.tsx` |
| `src/utils/price.ts` | 整数分格式化 | `price-display.tsx`、测试 |
| `src/styles/globals.css` | Token、基础样式、动效 | 全局 |
| `src/test/setup.ts` | jest-dom + MSW 生命周期 | Vitest |
| `src/mocks/*` | Mock 基础设施 | 测试、dev server |

测试相关：

```text
vitest.config（vite.config.ts 的 test 块）
 └─ src/test/setup.ts
     ├─ @testing-library/jest-dom/vitest
     └─ src/mocks/server.ts → src/mocks/handlers.ts
```

---

## 17. 实际遇到的问题与解决过程

以下问题均在本次任务中真实发生，并有对应的修复或验证证据。

### 17.1 TypeScript 与 typescript-eslint 的 peer 冲突

- 现象：`npm install` 退出并提示 `Found: typescript@7.0.2`，
  `typescript-eslint@8.72.2` 要求 `typescript >=4.8.4 <6.1.0`。
- 处理：改为安装 `typescript@~5.9.0`（实际 5.9.3），其余依赖不动；
  未使用 `--force` / `--legacy-peer-deps`。
- 结果：安装成功，`typecheck` 与 `lint` 均通过。

### 17.2 MSW 3 的配置项改名

- 现象：`tsc` 报 `TS2353: Object literal may only specify known properties,
  and 'onUnhandledRequest' does not exist in type 'StartOptions'`；
  测试 setup 同样报 `SharedOptions` 上不存在该属性。
- 处理：阅读 `node_modules/msw/lib/_chunks/shared-options.d.ts`，确认 3.x 改为
  `onUnhandledFrame`，同步修改 `src/mocks/browser.ts` 与 `src/test/setup.ts`。
- 结果：类型检查通过，MSW 在 Node 与浏览器均正常初始化。

### 17.3 Button `asChild` 触发 Slot 子元素错误

- 现象：首次 `vitest run` 时 3 个 App 测试全部失败，堆栈指向
  `@radix-ui/react-slot`，报 `React.Children.only expected to receive a single React element child`。
- 原因：`Button` 内部写成 `{isLoading && !asChild ? <Loader2/> : null}{children}`。
  即使条件为 false，`null` 仍是一个子节点，`Slot` 收到 `[null, <a>]` 两个 child。
- 处理：改为三元分支（`src/components/ui/button.tsx:34-41`）：
  `asChild` 时原样返回 `children`，非 `asChild` 时才包 spinner 与 children。
- 结果：同一批用例从 3 failed 变为 24 passed。

### 17.4 Playwright 无法连接 dev server（120s 超时）

- 现象：`npx playwright test` 报 `Timed out waiting 120000ms from config.webServer`。
- 排查：手动启动 dev server 后 `curl http://127.0.0.1:5173` 返回 `000`，
  `curl http://localhost:5173` 返回 `200`。说明 Vite 只监听了 IPv6 回环 `::1`，
  而 Playwright 的 `baseURL` 写的是 `127.0.0.1`。
- 处理：在 `vite.config.ts` 的 `server` 中显式设置 `host: '127.0.0.1'`，与 E2E 保持一致。
- 结果：E2E 正常启动并通过。

### 17.5 Chromium 缺少系统库

- 现象：浏览器启动失败，`error while loading shared libraries: libnspr4.so`。
- 处理：执行 `npx playwright install-deps chromium` 安装系统依赖（环境准备，
  不进入仓库、不改变代码）。
- 结果：Chromium 可启动，6 个 E2E 用例通过。

### 17.6 768px Header 文案竖排

- 现象与根因见第 15 节。
- 处理：`min-w-0` + `flex-1` + `shrink-0` + `whitespace-nowrap`，并把文案断点调整到 `lg`。
- 结果：768/1024 截图正常，无横向溢出。

---

## 18. 技术决策及其权衡

| 决策 | 选择 | 理由 | 代价 |
| --- | --- | --- | --- |
| Tailwind 版本 | v4（CSS-first） | 与设计 Token 天然贴合，构建配置最少 | 生态插件部分仍以 v3 为主，遇到不兼容需自行实现 |
| UI 方案 | shadcn 约定 + Radix | 源码可控、无运行时库锁定、可访问性由 Radix 保证 | 组件要自己维护，升级需手动同步 |
| 路由 | `createBrowserRouter` 数据路由 | 为 loader/action/errorElement 留能力 | 配置比 `<BrowserRouter>` 略重，部署需 history fallback |
| 服务端状态 | TanStack Query | 缓存、失效、重试、并发去重开箱即用 | 需要遵守 queryKey 约定，学习成本 |
| 客户端状态 | Zustand | 体积极小、选择器订阅、无 Provider 嵌套 | 不适合复杂表单/服务端数据 |
| Token | 只留 Provider，不落地存储 | 避免在安全方案确定前绑定 XSS/CSRF 取舍 | W2 前无法发起需鉴权的真实请求 |
| API 类型 | 不生成、不手写 | 没有契约时手写类型会制造"假安全" | W1 需要等 B0 契约 |
| 金额 | 整数分 + 展示组件 | 消除浮点误差，职责单一 | 后端必须遵守"分"的单位约定 |
| 字体 | 系统中文栈，不载入 Web Font | 首屏无字体请求、无 FOUT | 不同系统字形略有差异 |
| 首屏优化 | 暂不做路由拆分 | W0 只有一个页面，拆分收益低 | 当前单 chunk 450KB（gzip 142KB），W1 必须拆 |
| 占位交互 | 禁用按钮 + 说明，不放死链 | 不欺骗用户，也不产生 404 | 导航看起来比完整商城少 |

---

## 19. 测试结果和验证边界

### 真实结果（本次交付前执行）

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | exit 0，0 error / 0 warning |
| `npm run typecheck` | exit 0 |
| `npm run test` | 7 个文件 / 24 个用例通过，约 1.50s |
| `npm run build` | 2272 modules，约 355ms；CSS 33.98 kB（gzip 7.34）、JS 450.72 kB（gzip 142.50） |
| `npm run test:e2e` | 6 个用例通过，约 2.7s（Chromium） |

补充验证：

- 以 `VITE_ENABLE_MOCK=true` 启动 dev server，Playwright 读取
  `navigator.serviceWorker.getRegistrations()`，确认 `/mockServiceWorker.js` 注册成功；
- `npm run typegen` 在缺少 `contracts/openapi.yaml` 时报 `ENOENT`，未生成任何文件；
- 在 1440/1920/1024/768/390 五种视口截图检查，并断言无横向溢出。

### 验证边界（这些没有被证明）

- jsdom **不做布局计算**：组件测试不能证明"没有重叠、没有溢出、颜色正确"，
  这些只能靠浏览器截图与 E2E。
- E2E 只跑 Chromium，未验证 Firefox/WebKit。
- 没有真实后端参与：所有网络断言都是 MSW 的测试 handler，不代表后端行为。
- 没有契约测试：字段名、错误码、分页结构均未与后端核对。
- 未做覆盖率门禁：24 个用例覆盖关键基础组件，但 `EmptyState`、`ErrorState`、
  `LoadingState`、`Dialog` 没有独立单测，只通过首页渲染间接覆盖。
- 视觉检查是人工截图，不是像素回归（没有 baseline 图对比）。

---

## 20. 已知限制

1. **契约缺失**：`contracts/openapi.yaml` 不存在，`npm run typegen` 必然失败；
   `src/api/generated/` 为空。
2. **MSW handler 为空**：开发时开启 Mock 也不会有任何业务数据，只验证了 worker 能启动。
3. **无业务功能**：商品、搜索、购物车、下单、订单、账户均未实现；
   Header 的购物车/登录、分类、搜索提交是禁用占位。
4. **无认证**：没有登录页、没有 Token 存储、没有 401 统一跳转。
5. **无暗色模式**：未定义暗色 Token，禁止直接使用 `dark:`。
6. **路由无错误边界**：未配置 `errorElement`，渲染异常会落到默认错误页。
7. **单 chunk**：没有路由级 `lazy`，W1 增加页面后需要拆分。
8. **仅 Chromium E2E**：跨浏览器兼容未验证。
9. **无 CI 配置**：本任务只保证本地命令可执行，未新增流水线文件。
10. **无图片/静态资源策略**：商品图 CDN、占位图、懒加载均待 W1 设计。
11. **无 i18n**：界面固定中文，文案未做集中管理。
12. **可访问性只做了基础项**：skip link、语义标签、focus ring、aria 属性；
    未做屏幕阅读器实测与自动化 axe 扫描。

---

## 21. 值得深入学习的技术知识

1. **React 19 的 ref 与组合**：函数组件不再需要 `forwardRef`，ref 作为普通 prop 传递；
   `Slot` 通过克隆子元素合并 props 实现 `asChild`。理解这一点才能正确写"样式组件 + 链接"。
2. **StrictMode 的双调用**：开发环境重复 mount/unmount 是为了暴露不安全副作用；
   不要用 `useRef` 标记来"绕过"，而应保证 Effect 可重复执行。
3. **Data Router 的演进**：`createBrowserRouter` 把路由从"组件树"变成"数据树"，
   loader/action 让请求与渲染解耦；这也是 React Router 7/8 的主线。
4. **TanStack Query 的缓存生命周期**：`staleTime` 决定"何时重新请求"，
   `gcTime` 决定"无订阅后何时回收"，二者不是一回事；`invalidateQueries` 才是写后刷新的正确入口。
5. **Zustand 的选择器与不可变更新**：`useStore(s => s.x)` 按引用比较，
   返回新对象（`s => ({a: s.a})`）会导致每次渲染都变化；需要 `useShallow`。
6. **Tailwind 4 的 CSS-first 主题**：`@theme` 不是普通 CSS 变量声明，它会参与工具类生成；
   `inline` 决定变量是否复制到 `:root`。
7. **CSS `color-mix()`**：Tailwind 4 透明度修饰符的底层实现，理解后能解释
   "为什么十六进制变量也能 `bg-primary/10`"。
8. **AbortController 与错误归因**：中止原因是 `DOMException`，但业务需要区分超时/主动取消；
   用状态标记比解析 message 可靠。
9. **MSW 的拦截层**：浏览器是 Service Worker（网络层），Node 是拦截器（模块层）；
   这解释了为什么"Mock 能拦住 fetch，但拦不住某些 Node 原生请求"。
10. **Testing Library 的查询哲学**：测试应像用户一样找元素（role/label/text），
    而不是依赖 `data-testid` 或 class；这直接提升可访问性。
11. **Playwright 的 auto-waiting**：断言自带重试与可操作性检查，
    不要写 `waitForTimeout`；`webServer` 负责生命周期。
12. **Intl 与货币格式化**：`Intl.NumberFormat` 的千分位/小数位由 ICU 决定，
    但货币符号与"分转元"仍应自己控制，避免区域设置差异导致展示不一致。

---

## 22. 可能的技术面试问题及解答思路

### Q1：为什么金额要用整数分？前端如何保证不出现浮点误差？

思路：浮点无法精确表示十进制小数，累加/折扣会产生 1 分误差；后端以分为单位传整数，
前端只做"整数除法 + 取模"拆分与格式化（`src/utils/price.ts:28`），不做金额运算。
如果必须计算，使用整数运算或专门的 decimal 库，而不是 `toFixed` 补救。

### Q2：TanStack Query 和 Zustand 都能存数据，边界怎么划？

思路：按"数据所有权"划分。服务端数据归 Query（缓存、失效、重试、并发去重），
客户端 UI 状态归 Zustand（开关、偏好）。把接口数据复制进 store 会产生双写，
刷新时无法保证一致。可以补一句：派生状态用 selector 计算，不额外存一份。

### Q3：`asChild` 是怎么实现的？为什么不能直接包一层 `<button>`？

思路：Radix `Slot` 克隆唯一子元素，把 props（className、onClick、aria）合并到子元素上，
最终 DOM 是 `<a>` 而不是 `<button><a>`。嵌套交互元素既不符合 HTML 规范，
也会破坏键盘与读屏体验。注意 Slot 只接受**单个** React 元素，这就是 17.3 那个 bug 的根因。

### Q4：请求超时和用户主动取消都会 abort，你怎么区分？

思路：`AbortController` 只暴露 `aborted`，不暴露原因（reason 也不可靠）。
在定时器回调里先置 `timedOut = true` 再 abort，catch 中先判 `timedOut`，
再判 `signal.aborted`，最后归为网络错误（`src/api/client.ts:144`、`:210-226`）。
同时要在 `finally` 移除外部 signal 监听，避免泄漏。

### Q5：为什么现在不把 Token 存 localStorage？

思路：localStorage 可被 XSS 读取；HttpOnly Cookie 能防 XSS 但需要处理 CSRF。
这是安全模型取舍，不是纯前端偏好。W0 只提供 `setAuthTokenProvider` 这个"取 token"接口，
把存储决策留给认证模块；如果后端选 Cookie，则完全不需要该 Provider。

### Q6：Tailwind 4 的 `@theme inline` 和普通 `@theme` 有什么区别？

思路：`@theme` 会把变量写入生成的 `:root`；`inline` 不复制变量，而是把值内联到工具类。
我们的值是 `var(--primary)`，所以运行时仍可被 CSS 变量覆盖，同时少一层间接。
如果写成普通 `@theme`，会出现两份变量，运行时改 `:root` 可能不生效。

### Q7：为什么用 MSW，而不是 `vi.mock('fetch')`？

思路：MSW 在更真实的层拦截（Node 拦截器 / 浏览器 Service Worker），
请求仍然经过 URL 解析、headers、状态码、JSON 序列化，能暴露真实的客户端 bug；
`vi.mock` 直接替换模块，测不到这些。且同一份 handler 可同时服务测试与本地开发。

### Q8：Vitest 和 Playwright 都测页面，为什么需要两套？

思路：测试金字塔。Vitest + jsdom 快、反馈密集，适合组件行为与边界；
Playwright 慢但真实，适合布局、焦点、滚动、真实网络与跨页流程。
两者不是替代关系；E2E 只覆盖关键路径，避免用例膨胀导致反馈变慢。

### Q9：`createBrowserRouter` 和 `<BrowserRouter>` 的区别？部署要注意什么？

思路：前者是 Data Router，路由配置是数据树，支持 loader/action/errorElement，
请求可以与渲染并行；后者是组件式路由。二者都依赖 History API，
所以服务器必须把非静态路径回退到 `index.html`，否则刷新子路由 404。

### Q10：列表页价格参差不齐，如何避免布局跳动？

思路：`tabular-nums` 等宽数字 + 价格组件固定字号档位 + 标题固定两行（`line-clamp-2`）
+ 图片固定宽高比。数据未到达时用骨架屏占位，保持与真实内容相同的盒模型。
这也是 `PriceDisplay` 拆成三段并固定尺寸的原因。

### Q11：为什么 Dialog 的动画只做透明度？

思路：内容用 `left-1/2 top-1/2` + `translate(-50%, -50%)` 居中；
如果在 keyframes 里再写 `transform: translateY(...) scale(...)`，会覆盖定位位移，
弹层会从左上角飞入。解决方式要么在动画里同时写完整 translate，要么只动 opacity。
我们选了后者（`globals.css:207-232`），更稳也更克制。

### Q12：如果让你把 W0 的按钮组件扩展到支持 `<Link>`，需要改什么？

思路：`Button` 已支持 `asChild`，只需 `<Button asChild><Link to="...">…</Link></Button>`，
不需要新增 `LinkButton`。这也是"样式与语义分离"的收益：同一个视觉变体可以承载
`button`、`a`、`Link`，而不用为每种组合复制样式。

---

## 附：本地验证命令

```bash
cd web
npm ci
npm run lint
npm run typecheck
npm run test
npm run build
npx playwright install chromium        # 首次
npx playwright install-deps chromium   # Linux 首次
npm run test:e2e
```

开发：`npm run dev`（默认 `http://127.0.0.1:5173`，`/api` 代理到 `VITE_DEV_PROXY_TARGET`）。
