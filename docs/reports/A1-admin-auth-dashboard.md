# SurgeCart 管理后台 — A1 管理员认证与运营工作台 交付报告

- 阶段：A1（管理员认证 + Token 管理 + 路由守卫 + 基础角色权限 + 运营工作台）
- 分支：`feat/phase2-admin-frontend`
- 基线提交：`339b513c4a3460d15c555a24a8900296b72f5aed`（A0 文档交付完成）
- 代码提交：`b86402d8aba0fc3112172356654abe1d8493c8d0`
- 开发知识文档：`.agent/tasks/dev/A1-admin-auth-dashboard.md`
- 执行日期：2026-11-10

## 1. 任务目标

在 A0 工程基础上完成：

1. 管理员登录（`POST /admin/login`）。
2. Token 管理（`sessionStorage` 存 `access_token` + `expires_at`，无 Refresh Token）。
3. 当前管理员信息（`GET /admin/me`）。
4. 退出登录（`POST /admin/logout`）。
5. 路由认证守卫。
6. 基础角色权限适配机制（`is_super` + `roles`，前端角色映射）。
7. 运营工作台（对接后端五个 Dashboard 接口）。

本次只修改管理端 `admin/**`，未修改 Go 后端、OpenAPI 契约、数据库与任何部署配置。

## 2. 实际实现范围

### 2.1 认证与 Token

| 能力 | 状态 | 说明 |
|---|---|---|
| 登录页表单（必填校验、Loading、失败提示） | IMPLEMENTED / VERIFIED | `views/login/index.vue`，单测覆盖成功与失败路径 |
| 登录成功保存 `access_token` + `expires_at` | IMPLEMENTED / VERIFIED | `utils/auth.ts`，`expires_at = Date.now() + expires_in*1000` |
| `Authorization: Bearer <token>` 自动注入 | IMPLEMENTED / VERIFIED | 请求拦截器；handler 显式校验该头 |
| Token 过期判定与清理 | IMPLEMENTED / VERIFIED | 无 `expires_at` 也视为不可用；守卫与 ensure 流程均清理 |
| 页面刷新后调用 `/admin/me` 恢复身份 | IMPLEMENTED / VERIFIED | 身份只存 Pinia，不落盘 |
| 不把 JWT 解码结果当身份依据 | IMPLEMENTED | 代码中不存在任何 JWT 解析 |
| 退出登录（服务端失败也清本地） | IMPLEMENTED / VERIFIED | token 已失效时跳过服务端调用，直接清本地 |
| 并发 401 只清理一次 | IMPLEMENTED / VERIFIED | Promise 去重 + 500ms 复位窗口 |
| 不保存密码 / Refresh Token / 不使用 Cookie | IMPLEMENTED / VERIFIED | 仅 sessionStorage 两个键 |

### 2.2 路由守卫

| 能力 | 状态 | 说明 |
|---|---|---|
| 未登录访问管理页 → `/login` | IMPLEMENTED / VERIFIED | 带 `redirect` 查询参数（根路径除外） |
| 已登录访问 `/login` → `/dashboard` | IMPLEMENTED / VERIFIED | 跳转前先校验会话 |
| 刷新页面验证现有 Token | IMPLEMENTED / VERIFIED | `ensureAdminProfile()` |
| 认证失败清理状态 | IMPLEMENTED / VERIFIED | 清 token + Pinia + 权限快照 |
| 初始化期间不重复请求 `/admin/me` | IMPLEMENTED / VERIFIED | 缓存 + 并发合并，单测断言只请求 1 次 |
| 避免重定向循环 | IMPLEMENTED / VERIFIED | `/login` 分支先校验再跳转 |
| 复用 403 / 404 页面 | IMPLEMENTED | `views/error/*` 保持不变 |

### 2.3 权限适配

| 能力 | 状态 | 说明 |
|---|---|---|
| 统一 `hasPermission()` | IMPLEMENTED / VERIFIED | `utils/permission.ts`，支持单值/数组、`every`/`some` |
| `is_super=true` 通过全部检查 | IMPLEMENTED / VERIFIED | 返回 `["*"]` 语义 |
| 支持未来读取后端 `permissions` | IMPLEMENTED / VERIFIED | 字段存在即权威（含空数组） |
| 角色映射集中配置 | IMPLEMENTED | `ROLE_PERMISSION_MAP`，仅 `super_admin` |
| 未知角色默认拒绝 | IMPLEMENTED / VERIFIED | 白名单语义 |
| 不编造其他角色权限 | IMPLEMENTED | 映射表只有一条 |
| 前端权限不作为安全边界 | IMPLEMENTED | 代码注释 + 文档明确，实际授权由后端 `RequirePermission` |

工作台路由**不做**页面级权限拦截（所有已认证管理员可进入），数据由后端
`RequirePermission("dashboard:view")` 控制；403 在分区内展示"权限不足"。

### 2.4 运营工作台

| 能力 | 状态 | 说明 |
|---|---|---|
| 六个核心指标卡 | IMPLEMENTED / VERIFIED | 含「今日已支付订单金额」，分转元保留两位小数 |
| 订单趋势折线图 | IMPLEMENTED / VERIFIED（映射）/ NOT_VERIFIED（浏览器渲染） | ECharts，option 构建有单测 |
| 订单状态分布环形图 | IMPLEMENTED / VERIFIED（映射） | 使用后端真实整数状态枚举名称 |
| 热销商品表格 | IMPLEMENTED / VERIFIED | 排名 / 商品 ID / 名称 / 销量 |
| 秒杀运营统计 | IMPLEMENTED / VERIFIED | 活动、请求分布、当前排队、成功订单 |
| 五接口独立加载与局部失败隔离 | IMPLEMENTED / VERIFIED | 分区状态 + 页面级 `Promise.allSettled` |
| 每分区 loading / 空 / 错误 / 重试 | IMPLEMENTED | 组件内实现 |
| 403 显示权限不足 | IMPLEMENTED / VERIFIED | `useAsyncSection` 标记 `forbidden` |
| 401 走统一认证失效 | IMPLEMENTED / VERIFIED | 请求层处理 |
| 无假数据填充 | IMPLEMENTED | 失败即失败，可重试 |

## 3. API 接入情况

### 3.1 实际使用的路径

所有请求由 Axios `baseURL=/api` + Vite Proxy 处理，模块内**不重复写 `/api`**。

| 前端调用 | 实际 HTTP 路径 | 方法 |
|---|---|---|
| `adminLogin()` | `/api/admin/login` → 后端 `/admin/login` | POST |
| `getCurrentAdmin()` | `/api/admin/me` → 后端 `/admin/me` | GET |
| `adminLogout()` | `/api/admin/logout` → 后端 `/admin/logout` | POST |
| `getDashboardOverview()` | `/api/admin/dashboard/overview` | GET |
| `getDashboardOrderTrend()` | `/api/admin/dashboard/orders/trend` | GET |
| `getDashboardOrderStatus()` | `/api/admin/dashboard/orders/status` | GET |
| `getDashboardTopProducts()` | `/api/admin/dashboard/products/top` | GET |
| `getDashboardFlashSales()` | `/api/admin/dashboard/flash-sales` | GET |

### 3.2 请求参数

| 接口 | 参数 | 说明 |
|---|---|---|
| 登录 | body `{username, password}` | JSON |
| 状态分布 | query `start_time`, `end_time` | RFC3339，**必须同传**；默认最近 7 天（左闭右开） |
| 热销商品 | query `limit=10` | 后端默认 10、上限 50；前端固定 10 |
| 其余 | 无 | — |

时间窗示例：`start_time=2026-11-04T00:00:00+08:00&end_time=2026-11-11T00:00:00+08:00`。

### 3.3 关键响应字段

```ts
// 登录
{ access_token, token_type, expires_in }
// 当前管理员
{ id, username, is_super, roles[] }        // 无 permissions 字段
// 概览
{ user_total, today_new_users, product_total, order_total,
  today_new_orders, today_order_amount }   // 金额单位：分
// 趋势
{ days: [{ date, order_count }] }
// 状态分布
{ total, status_distribution: [{ status, count }] }   // status ∈ {10,20,30,40,50,60,70}
// 热销
{ items: [{ rank, product_id, product_name, sales }] }
// 秒杀
{ activity_total, activity_status:{enabled,disabled},
  request_status:{queued,success,failed,dead},
  current_queued, success_orders }
```

### 3.4 暂定（PROVISIONAL）DTO

`contracts/openapi.yaml` 尚未交付，以下类型均为手写，集中在两个文件：

| 文件 | 暂定类型 |
|---|---|
| `src/api/modules/admin-auth.ts` | `ProvisionalAdminLoginPayload`、`ProvisionalAdminLoginResult`、`ProvisionalAdminProfile` |
| `src/api/modules/dashboard.ts` | `DashboardOverview`、`DashboardTrendItem`、`DashboardOrderTrend`、`DashboardStatusItem`、`DashboardOrderStatus`、`DashboardTopProduct`、`DashboardTopProducts`、`DashboardFlashSaleActivityStatus`、`DashboardFlashSaleRequestStatus`、`DashboardFlashSales`、`DashboardStatusQuery` |

另：`src/api/types/envelope.ts` 的 `ApiEnvelope`/`BusinessError` 属于契约基础类型，同样待生成类型替换。

未创建任何虚假 OpenAPI 文件，`src/api/generated/` 仍为空（仅 `.gitkeep`）。

### 3.5 与初步交接设计的核对

结论：**未发现冲突**。以下为阅读后端实际代码（`api/admin/v1`、`api/dashboard/v1`）后补充确认的点：

1. `/admin/me` 的 `roles` 为 `[]string`，`is_super` 为 `bool`，确实没有 `permissions`。
2. `/admin/logout` 挂载时使用 `AdminAuthSignatureOnly`（仅验签，幂等撤销），
   因此"token 已失效也要清本地"的策略与后端行为一致。
3. `/admin/dashboard/products/top` 的 `limit` 在后端以 `string` 接收并严格校验
   （非整数返回 400/1001），前端传数字由 axios 序列化为 `limit=10`，语义一致。
4. 五个 Dashboard 接口均需权限 `dashboard:view`，与任务描述一致。
5. 订单状态整数枚举在后端有明确定义，因此前端使用了**真实状态名称**而非原始值：

   | 值 | 含义 | 依据 |
   |---|---|---|
   | 10 | 待支付 | `internal/logic/order/order.go` 状态机常量 |
   | 20 | 已支付 | 同上 |
   | 30 | 已发货 | 同上 |
   | 40 | 已收货 | 同上 |
   | 50 | 已完成 | 同上 |
   | 60 | 已取消 | 同上 |
   | 70 | 已退款 | 同上 |

   未在映射表登记的状态码显示为 `状态 <code>`，不做语义猜测。

## 4. 主要修改文件

> 完整清单见 `git show --stat <code-sha>`；以下只列职责。

**新增**

| 文件 | 职责 |
|---|---|
| `src/api/types/envelope.ts` | 统一响应 envelope、`BusinessError`、结构判定 |
| `src/api/modules/admin-auth.ts` | 管理员登录 / 当前管理员 / 登出（PROVISIONAL DTO） |
| `src/api/modules/dashboard.ts` | 五个 Dashboard 接口 + DTO + 默认时间窗 |
| `src/utils/permission.ts` | 角色映射、权限主体快照、`hasPermission` |
| `src/constants/order-status.ts` | 订单状态整数 → 中文名称（依据后端枚举） |
| `src/utils/money.ts` / `src/utils/number.ts` / `src/utils/datetime.ts` | 分转元、千分位、RFC3339 时间窗 |
| `src/router/guard.ts` | 认证守卫决策（可单测） |
| `src/views/dashboard/index.vue` + `components/*` + `composables/*` + `utils/*` | 工作台页面、分区组件、异步状态、图表配置 |
| `src/mocks/handlers/admin.ts` / `dashboard.ts` | Mock 夹具（认证 + 工作台） |
| `tests/setup.ts`、`tests/unit/{admin-auth,http-auth,auth-guard,permission,dashboard}.test.ts` | 测试基础设施与用例 |

**修改**

| 文件 | 改动 |
|---|---|
| `src/utils/auth.ts` | 从 Cookie/localStorage 改为 sessionStorage 存 token（重写） |
| `src/utils/http/index.ts` | Bearer 注入、envelope 解析、401 去重处理、403 保留登录态 |
| `src/utils/http/types.d.ts` | 新增 `silent` / `skipAuth` / `skipUnauthorizedHandling` |
| `src/store/modules/user.ts` | 管理员身份、`login` / `logOut` / `ensureAdminProfile` |
| `src/store/types.ts` | `userType` 扩展管理员字段 |
| `src/router/index.ts` | 守卫改为调用 `resolveAuthGuard` |
| `src/router/utils.ts` | 菜单权限过滤改读权限快照（去掉 localStorage 依赖） |
| `src/main.ts` | 注册 401 统一处理 |
| `src/views/login/index.vue` | 占位页改为真实登录表单 |
| `src/views/dashboard/index.vue` | A0 状态页改为运营工作台 |
| `src/layout/hooks/useDataThemeChange.ts` | "清空缓存"同步重置认证状态 |
| `src/layout/hooks/useNav.ts` | 登出改为异步调用（不阻塞 UI） |
| `src/router/modules/home.ts`、`locales/*.yaml` | 菜单文案改为「运营工作台」 |
| `vitest.config.ts` | 增加 `setupFiles` |
| `README.md` | 更新至 A1 状态（认证与权限、Mock 账号、路由表） |

## 5. 测试结果

### 5.1 命令与实际结果

| 命令 | 实际结果 |
|---|---|
| `pnpm typecheck` | 通过，0 error（`tsconfig.json` 保持 `strict: true`） |
| `pnpm lint:eslint` | 通过，0 error / 0 warning（`--max-warnings 0`） |
| `pnpm test` | 通过，**7 个测试文件 / 70 个用例**（A0 的 11 个用例作为回归保留） |
| `pnpm build` | 成功，`✓ built in 4.24s`，产物约 3.07 MB |
| `git diff --check` | 通过（无空白错误） |

### 5.2 新增用例分布

| 文件 | 用例数 | 主要覆盖 |
|---|---|---|
| `admin-auth.test.ts` | 10 | 登录成功/失败、token 保存、Header 注入、过期判定、身份恢复与并发合并、登出清理 |
| `http-auth.test.ts` | 9 | 401 去重、403 不清态、登录 401 不触发全局退出、业务错误 code/message、静默模式、envelope 解包 |
| `auth-guard.test.ts` | 9 | 未登录跳转、根路径不带 redirect、已登录跳工作台、服务端拒绝、过期不发请求、并发只请求一次 |
| `permission.test.ts` | 12 | 超管、映射角色、未知角色拒绝、`permissions` 权威性与 `null` 回退、some/every、快照隔离 |
| `dashboard.test.ts` | 17 | 五接口解析、RFC3339 窗口、分转元、千分位、状态映射与回退、option 映射、403 隔离、空数据、重试 |

### 5.3 测试手段说明

全部接口测试使用 **MSW**（`msw/node` 的 `setupServer`）在网络层拦截，
与浏览器端 Mock 共用同一份 `src/mocks/handlers`。

- 未匹配请求配置为 `onUnhandledRequest: "error"`，避免测试静默打到真实网络。
- 403 / 空数据 / 业务错误 / 网络异常通过 `server.use(...)` 按用例覆盖。

## 6. 真实后端联调结果

| 项 | 状态 | 说明 |
|---|---|---|
| 与 Go 后端真实联调 | **NOT_VERIFIED** | 本机 `127.0.0.1:8000` 无进程监听（直连返回连接失败），未启动后端 |
| 代理链路 | **PARTIALLY_VERIFIED** | `POST /api/admin/login` 经 dev server 返回 `502`，证明请求已进入代理并被转发到 8000 端口；不证明业务成功 |
| 浏览器端到端（点击登录、图表渲染） | **NOT_VERIFIED** | 环境无 headless 浏览器，未做 UI 自动化 |
| 开发服务器路由可达 | **VERIFIED** | `/console/`、`/console/login`、`/console/dashboard` 均返回 200 |
| MSW worker 资源可达 | **VERIFIED** | `/console/mockServiceWorker.js` 返回 200 |

**不能声称已完成端到端联调。** 当前所有业务链路的正确性依据是单元测试 + MSW，
真实后端行为（JWT 签发、会话撤销、聚合 SQL 口径）仍待后端联调确认。

## 7. 未完成事项

1. **OpenAPI 契约未交付**：`contracts/openapi.yaml` 不存在，DTO 仍为 PROVISIONAL；
   `pnpm api:types` 无法生成真实类型。
2. **真实后端联调未做**：需后端在 `:8000` 运行，或提供可访问的测试环境。
3. **浏览器验证未做**：登录交互、ECharts 渲染、Mock 在浏览器中的实际拦截均未验证。
4. **时区边界**：状态分布的时间窗按浏览器本地时区计算，与后端业务时区
   （`dashboard.timezone`，默认 `Asia/Shanghai`）可能不一致（未解决，已在知识文档记录）。
5. **无动态权限菜单**：路由仍为静态表，`meta.roles` 过滤未启用（后续阶段）。
6. **未实现 Refresh Token**：按任务要求不做。
7. **`permissions` 字段未生效**：后端未下发，前端已预留（`null` / `[]` 语义已区分并可测）。
8. **部署配置未动**：`/console/*` SPA fallback 与 `/api` 反代仍属部署阶段任务。
9. **构建体积**：主 chunk 仍约 1.9 MB（Element Plus 全量注册，A0 遗留问题未处理）。

## 8. Git 提交与推送

- 代码提交：`b86402d8aba0fc3112172356654abe1d8493c8d0`（46 个文件，+4180 / -333）
- 提交信息：`feat(admin): implement admin authentication and dashboard`
- 变更范围：仅 `admin/**`（未修改 `internal/**`、`api/**`、`migrations/**`、`contracts/**`、
  `web/**`、`Dockerfile`、`docker-compose.yml`、`deploy/**`、Nginx、Kubernetes、Prometheus）
- 推送：普通 push 至 `feat/phase2-admin-frontend`，未使用 force push，未重写历史
- 远端状态：`origin/feat/phase2-admin-frontend` = `b86402d`，与本地一致

## 9. A2 的启动条件

A2（商品与库存管理）开始前需要：

1. **后端 OpenAPI 契约交付**：至少包含商品/SKU/库存的 CRUD schema、错误码表、
   分页结构；契约交付后先执行 `pnpm api:types`，再用生成类型替换 PROVISIONAL DTO。
2. **真实后端联调环境可用**：`/admin/*` 可访问，且能签发有效 JWT，
   以便验证 A1 遗留的联调项（登录、会话撤销、五个 Dashboard 口径）。
3. **A1 联调遗留项闭环**：至少确认 `/admin/me` 与 `/admin/dashboard/*` 的真实响应与 DTO 一致。
4. **权限口径确认**：`/admin/me` 是否新增 `permissions[]`。若新增，前端无需改动
   （`permissions` 已优先于角色映射）；若不新增，需后端给出后续角色的权限约定，
   否则前端只能维持"仅 `super_admin`"的映射。
5. **分页与筛选约定**：商品/库存列表的分页参数名、排序参数、关键字搜索字段需在契约中明确。
6. **图片/上传能力确认**：商品图上传涉及存储与鉴权，需确认是否复用 `upload` 控制器。
7. **部署联调窗口**：`/console/` 的 SPA fallback 与 `/api` 反代需在部署环境生效，
   否则刷新子路由会 404。

满足上述条件后方可进入 A2。本阶段到此停止。

---

**文档边界**：本报告记录交付事实与验收证据；实现原理、排障过程、设计约束与
面试复习要点见 `.agent/tasks/dev/A1-admin-auth-dashboard.md`，两份文档不互相替代。
