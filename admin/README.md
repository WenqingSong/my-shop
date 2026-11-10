# SurgeCart 管理后台（admin）

SurgeCart 电商平台的运营管理端前端工程。当前处于 **A1 管理员认证与运营工作台** 阶段：
包含管理员登录、Token 管理、路由认证守卫、前端角色权限映射，以及对接后端五个 Dashboard 接口
的运营工作台。商品/订单/库存/优惠券/秒杀等业务模块留待后续阶段。

## 模板来源与许可证

| 项目 | 说明 |
|---|---|
| 上游项目 | [vue-pure-admin](https://github.com/pure-admin/vue-pure-admin) |
| 版本 | `v7.0.0` |
| 固定提交 | `b8177a202dceb1412d3bf56f3d15483dc1606c9c` |
| 许可证 | MIT（原始许可证见本目录 `LICENSE`，版权归 pure-admin 作者所有） |
| 引入方式 | 源码复制到本仓库 `admin/`，未携带上游 `.git`、`.github`、`husky` 钩子 |

在上游基础上做了以下调整（均记录于 `../docs/reports/A0-admin-frontend-bootstrap.md`，
工程原理与排障过程见 `../.agent/tasks/dev/A0-admin-frontend-bootstrap.md`）：

- 删除与电商管理后台无关的演示页面、演示组件、演示 Mock 与对应依赖。
- 路由改为纯静态路由，移除演示用的后端动态菜单与 Token 刷新逻辑（属于 A1）。
- 部署基础路径改为 `/console/`，路由使用 HTML5 history + base。
- Axios 统一实例改为 `/api` 基础地址，并接好 Vite Proxy。
- Mock 由 `vite-plugin-fake-server` 替换为 MSW。
- 补充 Vitest 单元测试与 OpenAPI 类型生成脚本。
- 开启 TypeScript `strict` 并通过检查。

## 技术栈

| 能力 | 选型 | 版本 |
|---|---|---|
| 框架 | Vue | `^3.5.31` |
| 语言 | TypeScript（`strict: true`） | `^6.0.2` |
| 构建 | Vite | `^8.0.3` |
| UI | Element Plus | `^2.13.6` |
| 路由 | Vue Router（history 模式） | `^5.0.4` |
| 状态 | Pinia | `^3.0.4` |
| 请求 | Axios | `1.14.0` |
| 图表 | ECharts（模板自带，A0 未使用） | `^6.0.0` |
| Mock | MSW | `^3.0.4` |
| 类型生成 | openapi-typescript | `^7.13.0` |
| 测试 | Vitest + jsdom | `^5.1.0` |
| 代码规范 | ESLint + Prettier + Stylelint | 见 `package.json` |

包管理器：**pnpm**（唯一锁文件 `pnpm-lock.yaml`）。

## 环境要求

- Node.js `^20.19.0 || >=22.13.0`
- pnpm `>=9`

## 快速开始

```bash
cd admin
pnpm install
pnpm dev
```

访问 <http://localhost:8848/console/>（开发服务器同样挂载在 `/console/` 子路径下）。

## 可用命令

| 命令 | 说明 |
|---|---|
| `pnpm dev` | 启动开发服务器（端口 8848，路径 `/console/`） |
| `pnpm build` | 生产构建，产物在 `dist/`，资源前缀 `/console/` |
| `pnpm preview` | 预览生产构建（端口 8848，路径 `/console/`） |
| `pnpm typecheck` | `vue-tsc --noEmit` 类型检查（strict 开启） |
| `pnpm test` | Vitest 单次运行 |
| `pnpm test:watch` | Vitest 监听模式 |
| `pnpm lint` | ESLint + Prettier + Stylelint（自动修复） |
| `pnpm lint:eslint:check` | ESLint 只检查不修复（CI 用） |
| `pnpm lint:prettier:check` | Prettier 只检查不修复（CI 用） |
| `pnpm api:types` | 从 `contracts/openapi.yaml` 生成 TypeScript 类型 |

## 环境变量

| 变量 | development | production | 说明 |
|---|---|---|---|
| `VITE_PUBLIC_PATH` | `/console/` | `/console/` | Vite `base`，静态资源前缀 |
| `VITE_ROUTER_HISTORY` | `h5,/console/` | `h5,/console/` | HTML5 history + base |
| `VITE_API_BASE_URL` | `/api` | `/api` | Axios `baseURL` |
| `VITE_PROXY_TARGET` | `http://127.0.0.1:8000` | 不生效 | 开发代理转发目标 |
| `VITE_MOCK_ENABLED` | `false` | `false` | 是否启用 MSW |
| `VITE_PORT` | `8848` | — | 开发服务器端口 |

本地私有覆盖请使用 `.env.development.local` / `.env.production.local`，该模式已被 `.gitignore`
（`*.local`、`.env.*.local`）忽略。**禁止在环境文件中写入真实 Token、密码或密钥。**

## 接口代理

开发环境通过 Vite Proxy 转发，**不改动 Go 后端路由**：

```
浏览器 /api/admin/login  --(Vite Proxy, rewrite 去掉 /api)-->  http://127.0.0.1:8000/admin/login
```

- 代理目标来自 `VITE_PROXY_TARGET`，默认 `http://127.0.0.1:8000`。
- 该默认值依据仓库已有配置确认：`manifest/config/config.yaml` 的 `server.address: ":8000"`
  （可由 `SERVER_ADDRESS` 覆盖）。若本地后端端口不同，请在 `.env.development.local` 中覆盖
  `VITE_PROXY_TARGET`，无需改动代码。

## OpenAPI 类型生成

后端将在仓库根目录维护 `contracts/openapi.yaml`。管理端不复制、不伪造该契约：

```bash
pnpm api:types   # 生成 src/api/generated/schema.d.ts
```

**当前状态：等待后端 B0 契约交付。** 契约文件不存在时脚本会明确报错退出，不会生成空文件或
编造的接口类型。契约交付后，先执行该命令生成类型，再在 `src/api/modules/` 中编写业务请求方法。

如需指定其他契约路径：`OPENAPI_SPEC=../xxx.yaml pnpm api:types`。

## Mock（MSW）

- 启动条件：`VITE_MOCK_ENABLED=true`（默认关闭）。
- 访问路径：`${BASE_URL}mockServiceWorker.js`，即 `/console/mockServiceWorker.js`。
- 扩展入口：`src/mocks/handlers/index.ts`，新增 `http.get("/api/xxx", resolver)` 并加入数组即可。
- 与真实请求层解耦：Mock 在网络层拦截，业务代码始终使用 `src/utils/http` 的 Axios 实例。
- 当前 handlers 覆盖已确认契约的两类接口：管理员认证（`/admin/login|me|logout`）与
  运营工作台（`/admin/dashboard/*`）。数值均为演示夹具，**不是真实业务数据**。
- 演示用 Mock 账号：用户名 `admin`，密码 `mock-password`（仅 Mock 环境有效）。

## 目录结构

```
admin/
├── build/                 # Vite 构建辅助（插件、优化、构建信息）
├── locales/               # 中英文文案
├── public/                # 静态资源（favicon、platform-config.json、MSW worker）
├── scripts/               # api:types 生成脚本
├── src/
│   ├── api/
│   │   ├── generated/     # OpenAPI 生成的类型（禁止手工编辑）
│   │   ├── modules/       # 按业务域拆分的请求方法（admin-auth、dashboard）
│   │   ├── types/         # 统一 envelope 与业务错误
│   │   └── index.ts       # API 层统一出口
│   ├── assets/            # 图标、字体、图片
│   ├── constants/         # 订单状态枚举等跨页面常量
│   ├── components/        # 通用组件（ReIcon/ReDialog/ReDrawer/ReSegmented 等）
│   ├── config/            # 运行时平台配置读取
│   ├── directives/        # 自定义指令
│   ├── layout/            # 后台布局（侧边栏、导航栏、标签页、设置面板）
│   ├── mocks/             # MSW 启动入口与 handlers（认证、工作台）
│   ├── plugins/           # element-plus / echarts / i18n 插件
│   ├── router/            # 路由表、history、工具函数
│   ├── store/             # Pinia 状态（app/settings/user/multiTags/permission）
│   ├── style/             # 全局样式
│   ├── types/             # 全局类型声明
│   ├── utils/             # 工具（http 统一实例、auth、permission、money、datetime）
│   └── views/
│       ├── dashboard/     # 工程状态页（A0 占位首页）
│       ├── error/         # 403 / 404 / 500
│       └── login/         # 登录页占位（不含认证逻辑）
├── tests/unit/            # Vitest 单元测试
├── .env / .env.development / .env.production
├── package.json
├── tsconfig.json
├── vite.config.ts
└── vitest.config.ts
```

## 路由

| 路径 | 页面 | 说明 |
|---|---|---|
| `/console/` | 重定向到 `/console/dashboard` | 首页 |
| `/console/dashboard` | 运营工作台 | 需登录，展示五个 Dashboard 接口数据 |
| `/console/login` | 管理员登录 | 已登录访问会自动回到工作台 |
| `/console/access-denied` | 403 | 权限不足页面（后续业务页面使用） |
| `/console/server-error` | 500 | 错误页 |

## 认证与权限

- 登录：`POST /admin/login` → 保存 `access_token` 与 `expires_at` 到 **sessionStorage** →
  `GET /admin/me` 校验身份 → 进入工作台。不使用 Cookie，不保存 Refresh Token。
- 刷新：页面刷新后由路由守卫调用 `GET /admin/me` 恢复身份，管理员信息只存在 Pinia 运行时状态。
- 失效：HTTP 401 触发统一清理（token + 状态 + 路由）并跳转登录页；并发 401 只处理一次。
- 权限：后端 `/admin/me` 目前只返回 `is_super` 与 `roles`，前端使用
  `src/utils/permission.ts` 的角色映射（当前仅登记 `super_admin`）。未知角色默认无权限。
  **前端权限判断只影响界面呈现，不是安全边界**；真正的授权由后端 `RequirePermission` 执行。
- 403：不清理登录态，由工作台分区展示"权限不足"。

## 部署约定（供后续阶段参考，A1 未修改任何部署配置）

- 前端静态资源部署在 `/console/`，服务器需对 `/console/*` 做 SPA fallback 到
  `/console/index.html`，否则刷新子路由会 404。
- 生产环境 `VITE_API_BASE_URL=/api`：需由网关将 `/api/*` 去掉前缀转发到 Go 后端
  （与开发代理行为保持一致）。
- A0/A1 均**未修改** `Dockerfile`、`docker-compose.yml`、`nginx/`、`deploy/`、Kubernetes 清单。

## 阶段边界

A1 已完成：管理员登录、Token 管理、路由认证守卫、基础角色权限映射、运营工作台（五个 Dashboard 接口）。

A1 不包含：商品/订单/库存/优惠券/秒杀活动/用户/管理员/角色/权限等管理页面，也不包含
Refresh Token 与动态权限菜单。这些从 A2 开始按阶段推进。
