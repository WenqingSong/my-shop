# SurgeCart 用户商城前端

SurgeCart 用户商城的 PC 优先前端工程。当前处于 **W0：工程初始化与设计基线**，
已完成 React 工程、设计系统、公共布局、API Client 与测试基础设施，
尚未实现商品列表、购物车、订单等业务功能。

- 设计规范：[`DESIGN_SYSTEM.md`](./DESIGN_SYSTEM.md)
- W0 交付报告：[`../docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md`](../docs/reports/w0-frontend-foundation/DELIVERY_REPORT.md)
- W0 开发知识文档：[`../.agent/tasks/dev/W0-frontend-foundation.md`](../.agent/tasks/dev/W0-frontend-foundation.md)
- 后端工程：仓库根目录的 Go 服务（本工程只通过 `/api` 访问，不读取其内部代码）

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

开发服务器会把 `/api` 代理到 `VITE_DEV_PROXY_TARGET`（默认 `http://127.0.0.1:8080`，
即后端 Nginx 入口）。生产环境由 Nginx 直接反代，不经过 Vite。

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
├── public/                     # favicon、MSW Service Worker
├── src/
│   ├── api/                    # API Client 与 QueryClient
│   ├── components/
│   │   ├── common/             # 容器、金额、空/错/加载、分页
│   │   ├── layout/             # Header、Footer、品牌、搜索、移动导航
│   │   └── ui/                 # shadcn/ui 风格基础组件
│   ├── hooks/                  # 通用 hooks
│   ├── layouts/                # 路由布局
│   ├── mocks/                  # MSW handlers / browser / server
│   ├── pages/                  # 页面（首页基线、404）
│   ├── providers/              # 全局 Provider
│   ├── router/                 # 路由表
│   ├── stores/                 # Zustand 客户端状态
│   ├── styles/                 # 设计 Token 与全局样式
│   ├── test/                   # Vitest 环境初始化
│   ├── utils/                  # cn、价格格式化、分页计算
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
| `VITE_API_BASE_URL` | `/api` | API 基础路径 |
| `VITE_ENABLE_MOCK` | `false` | 是否启用 MSW 浏览器 Mock |
| `VITE_API_TIMEOUT_MS` | `15000` | 请求超时（毫秒） |
| `VITE_DEV_PROXY_TARGET` | `http://127.0.0.1:8080` | 仅 Vite dev server 使用 |

## API Client

```ts
import { apiClient, ApiError } from '@/api/client'

const data = await apiClient.get<{ items: unknown[] }>('/products', {
  query: { page: 1, size: 20 },
})

try {
  await apiClient.post('/cart/items', { skuId: 'x', quantity: 1 })
} catch (error) {
  if (error instanceof ApiError && error.kind === 'http' && error.status === 401) {
    // 认证接入后统一处理
  }
}
```

能力与边界：

- 默认 base URL 为 `/api`，JSON 请求/响应，`204` 返回 `undefined`。
- 统一抛出 `ApiError`，`kind` 为 `http | network | timeout | aborted | parse`，带 `status` / `code` / `details`。
- 单次请求支持 `timeoutMs` 与外部 `AbortSignal`。
- Token 通过 `setAuthTokenProvider(() => token)` 注入；W0 不决定 Token 存储策略，也未实现登录。
- 不包含任何业务接口封装，业务模块在 `src/api/modules/` 按正式契约新增。

## 状态管理边界

- **TanStack Query**：后端数据（商品、库存、订单）唯一权威缓存，负责请求状态与失效。
- **Zustand**：仅存放轻量 UI 状态（当前为移动端导航开关）。
- **React Hook Form + Zod**：表单状态与前端校验。

禁止把接口数据复制进 Zustand，也不在组件内直接 `fetch`。

## 测试

```bash
npm run test        # 单元 + 组件测试（MSW 拦截网络）
npm run test:e2e    # Playwright，自动启动 dev server
```

- MSW 的 `src/mocks/handlers.ts` 当前为空：仓库尚无正式 OpenAPI 契约，不伪造业务接口。
  用例内可用 `server.use(...)` 临时注册 handler。
- E2E 使用 Chromium，视口 1440×900，另含 390×844 移动端用例。
- 需要图形依赖时执行：`npx playwright install-deps chromium`。

## OpenAPI 类型生成

```bash
npm run typegen
# openapi-typescript ../contracts/openapi.yaml -o src/api/generated/schema.d.ts
```

仓库当前**没有** `contracts/openapi.yaml`，该命令会在后端 B0 提供正式契约后才可执行。
在此之前不生成、也不手写业务 DTO。

## 边界

本工程只允许修改 `web/**`。后端代码、迁移、Docker、Nginx、Kubernetes、
Prometheus 配置均不在前端职责内。
