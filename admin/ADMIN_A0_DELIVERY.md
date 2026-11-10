# SurgeCart 管理后台 — A0 工程初始化交付报告

- 阶段：A0（工程基础设施，不含任何电商业务）
- 分支：`feat/phase2-admin-frontend`
- 代码提交：`a827674a75098a94ffd388bbfb212a79c95e9e69`（`feat(admin): initialize Vue admin frontend`）
- 执行日期：2026-11-10

## 1. 模板来源与版本

| 项目 | 内容 |
|---|---|
| 上游项目 | vue-pure-admin（https://github.com/pure-admin/vue-pure-admin） |
| 版本 | `v7.0.0` |
| 固定提交 SHA | `b8177a202dceb1412d3bf56f3d15483dc1606c9c` |
| 许可证 | MIT（原始 `LICENSE` 已保留于 `admin/LICENSE`，版权归 pure-admin 作者） |
| 引入方式 | 源码复制至 `admin/`；未复制上游 `.git`、`.github`、`.gitpod.yml`、`.husky`、`Dockerfile`、`.vscode`、`.npmrc`、CHANGELOG/README，未触碰本仓库 Git 历史 |

## 2. 技术栈

| 能力 | 选型 |
|---|---|
| 框架 | Vue `^3.5.31` |
| 语言 | TypeScript `^6.0.2`（`strict: true`） |
| 构建 | Vite `^8.0.3` |
| UI | Element Plus `^2.13.6` |
| 路由 | Vue Router `^5.0.4`（HTML5 history + base） |
| 状态 | Pinia `^3.0.4` |
| 请求 | Axios `1.14.0`（统一实例） |
| 图表 | ECharts `^6.0.0`（模板自带，A0 未在页面中使用） |
| Mock | MSW `^3.0.4` |
| 类型生成 | openapi-typescript `^7.13.0` |
| 测试 | Vitest `^5.1.0` + jsdom |
| 规范 | ESLint 10 + Prettier 3 + Stylelint 17 |
| 包管理器 | pnpm 9（唯一锁文件 `pnpm-lock.yaml`） |

## 3. 实际目录结构

```
admin/
├── build/                     # Vite 构建辅助（plugins / optimize / compress / info / utils）
├── locales/                   # 中英文文案（zh-CN / en）
├── public/                    # favicon、logo、platform-config.json、mockServiceWorker.js
├── scripts/
│   └── generate-api-types.mjs # api:types 生成脚本（含契约缺失保护）
├── src/
│   ├── api/
│   │   ├── generated/         # OpenAPI 生成产物目录（当前仅 .gitkeep）
│   │   ├── modules/           # 业务请求模块目录（当前仅 .gitkeep）
│   │   └── index.ts           # API 层统一出口
│   ├── assets/                # 图标、字体、图片
│   ├── components/            # ReAuth / ReDialog / ReDrawer / ReIcon / RePerms / ReSegmented / ReText
│   ├── config/                # platform-config.json 读取
│   ├── directives/            # auth / copy / longpress / optimize / perms / ripple
│   ├── layout/                # 布局（sidebar / navbar / tag / search / notice / setting / frame）
│   ├── mocks/                 # MSW：index.ts、browser.ts、handlers/index.ts
│   ├── plugins/               # echarts / elementPlus / i18n
│   ├── router/                # index.ts、history.ts、utils.ts、enums.ts、modules/{home,remaining}.ts
│   ├── store/                 # app / epTheme / multiTags / permission / settings / user
│   ├── style/                 # reset / index / tailwind / sidebar 等
│   ├── types/                 # 全局类型声明
│   ├── utils/                 # http/、auth.ts、message.ts、mitt.ts、tree.ts、progress/ 等
│   └── views/
│       ├── dashboard/index.vue
│       ├── error/{403,404,500}.vue
│       └── login/index.vue
├── tests/unit/
│   ├── http.test.ts
│   └── router.test.ts
├── .env / .env.development / .env.production
├── .gitignore
├── LICENSE
├── README.md
├── package.json
├── pnpm-lock.yaml
├── tsconfig.json
├── vite.config.ts
└── vitest.config.ts
```

## 4. 主要配置

### 4.1 部署路径与路由

- `VITE_PUBLIC_PATH=/console/` → Vite `base`，构建产物资源前缀为 `/console/`。
- `VITE_ROUTER_HISTORY="h5,/console/"` → `createWebHistory("/console/")`，刷新不依赖 hash。
- 静态路由：`/`（重定向 `/dashboard`）、`/dashboard`、`/login`、`/access-denied`、`/server-error`、`/redirect/:path(.*)`、`/:pathMatch(.*)*`（404）。
- A0 不启用动态路由与权限过滤：`initRouter()` 仅注册静态菜单；`meta.roles` 校验、Token 守卫显式留白并注释说明归属 A1。

### 4.2 请求层（`src/utils/http/index.ts`）

- 单例 Axios，`baseURL = import.meta.env.VITE_API_BASE_URL`（默认 `/api`），`timeout = 10000`。
- 默认 JSON 请求头（`Content-Type: application/json`、`Accept`），数组参数用 `qs` 序列化。
- 响应拦截返回 `response.data`，**不假设后端 envelope**（业务层自行解析与声明类型）。
- 统一错误入口 `setHttpErrorHandler()`：默认使用 Element Plus `Message`；HTTP 状态码映射（400/401/403/404/408/409/413/422/429/500/502/503/504）、超时（`ECONNABORTED`/`ETIMEDOUT`）、网络异常分别归一化；主动取消的请求不提示。
- 未注入 Token、未实现刷新逻辑（A1）。

### 4.3 开发代理（`vite.config.ts`）

```
/api/admin/login  --(rewrite 去除 /api)-->  VITE_PROXY_TARGET + /admin/login
```

- 默认 `VITE_PROXY_TARGET=http://127.0.0.1:8000`。
- 依据：仓库现有 `manifest/config/config.yaml` 中 `server.address: ":8000"`（可被 `SERVER_ADDRESS` 覆盖），
  `internal/cmd/routes_admin.go` 中后台路由前缀为 `/admin`（`/admin/login` 公开）。
- 端口通过环境变量配置，可本地覆盖，**未修改任何 Go 后端代码**。
- `preview` 复用同一代理，便于本地联调构建产物。

### 4.4 MSW

- `src/main.ts` 仅在 `VITE_MOCK_ENABLED === "true"` 时动态 `import("@/mocks")`。
- Worker 地址 `${BASE_URL}mockServiceWorker.js`，与 `/console/` 部署路径一致。
- `onUnhandledFrame: "bypass"`（MSW v3 API），未 Mock 的请求照常走真实后端。
- handlers 为空数组，**没有任何编造的电商业务数据或生产 API**。

### 4.5 OpenAPI 类型生成

- `pnpm api:types` → `scripts/generate-api-types.mjs` → `src/api/generated/schema.d.ts`。
- 默认读取 `../contracts/openapi.yaml`，支持 `OPENAPI_SPEC` 覆盖。
- 契约不存在时脚本以非零码退出并打印明确说明，不生成空文件、不编造类型。

### 4.6 从模板移除的内容

- 演示页面：`views/` 下 27 个目录（table、form、flow-chart、editor、chatai、system、permission、result、about 等）全部删除，仅保留 `login`、`error`、新建 `dashboard`。
- 演示路由模块：23 个 `router/modules/*.ts` 删除，仅保留 `home.ts`（改造为 dashboard）、`remaining.ts`（精简）。
- 演示组件：删除 17 个（ReCropper、ReEditor、ReFlowChart、ReVxeTableBar、ReMap、ReQrcode、ReTypeit 等），保留布局与权限骨架所需 7 个。
- 演示 Mock：`mock/` 目录删除，改接 MSW。
- 依赖清理：移除 43 个运行时依赖（vxe-table、wangeditor、codemirror、xgplayer、mqtt、xlsx、swiper、vuedraggable、@vue-flow/*、@logicflow/*、plus-pro-components、version-rocket、qrcode 等）与 12 个开发依赖（commitlint、husky、lint-staged、faker、dagre、vite-plugin-fake-server 等）。
- 演示资源：`public/audio`、`public/html`、`public/wasm` 删除。

## 5. 开发启动方式

```bash
cd admin
pnpm install
pnpm dev     # http://localhost:8848/console/
```

## 6. 测试命令与实际结果

| 校验项 | 命令 | 实际结果 |
|---|---|---|
| 类型检查（strict） | `pnpm typecheck` | 通过，0 error（`tsconfig.json` 中 `strict: true`） |
| ESLint | `pnpm lint:eslint:check` | 通过，0 error / 0 warning（`--max-warnings 0`） |
| 单元测试 | `pnpm test` | 通过，2 个测试文件 / 11 个用例全绿 |
| 生产构建 | `pnpm build` | 成功，`✓ built in 4.21s`，产物约 3 MB |
| 产物前缀 | 检查 `dist/index.html` | 资源引用为 `/console/static/...`、`/console/favicon.ico` |

单元测试覆盖：

- `tests/unit/http.test.ts`（5 用例）：默认 `baseURL=/api`、超时 10s、JSON 请求头；响应拦截直接返回 `data`；网络异常、502、超时分别归一化为对应提示并进入统一错误入口。
- `tests/unit/router.test.ts`（6 用例）：`/` 重定向 `/dashboard`；`/dashboard` 路由与懒加载组件存在；`/login` 不参与菜单；403/500/redirect 路由存在；`h5,/console/` 的 history base 为 `/console`；配置缺失时回退 h5 且无 base。

### 页面访问验证（开发服务器，实际执行）

| 路径 | HTTP 状态 |
|---|---|
| `http://127.0.0.1:8848/console/` | 200 |
| `http://127.0.0.1:8848/console/login` | 200 |
| `http://127.0.0.1:8848/console/dashboard` | 200 |

### 页面访问验证（生产预览 `pnpm preview`，实际执行）

| 路径 | HTTP 状态 |
|---|---|
| `http://127.0.0.1:8848/console/` | 200 |
| `http://127.0.0.1:8848/console/login` | 200 |
| `http://127.0.0.1:8848/console/dashboard` | 200 |

### 代理验证（实际执行）

`curl -X POST http://127.0.0.1:8848/api/admin/login` 返回 `502`（本机未启动 Go 后端，
代理已尝试连接 `127.0.0.1:8000` 且被拒），证明 `/api` 由代理接管而非落入前端路由。

> 说明：`vite preview` 对不存在的静态资源也会回退到 `index.html`（返回 200），
> 这是预览服务器行为；真实部署需要服务器对 `/console/*` 做 SPA fallback，见 README「部署约定」。

## 7. Git 提交与推送

- 代码提交：`a827674a75098a94ffd388bbfb212a79c95e9e69`
- 提交信息：`feat(admin): initialize Vue admin frontend`
- 变更范围：仅 `admin/**`（204 个文件），未修改 `internal/**`、`api/**`、`migrations/**`、`contracts/**`、`web/**`、`Dockerfile`、`docker-compose.yml`、`deploy/**`、`nginx/`、`prometheus/`、Kubernetes 清单、根目录 `.gitignore`。
- 推送结果：见本报告提交后的推送记录（分支 `feat/phase2-admin-frontend`，普通 push，未 force）。

## 8. 尚未解决的问题

1. **OpenAPI 契约未交付**：仓库根目录暂无 `contracts/openapi.yaml`，`src/api/generated/` 为空。
   `pnpm api:types` 已配置完成但需等后端 B0 交付后才能生成真实类型。
2. **后端未在本地运行**：代理链路只验证到「请求被转发、连接被拒」层面，未做真实的
   `/api/admin/login → /admin/login` 端到端联调（A0 不实现登录，属预期）。
3. **登录页为占位实现**：不调用任何接口、不写入 Token，点击「进入控制台」直接跳转 dashboard。
4. **未实现认证与权限**：路由守卫中的 Token 校验、`meta.roles` 判断、动态菜单加载均留白，
   当前所有页面可直接访问（A1 处理）。
5. **模板遗留代码的严格类型补充**：为在 `strict: true` 下通过检查，对布局/组件层的动态结构
   补充了显式 `any`、可选链与默认值（集中在 `src/layout`、`src/components`，未改动运行时逻辑）。
   该层类型约束仍弱于业务层，后续新增组件应保持严格类型，不沿用 `any`。
6. **i18n 文案沿用模板**：`locales/` 保留中英文资源（菜单、按钮、状态提示），未做电商化改写。
7. **部署配置未动**：`/console/` 的服务器 SPA fallback、`/api` 反代规则需在部署阶段实现
   （本阶段按边界要求未修改 `nginx/`、`Dockerfile`、`docker-compose.yml`、`deploy/`）。
8. **browserslist 数据提示**：构建时提示 caniuse-lite 数据 8 个月未更新，不影响构建，未执行
   `update-browserslist-db`（避免引入无关变更）。

## 9. A1 的启动条件

A1（管理员登录与认证）开始前需满足：

1. 后端 B0 交付 `contracts/openapi.yaml`，且至少包含 `POST /admin/login`、`GET /admin/me`、
   `POST /admin/logout` 的请求/响应 schema（含 envelope、错误码、Token 字段定义）。
2. 在 `admin/` 执行 `pnpm api:types` 成功生成 `src/api/generated/schema.d.ts`。
3. 确认 Token 存储方式（Cookie 或 localStorage）、过期/刷新策略，并与安全要求对齐。
4. 确认登录页所需的验证码/二次验证等能力是否在 B0 契约范围内。
5. 明确 `/console/` 生产部署的服务器 fallback 与 `/api` 反代规则（需 Owner 授权修改部署文件）。

满足后，A1 的改动集中在：`src/api/modules/`、`src/store/modules/user.ts`、`src/views/login/`、
`src/utils/http/index.ts`（注入 Token）、`src/router/index.ts`（恢复守卫）、`src/utils/auth.ts`。
