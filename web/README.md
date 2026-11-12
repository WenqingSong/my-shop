# SurgeCart 用户商城前端

SurgeCart 用户商城的 PC 优先前端工程。**W0—W4 业务开发已完成，W5 完成最终验收**，
当前覆盖完整用户链路：

- 商品浏览：首页（轮播/分类/推荐/最新上架）、商品列表与搜索、商品详情与 SKU、评价；
- 认证：注册、登录、当前用户、401 单飞刷新、退出登录、受保护路由；
- 个人中心：账户概览、只读资料、收货地址 CRUD 与默认地址、收藏；
- 购物车：加购、数量、勾选/全选、删除、库存与不可购买提示；
- 订单：结算、地址选择、幂等下单、成功页、订单列表与状态筛选、订单详情；
- 履约：模拟支付、取消订单、确认收货；
- 退款：整单退款申请、我的退款、退款详情、撤销申请；
- 优惠券：我的优惠券、结算页选券、用券下单、锁定/核销/释放。

- 设计规范：[`DESIGN_SYSTEM.md`](./DESIGN_SYSTEM.md)
- W5 最终验收报告：[`../docs/reports/w5-final-validation/DELIVERY_REPORT.md`](../docs/reports/w5-final-validation/DELIVERY_REPORT.md)
- W5 开发知识文档：[`../.agent/tasks/dev/W5-final-validation.md`](../.agent/tasks/dev/W5-final-validation.md)
- W4-04 交付报告：[`../docs/reports/w4-04-coupon-center/DELIVERY_REPORT.md`](../docs/reports/w4-04-coupon-center/DELIVERY_REPORT.md)
- W4-03 交付报告：[`../docs/reports/w4-03-refund-center/DELIVERY_REPORT.md`](../docs/reports/w4-03-refund-center/DELIVERY_REPORT.md)
- W4-02 交付报告：[`../docs/reports/w4-02-order-fulfillment/DELIVERY_REPORT.md`](../docs/reports/w4-02-order-fulfillment/DELIVERY_REPORT.md)
- W4-01 交付报告：[`../docs/reports/w4-01-order-center/DELIVERY_REPORT.md`](../docs/reports/w4-01-order-center/DELIVERY_REPORT.md)
- W3-02 交付报告：[`../docs/reports/w3-02-order-checkout/DELIVERY_REPORT.md`](../docs/reports/w3-02-order-checkout/DELIVERY_REPORT.md)
- W3-01 交付报告：[`../docs/reports/w3-01-shopping-cart/DELIVERY_REPORT.md`](../docs/reports/w3-01-shopping-cart/DELIVERY_REPORT.md)
- W2-02 交付报告：[`../docs/reports/w2-02-user-center/DELIVERY_REPORT.md`](../docs/reports/w2-02-user-center/DELIVERY_REPORT.md)
- W2-01 交付报告：[`../docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md`](../docs/reports/w2-01-user-authentication/DELIVERY_REPORT.md)
- W1 交付报告：[`../docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md`](../docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md)
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
│   │   ├── cart/               # 购物车条目行、数量步进器、汇总
│   │   ├── checkout/           # 地址选择、商品清单、优惠券选择、金额摘要
│   │   ├── common/             # 容器、金额、空/错/加载、分页、错误边界
│   │   ├── coupon/             # 优惠券卡片与状态徽标
│   │   ├── layout/             # Header、Footer、认证入口、搜索、移动导航
│   │   ├── order/              # 订单卡片、操作区、进度、状态徽标
│   │   ├── product/            # 商品卡片、栅格、图册、SKU、收藏、评价、分类筛选
│   │   ├── refund/             # 退款卡片、状态徽标、申请/撤销弹窗
│   │   └── ui/                 # shadcn/ui 风格基础组件
│   ├── config/                 # 前端运行时配置（推荐位 code 等）
│   ├── hooks/                  # 购物车/结算/订单/退款/优惠券/收藏/标题
│   ├── layouts/                # 路由布局
│   ├── mocks/                  # MSW handlers 与 Mock 数据（商品/认证/地址/收藏/购物车/订单/退款/优惠券）
│   ├── pages/                  # 首页、商品列表/详情、购物车、结算与成功页、登录、注册、404
│   │   └── account/            # 个人中心布局、概览、资料、地址、收藏、订单、退款、优惠券
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
- `src/api/*.ts`：按域封装的业务 API（products / categories / banners / reviews / recommendations / auth / account / addresses / favorites / cart / orders / refunds / coupons），组件禁止直接 `fetch`。
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

- **TanStack Query**：后端数据（商品、分类、评价、当前用户、购物车、订单、退款、优惠券）唯一权威缓存，负责请求状态与失效。
- **URL**：商品列表的关键词、分类、排序、分页以 URL 查询参数为唯一事实来源。
- **内存 Token Store**：认证凭证的唯一事实来源，通过 `useSyncExternalStore` 驱动 Header 与路由守卫。
- **账号私有缓存**：地址、收藏、购物车、订单、退款、优惠券通过 Query Key 分层管理；
  会话结束（主动退出或 refresh 失败）时由 `clearUserScopedQueries()` 统一清理同一份清单，
  避免切换账号看到上一位用户的数据。新增私有数据必须加入该清单。
- **Zustand**：仅存放轻量 UI 状态（当前为移动端导航开关），不保存认证或业务数据。
- **React Hook Form + Zod**：登录/注册/地址表单状态与前端校验。

禁止把接口数据复制进 Zustand，也不在组件内直接 `fetch`。

## 测试

```bash
npm run test        # 单元 + 组件测试（MSW 拦截网络）
npm run test:e2e    # Playwright，自动启动带 MSW 的 dev server
```

- `src/mocks/handlers.ts` 按真实后端结构提供商品、分类、SKU、评价、轮播、推荐、认证、
  地址、收藏、购物车、订单、退款与优惠券 Mock；
  `keyword=__error__` 是仅用于错误态验证的 Mock 钩子，真实后端不存在。
- 订单/退款/优惠券 Mock 实现与 Go 后端一致的状态闸门、幂等指纹（含 `coupon_id`）、
  库存与券的锁定/核销/释放语义；`seedMockOrder`/`seedMockRefund`/`seedMockCoupon`
  等仅测试可用的造数钩子不会进入生产构建。
- 认证 Mock 覆盖注册、登录、`/me`、refresh 轮换、logout；默认测试账号为 `demo / demo12345`。
- 当前规模：45 个单元/组件测试文件、317 个用例；10 个 E2E spec、46 个用例。
- E2E 通过 `VITE_ENABLE_MOCK=true` 启用浏览器 MSW，**不代表真实后端**；
  真实后端联调结果见 W5 交付报告。推荐位区块通过 `VITE_HOME_RECOMMEND_CODE=home` 启用。
- E2E 串行执行（`workers: 1`），避免 MSW Service Worker 注册与 Vite 依赖优化并发导致空页面。
- 视口 1440×900，另含 390×844 移动端用例；需要图形依赖时执行 `npx playwright install-deps chromium`。

## 生产构建与部署

```bash
npm run build     # tsc -b + vite build → dist/
npm run preview   # 本地验证 dist（含 SPA history fallback）
```

- 构建产物：`dist/` 约 896 KB，主 chunk `index-*.js` 371.2 KB（gzip 117.8 KB）+ CSS 44.6 KB，
  其余为按路由/页面拆分的懒加载 chunk。
- Mock 隔离：
  - 运行时 `main.tsx` 只有 `VITE_ENABLE_MOCK === 'true'` 才动态加载 MSW，生产默认关闭；
  - 构建插件 `stripMockArtifacts` 会从 `dist/` 删除 `mockServiceWorker.js` 与 `mock/` 图片，
    生产产物不包含任何 Mock 资源。
- API 基础路径固定 `/api`，转发层必须剥离该前缀（Go 路由本身不带 `/api`）。
- **部署缺口（基础设施职责，未在本工程修复）**：仓库 `nginx/nginx.conf` 的 `location /`
  原样 `proxy_pass` 到后端，既未剥离 `/api`，也未托管前端 `dist/` 与 `try_files` 回退。
  生产上线前需要基础设施线补齐：
  1. `location /api/ { proxy_pass http://my_shop_backend/; }`（尾部斜杠剥离前缀）；
  2. `location / { root <dist>; try_files $uri $uri/ /index.html; }`；
  3. 保留现有 `/storage/` 静态资源反代与安全响应头。

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
