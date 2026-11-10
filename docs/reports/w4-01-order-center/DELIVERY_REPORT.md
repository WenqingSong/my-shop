# W4-01 用户订单中心：交付报告

- 任务：SurgeCart 用户商城 W4-01（订单列表 + 订单详情）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W4-01-order-center.md`
- 前置依赖：W3-02 下单闭环（`POST /orders` / `GET /orders/:id` 已在用）
- 后端能力依据：`docs/reports/w4-backend-capability-audit/REPORT.md`（W4-00 只读审查）
- 提交说明：代码、两份文档属同一个提交；报告无法包含自身提交 SHA，见交付汇报与 `git log -1 --format=%H`

---

## 0. 摘要

本期完成用户订单中心：**订单列表**（本地状态筛选 + 本地分页）、**订单详情**
（商品明细、金额快照、收货快照、生命周期进度、时间信息），并接入个人中心导航、
Header 入口与下单成功页跳转。

- 全部数据来自真实接口 `GET /orders` 与 `GET /orders/:id`，未使用任何前端伪造状态或金额；
- 状态筛选与分页**明确是浏览器本地行为**，页面上有文案说明，请求不携带任何后端不支持的参数；
- **未实现待支付倒计时**，原因有实测证据（见第 6 节），不是遗漏；
- 未实现支付/取消/确认收货/退款按钮，这些属于 W4-02、W4-03；
- 未修改任何 Go 代码、SQL、管理端、Docker、Nginx、Kubernetes。

---

## 1. 完成的页面与功能

### 1.1 `/account/orders` 我的订单列表

| 功能 | 说明 |
| --- | --- |
| 全部订单 | `GET /orders` 一次拉取本人全部订单（后端已按 id 倒序） |
| 状态筛选 | 全部 / 待支付 / 待发货 / 待收货 / 已完成 / 已关闭，纯本地过滤 |
| 分组计数 | 每个筛选标签上的数量为本地统计，非服务端 count |
| 本地分页 | 每页 10 条，页面标注「本地分页，第 x/y 页」 |
| 订单卡片 | 订单号、下单时间、状态徽标、商品图/名称/SKU/成交单价/数量、件数、应付金额、详情入口 |
| 加载 / 空 / 错误 | Loading、全局空态（引导去逛逛）、分组空态（引导回全部）、错误态 + 重新加载 |
| 刷新 | 手动刷新按钮，走 `refetch()` |

### 1.2 `/account/orders/:id` 订单详情

| 功能 | 说明 |
| --- | --- |
| 订单头 | 订单号（等宽字体）、状态徽标、返回列表 |
| 生命周期进度 | 待支付 → 待发货 → 待收货 → 已完成，由当前状态推导，页面明确标注「不是历史事件记录」 |
| 终止态 | 已取消显示「订单已取消，交易终止」+ 取消原因；已退款显示「订单已退款，交易终止」 |
| 支付截止时间 | 仅 `pending_payment` 且 `expire_at` 非空时展示后端原值（静态，无倒计时） |
| 商品明细 | 商品图、商品名、SKU 名、数量、成交单价（不做小计） |
| 金额 | 商品总金额 / 优惠金额 / 应付金额，全部来自后端快照 |
| 收货信息 | 收货人、电话、省市区 + 详细地址（订单快照，不读地址簿） |
| 时间信息 | 只渲染后端返回非 null 的时间字段 |
| 异常 | 非法 id → 信息缺失；404 → 不存在或无权查看；其他错误 → 错误态 + 重试 |

### 1.3 与现有页面的整合

| 位置 | 变更 |
| --- | --- |
| 个人中心侧边导航 | 新增「我的订单」（`/account/orders`，`end: false`，详情页也高亮） |
| 个人中心概览 | 新增「我的订单」功能卡片 |
| Header | 登录后新增「我的订单」入口 |
| 移动端导航抽屉 | 登录后新增「我的订单」 |
| 下单成功页 | 新增主按钮「查看订单」→ `/account/orders/:id`；购物车改为 ghost 按钮 |
| 下单成功 | 成功回调额外失效 `orders.list()`，保证新订单立刻出现在列表 |

---

## 2. 路由清单

| 路径 | 组件 | 保护 | 懒加载 | 布局 |
| --- | --- | --- | --- | --- |
| `/account/orders` | `OrdersPage` | `RequireAuth`（外层 `/account`） | 是 | `AccountLayout` |
| `/account/orders/:id` | `OrderDetailPage` | `RequireAuth`（外层 `/account`） | 是 | `AccountLayout` |

新增 chunks：`orders-page-D3HDlvLl.js` 5.71 kB（gzip 2.42 kB）、
`order-detail-page-D3EWQTw0.js` 10.03 kB（gzip 3.17 kB）。

---

## 3. 使用的 API

| Method | Path | 用途 | 认证 |
| --- | --- | --- | --- |
| GET | `/orders` | 本人订单列表 | `Authorization: Bearer <access_token>` |
| GET | `/orders/:id` | 本人订单详情 | 同上 |

**明确未使用**：`status` / `page` / `size` 查询参数（后端不支持且会静默忽略），
以及任何支付/取消/收货/退款接口。

---

## 4. 请求与响应结构

### 4.1 `GET /orders`

请求：无参数。

响应：

```json
{
  "code": 0,
  "message": "OK",
  "data": {
    "items": [ /* Order[]，按 id DESC */ ]
  }
}
```

失败：

```json
{ "code": 1002, "message": "未授权", "data": null }   // HTTP 401
{ "code": 1000, "message": "内部错误", "data": null } // HTTP 500
```

### 4.2 `GET /orders/:id`

请求：path 参数 `id`（int64）。

响应 `data` 为完整 Order：

```json
{
  "id": 1,
  "order_no": "1794298503424e96d154a5788",
  "user_id": 1,
  "status": "paid",
  "total_amount": 3998,
  "original_amount": 3998,
  "discount_amount": 0,
  "user_coupon_id": null,
  "idempotency_key": "...",
  "recipient_name": "张三",
  "phone": "13800138000",
  "province": "广东省",
  "city": "深圳市",
  "district": "南山区",
  "detail": "科技园1号",
  "address_id": 1,
  "expire_at": "2026-11-10 16:30:03",
  "cancel_reason": null,
  "paid_at": "2026-11-10 16:15:03",
  "shipped_at": null,
  "received_at": null,
  "completed_at": null,
  "cancelled_at": null,
  "refunded_at": null,
  "created_at": "2026-11-10 08:15:03",
  "updated_at": "2026-11-10 08:15:03",
  "items": [
    {
      "id": 1,
      "sku_id": 1,
      "product_id": 1,
      "sku_name": "默认规格",
      "product_name": "W4验证商品",
      "product_main_image": "",
      "price": 1999,
      "quantity": 2
    }
  ]
}
```

> 以上为**真实后端实际返回**的报文（取自本次联调），未做任何字段增删。

失败：

| code | HTTP | 含义 |
| --- | --- | --- |
| `1002` | 401 | 未授权（缺 token / token 无效 / 会话失效） |
| `9001` | 404 | 订单不存在或不属于当前用户（防枚举，二者不区分） |
| `1000` | 500 | 内部错误 |

---

## 5. 订单状态映射

后端 7 态 → 前端展示：

| 后端状态 | 徽标文案 | 列表分组 | 进度表现 |
| --- | --- | --- | --- |
| `pending_payment` | 待支付 | 待支付 | 第 1 步 current |
| `paid` | 已支付 | 待发货 | 第 1 步 done，第 2 步 current |
| `shipped` | 已发货 | 待收货 | 前 2 步 done，第 3 步 current |
| `received` | 已收货 | 已完成 | 全部 done（后端实际不可观测） |
| `completed` | 已完成 | 已完成 | 全部 done |
| `cancelled` | 已取消 | 已关闭 | 全部 todo + 终止提示 |
| `refunded` | 已退款 | 已关闭 | 第 1 步 done、第 2 步 current + 终止提示 |

- 徽标始终展示**后端状态原文**；「待发货/待收货/已关闭」是前端分组名，不是后端状态；
- 分组映射集中在 `web/src/utils/orders.ts` 的 `ORDER_STATUS_GROUPS`，
  并有单测锁定「7 个状态被且仅被一个分组覆盖」。

---

## 6. 本地筛选与本地分页

### 6.1 本地筛选

```ts
const filtered = filterOrdersByGroup(orders, activeGroup)
```

- 输入是 `GET /orders` 一次性返回的全部订单；
- 切换分组时重置到第 1 页；
- 分组计数由同一份数据在本地统计。

### 6.2 本地分页

```ts
const totalPages = Math.max(1, Math.ceil(filtered.length / 10))
const currentPage = Math.min(page, totalPages)
const pageItems = filtered.slice((currentPage - 1) * 10, currentPage * 10)
```

页面固定展示：「共 N 笔订单 · 本地分页，第 x/y 页」，
并在页面描述中写明「状态筛选与分页在浏览器本地完成，不是服务端筛选/分页」。

**这不是服务端分页**：请求始终是 `GET /orders`，没有 `page/size`，
网络传输量不随页码变化，后端也不返回 `total`。

---

## 7. 倒计时：未实现，及依据

### 7.1 后端时间依据核查

| 检查项 | 结论 |
| --- | --- |
| 是否提供截止时间 | 是，`expire_at`（`api/order/v1/order.go:49`） |
| 超时配置 | `order.pay_timeout` 默认 900 秒（`manifest/config/config.yaml`） |
| 超时取消机制 | 详情/支付懒取消 + 后台扫描器每 60s（`internal/logic/order/order.go:683-761`、`internal/worker/scanner.go:50-68`） |
| 超时在详情的表现 | `status=cancelled`、`cancel_reason=2`（实测确认） |

### 7.2 实测发现的阻塞问题：同一响应内时区不一致

真实后端（真实 MySQL 8.0 + 真实 Go 服务，业务时区 CST +0800）返回：

```
created_at = 2026-11-10 08:15:03     // UTC 墙钟
paid_at    = 2026-11-10 16:15:03     // +08 墙钟
expire_at  = 2026-11-10 16:30:03     // +08 墙钟
cancelled_at = 2026-11-10 16:16:12   // +08 墙钟
```

同一笔订单、同一分钟内产生的 `created_at` 与 `paid_at` 相差 8 小时。根因：

1. 应用启动时把 MySQL 会话时区固定为 `dashboard.timezone`（默认 Asia/Shanghai），
   DSN 参数为 `time_zone='+08:00'`（`internal/boot/boot.go:176-201`）；
2. `expire_at` / `paid_at` 使用 `NOW()` 由 MySQL 会话计算（+08 墙钟）；
3. `created_at` / `updated_at` 由 GoFrame 在 Go 侧自动填充，驱动按 `loc`（UTC）写入（UTC 墙钟）。

### 7.3 结论

**不实现倒计时**，改为展示后端返回的静态「支付截止时间」，页面同时说明
「超时后由服务端自动取消订单，页面不提供前端倒计时与取消操作」。

理由：客户端无法在同一响应内建立自洽时间基准；任何一侧的硬编码偏移都会在后端修复后
变成二次偏移错误。该问题已列入「需要后端决策」：建议后端统一输出带偏移的 RFC3339。

**未硬编码任何超时时长**（没有 15/30 分钟常量），也**不允许前端触发取消**。

---

## 8. 对应 Go 源码位置

| 关注点 | 源码 |
| --- | --- |
| 路由注册（订单在前台 Auth 分组） | `internal/cmd/routes_frontend.go:91-98` |
| 路由表锁定测试 | `internal/cmd/routes_test.go:64-70` |
| API 契约（Order / OrderItem / ListRes / DetailRes） | `api/order/v1/order.go:31-72, 91-110` |
| 列表实现（仅本人、id DESC、含明细、N+1、不懒取消） | `internal/logic/order/order.go:405-420` |
| 详情实现（9001 防枚举、过期懒取消） | `internal/logic/order/order.go:422-444` |
| 状态枚举与 DB 映射 | `internal/logic/order/order.go:36-44, 896-914` |
| 取消原因常量 | `api/order/v1/order.go:25-29` |
| 取消/超时（恢复库存 + 释放券） | `internal/logic/order/order.go:683-761` |
| 确认收货 30→40→50（`received` 不可观测） | `internal/logic/order/order.go:551-595` |
| 支付截止计算 `DATE_ADD(NOW(), ...)` | `internal/logic/order/order.go:322-323` |
| 超时配置默认值 900s | `internal/logic/order/order.go:32-33, 946-963` |
| 扫描器周期 | `internal/worker/scanner.go:50-68` |
| 会话时区注入 | `internal/boot/boot.go:176-201` |
| 表结构 | `internal/migrations/sql/20261001000007_orders.up.sql`、`20261001000022_order_coupon_amounts.up.sql` |
| 统一响应 / 错误码 | `internal/middleware/response.go:12-45`、`internal/codes/codes.go:83-89` |
| 认证中间件 | `internal/middleware/auth.go:26-63` |

---

## 9. 真实后端联调结果

### 9.1 环境（真实，非 Mock）

- MySQL 8.0（独立容器，3307，全新库，`migrate up` 应用到 `20261001000024`）
- Redis 7
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + 仓库自带 `test/container/qiniu_mock`
  （绕过启动期七牛 bucket 校验；**未修改任何 Go 代码**）
- 前端：`vite` dev server，`VITE_DEV_PROXY_TARGET=http://127.0.0.1:18000`，
  **未设置 `VITE_ENABLE_MOCK`**（因此没有 MSW 拦截）
- 浏览器：Playwright Chromium，真实交互

### 9.2 接口级验证（脚本直连真实后端）

| 验证项 | 结果 |
| --- | --- |
| `POST /orders` 创建订单（购物车，2 件） | 200，`total_amount=3998`，`expire_at` 已写入 |
| `GET /orders` 返回结构 | `data.items[]`，每条含 `items[]`，按 id 倒序 |
| `GET /orders?status=cancelled&page=1&size=1` | 200，**仍返回全部订单**（证明后端忽略这些参数） |
| `GET /orders/:id` 本人 | 200，金额三联与收货快照正确 |
| 他人访问 | **404 / 9001** |
| 他人列表 | 200，`items=[]` |
| 无 token 列表/详情 | **401 / 1002** |
| 不存在订单 | **404 / 9001** |
| 过期订单查询详情 | 触发懒取消：`status=cancelled`、`cancel_reason=2`、`cancelled_at` 有值 |
| 金额自洽 | `Σ price×qty = original_amount`；`original - discount = total` |

### 9.3 UI 级验证（真实浏览器 + 真实后端）

17 项检查全部 PASS：

- 真实后端登录 → Header「我的订单」→ 列表加载 2 笔；
- 分组计数：待支付 0 / 待发货 1 / 待收货 0 / 已完成 0 / 已关闭 1；
- 卡片 1 = 已取消（¥19.99）、卡片 2 = 已支付（¥39.98），id 倒序正确；
- 筛选「已关闭」= 1 笔（cancelled）、「已完成」= 空态、「待发货」= 1 笔（paid）；
- 已支付详情：金额 ¥39.98、状态「已支付」、收货快照「张三 / 13800138000 / 广东省深圳市南山区科技园1号」、
  进度说明存在；
- 已取消详情：终止提示 + 「超时未支付自动取消」；
- 返回订单列表正常；
- 未登录访问 `/account/orders` → 跳 `/login`；
- 浏览器控制台**零错误**；
- 另一账号（`w4buyer2`）列表为空 → 用户隔离在真实数据下成立。

> 联调环境属于临时验证设施（临时容器、`/tmp` 下的二进制与脚本），
> 未向仓库写入任何测试后端代码或脚本。

---

## 10. Mock 测试结果（与真实联调严格区分）

### 10.1 MSW 组件测试（Vitest + jsdom）

- 4 个文件、44 个用例覆盖订单中心（见第 12 节明细）；
- Mock 行为与真实后端对齐：仅本人、id 倒序、无分页无筛选、`expire_at = created + 900s`；
- 新增测试钩子 `seedMockOrder`（仅 Mock 存在，真实后端没有该入口），
  用于构造各状态订单，避免测试必须走完整履约链路。

### 10.2 Playwright e2e（`VITE_ENABLE_MOCK=true`）

- `tests/e2e/orders.spec.ts` 2 个用例：路由保护、下单→成功页→详情→列表→筛选→详情；
- **注意：e2e 使用 MSW，不是真实后端**；真实后端验证见第 9 节。

---

## 11. 已知限制

1. 列表无服务端筛选/分页，数据量增长后首屏成本线性上升；
2. `GET /orders` 后端为 N+1 查询（逐单查 `order_items`）；
3. 列表不触发超时懒取消，可能展示已过 `expire_at` 的 `pending_payment`；
4. **时间字段时区不一致**（`created_at`/`updated_at` 为 UTC 墙钟，
   `expire_at`/`paid_at`/`cancelled_at` 为 +08 墙钟），前端原值展示，未做偏移；
5. 因第 4 条，**未实现倒计时**，只展示静态支付截止时间；
6. 无支付/取消/确认收货/退款操作（W4-02、W4-03）；
7. Token 仅存内存，刷新需重新登录（既有设计），真实联调需 SPA 内导航；
8. 秒杀订单不在本列表（独立表）；
9. 订单详情页未展示退款进度（需 W4-03 接 `GET /refunds?order_id=`）。

---

## 12. 测试结果

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error / 0 warning） |
| `npm run typecheck` | 通过 |
| `npm run test` | **207 passed / 34 files** |
| `npm run build` | 成功；`orders-page` 5.71 kB、`order-detail-page` 10.03 kB（gzip 2.42 / 3.17 kB） |
| `npm run test:e2e` | **38 passed**（含新增 2 个订单中心用例） |

新增/修改的测试文件：

| 文件 | 类型 | 用例数 |
| --- | --- | --- |
| `web/src/utils/orders.test.ts` | 新增 | 18 |
| `web/src/pages/account/orders-page.test.tsx` | 新增 | 6 |
| `web/src/pages/account/order-detail-page.test.tsx` | 新增 | 11 |
| `web/src/api/orders.test.ts` | 追加 3 个列表用例 | 9（合计） |
| `web/tests/e2e/orders.spec.ts` | 新增 | 2 |
| `web/src/pages/checkout-page.test.tsx` | 更新成功页文案断言 + 新增「查看订单」断言 | — |

覆盖任务要求的测试点：

| 要求 | 覆盖位置 |
| --- | --- |
| 订单列表加载 | `orders-page.test.tsx`「展示订单号…」+ 真实联调 |
| 列表为空 | `orders-page.test.tsx`「无订单时展示空状态」 |
| 不同状态筛选 | `orders-page.test.tsx`「状态筛选为本地过滤」+ e2e |
| 详情正常展示 | `order-detail-page.test.tsx`「展示订单号、状态…」 |
| 订单不存在 | 「订单不存在或无权查看时展示 404」 |
| 无权访问 | 后端同样返回 9001（与不存在不可区分），前端统一 404 文案；真实联调已验 |
| 状态映射 | `utils/orders.test.ts`（7 态 + 色调 + 进度） |
| 金额展示 | 详情「金额三项」+ 卡片「¥19.98」 |
| 地址快照 | 「收货信息使用订单快照，不受地址簿影响」 |
| 时间字段缺失 | 「expire_at 缺失时不展示支付截止时间」+ 时间区块只渲染非 null |
| 倒计时 | **未实现**，详见第 7 节 |
| 未登录路由保护 | 列表与详情各一条 |
| 成功页跳转详情 | `checkout-page.test.tsx` + `orders.spec.ts` |

---

## 13. 文档路径

| 文档 | 路径 |
| --- | --- |
| 开发知识文档 | `.agent/tasks/dev/W4-01-order-center.md` |
| 交付报告（本文） | `docs/reports/w4-01-order-center/DELIVERY_REPORT.md` |

---

## 14. Git 提交

本次为**单一提交**：W4-01 代码 + 开发知识文档 + 本交付报告。
报告中无法写入自身提交 SHA，提交后由交付汇报给出，可用
`git log -1 --format=%H` 复核。

前置提交：`6935f36 docs(web): add W4 backend capability audit`（W4-00 审查报告，单独提交）。

---

## 15. 工作区状态

- 分支：`feat/phase2-user-frontend`
- 仅修改 `web/`（前端）与 `docs/`、`.agent/`（文档）
- **未修改**：Go 后端、SQL、管理端、Docker、Nginx、Kubernetes、其他分支
- 未执行：push、force push、合并、切分支
- 交付后工作区应为 clean
