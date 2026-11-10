# A0 开发知识文档：管理后台前端工程初始化

| 项 | 内容 |
|---|---|
| 任务 | A0 — SurgeCart 管理后台前端工程初始化（vue-pure-admin 集成） |
| 分支 | `feat/phase2-admin-frontend` |
| 代码提交 | `a827674a75098a94ffd388bbfb212a79c95e9e69`（`feat(admin): initialize Vue admin frontend`） |
| 正式交付报告 | `docs/reports/A0-admin-frontend-bootstrap.md` |
| 代码位置 | 仓库根目录 `admin/` |
| 文档用途 | 记录本次工程初始化的架构事实、核心原理、实现映射、决策依据与真实排障过程 |

> 阅读约定：本文所有结论都对应仓库中的实际文件或实际执行过的命令输出。
> 未验证的内容会显式标注状态，不作为"已可用"的结论。

---

## 1. 工程架构

### 1.1 模板来源与版本

| 项 | 值 | 证据位置 |
|---|---|---|
| 上游项目 | vue-pure-admin | `admin/LICENSE`、`admin/README.md` |
| 版本 | `v7.0.0` | `admin/README.md`「模板来源与许可证」 |
| 固定提交 | `b8177a202dceb1412d3bf56f3d15483dc1606c9c` | 同上 |
| 许可证 | MIT（原始版权声明保留） | `admin/LICENSE` |

引入方式是**源码复制**，不是 git submodule、不是 `create-vue` 脚手架，也不是 npm 依赖。
因此：

- 模板没有带入上游 `.git` 目录，SurgeCart 仓库的 Git 历史保持线性。
- 上游的 `.github/workflows`、`.husky`、`commitlint`、`Dockerfile`、`.vscode` 均未复制
  （上游 CI/hook 会与本仓库既有规范冲突）。
- 后续升级上游只能靠人工比对，没有自动化升级通道——这是复制的代价，属已知限制。

### 1.2 技术栈构成

| 层 | 选型 | 声明版本 | 说明 |
|---|---|---|---|
| 视图 | Vue | `^3.5.31` | 全部使用 `<script setup lang="ts">` |
| 类型 | TypeScript | `^6.0.2` | `tsconfig.json` 中 `"strict": true` |
| 构建 | Vite | `^8.0.3` | 内部使用 rolldown 构建管线（`build.rolldownOptions`） |
| 路由 | Vue Router | `^5.0.4` | HTML5 history + base |
| 状态 | Pinia | `^3.0.4` | 模块化 store，`src/store/modules/*` |
| UI | Element Plus | `^2.13.6` | 显式导入组件 + 全局注册 + 全量 CSS |
| 请求 | Axios | `1.14.0` | 类 `PureHttp` 单例封装 |
| 图表 | ECharts | `^6.0.0` | `src/plugins/echarts.ts`，A0 页面未使用 |
| Mock | MSW | `^3.0.4` | `src/mocks/*`，默认关闭 |
| 类型生成 | openapi-typescript | `^7.13.0` | `scripts/generate-api-types.mjs` |
| 测试 | Vitest + jsdom | `^5.1.0` / `^31.0.0` | `tests/unit/*` |

包管理器为 pnpm，锁文件只保留 `admin/pnpm-lock.yaml`。

### 1.3 启动与构建流程

**开发启动**（`pnpm dev` → `vite`）：

1. `vite.config.ts` 读取 `.env` + `.env.development`，经 `build/utils.ts` 的 `wrapperEnv()`
   解析成强类型 `ViteEnv`。
2. `base = VITE_PUBLIC_PATH = /console/`，因此开发地址是
   `http://localhost:8848/console/`，而不是根路径 `/`。
3. `server.proxy` 按 `VITE_PROXY_TARGET` 注册 `/api` 代理。
4. 浏览器加载 `/console/` → `index.html` → `/console/src/main.ts`。

**生产构建**（`pnpm build` → `rimraf dist && vite build`）：

1. 入口 `index.html`，资源前缀 `base=/console/`。
2. 产物分目录：`static/js/[name]-[hash].js`、`static/css/[ext]/[name]-[hash].[ext]`
   （`build.rolldownOptions.output`）。
3. `public/` 下文件原样复制到 `dist/` 根（`favicon.ico`、`logo.svg`、
   `platform-config.json`、`mockServiceWorker.js`）。
4. 实测产物：`dist/index.html` 引用 `/console/static/js/index-*.js`，
   总体积约 3 MB。

### 1.4 主要目录及职责

| 目录 | 职责 | A0 中的状态 |
|---|---|---|
| `build/` | Vite 配置辅助：`plugins.ts`（插件装配）、`optimize.ts`（预构建依赖）、`utils.ts`（别名/env 解析）、`info.ts`（构建横幅） | 沿用模板，删除无用插件 |
| `locales/` | 中英文文案（`zh-CN.yaml`、`en.yaml`） | 沿用，未电商化改写 |
| `public/` | 无需编译的静态资源；`platform-config.json` 是运行时配置，`mockServiceWorker.js` 是 MSW worker | 保留 4 个文件 |
| `scripts/` | 工程脚本 | A0 新增 `generate-api-types.mjs` |
| `src/api/` | 请求层出口：`index.ts` 导出 `http`；`generated/`（OpenAPI 产物）、`modules/`（业务请求）为空占位 | 仅骨架 |
| `src/assets/` | 图标、字体、图片、SVG | 删除演示图片 |
| `src/components/` | 通用组件 `ReIcon`、`ReDialog`、`ReDrawer`、`ReSegmented`、`ReAuth`、`RePerms`、`ReText` | 只剩布局骨架依赖的 7 个 |
| `src/config/` | 运行时平台配置（`platform-config.json`）读取与 `$config` 注入 | 沿用 |
| `src/directives/` | 自定义指令：`auth`、`perms`、`copy`、`longpress`、`optimize`、`ripple` | 沿用，A0 不使用 |
| `src/layout/` | 后台布局：侧边栏、导航栏、标签页、搜索、通知、设置面板、iframe 容器 | 沿用并修正类型 |
| `src/mocks/` | MSW 启动入口、worker 实例、handlers | 新建，handlers 为空 |
| `src/plugins/` | `elementPlus.ts`、`echarts.ts`、`i18n.ts` | 沿用 |
| `src/router/` | `index.ts`（实例+守卫）、`history.ts`（history 模式）、`utils.ts`（路由工具/初始化）、`modules/`（静态路由表） | 大幅精简，新增 `history.ts` |
| `src/store/` | Pinia：`app`、`epTheme`、`multiTags`、`permission`、`settings`、`user` | 沿用 |
| `src/style/` | 全局样式、Tailwind、Element Plus 定制变量 | 沿用 |
| `src/types/` | 全局类型声明（`router.d.ts`、`global.d.ts` 等） | 沿用 |
| `src/utils/` | `http/`（统一实例）、`auth.ts`、`message.ts`、`mitt.ts`、`tree.ts`、`progress/` 等 | `http/` 改造，`auth.ts` 保留骨架 |
| `src/views/` | `dashboard/`（A0 占位首页）、`login/`（占位）、`error/`（403/404/500） | 只剩 3 个目录 |
| `tests/` | Vitest 用例 | 新建 2 个文件 11 个用例 |

---

## 2. 前端工程核心原理

### 2.1 Vite 的 base 与部署子路径

`base` 决定三件事，缺一不可：

1. **HTML 中资源引用前缀**：`index.html` 里 `<script src="/console/static/js/index-*.js">`。
2. **`import.meta.env.BASE_URL`**：运行时读取的基路径，本项目用它拼
   `mockServiceWorker.js` 的注册地址（`src/mocks/index.ts`）。
3. **开发服务器挂载点**：dev server 把应用挂在 `/console/`，根路径 `/` 不再返回应用。

`base` 与路由 `base` 必须一致，否则会出现「资源 404」或「路由把 `/console` 当成业务路径」。
本项目在 `.env.*` 中用 `VITE_PUBLIC_PATH=/console/` 与 `VITE_ROUTER_HISTORY="h5,/console/"`
成对配置，两处都从环境变量读取，避免硬编码偏移。

### 2.2 Vue Router 的 History 模式与 SPA 回退

`createWebHistory("/console/")` 产生的 URL 是 `/console/dashboard`（真实路径，无 `#`）。
浏览器直接访问或刷新该 URL 时，服务器上并不存在这个文件，必须由服务器把
`/console/*` 回退到 `/console/index.html`，前端路由才能接管。

因此「刷新 404」几乎总是服务器配置问题，而不是前端问题：

- 开发环境：Vite dev server 内置 SPA fallback，直接可用。
- `vite preview`：同样内置 fallback。
- 生产环境：依赖 Nginx/Apache 的 `try_files`（**A0 未修改任何部署配置**，属部署阶段任务）。

`vue-router` 会规范化 base，`createWebHistory("/console/").base` 实际是 `/console`
（去掉尾部斜杠）。这个细节在单元测试里踩到过，见第 5 节。

本项目 `src/router/history.ts` 用一个函数把 `"h5,/console/"` 解析为
`createWebHistory("/console/")`，配置缺失时兜底为 `createWebHistory("")`，
避免 `undefined.split(",")` 直接抛错。

### 2.3 Pinia 的状态管理机制

`src/store/index.ts` 只做一件事：`createPinia()` 并在 `setupStore(app)` 里注册。
模块按域拆分（`src/store/modules/*.ts`），业务组件通过
`useAppStoreHook()` / `useUserStoreHook()` 这类包装函数访问。

本次初始化中 Pinia 的作用是「承载布局状态」而非业务状态：

- `app`：侧边栏折叠、设备类型、布局模式。
- `settings`：主题色、暗黑模式、灰色模式。
- `multiTags`：多标签页缓存（含 `sessionStorage` 持久化）。
- `permission`：`wholeMenus`、`flatteningRoutes`、`cachePageList`；A0 只装载静态路由。

关键点：`permission` store 同时被**路由初始化**（`src/router/utils.ts` 的
`handleAsyncRoutes`）和**菜单渲染**（`src/layout/**`）读取，是路由与 UI 的耦合点。
A1 接入动态菜单时，改动集中在这里，而不是散落到各个页面。

### 2.4 Element Plus 的集成方式

本项目**没有**使用 `unplugin-vue-components` 自动按需导入，而是
`src/plugins/elementPlus.ts` 显式 `import` 组件与插件，再 `app.component(name, comp)` +
`app.use(plugin)` 全局注册，样式来自 `main.ts` 的 `import "element-plus/dist/index.css"`。

代价与收益：

- 收益：注册关系显式、IDE 跳转可靠、不存在自动导入的「组件名拼错却静默失败」。
- 代价：进入主 chunk 的组件很多，`index-*.js` 实测约 1.9 MB（gzip 约 611 kB）。
  这是 A0 的已知体积问题，A1 之前可优化为真正的按需导入。

`ElMessage` / `ElMessageBox` 等以插件形式注册，同时 `src/utils/message.ts` 又直接
`import { ElMessage }` 以支持在非组件上下文（如 Axios 拦截器）调用——两条路径并存是有意为之。

### 2.5 Axios 实例、拦截器与错误归一化

`src/utils/http/index.ts` 用一个类 `PureHttp` 封装单例：

- `defaultConfig`：`baseURL = import.meta.env.VITE_API_BASE_URL || "/api"`、
  `timeout = 10000`、`Content-Type: application/json`、`Accept`，
  `paramsSerializer` 用 `qs.stringify`（数组参数兼容后端常见格式）。
- 请求拦截：只支持 `beforeRequestCallback` 钩子；**Token 注入位置留空并加注释**（A1）。
- 响应拦截：成功分支直接 `return response.data`。
- 失败分支：先 `Axios.isCancel()` 判定主动取消（路由切换时不弹错误），
  再交给统一的 `errorHandler(resolveErrorText(error), error)`。

`resolveErrorText()` 的优先级是「超时 → HTTP 状态码 → 网络异常」：

- `ECONNABORTED` / `ETIMEDOUT` → 「请求超时，请稍后重试」
- 有 `response` → 查 `httpStatusMessages`（400/401/403/404/408/409/413/422/429/500/502/503/504）
- 无 `response` → 「网络异常，请检查网络连接后重试」

`setHttpErrorHandler()` 把提示出口抽成可替换函数，默认 `message(text, { type: "error" })`。
单测正是利用这个出口断言「错误被归一化成什么文案」，而不去断言 UI 弹窗。

### 2.6 Vite Proxy 的转发与 rewrite

`vite.config.ts` 的 `createProxy(target)`：

```ts
"/api": {
  target,
  changeOrigin: true,
  rewrite: (path) => path.replace(/^\/api/, "")
}
```

三个参数各自解决的问题：

- 匹配：浏览器请求 `/api/...` 命中该规则，dev server 转发到 `target`（默认 `http://127.0.0.1:8000`）。
- `changeOrigin`：把 `Host` 头改成目标主机，避免后端/网关按 Host 校验拒绝。
- `rewrite`：把路径前缀 `/api` **去掉**，因为 Go 后端实际路由是 `/admin/login`，
  不是 `/api/admin/login`。

所以 `/api/admin/login` → `http://127.0.0.1:8000/admin/login`。
dev 与 preview 复用同一个 `createProxy`，保证两者行为一致。

### 2.7 MSW 的 Service Worker 拦截机制

MSW 不是「在 Axios 上加一层假数据」，而是在**网络层**拦截：

1. 浏览器注册 `mockServiceWorker.js` 为 Service Worker。
2. `setupWorker(...handlers)` 把 handler 列表交给 SW。
3. 页面发出的 `fetch`/`XHR` 被 SW 捕获；命中 handler 则由 SW 直接构造响应，未命中则放行到网络。

这带来两个工程性质：

- 业务代码零感知：`src/utils/http` 的 Axios 实例照常发请求，不需要 `if (mock)` 分支。
- 与真实 API 客户端解耦：关掉 Mock 等于 SW 不启动，请求直接到后端。

本项目 `src/mocks/index.ts` 的启动参数：

- `serviceWorker.url = ${import.meta.env.BASE_URL}mockServiceWorker.js`
  → 子路径部署时注册地址是 `/console/mockServiceWorker.js`（若写死 `/mockServiceWorker.js` 会 404）。
- `onUnhandledFrame: "bypass"` → 未显式 Mock 的请求直接透传到真实后端，避免"Mock 忘写导致整站 404"。
- 由 `main.ts` 动态 `import("@/mocks")`，且被 `VITE_MOCK_ENABLED === "true"` 包裹。

### 2.8 OpenAPI 类型生成流程

```
后端维护 contracts/openapi.yaml
        │
        ├─ pnpm api:types
        │     └─ scripts/generate-api-types.mjs
        │           ├─ 校验契约文件是否存在（不存在 → 打印说明 + exit 1）
        │           └─ 调用 openapi-typescript → src/api/generated/schema.d.ts
        │
        └─ src/api/modules/*.ts 引用生成的类型定义请求函数
```

`openapi-typescript` 只生成**类型**（`.d.ts`），不生成运行时 client。
这是刻意的：运行时逻辑（Axios 实例、错误归一化）由前端自己控制，
类型只负责和后端契约对齐，避免引入一个黑盒 client 与统一实例打架。

生成脚本的关键设计是**拒绝为空**：契约缺失时脚本失败退出，而不是生成一个空文件——
空文件会让 `import type { paths } from "@/api/generated/schema"` 编译通过但内容全错。

---

## 3. SurgeCart 中的具体实现

### 3.1 `/console/` 如何成为前端部署前缀

链路上有四个独立环节，缺一不可：

| 环节 | 文件 | 内容 |
|---|---|---|
| 构建 base | `admin/vite.config.ts:35` | `base: VITE_PUBLIC_PATH` |
| 环境变量 | `admin/.env.development` / `.env.production` | `VITE_PUBLIC_PATH = /console/` |
| 路由 base | `admin/.env.*` + `admin/src/router/index.ts:61` | `VITE_ROUTER_HISTORY="h5,/console/"` → `getHistoryMode()` → `createWebHistory("/console/")` |
| SPA 回退 | 服务器（A0 未配置） | 部署阶段把 `/console/*` 回退到 `/console/index.html` |

实测 `dist/index.html` 中资源引用为 `/console/static/js/...`、`/console/favicon.ico`，
证明 base 生效；dev 与 preview 访问 `/console/`、`/console/login`、`/console/dashboard`
均返回 200，证明路由与 fallback 生效。

### 3.2 `/api` 如何被代理到 Go 后端

1. Axios `baseURL` 取 `VITE_API_BASE_URL=/api`（`src/utils/http/index.ts:72`）。
2. 业务发起 `/admin/login` 时，实际请求 URL 是 `/api/admin/login`。
3. dev server 匹配 `"/api"` 前缀 → 转发到 `VITE_PROXY_TARGET`（默认 `http://127.0.0.1:8000`）。

前端**不写死后端地址**：`VITE_PROXY_TARGET` 可被 `.env.development.local` 覆盖，
换端口不需要改代码。

### 3.3 `/api/admin/login` → `/admin/login` 的映射依据

事实依据（不是猜测）：

- `manifest/config/config.yaml`：`server.address: ":8000"`，注释说明可用 `SERVER_ADDRESS` 覆盖
  → 后端默认监听 8000。
- `internal/cmd/routes_admin.go:52`：`root.POST("/admin/login", adminCtrl.Login)`
  → 后台登录路由是 `/admin/login`，前缀 `/admin`。

因此 rewrite 规则是去掉 `/api` 前缀，保留 `/admin/login`。
**A0 未修改任何 Go 路由**，只是为了对齐外部访问前缀而在前端做路径重写。

### 3.4 为什么请求层不擅自假设后端 envelope

后端响应结构（`{code, data, msg}`？`{data, error}`？错误码字符串还是数字？）
属于**契约的一部分**，目前只存在于后端实现里，尚未以 `contracts/openapi.yaml` 形式交付。

如果前端在 A0 就写死：

```ts
// 反例：假设后端一定有 code/message
if (res.code !== 0) throw new Error(res.message);
```

那么一旦后端最终结构不同，所有业务请求都要返工，而且这种错误在编译期完全发现不了。

所以 A0 的响应拦截只做「返回 `response.data`」这一件确定的事，
把 envelope 解析、业务错误码判断留给 `src/api/modules/`，并等待契约提供类型。

### 3.5 为什么 OpenAPI 契约缺失时必须拒绝生成类型

- 生成的类型会成为业务代码的编译依据。伪造的 `paths` 会让 TypeScript 通过，
  但字段名和实际后端对不上——比没有类型更危险（假的类型安全感）。
- `openapi-typescript` 的输出由契约推导，手工编造等于「手写一份假契约」，
  违反「契约由后端维护」的职责边界。
- 因此 `scripts/generate-api-types.mjs` 在文件缺失时打印
  「该契约由后端在 B0 阶段交付到仓库根目录 contracts/openapi.yaml」并以 `exit 1` 结束。

当前状态：`contracts/openapi.yaml` 不存在，`src/api/generated/` 只有 `.gitkeep`。

### 3.6 为什么 A0 不实现 Token、认证和动态权限

- 认证依赖契约：登录接口的请求字段、Token 字段名、过期语义、刷新策略都未定
  （`contracts/openapi.yaml` 未交付）。
- 认证是横切能力：一旦实现，会同时影响请求拦截器、路由守卫、user store、
  `utils/auth.ts`、登录页、错误码处理，属于一个完整阶段（A1）而不是"顺手加一下"。
- 半成品认证比没有认证更危险：写死了错误的 Token 头名，后续排查成本远高于从零接入。

所以 A0 的动作是**留出结构、写清注释**：

- `src/router/index.ts` 守卫中删除 Token 校验，保留注释说明 A1 在哪恢复。
- `src/utils/http/index.ts` 请求拦截器保留空位并注释「届时在此处统一附加请求头」。
- `src/router/utils.ts` 的 `initRouter()` 传入空数组，只注册静态路由，
  注释说明 A1 改为 `getAsyncRoutes()` + `handleAsyncRoutes()`。
- `src/views/login/index.vue` 为占位页，不调用接口、不写 Token。
- `src/utils/auth.ts` 保留 `getToken/setToken/removeToken` 骨架，但无人调用。

---

## 4. 技术决策与权衡

### 4.1 为什么采用 vue-pure-admin

| 维度 | vue-pure-admin | 从零搭建 / 其他模板 |
|---|---|---|
| 后台骨架 | 布局、多标签页、面包屑、搜索、通知、设置面板齐全 | 需自建，A0 会变成两周的 UI 工作 |
| 工程配置 | Vite + TS + ESLint + Stylelint + Tailwind 已成型 | 需逐项配置并踩坑 |
| 成本 | 需要裁剪 30+ 演示页面 | 不需要裁剪，但缺基础设施 |

结论：任务目标是「工程基础设施」，复用成熟骨架能让 A0 聚焦在
**部署路径、请求层、代理、Mock、类型生成**这些真正与 SurgeCart 相关的接缝上。
同时保留放弃的选项：模板只是源码复制，随时可以按文件粒度替换。

### 4.2 为什么清理大量演示依赖

A0 执行期间从 `admin/package.json` 移除了模板演示功能所需的运行时依赖与开发依赖
（`vxe-table`、`@pureadmin/table`、`@wangeditor/*`、`codemirror*`、`xgplayer`、`mqtt`、
`xlsx`、`swiper`、`vuedraggable`、`@vue-flow/*`、`@logicflow/*`、`plus-pro-components`、
`version-rocket`、`vite-plugin-fake-server`、`husky`、`lint-staged` 等）。
当前 `admin/package.json` 中已不存在这些条目，可直接核实。

动因不是"体积好看"，而是：

- **安全与维护面**：`xlsx`、`codemirror`、`wangeditor`、`mqtt` 等演示依赖会被
  依赖扫描工具计入漏洞面，与电商后台实际需求无关。
- **构建时间与 chunk 体积**：演示依赖大多进入 `index` chunk，会放大首屏体积。
- **认知成本**：新成员看到 `views/table/*`、`views/flow-chart/*` 会误以为
  "以后照这个写"，但那些页面用的是演示级数据结构和已废弃 API。

裁剪原则是「删除与布局骨架无关的页面/组件/依赖」，保留布局依赖的
`ReIcon/ReDialog/ReDrawer/ReSegmented/ReAuth/RePerms/ReText`。

### 4.3 为什么选择 MSW

| 方案 | 与真实请求层关系 | 关闭成本 | 判断 |
|---|---|---|---|
| `vite-plugin-fake-server`（模板原方案） | 在 dev server 层加中间件，Mock 数据与构建耦合 | 需移除插件与 mock 目录 | 已替换 |
| 手写 Axios 拦截器返回假数据 | 侵入请求层，业务代码要判断是否 mock | 需改请求层 | 否 |
| MSW | SW 在网络层拦截，Axios 无感知 | 不启动 SW 即可 | **采用** |

MSW 的关键价值是**契约一致性**：后续可以用同一份 OpenAPI 契约生成 handler，
让 Mock 与真实接口共享 schema，而不是维护两套字段。A0 只搭基础设施，handlers 留空。

### 4.4 为什么统一 Axios 实例

- 一处配置 `baseURL`/`timeout`/`paramsSerializer`，避免各页面各写一套。
- 错误提示只有一个出口（`errorHandler`），可以整体替换为 Toast/监控上报，
  不需要翻遍业务代码。
- A1 注入 Token、A2 加租户头、后续加 traceId，都只需改一处拦截器。
- 单测可以针对实例写断言（`baseURL`/`timeout`/错误文案），成本远低于测各个页面。

### 4.5 为什么暂时不引入虚假的 API 类型

见 3.5。补充一点：TypeScript 的 `any`/手写 `interface` 都能"让编译通过"，
但只有契约推导的类型能在后端改动时**报错**。A0 选择暂时"没有类型"，
是为了让 A1 的第一件事是"接入真契约"，而不是"清理假类型"。

### 4.6 为什么把认证和权限留到 A1

见 3.6。补充权衡：A0 的验收标准是「页面能访问、构建成功、工具链可用」，
如果此时加上未定的认证逻辑，验收标准会变成"登录能成功"——
而这需要后端契约与联调环境同时到位，不属于工程初始化范围。

---

## 5. 实际问题及排查

以下问题全部在本次执行过程中真实出现，按发现顺序记录。

### 5.1 模板裁剪引发的连锁引用错误

删除演示页面后，`src/router/modules/*`、`src/layout/**`、`src/components/**`
中残留了对已删除组件的 import。表现是 `pnpm typecheck` 大量
`Cannot find module "@/views/xxx"`。

处理方式：从路由表反向清理（路由是引用入口），再逐步删除无人引用的组件目录，
最后用 `typecheck` 收敛，而不是一次性删除全部演示目录。

### 5.2 开启 `strict` 后的类型错误（模板默认是非 strict）

固定 SHA 的模板 `tsconfig.json` 为 `strict: false`。A0 要求 strict，因此暴露出大量
模板遗留的弱类型写法。代表性错误与修法：

| 错误（原文） | 位置 | 根因 | 修法 |
|---|---|---|---|
| `Type 'boolean \| undefined' is not assignable to type 'boolean'` | `src/layout/hooks/useDataThemeChange.ts` | `$storage.layout?.darkMode` 可能为 `undefined` 却赋给 `ref<boolean>` | 补默认值 `?? false` |
| `Type 'string \| undefined' is not assignable to type 'string'` | 同上（`themeMode`、`ThemeColor`） | 运行时配置可缺省 | `?? "light"` / `?? "#409EFF"` |
| `Property 'scrollWidth' does not exist on type 'never'` | `src/layout/components/lay-notice/components/NoticeItem.vue` | `ref(null)` 被推断为 `Ref<null>`，`titleRef.value` 变成 `never` | `ref<any>(null)` |
| `Type 'RouteRecordRaw[]' is not assignable to type 'never[]'` | `lay-sidebar/NavVertical.vue`、`SidebarBreadCrumb.vue`、`lay-frame/index.vue` | `ref([])` 推断为 `never[]` | `ref<any[]>([])` |
| `Argument of type 'null' is not assignable to parameter of type 'string'` | `components/ReIcon/src/iconifyIconOffline.ts` | `props.icon` 默认 `null`，传给 `addIcon`/`h()` | 显式收窄并 `as any` 过渡 |
| `Type 'number' is not assignable to type 'string'` | `lay-setting/index.vue`（3 处） | `ReSegmented` 的 `modelValue` 声明为 `type: undefined`，被推断为 string | 改为 `type: [String, Number]` |
| `No overload matches this call` | `store/modules/multiTags.ts` | `splice(position?.startIndex, position?.length)` 参数可选 | 补 `?? 0` |
| `... is not assignable to parameter of type 'DataInfo<number>'` | `utils/auth.ts` | `setUserKey` 入参声明为完整类型，实际只传 5 个字段 | 改为 `Partial<DataInfo<number>>` |
| `'__VLS_ctx.inputRef' is possibly 'null'` | `lay-search/components/SearchModal.vue` 模板 | 模板里直接 `inputRef.focus()` | 模板改可选链 `inputRef?.focus()` |

修复原则：**只改类型，不改运行时行为**。例外是 `ref([])` → `ref<any[]>([])` 与
`?? 默认值`——这类改动在运行时等价（或把 `undefined` 归一为确定值），属于必要收窄。

已知代价：`src/layout`、`src/components` 中留下若干显式 `any`（动态菜单项、
图标组件、第三方 Sortable 实例）。这些位置运行时结构确实动态，硬套类型收益低；
但**新增业务组件不应沿用这个模式**，这是 A0 留下的技术债，已记入交付报告。

### 5.3 ESLint 与 Prettier 的格式冲突

`eslint.config.js` 集成了 `eslint-plugin-prettier`，因此格式差异会报 error 而不是 warning。
批量修完类型后 `pnpm lint:eslint:check` 报 34 个 error，几乎全是
`prettier/prettier` 的换行/缩进差异，另有 1 个
`@typescript-eslint/no-import-type-side-effects`（`import { type A, type B } from "vue-router"`）。

处理：`pnpm lint:eslint`（带 `--fix`）自动修复格式；该 import 手动改为
`import type { ... } from "vue-router"`（全为类型时应使用顶层 `import type`）。
最终 `--max-warnings 0` 通过。

### 5.4 生产构建告警：`Cannot optimize dependency: animate.css`

`build/optimize.ts` 的 `optimizeDeps.include` 里包含 `"animate.css"`。
`animate.css` 是纯 CSS 包，没有可预构建的 JS 入口，Vite 启动时报
`Cannot optimize dependency: animate.css, present in client 'optimizeDeps.include'`。

处理：从 `include` 数组移除该项（样式仍由 `layout/index.vue` 的 `import` 正常处理）。
该问题不影响构建成功，但会污染每次启动日志。

### 5.5 Vitest 无法解析 i18n 的 YAML 资源

首版 `tests/unit/router.test.ts` 直接 `import home from "@/router/modules/home"`，
而 `home.ts` 依赖 `@/plugins/i18n` 的 `$t`，i18n 插件又通过
`@intlify/unplugin-vue-i18n` 处理 `locales/*.yaml`。Vitest 配置里没有该插件，
报错为 YAML 内容被当作 JS 解析（`pureLoginOut: LoginOut` 处 `SyntaxError`）。

处理：在测试文件顶部 `vi.mock("@/plugins/i18n", ...)` 提供最小 `$t/transformI18n`，
让路由定义的标题退化为 key。理由：单测要验证的是**路由表结构**，不是 i18n 译文。

### 5.6 循环依赖导致 `Cannot access '__vite_ssr_import_4__' before initialization`

第二版测试改为动态 `import("@/router/utils")` 取 `getHistoryMode`，
触发 `src/router/utils.ts` ↔ `src/router/index.ts` 的循环引用：
`index.ts` 顶层执行 `ascending(routes)` → 用到 `utils.ts` 的导入，
而 `utils.ts` 顶层又 `import { router } from "./index"`，模块初始化顺序被打乱。

处理：把 `getHistoryMode` 抽到独立文件 `src/router/history.ts`（不依赖路由实例），
`utils.ts` 改为 `import { getHistoryMode } from "./history"` 并继续对外重导出，
保持 `router/index.ts` 的 import 不变。测试直接引用 `@/router/history`。

这是本次唯一的结构性重构，动因是**消除循环依赖**而非迎合测试。

### 5.7 测试断言错误：base 尾斜杠

`expect(getHistoryMode("h5,/console/").base).toBe("/console/")` 失败，
实际值是 `/console`——`vue-router` 会规范化 base 去掉尾部斜杠；
`createWebHistory("")` 的 `base` 是空字符串而不是 `"/"`。

处理：修正断言并加注释说明规范化行为。**没有为了让测试通过去改业务代码**。

### 5.8 代理验证为什么是 502

`curl -X POST http://127.0.0.1:8848/api/admin/login` → `502`。
直接 `curl http://127.0.0.1:8000/admin/login` → `000`（连接失败）。

结论：502 说明**代理已生效并尝试连接 `127.0.0.1:8000`，但该端口没有进程监听**
（A0 期间未启动 Go 后端）。这证明的是「转发链路已建立」，
**不证明**「前后端联调通过」。真实登录联调属于 A1 且需要后端运行。

### 5.9 依赖与版本相关

- `pnpm install` 本身无安装失败记录。
- 构建时提示 `Browserslist: browsers data (caniuse-lite) is 8 months old`。
  未执行 `npx update-browserslist-db@latest`——它会引入与 A0 无关的锁文件变更，
  记入已知限制。
- `vue-tsc` 需带 `--skipLibCheck`（`package.json` 的 `typecheck` 脚本已包含），
  否则第三方 `.d.ts`（含 MSW、Element Plus 的内部声明）会产生噪声错误。

---

## 6. 测试与验证

状态定义：

- **IMPLEMENTED**：代码/配置已写入仓库。
- **VERIFIED**：实际执行过命令并得到预期结果。
- **PARTIALLY_VERIFIED**：部分环节实际验证，关键环节未验证。
- **NOT_VERIFIED**：已实现但本次未执行验证。
- **NOT_IMPLEMENTED**：本次未实现。

### 6.1 逐项状态

| 项目 | 状态 | 证据 |
|---|---|---|
| vue-pure-admin 集成、源码落位 `admin/` | IMPLEMENTED | `admin/LICENSE`、`admin/package.json`、提交 `a827674` |
| 模板演示页面/组件/依赖裁剪 | IMPLEMENTED | `src/views` 仅剩 `dashboard/error/login`；`package.json` 依赖删减 |
| TypeScript strict 配置 | IMPLEMENTED | `tsconfig.json` `"strict": true` |
| strict 下类型检查通过 | VERIFIED | `pnpm typecheck` 退出码 0，无 error |
| ESLint 通过 | VERIFIED | `pnpm lint:eslint:check`，`--max-warnings 0`，无输出即无问题 |
| Vitest 基础测试 | VERIFIED | `2 passed (2)`、`11 passed (11)` |
| 生产构建 | VERIFIED | `✓ built in 4.17s`，产物约 3 MB |
| 构建产物使用 `/console/` 前缀 | VERIFIED | `dist/index.html` 内 `/console/static/js/...` |
| dev 下 `/console/`、`/console/login`、`/console/dashboard` 可访问 | VERIFIED | `curl` 均返回 200 |
| dev 下 `/console/access-denied` 可访问 | VERIFIED | 200 |
| `vite preview`（生产产物）下三条路由可访问 | VERIFIED | 历史执行记录：`/console/`、`/console/login`、`/console/dashboard` 均 200 |
| `/api` 代理已建立转发 | VERIFIED | `POST /api/admin/login` → 502；直连 8000 → 000 |
| `/api/admin/login` → `/admin/login` 真实响应 | NOT_VERIFIED | 后端未运行，只验证到"连接被拒"层面 |
| Axios 实例配置（baseURL/timeout/JSON 头） | VERIFIED | `tests/unit/http.test.ts` 断言 |
| Axios 错误归一化（超时/HTTP/网络） | VERIFIED | 同上，3 个用例分别断言文案 |
| 统一错误入口可替换 | VERIFIED | 同上，用 `setHttpErrorHandler` 捕获 |
| 路由表结构（dashboard/login/403/500/redirect） | VERIFIED | `tests/unit/router.test.ts` |
| history base 解析 | VERIFIED | 同上 |
| `pnpm api:types` 脚本存在 | IMPLEMENTED | `scripts/generate-api-types.mjs` |
| OpenAPI 类型实际生成 | NOT_VERIFIED | `contracts/openapi.yaml` 不存在，无法生成 |
| 契约缺失时脚本拒绝执行 | IMPLEMENTED（未逐次执行断言） | 脚本 `existsSync` 分支 `exit 1`；A0 期间执行时确实因缺文件未产出 |
| MSW 基础设施（worker 文件、启动入口、handlers） | IMPLEMENTED | `public/mockServiceWorker.js`（3.0.4）、`src/mocks/*` |
| MSW 在生产构建中被剔除 | VERIFIED | `grep -rl "setupWorker\|mockServiceWorker" dist/static` 无结果 |
| MSW 在浏览器中实际拦截请求 | NOT_VERIFIED | `VITE_MOCK_ENABLED=false`，未做真机验证 |
| 管理员登录业务 | NOT_IMPLEMENTED | 属 A1 |
| Token 认证 / 刷新 | NOT_IMPLEMENTED | 属 A1 |
| RBAC / 动态权限菜单 | NOT_IMPLEMENTED | 属 A1 |
| 商品/订单/库存/优惠券/秒杀 | NOT_IMPLEMENTED | 属 A2+ |
| 生产服务器 SPA 回退（Nginx 等） | NOT_IMPLEMENTED | A0 禁止修改部署配置 |
| Go 后端改动 | NOT_IMPLEMENTED | `git diff` 未触及 `internal/**` |

### 6.2 执行过的命令与结果（本轮复跑）

| 命令 | 结果 |
|---|---|
| `pnpm typecheck` | 退出码 0，无输出 |
| `pnpm lint:eslint:check` | 退出码 0，无输出 |
| `pnpm test` | `Test Files 2 passed (2)` / `Tests 11 passed (11)` |
| `pnpm build` | `✓ built in 4.17s`，`dist/index.html` 1.78 kB |
| `curl /console/` `/console/login` `/console/dashboard` `/console/access-denied` | 200 / 200 / 200 / 200 |
| `curl -X POST /api/admin/login` | 502 |
| `curl http://127.0.0.1:8000/admin/login` | 000（无进程监听） |
| `grep -rl "setupWorker" dist/static` | 无匹配（MSW 未进入产物） |

### 6.3 明确不能声称的结论

- 不能声称「前后端登录联调成功」——后端未运行，只验证到代理转发。
- 不能声称「OpenAPI 类型生成可用」——契约缺失，只验证了脚本存在与拒绝逻辑。
- 不能声称「Mock 在运行时可用」——未在浏览器中打开 Mock 验证拦截。
- 不能声称「生产部署已就绪」——服务器 SPA 回退与 `/api` 反代尚未配置。

---

## 7. 已知限制

**A0 范围内的限制**

1. `src/layout`、`src/components` 存在若干显式 `any`（动态菜单、图标、Sortable 实例），
   该层类型约束弱于业务层。
2. Element Plus 全量注册导致主 chunk `index-*.js` 约 1.9 MB（gzip 约 611 kB），
   首屏体积可优化（真正按需导入 / `manualChunks`）。
3. `locales/` 仍为模板文案（菜单、按钮、状态提示），未电商化改写。
4. `caniuse-lite` 数据过期告警未处理（避免锁文件无关变更）。
5. 上游模板无自动升级通道，只能人工比对。

**A1 需要处理**

6. 接入 `contracts/openapi.yaml` 后执行 `pnpm api:types`，用真实类型替换占位。
7. 实现 Token 注入、401 处理、刷新策略，同步更新 `src/utils/http` 拦截器。
8. 恢复路由守卫中的认证判断，按契约实现动态菜单（`initRouter()` → `getAsyncRoutes()`）。
9. 登录页从占位改为真实表单，接入校验与错误提示。

**部署阶段需要处理**

10. `/console/*` 的 SPA fallback（`try_files $uri $uri/ /console/index.html`）。
11. `/api/*` 去掉前缀反代到 Go 后端（与 dev proxy rewrite 行为一致）。
12. `public/mockServiceWorker.js` 会随构建进入 `dist/`；
    生产不启用 Mock 时该文件无副作用，但可在部署时排除。

---

## 8. 技术复盘与面试准备

> 说明：以下是根据本次实际代码整理的**应掌握要点与提问方向**，
> 不代表已经掌握。建议按「问题 → 打开对应文件 → 讲清因果」的方式自测。

### 8.1 值得掌握的知识点

**Vite `base` 与部署子路径**

- `base` 同时影响 HTML 资源前缀、`import.meta.env.BASE_URL`、dev server 挂载点。
- 子路径部署时，Service Worker 注册路径也必须走 `BASE_URL`，否则 SW 404 → Mock 静默失效。
- 结合文件：`admin/vite.config.ts`、`admin/src/mocks/index.ts`、`admin/.env.production`。

**Vue Router history 与服务器回退**

- history 模式把 404 责任交给服务器；`createWebHistory("/console/").base` 会被规范化为 `/console`。
- 结合文件：`admin/src/router/history.ts`、`admin/src/router/index.ts`、`admin/tests/unit/router.test.ts`。

**Axios 拦截器与错误归一化**

- 成功拦截返回 `response.data` 会让 `get<T>()` 的 `T` 语义变成"响应体"而非"响应"，要讲清这个约定了什么。
- `Axios.isCancel` 判断用于区分"用户主动取消"和"真错误"，避免路由切换时弹错误。
- 结合文件：`admin/src/utils/http/index.ts`、`admin/src/utils/http/types.d.ts`。

**Vite Proxy 的 rewrite 与 changeOrigin**

- rewrite 决定后端看到什么路径，changeOrigin 决定 `Host` 头。
- 代理只在 dev/preview 生效，生产必须靠网关，这是两套配置必须保持同一语义的原因。
- 结合文件：`admin/vite.config.ts` 的 `createProxy`。

**MSW 的拦截层次**

- SW 在网络层拦截，故业务代码零感知；`onUnhandledFrame: "bypass"` 决定未命中时的行为。
- 生产构建能剔除 MSW 是因为 `import.meta.env.VITE_MOCK_ENABLED` 在构建期被静态替换，
  动态 import 分支变成死代码（实测产物中无 `setupWorker`）。
- 结合文件：`admin/src/mocks/index.ts`、`admin/src/main.ts`、`admin/public/mockServiceWorker.js`。

**OpenAPI → TypeScript 的契约驱动**

- `openapi-typescript` 只产类型不产 client，把"传输行为"和"数据结构"分离。
- 契约缺失时失败退出，是为了避免假类型通过编译。
- 结合文件：`admin/scripts/generate-api-types.mjs`、`admin/package.json` 的 `api:types`。

**Pinia 在布局中的作用**

- `permission` store 同时被路由初始化与菜单渲染消费，是路由/UI 耦合点。
- 结合文件：`admin/src/store/modules/permission.ts`、`admin/src/router/utils.ts`。

### 8.2 面试问题（结合 SurgeCart 文件作答）

1. **部署到 `/console/` 需要改哪些地方？只改 `vite.config.ts` 的 `base` 够不够？**
   期望覆盖：base、路由 base、`import.meta.env.BASE_URL` 使用点（MSW worker）、服务器 fallback。
   文件：`vite.config.ts`、`src/router/history.ts`、`src/mocks/index.ts`。

2. **为什么 `/api/admin/login` 到后端变成了 `/admin/login`？如果不 rewrite 会怎样？**
   期望覆盖：rewrite 语义、后端路由前缀来自 `routes_admin.go`、不 rewrite 会 404。
   文件：`vite.config.ts`、`internal/cmd/routes_admin.go`。

3. **`createWebHistory("/console/")` 的 `base` 到底是 `/console/` 还是 `/console`？刷新 404 该谁负责？**
   文件：`src/router/history.ts`、`tests/unit/router.test.ts`。

4. **Axios 响应拦截返回 `response.data` 有什么利弊？业务层怎么拿到状态码？**
   期望覆盖：调用点简洁 vs 丢失 headers/status；需要时用 `beforeResponseCallback` 或原始 `instance`。
   文件：`src/utils/http/index.ts`。

5. **为什么超时判断要看 `ECONNABORTED` / `ETIMEDOUT`？`isCancel` 和它们有什么区别？**
   文件：`src/utils/http/index.ts` 的 `resolveErrorText`。

6. **MSW 和「在请求层造假数据」的本质区别？为什么生产构建里看不到 MSW？**
   文件：`src/mocks/index.ts`、`src/main.ts`、构建后 `grep` 产物。

7. **`element-plus` 全量注册 vs 自动按需导入，本项目选了哪个？代价是什么？怎么改？**
   期望覆盖：`plugins/elementPlus.ts` 显式注册、`index.css` 全量样式、主 chunk 约 1.9 MB。
   文件：`src/plugins/elementPlus.ts`、`src/main.ts`。

8. **为什么 `openapi-typescript` 生成失败比生成空文件更好？**
   文件：`scripts/generate-api-types.mjs`。

9. **`ref([])` 在 strict 下为什么变成 `never[]`？会导致什么典型报错？**
   期望覆盖：`ref` 泛型推断、`subMenuData.value = xxx` 报 not assignable。
   文件：`src/layout/components/lay-sidebar/NavVertical.vue`。

10. **`router/utils.ts` 和 `router/index.ts` 的循环依赖是怎么暴露的？怎么解的？**
    期望覆盖：Vitest SSR 转换下的 TDZ 报错、抽 `history.ts` 消除环。
    文件：`src/router/history.ts`、`src/router/utils.ts`。

11. **如果 A1 要加 Token，需要动哪几个文件？为什么不是只改 Axios？**
    期望覆盖：请求拦截器、401 处理、user store、路由守卫、登录页、`utils/auth.ts`。
    文件：`src/utils/http/index.ts`、`src/router/index.ts`、`src/store/modules/user.ts`。

12. **静态路由与动态菜单在这个模板里如何衔接？A0 为什么传空数组？**
    文件：`src/router/utils.ts` 的 `initRouter` / `handleAsyncRoutes`。

### 8.3 自检清单（用于确认是否真的理解）

- [ ] 能不看代码画出「浏览器 → Vite dev server（proxy）→ Go 后端」的完整路径与路径变化。
- [ ] 能解释 `dist/index.html` 为什么是 `/console/static/...` 而不是 `./static/...`。
- [ ] 能指出把 `VITE_PUBLIC_PATH` 改成 `/` 后会连锁坏掉哪些地方。
- [ ] 能说明 `pnpm test` 里 11 个用例分别验证了什么，以及为什么没有测 UI 交互。
- [ ] 能解释 502 是"代理通了"还是"后端通了"。
- [ ] 能说清 `contracts/openapi.yaml` 缺失时，为什么 `src/api/generated/` 必须为空。
