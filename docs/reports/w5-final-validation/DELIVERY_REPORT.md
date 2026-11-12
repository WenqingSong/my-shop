# W5 最终验收报告：SurgeCart React 用户商城

- 任务：W5 用户商城最终工程验收（W0—W4 交付后的最后阶段）
- 执行模型：DeepSeek-V4.1-Flash
- 验收日期：2026-11-10
- 分支：`feat/phase2-user-frontend`
- 验收基线：`d33948704511d2d766becedcbe35b67adcbb1269`（W4-04）
- 范围限制：仅修改 `web/`；未修改 Go 后端、SQL、管理端、Docker/Nginx/Kubernetes

---

## 0. 最终结论

**CONDITIONAL PASS**

- **前端自身：PASS**。核心业务链路、自动化门禁、生产构建、响应式布局、Mock 隔离全部通过；未发现未解决的 P0/P1 前端缺陷。
- **整体上线：CONDITIONAL PASS**。存在 1 个明确的外部依赖阻塞：仓库 `nginx/nginx.conf` 未剥离 `/api` 前缀、未托管前端 `dist/`、无 SPA `try_files` 回退。该文件属于基础设施开发线，本任务禁止修改，已记录准确文件路径与所需变更（见第 10 节）。

结论依据：真实后端全链路 81/81 通过；UI/响应式 82/82 通过；lint/typecheck/test/build/E2E 全部通过；安全审查无 P0。

---

## 1. Git 状态与阶段完成情况

- 分支：`feat/phase2-user-frontend`
- 验收开始时 HEAD：`d33948704511d2d766becedcbe35b67adcbb1269`
- 相对 `main` 领先 11 个提交，W0—W4 全部在历史中：

| 阶段 | 提交 | 主题 |
| --- | --- | --- |
| W0 | `631dc03` | 前端工程基础 |
| W1-01 | `5e40486` | 商品浏览 |
| W2-01 | `ea0b025` | 用户认证 |
| W2-02 | `93d916b` | 个人中心/地址/收藏 |
| W3-01 | `ad21be2` | 购物车 |
| W3-02 | `1186162` | 结算与幂等下单 |
| W4-00 | `6935f36` | 后端能力审查 |
| W4-01 | `a0dc9b0` | 订单中心 |
| W4-02 | `c669855` | 订单履约 |
| W4-03 | `7ccbe79` | 退款中心 |
| W4-04 | `d339487` | 优惠券中心 |

W0—W4 交付报告与开发知识文档均存在（`docs/reports/w*`、`.agent/tasks/dev/W*.md`）。

- 验收结束时最终 Commit SHA：由本次 W5 提交给出（提交后写入 Owner 汇报；同一提交内无法写入自身 SHA）
- 工作区：提交后 clean

---

## 2. 工程审查结果（W5-01）

### 2.1 工程结构

| 目录 | 文件数 | 行数 | 结论 |
| --- | --- | --- | --- |
| `api/` | 29 | 2847 | 按域拆分；传输/解包/刷新/Key 分层清晰 |
| `auth/` | 15 | ~900 | Token/刷新/守卫/缓存清理职责单一 |
| `components/` | 54 | 4240 | 按业务域分组（cart/checkout/order/refund/coupon/product/layout/ui） |
| `hooks/` | 7 | 819 | 服务端状态封装，无渲染逻辑 |
| `pages/` | 45 | 7596 | 页面装配与表单编排 |
| `utils/` | 17 | 1576 | 纯函数，可独立测试 |
| `mocks/` | 11 | 2794 | 与 Go 语义对齐的内存 Mock |
| `router/` | 2 | 244 | 路由表 + 懒加载 |

未发现明显职责混乱：组件层无 `fetch`/`apiClient` 直调；`queryKey` 字面量出现次数为 **0**（78 处全部走 `queryKeys` 工厂）。

### 2.2 状态管理

- TanStack Query：服务端数据唯一权威缓存（商品/购物车/订单/退款/优惠券/地址/收藏/当前用户）；
- Zustand：仅 `ui-store`（移动端导航开关），不含业务数据；
- 无重复请求：同 key 查询自动去重；Header 购物车徽标复用 `cart.list`；
- 无无效缓存：写操作按因果链失效（下单→订单列表+购物车+券；支付/取消→订单+券）；
- **账号隔离缺陷已修复**（见第 11 节 P1）。

### 2.3 API Client

| 检查项 | 结论 |
| --- | --- |
| 统一响应解析 | `unwrapEnvelope` 校验 `{code,message,data}`，`code!=0` 抛 `ApiError` |
| 错误处理 | `kind: http/network/timeout/aborted/parse` + `status/code/details` |
| Token 注入 | `setAuthTokenProvider`，每次请求读取内存值 |
| Refresh 单飞 | 模块级 Promise，并发 401 只发一次 |
| 401 重试 | 刷新成功重试一次，仍失败清 Token |
| 请求取消 | 查询 `signal` 透传到 `apiClient`，与超时控制器串联 |
| 超时 | 默认 15s，可配 |
| Base URL | 默认 `/api`，可由 `VITE_API_BASE_URL` 覆盖 |

### 2.4 业务数据一致性

| 检查项 | 结论 |
| --- | --- |
| 金额整数分 | 交易金额全部为后端整数字段；前端仅展示格式化（`Intl.NumberFormat`），无浮点换算 |
| 订单金额 | 只展示服务端 `original/discount/total`；结算页明确标注「预估」 |
| 幂等键 | 指纹含 `coupon_id`，与后端 `requestHash` 一致 |
| 优惠券 ID | 真实 `user_coupon_id` 参与签名与请求 |
| 购物车同步 | 加购/改量/勾选/删除/下单后失效 `cart.all` |
| 订单状态 | 入口严格按后端状态闸门；不做本地状态推导 |
| 退款状态 | 7 状态映射；无可撤销入口时不渲染 |
| 优惠券状态 | 3 状态映射；下单锁定/支付核销/取消释放 |

### 2.5 安全性

| 检查项 | 结果 |
| --- | --- |
| 硬编码 Token/密码/密钥 | 未发现；仓库内无联调凭据、无 `.env.local`；`demo12345` 仅 MSW 测试账号 |
| 敏感信息进日志 | 仅 `AppErrorBoundary` 的渲染错误 `console.error`，不含凭证 |
| 不安全 HTML 渲染 | `dangerouslySetInnerHTML` 使用次数 **0** |
| 前端绕过认证 | 无 `skipAuth`/`bypass`/`devMode` 标记；路由守卫仅 UX，后端强制鉴权 |
| 前端权限当授权 | 未发现；所有受保护接口由 Go `Auth`/`RequirePermission` 校验 |
| 切换用户缓存泄漏 | 主动登出 + 会话失效共用清理清单（W5 修复） |
| XSS 面 | Token 只在内存，无 localStorage/sessionStorage |

---

## 3. 完整业务链路验证结果（W5-02，真实后端）

### 3.1 环境（真实，非 Mock）

- MySQL 8.0（独立容器 `127.0.0.1:3307`，全新库 `my_shop_w5`，`migrate up` 至 `20261001000024`）
- Redis 7（`127.0.0.1:6380`，DB 9）
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + `test/container/qiniu_mock`（未修改任何 Go 代码）
- 前端：`vite` dev（5174）+ `/api` 代理到 8000，**未设置 `VITE_ENABLE_MOCK`**
- 数据：2 个分类、2 个商品、4 个 SKU、库存、轮播、推荐位、2 条评价（独立测试库，不触碰正式库）

### 3.2 接口级全链路：81 / 81 PASS

| 链路 | 验证项 |
| --- | --- |
| 商品浏览 | 分类树、轮播、商品列表 total=2、关键词搜索、分类精确匹配（父分类不递归、子分类=2）、详情 2 SKU、评价列表与平均分、推荐位 2 商品 |
| 用户认证 | 注册、登录、`/me`、未登录 401、refresh 换发、logout、登出后 refresh 401、重新登录 |
| 个人中心 | 地址新增/首条默认/修改/新默认覆盖旧默认/删除、收藏添加/列表/状态/取消 |
| 购物车 | 加购、数量修改、取消勾选、无勾选下单 9004、重新勾选 |
| 订单交易 | 创建（金额 1289700）、同键同内容重试返回既有订单、同键不同内容 9006、列表、详情、订单项 |
| 订单履约 | 模拟支付（paid+paid_at）、管理员发货（shipped）、确认收货（completed）、提交评价、重复评价拒绝(10003) |
| 库存 | 下单扣减、取消恢复、退款成功恢复；库存不足 6001 |
| 退款 | 申请（金额=实付）、列表、详情、撤销（订单仍 paid）、审核通过+执行成功（订单 refunded） |
| 优惠券 | 领取、用券下单（discount=30000,total=399900）、锁定、支付核销、取消释放（order_id 清空）、退款不返还 |
| 不可购买 | 先加购后下架 → 购物车 `available=false`/`off_shelf` → 下单 9003 → 恢复上架 |
| 账号隔离 | 用户B 订单/退款/券列表为空；访问用户A 订单 9001/404、退款 18001/404 |

### 3.3 UI 级与响应式：82 / 82 PASS

| 分类 | 验证项 |
| --- | --- |
| 5 个视口 | 390 / 768 / 1024 / 1440 / 1920；每视口覆盖首页、商品列表、商品详情、注册、登录后首页、账户概览、订单列表、退款列表、优惠券、收货地址、收藏、个人资料、购物车、结算页 |
| 横向溢出 | 每页 `scrollWidth ≤ clientWidth`（全部 equal，无横向滚动） |
| 移动端导航抽屉 | 宽 358 / 视口 390，不溢出 |
| 移动端 Dialog | 退款申请弹窗 358×602，适配 390×844 |
| 详情页 | 390 订单详情；1440 订单详情、退款详情 |
| 键盘与焦点 | Tab 可聚焦链接，`outline: 2px solid` 可见 |
| 全链路交互 | 选券 → 提交 → 成功页三项金额 → 查看订单 → 取消 → 券恢复「可使用」 |
| 控制台 | 无未捕获异常；无非预期错误 |
| 空态/错误态/加载态 | 各页 Empty/Error/Loading 正常（空购物车、空券、空订单均有明确文案） |

### 3.4 无法验证的链路（环境限制）

- **真实支付渠道**：项目为 Mock 支付，无第三方 SDK/流水/回调，「资金到账」类链路不存在，不可验证。
- **真实优惠券领取入口**：后端无公开券模板列表接口，领取仅在接口级用管理员创建的模板验证，UI 不提供领券中心。
- **秒杀/拼团/砍价**：不属于用户商城前端范围。
- **生产 Nginx 端到端**：仓库 Nginx 未托管前端且未剥离 `/api`，属于外部阻塞（见第 10 节），本次以 `vite preview` 验证 SPA 深链刷新。

---

## 4. Mock 自动化测试结果（与真实联调严格区分）

| 层 | 环境 | 规模 | 结果 |
| --- | --- | --- | --- |
| 单元测试 | Vitest（纯函数/Client/Auth） | 含在下列 45 文件内 | 通过 |
| 组件测试 | Vitest + jsdom + MSW | **45 个文件 / 317 个用例** | 全部通过 |
| E2E | Playwright + `VITE_ENABLE_MOCK=true`（MSW） | **10 个 spec / 46 个用例** | 全部通过 |

覆盖：商品浏览/搜索/分页、认证与 401 刷新、地址 CRUD、收藏、购物车（数量/勾选/删除/库存/不可购买）、结算与幂等（9006/网络重试）、订单列表/详情/操作、退款全流程、优惠券全流程、账号缓存隔离、键盘与错误态。

**声明**：以上使用 MSW，不代表真实后端；真实后端结果见第 3 节。

---

## 5. UI 与兼容性检查结果（W5-03）

| 检查项 | 390 | 768 | 1024 | 1440 | 1920 |
| --- | --- | --- | --- | --- | --- |
| Header 溢出 | 无 | 无 | 无 | 无 | 无 |
| 商品卡片错位 | 无 | 无 | 无 | 无 | 无 |
| 列表/表格横向溢出 | 无 | 无 | 无 | 无 | 无 |
| Dialog 适配 | 358×602 | — | — | 正常 | — |
| 导航抽屉 | 358/390 | — | — | — | — |
| 表单可操作 | 是 | 是 | 是 | 是 | 是 |
| 错误提示 | 完整 | 完整 | 完整 | 完整 | 完整 |
| Loading/Empty | 正常 | 正常 | 正常 | 正常 | 正常 |
| 价格显示 | 正确（整数分格式化） | 正确 | 正确 | 正确 | 正确 |

说明：表格中的「—」表示该检查只在对应视口执行（移动端专用项）。全部页面均通过横向溢出检测，未发现明显视觉缺陷，因此**未做任何 UI 重设计**。

基本键盘操作：Tab 可聚焦、焦点样式可见（2px solid outline）；表单标签均有关联 `label`/`aria-label`（现有测试与 Playwright role 查询共同验证）。

---

## 6. lint / typecheck / test / build / E2E 结果（W5-04）

执行目录 `web/`：

| 命令 | 结果 | 备注 |
| --- | --- | --- |
| `npm run lint` | ✅ 0 error / 0 warning | ESLint flat config |
| `npm run typecheck` | ✅ | `tsc -b` strict |
| `npm run test` | ✅ 45 文件 / 317 用例 | Vitest + MSW |
| `npm run build` | ✅ | `tsc -b && vite build`，460ms |
| `npm run test:e2e` | ✅ 46 用例 | Playwright，MSW |

### 构建产物

| 项 | 数值 |
| --- | --- |
| `dist/` 总大小 | 896 KB（修复 Mock 资源前 928 KB） |
| 主入口 `index-*.js` | 371.21 KB（gzip 117.77 KB） |
| 样式 `index-*.css` | 44.58 KB |
| 最大页面 chunk | `order-detail-page` 23.32 KB（gzip 6.61 KB） |
| 最小页面 chunk | `profile-page` 1.99 KB |
| 路由懒加载 | 全部账户页/结算/详情独立 chunk |

---

## 7. 生产环境配置检查

| 配置 | 现状 | 结论 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | 默认 `/api`；`.env.example` 明确说明需转发层剥离 | 正确 |
| `VITE_ENABLE_MOCK` | 默认 `false`；仅 `=== 'true'` 才加载 MSW | 生产默认关闭 |
| `VITE_HOME_RECOMMEND_CODE` | 默认空；空则不请求、不渲染推荐区块 | 正确 |
| `VITE_API_TIMEOUT_MS` | 默认 15000 | 正确 |
| `VITE_DEV_PROXY_TARGET` | 仅 dev server 使用 | 不进入生产 |
| 测试账号/Token | 未写入任何生产配置；仓库无 `.env.local` | 安全 |
| Mock 静态资源 | 构建插件剔除 `mockServiceWorker.js` 与 `mock/` | 生产不包含 |

`dist/index.html` 资源引用为 `/assets/...` 绝对路径，适用于根路径部署。

---

## 8. Nginx / API 代理检查结果

| 文件 | 位置 | 现状 | 结论 |
| --- | --- | --- | --- |
| `nginx/nginx.conf` | `location /`（第 81–82 行） | `proxy_pass http://my_shop_backend;` 原样转发 | ❌ 未剥离 `/api`；未托管前端静态资源 |
| `nginx/nginx.conf` | 全文 | 无 `try_files`、无 `root` 指向前端 dist | ❌ SPA 深链在真实部署会 404 |
| `nginx/Dockerfile` | 第 12–13 行 | 仅 COPY `nginx.conf` 与 `error/` | ❌ 不包含前端产物 |

实测证据：

```
GET http://127.0.0.1:8000/api/products  → 404   （Go 路由不带 /api）
GET http://127.0.0.1:8000/products      → 200
GET http://127.0.0.1:4174/products/1    → 200   （vite preview 的 SPA fallback 正常）
```

所需变更（交基础设施负责人，本任务未修改）：

```nginx
# 1) API 前缀剥离（尾部斜杠是关键）
location /api/ {
    proxy_pass http://my_shop_backend/;
    # 其余 proxy_set_header / 超时 / proxy_next_upstream off 保持现状
}

# 2) 前端静态资源 + SPA 回退
location / {
    root /usr/share/nginx/html;   # 挂载或 COPY 前端 dist
    try_files $uri $uri/ /index.html;
}
```

同时需确认 `/storage/` 静态资源反代与现有安全响应头保留。

---

## 9. 安全检查结果

| 检查项 | 结果 |
| --- | --- |
| 硬编码凭据 | 未发现（扫描 Token/密码/密钥/联调口令） |
| 测试凭据入库 | 仅 `demo/demo12345` 的 MSW 测试账号，位于测试与 Mock 代码，不进入生产运行路径 |
| 日志泄漏 | 仅错误边界打印渲染错误，无凭证/请求体 |
| 不安全 HTML | 0 处 `dangerouslySetInnerHTML` |
| 认证绕过 | 无 skipAuth/bypass/devMode |
| 前端权限当授权 | 未发现；后端强制鉴权 |
| 账号缓存泄漏 | 主动登出 + 会话失效共用清单（本次修复） |
| Token 存储 | 内存 only，无 Web Storage |
| 依赖 | 无新增依赖；无已知高危包引入 |

---

## 10. 已修复问题

| # | 级别 | 问题 | 修复 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | P1 | 会话失效路径（refresh 失败/401 重试失败）只清理 auth/addresses/favorites/cart，订单/退款/优惠券缓存残留，切换账号可能看到上一位用户数据 | 新增 `src/auth/user-cache.ts` 的 `clearUserScopedQueries()`；`useLogout` 与 `useAuthCacheReset` 共用同一清单 | 新增 `user-cache.test.ts`（2 用例，7 类 key 全清、公开 key 保留）与 `use-auth-cache-reset.test.tsx`（清 Token 后券缓存移除并跳登录）；全量测试 317 通过 |
| 2 | P2 | 生产构建包含 `mockServiceWorker.js`（20 KB）与 `mock/*.svg`（12 KB），测试专用资源进入产物 | `vite.config.ts` 新增 `stripMockArtifacts` 构建插件 | 重建后 `dist` 仅 `assets/`、`favicon.svg`、`index.html`；体积 928K→896K；`grep mockServiceWorker dist/assets` 无结果 |
| 3 | P2 | `web/README.md` 仍停留在 W2-02，声称购物车/订单未开放 | 更新为 W0—W5 最终状态，补充目录、数据层、状态边界、测试规模、生产构建与部署缺口 | 人工校对 + 与最终代码一致 |

修复只涉及 `web/src/**`、`web/vite.config.ts`、`web/README.md`，未触碰其他所有权文件。

---

## 11. 未解决问题及其影响

| # | 级别 | 问题 | 影响 | 责任方 |
| --- | --- | --- | --- | --- |
| 1 | P1（外部阻塞） | `nginx/nginx.conf` 未剥离 `/api`、未托管 `dist`、无 `try_files` | 生产环境前端无法直接上线；接口 404、深链刷新 404 | 基础设施线（本任务禁止修改） |

除该外部依赖外，**不存在未解决的 P0/P1 前端问题**。

---

## 12. 已知技术债

| # | 级别 | 内容 |
| --- | --- | --- |
| 1 | P3 | 后端 `category_id` 精确匹配、不递归子分类（前端分类导航仅渲染叶子节点，已规避） |
| 2 | P3 | `npm run typegen` 指向不存在的 `contracts/openapi.yaml`，DTO 仍手写对齐 |
| 3 | P3 | 主 chunk 371 KB（gzip 117.8 KB），可进一步拆 vendor |
| 4 | P3 | 订单/退款/优惠券列表无服务端分页与筛选，本地处理 |
| 5 | P3 | 后端时间字段时区不统一，前端原样展示、不做换算 |
| 6 | P3 | 会话失效清理依赖 `clearAuthTokens` 触发，新增失效路径需同步 |

---

## 13. 文档路径

- 开发知识文档：`.agent/tasks/dev/W5-final-validation.md`
- 正式交付报告：`docs/reports/w5-final-validation/DELIVERY_REPORT.md`

---

## 14. 验收对照

| 完成标准 | 结果 |
| --- | --- |
| 全部核心业务链路完成回归 | ✅ 接口级 81/81 + UI 级 82/82（真实后端） |
| 前端测试门禁通过 | ✅ lint / typecheck / 317 单测 / build / 46 E2E |
| 生产构建通过 | ✅ 896 KB，Mock 资源已剔除，深链 fallback 正常 |
| 响应式页面无严重问题 | ✅ 5 视口全覆盖，无横向溢出 |
| Mock 与真实环境隔离正确 | ✅ 运行时开关 + 构建插件 + 产物扫描 |
| 不存在未解决的 P0 | ✅ |
| 不存在未处理的 P1（前端） | ✅（唯一 P1 为外部 Nginx 配置） |
| 文档完整 | ✅ |
| Git 工作区干净 | ✅（提交后） |

---

## 15. 后续建议（不扩大范围）

1. 基础设施线补齐 Nginx 三条配置（第 8 节），完成后可解除 CONDITIONAL PASS。
2. 后端若开放订单/退款/优惠券分页，前端可移除本地分页。
3. 后端若补齐 OpenAPI 契约，可启用 `npm run typegen` 替换手写 DTO。
4. 可选：把 React/Router/Query 拆为 vendor chunk，进一步降低首屏主 chunk。
