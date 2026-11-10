# SurgeCart 用户商城前端

SurgeCart 用户商城的 PC 优先前端工程。当前处于 **W2-02：个人中心、收货地址与商品收藏**，
已开放商城浏览、注册/登录/退出、个人中心、只读个人资料、收货地址 CRUD 与收藏管理；
购物车（W3）、下单与订单（W4/W5）尚未开放。

- 设计规范：[`DESIGN_SYSTEM.md`](./DESIGN_SYSTEM.md)
- W2-02 交付报告：[`../docs/reports/w2-02-user-center/DELIVERY_REPORT.md`](../docs/reports/w2-02-user-center/DELIVERY_REPORT.md)
- W2-02 开发知识文档：[`../.agent/tasks/dev/W2-02-user-center.md`](../.agent/tasks/dev/W2-02-user-center.md)
- W2-01 交付报告：[`../docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md`](../docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md)
- W2-01 开发知识文档：[`../.agent/tasks/dev/W2-01-user-authentication.md`](../.agent/tasks/dev/W2-01-user-authentication.md)
- W1 交付报告：[`../docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md`](../docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md)
- W1 开发知识文档：[`../.agent/tasks/dev/W1-01-product-browsing.md`](../.agent/tasks/dev/W1-01-product-browsing.md)
- W0 交付报告：[`../docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md`](../docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md)
- 后端工程：仓库根目录的 Go 服务（本工程只通过 `/api` 访问，不修改其后端代码）

## 技术栈

| 领域 | 选型 |
| --- | --- |
| 框架 | React 19 + TypeScript（strict） |
| 构建 | Vite 8 |
| 路由 | React Router 8 |
| 服务端状态 | TanStack Query 5 |
| 客户端状态 | Zustand 5 |
| 样式 | Tailwind CSS 4（CSS-first Token） |
| 组件 | shadcn/ui 约定 + Radix 原语 + lucide-react |
| 表单 | React Hook Form + Zod |
| 单元/组件测试 | Vitest 5 + Testing Library + jsdom |
| API Mock | MSW 3 |
| E2E | Playwright（Chromium） |
| API 类型 | openapi-typescript |

精确版本见 `package.json` 与 `package-lock.json`。

## 环境要求

- Node.js ≥ 20.19（开发使用 Node 22）
- npm 10+
- 首次运行 E2E 前安装浏览器：`npx playwright install chromium`

## 快速开始

```bash
cd web
npm ci

cp .env.example .env.local   # 按需修改，.env.local 不会被提交
npm run dev                  # http://127.0.0.1:5173
```

开发服务器把 `/api/*` 代理到 `VITE_DEV_PROXY_TARGET`（默认 `http://127.0.0.1:8080`，
即后端 Nginx 入口）并在转发时剥离 `/api` 前缀；`/storage/*` 静态资源同样转发。
生产环境需要由反向代理/网关配置等价的 `/api` 前缀剥离规则（当前仓库的
`nginx/nginx.conf` 尚未配置，见 W1 交付报告「已知缺口」）。

页面路由 `/products`、`/products/:id` 与后端契约路径同名，因此不能把 API 直接挂在
同源根路径上，否则 SPA 页面请求会被代理拦截。

## npm scripts

| 命令 | 说明 |
| --- | --- |
| `npm run dev` | 启动开发服务器 |
| `npm run build` | `tsc -b` 类型检查 + Vite 生产构建 |
| `npm run preview` | 预览 `dist/` |
| `npm run lint` | ESLint（flat config） |
| `npm run typecheck` | TypeScript 项目引用构建检查 |
| `npm run test` | Vitest 单次运行 |
| `npm run test:watch` | Vitest 监听模式 |
| `npm run test:e2e` | Playwright E2E（自动拉起 dev server） |
| `npm run typegen` | 由 OpenAPI 契约生成类型（见下文） |

## 目录结构

```text
web/
├── public/                     # favicon、MSW Service Worker、Mock 商品图
├── src/
│   ├── api/                    # Client、响应解包、401 刷新、Query Key、按域 API 模块
│   ├── auth/                   # 内存 Token、刷新、Auth 状态、路由守卫、校验/错误映射
│   ├── components/
│   │   ├── account/            # 地址表单弹窗
│   │   ├── auth/               # 登录/注册卡片、密码输入
│   │   ├── common/             # 容器、金额、空/错/加载、分页、错误边界
│   │   ├── layout/             # Header、Footer、认证入口、搜索、移动导航
│   │   ├── product/            # 商品卡片、栅格、图册、SKU、收藏、评价、分类筛选
│   │   └── ui/                 # shadcn/ui 风格基础组件
│   ├── config/                 # 前端运行时配置（推荐位 code 等）
│   ├── hooks/                  # 通用 hooks（收藏状态/切换等）
│   ├── layouts/                # 路由布局
│   ├── mocks/                  # MSW handlers 与 Mock 数据（商品/认证/地址/收藏）
│   ├── pages/                  # 首页、列表、详情、登录、注册、404
│   │   └── account/            # 个人中心布局、概览、资料、地址、收藏
│   ├── providers/              # 全局 Provider
│   ├── router/                 # 路由表与路由级懒加载
│   ├── stores/                 # Zustand 客户端状态
│   ├── styles/                 # 设计 Token 与全局样式
│   ├── test/                   # Vitest 环境初始化与测试渲染工具
│   ├── utils/                  # cn、价格格式化、分页、分类树、列表 URL 状态
│   ├── App.tsx
│   └── main.tsx
├── tests/e2e/                  # Playwright 用例
├── DESIGN_SYSTEM.md
├── components.json             # shadcn/ui 配置
├── eslint.config.js
├── playwright.config.ts
├── tsconfig*.json
└── vite.config.ts
```

## 环境变量

只提交 `.env.example`，真实值写入 `.env.local`（已被忽略）。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `/api` | API 基础路径（转发层需剥离 `/api`） |
| `VITE_ENABLE_MOCK` | `false` | 是否启用 MSW 浏览器 Mock |
| `VITE_API_TIMEOUT_MS` | `15000` | 请求超时（毫秒） |
| `VITE_DEV_PROXY_TARGET` | `http://127.0.0.1:8080` | 仅 Vite dev server 使用 |
| `VITE_HOME_RECOMMEND_CODE` | 空 | 首页推荐位 code；空则不请求、不渲染推荐区块 |

## API 数据层

```ts
import { fetchProductList } from '@/api/products'

// 业务模块内部：apiClient 负责传输，getData 负责解包 {code,message,data}
const data = await fetchProductList({ page: 1, size: 12, keyword: '耳机' })
```

分层约定：

- `src/api/client.ts`：纯传输层，处理超时、AbortSignal、Token、错误归类；`204` 返回 `undefined`。
- `src/api/envelope.ts`：解包后端统一响应 `{code,message,data}`；`code !== 0` 抛 `ApiError`。
- `src/api/auth-request.ts`：需要登录态的请求包装，401 时单飞刷新并重试一次，失败后清空 Token。
- `src/api/*.ts`：按域封装的业务 API（products / categories / banners / reviews / recommendations / auth / account / addresses / favorites），组件禁止直接 `fetch`。
- `src/api/types.ts`：DTO 定义，字段逐一对应 Go 后端 JSON tag；仓库没有 `contracts/openapi.yaml`，因此不做代码生成。
- `src/api/query-keys.ts`：统一 Query Key 工厂，列表 key 携带完整查询参数，认证用户 key 为 `['auth','me']`。
- 统一抛出 `ApiError`，`kind` 为 `http | network | timeout | aborted | parse`，带 `status` / `code` / `details`；后端数字业务码统一转为字符串。

## 认证与 Token

后端 IAM 同时返回 access token 与 refresh token（JSON），没有 HttpOnly Cookie：

- **Token 只保存在内存**（`src/auth/token-store.ts`），不写 localStorage / sessionStorage；
- 调用时通过 `setAuthTokenProvider` 注入 `Authorization: Bearer`；
- access token 过期（401）时，`authGetData` 会单飞调用 `POST /refresh`，成功后重试原请求；
- refresh token 轮换后旧 token 失效；并发 401 只发一次 refresh，避免后端判定 reuse 后全量撤销；
- **刷新页面 / 新标签页会丢失内存 Token，需要重新登录**（安全换取的明确限制）；
- `/me` 由 TanStack Query 统一缓存，Header、路由守卫与页面共享同一个请求。

## 状态管理边界

- **TanStack Query**：后端数据（商品、分类、评价、当前用户）唯一权威缓存，负责请求状态与失效。
- **URL**：商品列表的关键词、分类、排序、分页以 URL 查询参数为唯一事实来源。
- **内存 Token Store**：认证凭证的唯一事实来源，通过 `useSyncExternalStore` 驱动 Header 与路由守卫。
- **用户私有缓存**：地址 `['addresses']`、收藏 `['favorites']` 通过 Query Key 分层管理；
  会话结束（退出或 refresh 失败）时统一清理，避免切换账号看到上一位用户的数据。
- **Zustand**：仅存放轻量 UI 状态（当前为移动端导航开关），不保存认证或业务数据。
- **React Hook Form + Zod**：登录/注册/地址表单状态与前端校验。

禁止把接口数据复制进 Zustand，也不在组件内直接 `fetch`。

## 测试

```bash
npm run test        # 单元 + 组件测试（MSW 拦截网络）
npm run test:e2e    # Playwright，自动启动带 MSW 的 dev server
```

- `src/mocks/handlers.ts` 按真实后端结构提供分类、商品、SKU、评价、轮播图、推荐位与认证 Mock；
  `keyword=__error__` 是仅用于错误态验证的 Mock 钩子，真实后端不存在。
- 认证 Mock 覆盖注册、登录、`/me`、refresh 轮换、logout；默认测试账号为 `demo / demo12345`。
- 地址与收藏 Mock 使用内存状态，覆盖默认地址、CRUD、收藏可用性（下架/删除）与取消幂等。
- E2E 通过 `VITE_ENABLE_MOCK=true` 启用浏览器 MSW，不依赖本地后端；
  推荐位区块通过 `VITE_HOME_RECOMMEND_CODE=home` 启用。
- E2E 串行执行（`workers: 1`），避免 MSW Service Worker 注册与 Vite 依赖优化并发导致空页面。
- 视口 1440×900，另含 390×844 移动端用例；需要图形依赖时执行 `npx playwright install-deps chromium`。

## OpenAPI 类型生成

```bash
npm run typegen
# openapi-typescript ../contracts/openapi.yaml -o src/api/generated/schema.d.ts
```

仓库当前**没有** `contracts/openapi.yaml`（已核查仓库根目录与 `docs/`）。
W1/W2 的 DTO 以 `api/*/v1/*.go` 的 JSON tag 为事实来源，集中定义在 `src/api/types.ts`
与 `src/api/auth.ts`；后端提供正式契约后再执行 `npm run typegen` 并逐步替换手写 DTO。
个人资料当前只有 `GET /me`（id/username），后端没有资料修改接口，因此资料页为只读。

## 边界

前端代码只修改 `web/**`；任务要求的开发知识文档与交付报告分别位于
`.agent/tasks/dev/` 与 `docs/reports/`。后端代码、迁移、Docker、Nginx、Kubernetes、
Prometheus 配置均不在前端职责内。
