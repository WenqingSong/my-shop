# W0 用户商城前端工程初始化 — 交付报告

- 任务：SurgeCart 用户商城前端 W0（工程初始化与设计基线）
- 分支：`feat/phase2-user-frontend`
- 基线提交：`82243c0fbce614e03c4ff57dcc829b5f68fb08f2`
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W0-frontend-foundation.md`

---

## 1. 任务目标

从零建立 SurgeCart 用户商城前端工程，为 W1–W5 业务开发提供稳定底座：

- 可启动、可构建、TypeScript strict 的 React 工程；
- 统一视觉规范（颜色、字体、间距、圆角、阴影、按钮、表单、商品卡片、页面状态）；
- 商城公共布局（Header / Footer / 内容容器）与基础通用组件；
- 统一 API Client、服务端/客户端状态边界；
- Vitest、Testing Library、MSW、Playwright 测试与 Mock 基础设施；
- 明确边界：不猜后端接口、不提前实现业务、不伪造 OpenAPI 契约。

本阶段不以业务页面数量为目标，而以"后续可以直接开发"为目标。

---

## 2. 前置检查

| 检查项 | 结果 |
| --- | --- |
| 仓库根目录 | `/root/projects/my-shop` |
| 当前分支 | `feat/phase2-user-frontend`（正确） |
| HEAD | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（与预期一致） |
| 工作区 | 基线干净；本任务只新增 `web/` 与两份文档 |
| 既有 `web/` | 无，未覆盖任何内容 |
| `contracts/openapi.yaml` | 无 |
| 其他前端工程 | 无 |
| `.agent/tasks/dev/` | 原不存在，已按任务要求创建 |
| `docs/reports/` | 已存在，沿用"任务目录 + 报告文件"命名 |

---

## 3. 实际实现范围

### 已实现

- React 19 + TypeScript 5.9（strict）+ Vite 8 工程，npm 依赖与 lock 文件。
- Tailwind CSS 4 设计 Token（原始色板 → 语义 Token → Theme 映射）与基础样式。
- 商城公共布局：Header（品牌、搜索、操作区、导航行、移动端抽屉）、Footer、1440px 内容容器、skip link。
- 通用组件：Button、Input、Dialog、PageContainer、PagePlaceholder、PriceDisplay、LoadingState、
  EmptyState、ErrorState、Pagination。
- 首页工程占位页：品牌 Hero、设计基线展示、组件状态展示、工程能力与阶段规划。
- 404 页面、路由表与布局路由。
- API Client：`/api` 基础路径、JSON、超时、取消、统一 `ApiError`、Token Provider 扩展点。
- TanStack Query（QueryClient 默认值与可注入 Provider）、Zustand（移动导航开关）。
- MSW（Node Server + Browser Worker）、Vitest、Testing Library、Playwright 配置与示例。
- `DESIGN_SYSTEM.md` 设计规范、`README.md` 使用说明、本报告与开发知识文档。

### 未实现（按 W0 边界）

- 商品列表/详情/搜索、购物车、下单、订单、账户等业务功能。
- 登录流程与 Token 存储策略。
- 商品卡片组件（仅定义规范）。
- MSW 业务 handlers（无契约，不伪造）。
- 暗色模式、i18n、跨浏览器 E2E、路由级代码拆分。

### 交互占位策略

购物车、登录/注册、全部商品分类、搜索提交均为**禁用控件**并附 `title` / 可见文案说明开放阶段，
不使用指向不存在路由的死链，避免产生 404 或误导用户。

---

## 4. 技术栈及版本

| 领域 | 选型与版本 |
| --- | --- |
| 框架 | React 19.3.1 / React DOM 19.3.1 |
| 语言 | TypeScript 5.9.3（fix `~5.9.0`） |
| 构建 | Vite 8.3.7、@vitejs/plugin-react 6.3.0 |
| 路由 | React Router 8.5.1（`createBrowserRouter`） |
| 服务端状态 | @tanstack/react-query 5.107.1 |
| 客户端状态 | Zustand 5.1.0 |
| 样式 | Tailwind CSS 4.3.5、@tailwindcss/vite 4.3.5 |
| UI | shadcn/ui 约定、@radix-ui/react-dialog 1.3.1、react-slot 1.5.0、lucide-react 1.70.0 |
| 样式工具 | class-variance-authority 0.7.1、clsx 2.1.1、tailwind-merge 3.7.0 |
| 表单 | react-hook-form 7.91.0、zod 4.6.7、@hookform/resolvers 7.0.5 |
| 单元/组件测试 | Vitest 5.1.0、jsdom 31.0.0、Testing Library 16.3.3 / 7.0.1 / 14.6.8 |
| API Mock | MSW 3.0.4 |
| E2E | @playwright/test 1.65.0（Chromium） |
| 契约 | openapi-typescript 7.13.0 |
| 静态检查 | ESLint 10.14.0、typescript-eslint 8.72.2 |

版本说明：

- npm latest 的 TypeScript 为 7.0.2，与 typescript-eslint 8.72.2 的 peer 范围
  `>=4.8.4 <6.1.0` 冲突，因此固定 `~5.9.0`，未使用 `--force` / `--legacy-peer-deps`。
- 使用 npm 安装，`package-lock.json` 已提交，保证可复现。

---

## 5. 核心交付内容

### 5.1 设计系统

- 主色 `#1e5ae8`，页面背景 `#f5f7fa`，卡片 `#ffffff`，正文 `#111827`，辅助 `#6b7280`，
  边框 `#e5e7eb`，价格 `#d92d3a`，促销 `#e5484d`，成功/警告/错误 `#0f9d63` / `#b45309` / `#dc2626`。
- 中文优先字体栈；1440px 主容器 / 1080px 窄容器；断点 640/768/1024/1280/1536；
  4px 间距基准；圆角 4/6/8/12/16；三级阴影。
- 全部 Token 落在 `web/src/styles/globals.css` 的 `:root` 与 `@theme inline`，
  组件只引用语义类（`bg-primary`、`text-price` 等）。
- 规范文档 `web/DESIGN_SYSTEM.md` 覆盖品牌方向、色彩、字体、页面宽度、断点、间距、圆角、
  阴影、按钮、表单、商品卡片、页面状态、可访问性与评审清单。

视觉验证：1440×900、1920×1080、1024×800、768×900、390×844 五种视口截图检查，
均无横向溢出；已修复 768px 下 Header 操作文案被压成竖排的问题。

### 5.2 公共布局与组件

| 类别 | 内容 |
| --- | --- |
| 布局 | `SiteHeader`（主行 + 导航行 + 移动搜索行）、`SiteFooter`、`RootLayout`、`PageContainer` |
| 基础 UI | `Button`（6 变体 × 4 尺寸、asChild、disabled、loading）、`Input`、`Dialog` |
| 通用展示 | `PriceDisplay`（整数分）、`LoadingState`、`EmptyState`、`ErrorState`、`PagePlaceholder`、`Pagination` |
| 品牌与搜索 | `BrandLogo`、`SearchBox`、`MobileNav` |

### 5.3 API 与状态

- API Client 默认 `/api`，超时 15s（可配置），支持外部取消；统一 `ApiError`
  （`http` / `network` / `timeout` / `aborted` / `parse`），带 `status`、`code`、`details`。
- `setAuthTokenProvider` 提供 Token 扩展点，未实现登录、未决定 Token 存储策略。
- TanStack Query：`staleTime 30s`、`gcTime 5min`、`retry 1`、窗口聚焦不自动刷新。
- Zustand：仅移动导航开关；禁止后端数据进入客户端 store。

### 5.4 测试与 Mock

- Vitest：jsdom、`src/test/setup.ts` 统一初始化，排除 `tests/e2e`。
- Testing Library + jest-dom + user-event：组件查询与交互。
- MSW：Node `setupServer`（测试）与 Browser `setupWorker`（仅 `VITE_ENABLE_MOCK=true` 动态加载）；
  handlers 当前为空，测试内使用 `/api/__test__/*` 临时注册。
- Playwright：Chromium、1440×900、`webServer` 自动拉起 dev server，失败保留 trace 与截图。
- 用例：7 个 Vitest 文件 / 24 个用例 + 6 个 E2E 用例。

### 5.5 OpenAPI

- `npm run typegen` 已配置为 `openapi-typescript ../contracts/openapi.yaml -o src/api/generated/schema.d.ts`。
- 仓库无 `contracts/openapi.yaml`，实测命令报 `ENOENT`，未生成任何文件、未手写业务 DTO。
- 契约由后端 B0 提供后再生成，生成目录已加入 ESLint 忽略。

---

## 6. 工程目录

```text
web/
├── public/                 favicon、mockServiceWorker.js
├── src/
│   ├── api/                client.ts、query-client.ts
│   ├── components/
│   │   ├── common/         容器、金额、空/错/加载、分页、页面占位
│   │   ├── layout/         Header、Footer、Logo、搜索、移动导航
│   │   └── ui/             Button、Input、Dialog
│   ├── hooks/              useDocumentTitle
│   ├── layouts/            RootLayout
│   ├── mocks/              handlers / browser / server
│   ├── pages/              首页基线、404、首页区块
│   ├── providers/          AppProviders
│   ├── router/             路由表与 browser router
│   ├── stores/             Zustand UI store
│   ├── styles/             globals.css
│   ├── test/               Vitest setup
│   ├── utils/              cn、price、pagination
│   ├── App.tsx / main.tsx
│   └── vite-env.d.ts
├── tests/e2e/              Playwright 用例
├── DESIGN_SYSTEM.md
├── README.md
├── components.json
├── eslint.config.js
├── playwright.config.ts
├── tsconfig*.json
└── vite.config.ts
```

未创建无实际用途的空目录（`features/`、`types/`、`api/modules/`、`api/generated/`）。

---

## 7. 修改文件

本任务为纯新增，未修改任何已跟踪文件（`git diff --stat` 为空），
未改动 `internal/**`、`api/**`、`migrations/**`、`go.mod/go.sum`、`contracts/**`、
Docker / Nginx / Kubernetes / Prometheus 配置。

统计：**65 个文件**

- `web/**`：63 个
- `.agent/tasks/dev/W0-frontend-foundation.md`：1 个
- `docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md`：1 个

原 `web/W0_DELIVERY_REPORT.md` 的正式交付内容已完整迁入本报告，旧文件已删除，避免重复维护。

<details>
<summary>web/ 文件清单（63 个）</summary>

```text
web/.env.example
web/.gitignore
web/DESIGN_SYSTEM.md
web/README.md
web/components.json
web/eslint.config.js
web/index.html
web/package-lock.json
web/package.json
web/playwright.config.ts
web/public/favicon.svg
web/public/mockServiceWorker.js
web/src/App.tsx
web/src/__tests__/app.test.tsx
web/src/api/client.test.ts
web/src/api/client.ts
web/src/api/query-client.ts
web/src/components/common/empty-state.tsx
web/src/components/common/error-state.tsx
web/src/components/common/loading-state.tsx
web/src/components/common/page-container.tsx
web/src/components/common/page-placeholder.tsx
web/src/components/common/pagination.test.tsx
web/src/components/common/pagination.tsx
web/src/components/common/price-display.test.tsx
web/src/components/common/price-display.tsx
web/src/components/layout/brand-logo.tsx
web/src/components/layout/mobile-nav.tsx
web/src/components/layout/search-box.tsx
web/src/components/layout/site-footer.tsx
web/src/components/layout/site-header.tsx
web/src/components/ui/button-variants.ts
web/src/components/ui/button.tsx
web/src/components/ui/dialog.tsx
web/src/components/ui/input.tsx
web/src/hooks/use-document-title.ts
web/src/layouts/root-layout.tsx
web/src/main.tsx
web/src/mocks/browser.ts
web/src/mocks/handlers.ts
web/src/mocks/server.ts
web/src/pages/home-page.tsx
web/src/pages/home/component-showcase-section.tsx
web/src/pages/home/design-baseline-section.tsx
web/src/pages/home/section-primitives.tsx
web/src/pages/not-found-page.tsx
web/src/providers/app-providers.tsx
web/src/router/index.tsx
web/src/stores/ui-store.test.ts
web/src/stores/ui-store.ts
web/src/styles/globals.css
web/src/test/setup.ts
web/src/utils/cn.ts
web/src/utils/pagination.test.ts
web/src/utils/pagination.ts
web/src/utils/price.test.ts
web/src/utils/price.ts
web/src/vite-env.d.ts
web/tests/e2e/home.spec.ts
web/tsconfig.app.json
web/tsconfig.json
web/tsconfig.node.json
web/vite.config.ts
```

</details>

---

## 8. 测试门禁及真实结果

执行目录：`/root/projects/my-shop/web`，环境 Node.js v22.23.3、npm 10.9.9、Linux x64。

| 门禁 | 命令 | 结果 |
| --- | --- | --- |
| 静态检查 | `npm run lint` | 通过，0 error / 0 warning（exit 0） |
| 类型检查 | `npm run typecheck` | 通过（exit 0） |
| 单元/组件测试 | `npm run test` | 7 个文件 / 24 个用例通过（约 1.50s） |
| 生产构建 | `npm run build` | 成功，2272 modules，约 355ms |
| 端到端 | `npm run test:e2e` | 6 个用例通过（约 2.7s，Chromium） |

构建产物：

- `dist/index.html` 0.69 kB
- `dist/assets/*.css` 33.98 kB（gzip 7.34 kB）
- `dist/assets/*.js` 450.72 kB（gzip 142.50 kB）

用例分布：

| 文件 | 用例数 | 覆盖 |
| --- | --- | --- |
| `src/__tests__/app.test.tsx` | 3 | 首页布局、设计/组件区块、404 保留布局 |
| `src/api/client.test.ts` | 4 | JSON 请求、HTTP 错误、超时、204 |
| `src/components/common/price-display.test.tsx` | 3 | 人民币展示、零元/小额、字号与色调 |
| `src/components/common/pagination.test.tsx` | 3 | 首尾禁用、`aria-current`、单页不渲染 |
| `src/utils/price.test.ts` | 4 | 整数分格式化、负数、非法入参 |
| `src/utils/pagination.test.ts` | 5 | 页数序列与折叠 |
| `src/stores/ui-store.test.ts` | 2 | 默认值、切换与关闭 |
| `tests/e2e/home.spec.ts` | 6 | 布局、锚点、Dialog、分页、404、移动导航 |

补充验证：

- `VITE_ENABLE_MOCK=true` 启动 dev server 后，浏览器检测到 `/mockServiceWorker.js` 注册成功。
- `npm run typegen` 在无契约时报 `ENOENT`，未生成 `src/api/generated/`。
- 1440/1920/1024/768/390 五种视口无横向溢出，768px Header 竖排问题已修复。

验证边界：jsdom 不做布局与样式计算；E2E 仅 Chromium；无真实后端参与；无契约测试；
未设置覆盖率门禁（Loading/Empty/Error/Dialog 仅通过首页渲染间接覆盖）。

---

## 9. Git 提交信息

- 分支：`feat/phase2-user-frontend`
- 提交方式：单提交，包含 W0 代码、开发知识文档、正式交付报告
- Commit Message：`feat(web): initialize storefront foundation`
- 提交范围：`web/**`、`.agent/tasks/dev/W0-frontend-foundation.md`、
  `docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md`
- 未执行 Push，未修改其他分支，未合并代码

说明：本报告与该提交一同入库，因此不在文档内自引用提交 SHA（否则需 amend 导致哈希变化）。
最终 SHA 以 `git rev-parse HEAD` 与面向 Owner 的汇报为准。

---

## 10. 开发知识文档

路径：`.agent/tasks/dev/W0-frontend-foundation.md`

内容覆盖 22 个主题：W0 目标、技术栈版本取舍、目录职责、启动与路由机制、Query/Zustand 边界、
Tailwind 4 主题、shadcn/Radix 组合、API Client 流程、Token 扩展点、OpenAPI 原理、
MSW 拦截、三类测试职责、整数分展示、Header 响应式修复、文件调用关系、真实问题复盘、
技术决策权衡、测试边界、已知限制、延伸学习与面试问答。

与本文分工：**知识文档解释"为什么/怎么运作"，本报告记录"交付了什么/如何验收"**，不互相复制。

---

## 11. 未完成事项及原因

| 事项 | 原因 |
| --- | --- |
| 商品、购物车、订单、账户等业务 | 属 W1–W5，W0 明确不提前实现 |
| 登录与 Token 存储 | 只保留扩展点，安全方案与契约未定 |
| OpenAPI 类型生成 | 仓库无 `contracts/openapi.yaml`，等待后端 B0 |
| MSW 业务 handlers | 不编造接口 |
| 商品卡片组件 | 仅有规范，避免与 W1 真实结构冲突 |
| 暗色模式 | 未要求；需先补暗色 Token |
| 跨浏览器 E2E | 当前仅 Chromium |
| 路由级代码拆分 | 单页阶段收益低，W1 引入业务路由时实施 |
| 路由错误边界 | 未配置 `errorElement`，W1 补统一错误页 |
| CI 流水线 | 本任务只保证本地门禁，未新增 CI 配置 |
| 图片/CDN 策略、i18n | 待 W1 结合真实商品数据设计 |

---

## 12. W1 开发准备事项

1. **契约先行**：后端 B0 提供 `contracts/openapi.yaml` 后执行 `npm run typegen`，
   在 `src/api/modules/` 按域编写请求函数，禁止组件内直接调用 `apiClient`。
2. **Query Key 约定**：新增 `src/api/query-keys.ts`，列表/详情共用失效入口。
3. **商品卡片**：按 `DESIGN_SYSTEM.md` 第 11 节实现图片骨架、两行标题、`PriceDisplay`、
   促销标签、缺货态，并补组件测试。
4. **商品列表页**：接入 `Pagination`、筛选、空/错/加载状态；列表使用骨架屏。
5. **图片策略**：确认 CDN 域名与占位图，使用 `loading="lazy"` 与固定宽高比避免 CLS。
6. **路由扩展**：新增 `/products`、`/products/:id`，使用 `lazy` 拆分并补 `errorElement`。
7. **表单接入**：登录/结算使用 React Hook Form + Zod，金额提交前转整数分。
8. **CI 门禁**：流水线执行 `lint`、`typecheck`、`test`、`build`、`test:e2e`，
   E2E 预装 Chromium 与系统依赖。

---

## 13. 最终验收结论

| 验收项 | 结果 |
| --- | --- |
| React 工程正常启动 | 通过（`npm run dev`，127.0.0.1:5173） |
| TypeScript strict | 通过 |
| ESLint | 通过，0 warning |
| Vite Build | 通过 |
| React Router | 通过（`/` 与 404 路由、测试复用路由表） |
| Tailwind CSS | 通过（Token 与工具类生效） |
| shadcn/ui 基础组件 | 通过（Button/Input/Dialog） |
| TanStack Query Provider | 通过（可注入 QueryClient） |
| Zustand 状态示例 | 通过（移动导航开关 + 单测） |
| API Client | 通过（JSON、超时、错误、Token 头、4 个单测） |
| MSW 初始化 | 通过（Node server + Browser worker 注册） |
| Vitest | 通过（7 文件 / 24 用例） |
| Playwright | 通过（6 用例） |
| 视觉规范 | 通过（DESIGN_SYSTEM.md + CSS Token 一致） |
| 公共布局 | 通过（Header/Footer/容器/响应式） |
| Git 范围 | 通过（仅 web/ 与两份文档，单提交） |

**结论：W0 达到完整交付条件，无阻塞问题。** 未执行 Push，等待 Owner 与主 Chat 验收。
