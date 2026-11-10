# W5：SurgeCart React 用户商城最终验收（开发知识文档）

- 分支：`feat/phase2-user-frontend`
- 验收基线：`d33948704511d2d766becedcbe35b67adcbb1269`（W4-04）
- 范围：工程审查、全链路回归、UI/响应式验收、生产构建与部署兼容性
- 约束：只改 `web/`；未改 Go、SQL、管理端、Docker/Nginx/K8s
- 本文记录**最终代码与实测证据**；交付报告只给结论与清单，两者职责分离。

---

## 1. 用户商城整体架构

```
React SPA (Vite)
├── src/api          传输层 + 统一响应解包 + 401 刷新 + Query Key + 按域 API
├── src/auth         内存 Token、单飞刷新、Auth 状态、路由守卫、账号缓存清理
├── src/hooks        TanStack Query 领域 hooks（购物车/结算/订单/退款/优惠券/收藏）
├── src/components   纯展示与交互组件（按业务域分组 + ui 基础组件）
├── src/pages        路由页面（商品、购物车、结算、个人中心）
├── src/router       路由表 + 路由级懒加载
├── src/mocks        MSW handlers 与内存 Mock 数据（仅 dev / 测试）
├── src/utils        纯函数（价格格式化、购物车汇总、订单/退款/优惠券展示、幂等指纹）
└── src/stores       Zustand（仅移动端导航开关）
```

数据流：页面 → hook → `src/api/*` → `apiClient` → `/api` → 反向代理（剥离前缀）→ Go 服务 → MySQL/Redis。

## 2. 前端模块职责

| 目录 | 职责 | 明确不负责 |
| --- | --- | --- |
| `api/` | HTTP、超时、取消、Token 注入、envelope 解包、DTO 与 Query Key | 任何业务规则 |
| `auth/` | Token 生命周期、401 单飞刷新、路由守卫、账号缓存清理 | 后端授权判定 |
| `hooks/` | 服务端状态查询/变更、缓存失效、乐观更新与回滚 | 渲染结构 |
| `components/` | 展示与交互、无障碍属性 | 直接请求接口 |
| `pages/` | 路由页面装配、表单提交编排 | 数据缓存策略 |
| `utils/` | 纯函数（金额展示、进度推导、幂等指纹、状态文案） | 服务端数据获取 |
| `mocks/` | 与 Go 后端同语义的内存 Mock | 生产运行路径 |

审查结论：未发现职责颠倒（组件内无 `fetch`、Zustand 不存业务数据、Query Key 无字面量）。

## 3. 核心业务调用链

| 链路 | 调用顺序 |
| --- | --- |
| 商品浏览 | Page → `useQuery` → `fetchProductList` → `GET /products`（URL 为筛选唯一事实源） |
| 登录 | LoginPage → `useLogin` → `POST /login` → 写入内存 Token → `fetchQuery(/me)` → 跳回 `from` |
| 加购 | `AddToCartPanel` → `useAddCartItem` → `POST /cart/items` → 失效 `cart.all` |
| 结算 | CheckoutPage → 购物车 + 地址 + 优惠券三个 query → `useCreateOrder` |
| 下单 | `buildOrderSignature(address, lines, couponId)` → `POST /orders`（幂等键）→ 成功页 |
| 支付/取消/收货 | `useOrderAction` → `POST /orders/:id/{pay,cancel,receive}` → 写详情缓存 + 失效列表/券 |
| 退款申请 | OrderDetailPage → `useApplyRefund` → `POST /orders/:id/refund` → 跳退款详情 |
| 撤销退款 | RefundDetailPage → `useRevokeRefund` → `POST /refunds/:id/revoke` |
| 用券 | CheckoutCouponSection → `coupon_id` → `POST /orders` → 服务端计算优惠 |

## 4. API Client 与认证体系

### 4.1 传输层（`src/api/client.ts`）

- `baseUrl` 默认 `/api`，可用 `VITE_API_BASE_URL` 覆盖；
- 统一超时（默认 15s，可配），超时/取消区分 `kind: timeout | aborted`；
- 请求头统一 `Accept`，有 body 时 `Content-Type: application/json`；
- 非 2xx 解析 `{code,message,data}`，数字业务码转字符串存入 `ApiError`；
- `204` 返回 `undefined`；非 JSON 响应按文本返回；
- 外部 `AbortSignal` 与内部超时控制器串联。

### 4.2 认证（`src/auth/`）

- Token **只存内存**，不写 localStorage/sessionStorage（XSS 面最小化）；
- `setAuthTokenProvider(getAccessToken)` 注入到 Client，每次请求读取最新值；
- `authGetData/authPostData/authPutData/authDeleteData`：首次 401 → `refreshSession()` 单飞刷新 → 成功后重试一次 → 仍 401 则 `clearAuthTokens()`；
- `refreshSession` 用模块级 Promise 单飞，避免并发刷新触发后端 reuse 检测；
- 刷新期间 Token 被替换/清空时丢弃旧结果；
- 路由守卫 `RequireAuth` 只做 UX 引导，**后端仍是唯一授权方**（所有受保护接口在 Go 侧校验 Principal）。

### 4.3 账号私有缓存清理（W5 修复）

`src/auth/user-cache.ts` 维护唯一清单：

```
auth / addresses / favorites / cart / orders / refunds / coupons
```

两个触发点共用：
- `useLogout`（主动退出）；
- `useAuthCacheReset`（有会话 → 无会话：refresh 失败、401 重试失败、服务端撤销）。

修复前 `useAuthCacheReset` 只清 4 类，订单/退款/优惠券会残留到下一个登录用户。

## 5. TanStack Query 缓存策略

| 数据 | Key | staleTime | 失效点 |
| --- | --- | --- | --- |
| 当前用户 | `['auth','me']` | 5 min | 登录/登出 |
| 商品列表 | `['products','list',params]` | 30s（默认） | 无（只读） |
| 商品详情 | `['products','detail',id]` | 30s | 无 |
| 购物车 | `['cart','list']` | 30s | 加购/改量/勾选/删除/下单/登出 |
| 订单列表 | `['orders','list']` | 15s | 下单/支付/取消/收货/退款申请 |
| 订单详情 | `['orders','detail',id]` | 15s | 同上 + 写接口响应回填 |
| 退款列表 | `['refunds','list'(,orderId)]` | 15s | 申请/撤销/失败 |
| 退款详情 | `['refunds','detail',id]` | 15s | 申请/撤销/失败回填 |
| 优惠券 | `['coupons','list']` | 15s | 下单/支付/取消/19003/19007/登出 |
| 地址/收藏 | `['addresses'|'favorites',...]` | 30s | CRUD/收藏切换 |

原则：
- 服务端数据只进 Query，不进 Zustand；
- 交易类状态**不做乐观更新**（唯一例外是收藏切换，失败回滚）；
- 列表筛选/分页是本地行为，不产生新 key（后端无对应参数）；
- 组件卸载即 `gcTime` 回收，登出时显式 `removeQueries`。

## 6. 订单、库存、退款、优惠券的前端协作

```
购物车勾选 ──下单──> 订单(pending_payment) ──支付──> paid ──发货──> shipped ──收货──> completed
      │                    │                        │
      │                    └──取消──> cancelled      └──退款申请──> applying ──审核──> pending_refund ──执行成功──> refunded
      │                          (恢复库存/释放券)
      └──库存不足 6001 / 不可购买 9003 / 无勾选 9004 由服务端拒绝

优惠券：available ──下单──> locked ──支付──> used（退款不返还）
                        └──取消──> available
```

前端只做三件事：展示服务端状态、按状态渲染入口、写操作后失效对应缓存。
库存扣减/恢复、券锁定/核销/释放、退款状态迁移全部由 Go 事务完成。

## 7. 金额与幂等

### 金额

- 所有金额为整数分；前端唯一展示格式化在 `utils/price.ts`（`Intl.NumberFormat` 千分位 + 两位小数）；
- 商品/订单/退款/优惠券金额全部直接展示后端返回值，不参与交易计算；
- 结算页「商品合计（预估）」「优惠（预估）」「应付（预估）」明确标注预估；
- 成功页展示服务端 `original_amount / discount_amount / total_amount`。

### 幂等

`utils/idempotency.ts`：

```
buildOrderSignature(addressId, lines, couponId = 0)
→ `cart|${addressId}|${couponId}|${sku:qty,...}`（sku 升序）
```

- 与后端 `requestHash` 完全一致；
- `useCreateOrder` 用 `keyRef{signature,key}`：signature 相同复用 key，变化生成新 key；
- 换地址/商品增减/换券/取消用券都会换 key，避免 9006；
- 网络超时重试复用原 key，服务端唯一键兜底。

## 8. Mock 与真实后端测试的区别

| 层 | 网络 | 数据 | 能证明什么 |
| --- | --- | --- | --- |
| 单元测试（Vitest 纯函数） | 无 | 构造 | 展示/计算/校验逻辑 |
| 组件测试（Vitest + MSW） | MSW 拦截 | 内存 Mock | 交互、缓存同步、错误态 |
| E2E（Playwright + MSW） | MSW | 内存 Mock | 浏览器级流程与路由 |
| 真实联调（W5） | 真实 HTTP | MySQL/Redis | 前后端契约、事务与状态流转 |

W5 真实联调环境：MySQL 8.0（独立容器 3307，全新库） + Redis 7（6380, DB9） + `serveproc`（tags=serve_lifecycle） + `qiniu_mock` + `vite dev`（5174 代理到 8000），**未设置 `VITE_ENABLE_MOCK`**。

> MSW 的 Mock 与真实后端语义对齐（状态闸门、错误码、幂等指纹、券锁定/释放），但**不能**替代真实联调：
> 真实事务、并发、SQL 时区、审计写入只有真实后端能验证。

## 9. 生产构建与部署路径

### 构建

- `npm run build` = `tsc -b` + `vite build`；
- 产物 `dist/` 896 KB；主 chunk `index-*.js` 371.2 KB（gzip 117.8 KB）+ CSS 44.6 KB；
- 路由级懒加载：每个账户页/结算页/详情页独立 chunk（1.9–23.3 KB）。

### Mock 隔离（W5 修复）

- 运行时：`main.tsx` 仅 `VITE_ENABLE_MOCK === 'true'` 时动态 import MSW；
- 构建期：`stripMockArtifacts` 插件在 `closeBundle` 删除 `dist/mockServiceWorker.js` 与 `dist/mock/`；
- 验证：`dist/` 仅 `assets/`、`favicon.svg`、`index.html`；`grep mockServiceWorker dist/assets` 无结果。

### 部署路径

```
浏览器 → Nginx:80
  ├── /            → 前端静态资源（需要 try_files fallback）
  └── /api/*       → proxy_pass http://app:8000/（需要剥离 /api）
```

当前仓库 `nginx/nginx.conf`：

- `location / { proxy_pass http://my_shop_backend; }` —— 原样转发，**不剥离 `/api`**；
- 镜像只 COPY 配置与错误页，**不托管前端 dist**，无 `try_files`；
- 实测：Go 服务 `/api/products` → 404，`/products` → 200。

→ 生产上线需基础设施线补两条 location（详见交付报告「未解决问题」）。
前端侧已通过 `vite preview` 验证深链刷新（`/products/1`、`/account/refunds` 等均返回 index.html）。

## 10. 本次发现的问题及修复

| # | 级别 | 问题 | 证据 | 修复 |
| --- | --- | --- | --- | --- |
| 1 | P1 | 会话失效（refresh 失败 / 401 重试失败）只清理 auth/addresses/favorites/cart，订单/退款/优惠券缓存残留，切换账号可能看到上一位用户数据 | `use-auth-cache-reset.ts` 旧实现仅 4 个 `removeQueries`；`useLogout` 有 7 个 → 两处清单漂移 | 新增 `auth/user-cache.ts` 的 `clearUserScopedQueries()`，两处共用；新增 `user-cache.test.ts`（7 类 key 全清、公开 key 不动）与 `use-auth-cache-reset.test.tsx`（清 Token 后券缓存被移除并跳登录） |
| 2 | P2 | 生产构建把 `public/mockServiceWorker.js`（20 KB）与 `public/mock/*.svg`（12 KB）复制进 `dist/`，测试专用资源进入生产产物 | `ls dist` 可见 `mock/` 与 `mockServiceWorker.js` | `vite.config.ts` 新增 `stripMockArtifacts` 构建插件；重建后 `dist` 仅 3 项，体积 928K → 896K |
| 3 | P2 | `web/README.md` 停留在 W2-02 状态（声称购物车/订单未开放），与最终代码不符 | README 开头与目录结构 | 更新为 W0—W5 最终状态，补充目录、数据层、状态边界、测试规模、生产构建/部署与已知缺口 |

未修改：Go 后端、SQL、管理端、Docker/Nginx/K8s 配置。

## 11. 尚存技术债

| # | 级别 | 内容 | 影响 |
| --- | --- | --- | --- |
| 1 | P1（外部） | Nginx 未剥离 `/api`、未托管 `dist`、无 `try_files` | 生产环境前端无法直接上线（基础设施线） |
| 2 | P3 | 后端 `category_id` 精确匹配、不递归子分类 | 前端分类导航只渲染叶子节点，规避了该限制；若以后改成可点父分类需后端支持 |
| 3 | P3 | `npm run typegen` 指向不存在的 `contracts/openapi.yaml` | 契约生成不可用，DTO 仍手写对齐 Go JSON tag |
| 4 | P3 | 主 chunk 371 KB（React/Router/Query 等） | gzip 117.8 KB，首屏可接受；可进一步拆 vendor |
| 5 | P3 | 订单列表一次返回全部订单（无分页） | 订单量大时首屏变慢，已在页面标注本地分页 |
| 6 | P3 | 退款/优惠券列表同样无服务端分页与筛选 | 同上 |
| 7 | P3 | 时间字段时区不统一（后端 DATETIME 原样展示） | 前端不做时区换算，避免把数据问题伪装成正确时间 |
| 8 | P3 | 401 后依赖 `clearAuthTokens` 触发清理，若未来出现不经过该函数的会话失效路径需同步扩展 | 维护注意 |

## 12. 维护注意事项

1. **新增账号私有数据**：必须把 `all` key 加入 `src/auth/user-cache.ts` 的 `USER_SCOPED_KEYS`，否则会话失效路径会残留。
2. **新增下单字段**：必须同步 `buildOrderSignature` 与后端 `requestHash`，否则换内容仍复用旧键 → 9006。
3. **新增业务状态**：先改后端，再同步 `utils/orders.ts`、`utils/refunds.ts`、`utils/coupons.ts` 的文案/色调/筛选/可操作性判断。
4. **Mock 与真实后端对齐**：任何 Mock 行为变更都要先看 Go 源码（状态闸门、错误码、事务边界），不要把 Mock 当契约。
5. **不要把前端守卫当授权**：`RequireAuth` 只是导航体验；所有权限由后端校验。
6. **金额**：只展示后端返回的整数字段，禁止在页面里做折扣/运费/退款计算。
7. **生产构建**：新增 `public/` 下的测试资源时，要同步扩展 `stripMockArtifacts`。
8. **部署**：上线前必须确认反向代理的 `/api` 剥离与 SPA fallback。

## 13. 技术学习与面试准备

### 13.1 可以讲的架构点

1. **三层数据边界**：服务端数据（Query）、URL（列表筛选）、内存 Token（认证）+ Zustand（纯 UI）。
2. **401 单飞刷新**：并发 401 只发一次 refresh，避免后端 refresh 轮换 + reuse 检测导致全量会话撤销。
3. **Token 只存内存**：以"刷新页面需重新登录"换取 XSS 不可窃取长期凭证。
4. **幂等指纹含业务字段**：地址、商品行、优惠券共同决定 key，前端与后端 hash 对齐。
5. **缓存失效的因果链**：写操作影响哪些服务端视图，就失效哪些 key（订单 → 列表/详情；支付/取消 → 券）。
6. **账号隔离双保险**：主动登出清理 + 会话失效监听，共用一份 key 清单。

### 13.2 常见追问

- **Q：前端如何保证不重复下单？**
  A：`mutation.isPending` + 同步 ref 锁防连点；同内容复用幂等键；服务端 `uk_user_idempotency` + `request_hash` 做最终裁决。
- **Q：换券为什么必须换幂等键？**
  A：后端 hash 含 `coupon_id`，同键不同券直接 9006；前端 signature 变化即生成新键。
- **Q：401 后为什么不能直接跳登录？**
  A：先尝试 refresh；refresh 失败才清 Token。否则短暂网络抖动会把用户踢出。
- **Q：为什么退款/优惠券不做乐观更新？**
  A：状态由后台人工推进或服务端事务决定，前端无法预测；乐观更新失败回滚会造成状态闪回。
- **Q：前端如何避免切换账号看到旧数据？**
  A：所有账号私有 key 不含 user id，因此必须在会话结束时 `removeQueries`；W5 把清单收敛到一处并加回归测试。
- **Q：生产环境为什么不能带 MSW？**
  A：会掩盖真实接口问题，且 Mock 数据可能污染真实业务展示；本项目用运行时开关 + 构建插件双重隔离。

### 13.3 验收方法论

- 先静态审查（结构/状态/请求/安全），再门禁（lint/typecheck/test/build/e2e），再真实联调，最后 UI/响应式巡检；
- 真实联调必须设计独立数据与顺序，避免前一用例改变状态影响后一用例；
- 结论要区分「前端自身达标」与「外部依赖阻塞」。
