# W4-01 用户订单中心：开发知识文档

- 任务：SurgeCart 用户商城 W4-01（订单列表 + 订单详情）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 上游依赖：W3-02 订单创建（`POST /orders`、`GET /orders/:id` 已在用）
- 后端能力依据：`docs/reports/w4-backend-capability-audit/REPORT.md`（W4-00 审查）

---

## 1. SurgeCart 订单模型

### 1.1 数据表

| 表 | 作用 | 迁移 |
| --- | --- | --- |
| `orders` | 订单主数据 + 金额快照 + 收货地址快照 | `20261001000007_orders.up.sql`、`20261001000022_order_coupon_amounts.up.sql` |
| `order_items` | 订单项（SKU/商品名/主图/成交价/数量快照），FK → orders `ON DELETE CASCADE` | 同上 |
| `refund_requests` | 退款申请（W4-03 使用，`amount` 取 `orders.total_amount`） | `20261001000020_refund.up.sql` |
| `user_coupons` | 用户券（`locked` 时会回写 `order_id`） | `20261001000021_coupon.up.sql` |
| `flash_sale_orders` | **秒杀订单，独立表**，不写入 `orders` | `20261001000011_flash_sale.up.sql` |

### 1.2 关键字段语义

```
orders
  order_no         VARCHAR(32)  全局唯一（uk_order_no）
  status           TINYINT      10/20/30/40/50/60/70
  total_amount     INT UNSIGNED 应付 = original - discount（后端计算）
  original_amount  INT UNSIGNED Σ order_items.price × quantity
  discount_amount  INT UNSIGNED 优惠金额（无券为 0）
  user_coupon_id   BIGINT NULL  下单时使用的用户券（软引用，快照）
  idempotency_key  VARCHAR(64)  客户端幂等键（uk_user_idempotency）
  request_hash     VARCHAR(64)  sha256(source|address|coupon|sku:qty...)
  recipient_name / phone / province / city / district / detail   收货快照
  expire_at        DATETIME     支付截止 = NOW() + order.pay_timeout（默认 900s）
  cancel_reason    1=用户、2=超时
  paid_at / shipped_at / received_at / completed_at / cancelled_at / refunded_at
```

**软引用 + 快照自足**：`user_id`/`address_id`/`sku_id`/`product_id` 都没有外键，
订单在商品、SKU、地址被修改或删除后仍然可读——这是订单详情能稳定展示的前提。

---

## 2. 订单状态机

```
                    ┌──────── 用户取消（cancel_reason=1）────────┐
pending_payment(10) ├──────── 超时（懒取消/扫描器，reason=2）────► cancelled(60)
        │  POST /orders/:id/pay（Mock）                          ▲
        ▼                                                       │
     paid(20) ── 管理员 ship ──► shipped(30) ── receive ──► completed(50)
        │                          （同一事务 30→40→50）
        └── 退款执行成功 ──► refunded(70)
```

前端需要知道的三个事实：

1. **`received(40)` 对外不可观测**：`Receive` 在同一事务里先 30→40、再 40→50，
   返回给客户端的永远是 `completed`。前端的进度映射把 `received` 归入「已完成」。
2. **`cancelled` / `refunded` 是终态**，不会再变。
3. **`GET /orders` 不会触发懒取消**，只有 `GET /orders/:id` 会。
   因此列表里可能出现「已经过了 `expire_at` 但仍是 `pending_payment`」的订单，
   这是后端已知行为，前端不得自行判定为已取消。

---

## 3. 数据流

### 3.1 列表

```
OrdersPage
  └─ useOrderList()                       hooks/use-orders.ts
       └─ fetchOrders(signal)             api/orders.ts  → authGetData('/orders')
            └─ GET /orders                → { code, message, data: { items: Order[] } }
                 └─ data.items            （后端已按 id DESC，含每条订单的 items[]）
                      ├─ groupCounts      本地统计（筛选标签上的数字）
                      ├─ filterOrdersByGroup(orders, activeGroup)
                      ├─ slice((page-1)*10, page*10)   本地分页
                      └─ <OrderCard order={order} />
```

### 3.2 详情

```
OrderDetailPage
  ├─ useParams().id → Number(id) → isValidOrderId
  ├─ useOrderDetail(id) → fetchOrder(id) → GET /orders/:id
  └─ <OrderDetailContent order={order} />
       ├─ <OrderStatusBadge status />     后端状态原文
       ├─ <OrderProgress status />        由当前状态推导（非事件历史）
       ├─ 商品明细（items[]：名称/SKU/成交单价/数量）
       ├─ 金额（original_amount / discount_amount / total_amount）
       ├─ 收货信息（订单快照，不读地址簿）
       └─ 时间信息（只渲染非 null 字段）
```

### 3.3 缓存关系

- 下单成功：`useCreateOrder.onSuccess` 写入 `orders.detail(id)`，并**失效 `orders.list()`**。
  不失效会导致「刚下的单不在列表里」，直到下一次重新进入页面。
- 订单中心是只读页面，本任务不引入任何乐观更新。

---

## 4. 前端状态筛选的实现

后端只有 7 个状态，没有「状态分类」字段，也没有筛选参数。前端分组定义在
`web/src/utils/orders.ts` 的 `ORDER_STATUS_GROUPS`：

| 分组 | 覆盖的后端状态 | 说明 |
| --- | --- | --- |
| 全部 | — | 不过滤 |
| 待支付 | `pending_payment` | |
| 待发货 | `paid` | 已付款、等发货 |
| 待收货 | `shipped` | 已发货、等收货 |
| 已完成 | `received`、`completed` | `received` 实际不可观测，保留兼容 |
| 已关闭 | `cancelled`、`refunded` | 两个终态 |

实现要点：

- `filterOrdersByGroup(orders, groupId)` 是纯函数，返回新数组，便于单测；
- 切换分组时**重置页码到第 1 页**，否则会停留在越界页码；
- 渲染前把页码收敛到 `totalPages`（`Math.min(page, totalPages)`），
  避免过滤后条目减少导致空白页；
- 分组标签上的数字是**本地统计**，不是服务端 count。

### 4.1 状态徽标 vs 分组标签

列表卡片的状态徽标展示**后端状态原文**（`paid` → 「已支付」），
而筛选标签展示**业务分组名**（「待发货」）。二者刻意不同：

- 徽标必须忠实于后端，否则等于伪造状态；
- 分组是电商惯用的用户视角命名。

这一点在任务文档与交付报告中都显式说明，避免被误解为「状态不一致」。

---

## 5. TanStack Query 缓存设计

```ts
orders: {
  all: ['orders'],
  list: () => ['orders', 'list'],              // 无参数：后端没有筛选/分页参数
  details: () => ['orders', 'detail'],
  detail: (id: number) => ['orders', 'detail', String(id)],
}
```

| 设计点 | 取值 | 原因 |
| --- | --- | --- |
| 列表 key 不带参数 | `['orders','list']` | 筛选/分页是纯本地状态，带参数会产生大量等价缓存 |
| 详情 key 归一化 | `String(id)` | 路由参数是字符串、创建响应里是数字，避免同一订单两份缓存 |
| `staleTime` | 15s | 后端扫描器每 60s 取消过期订单，状态会被异步改变 |
| `enabled` | `hasSession` | 未登录不发请求，避免无意义的 401 |
| `retry` | 仅网络/超时重试 1 次 | 4xx/5xx 是确定的业务结果，重试只会延后错误态 |

---

## 6. 订单金额字段的含义

| 字段 | 含义 | 前端处理 |
| --- | --- | --- |
| `original_amount` | 优惠前金额 = Σ(`price` × `quantity`)，下单时重读 `skus.price` | 直接展示 |
| `discount_amount` | 优惠金额，服务端按券计算，无券为 0 | 直接展示 |
| `total_amount` | 应付金额 = `original_amount - discount_amount` | 直接展示 |
| `items[].price` | 成交单价快照 | 直接展示，**不乘数量** |

**前端没有任何金额运算**：订单卡片、详情页都没有「小计 = 单价 × 数量」，
也没有合计求和。所有金额都由 `PriceDisplay` 把整数分渲染成 `¥0.00`。
唯一的算术是 `orderTotalQuantity`（数量求和）与分组计数，与金额无关。

原因：后端 `total_amount` 是事务内计算的快照，一旦前端再算一遍，
任何规则变化（例如未来引入运费、阶梯优惠）都会出现两份口径。

---

## 7. 订单快照机制

订单在创建时把「商品名、SKU 名、主图、成交价、收货人、电话、省市区、详细地址」
全部写进 `orders` / `order_items`。因此：

- 商品改名、SKU 改价、地址被编辑或删除，**都不影响历史订单**；
- 详情页的收货信息必须读 `order.recipient_name` 等字段，
  **不能**去 `GET /addresses` 反查——那会把「当前地址」错当成「下单时的地址」；
- 单测 `order-detail-page.test.tsx` 的「收货信息使用订单快照，不受地址簿影响」
  就是锁定这条不变量：地址簿为空，订单仍展示快照里的收货人。

---

## 8. 订单状态进度的推导边界

后端**没有状态历史/事件流接口**，因此页面展示的进度是
`buildOrderProgress(status)` 由当前状态推导出来的：

| 状态 | 步骤状态 |
| --- | --- |
| `pending_payment` | current / todo / todo / todo |
| `paid` | done / current / todo / todo |
| `shipped` | done / done / current / todo |
| `received`、`completed` | done / done / done / done |
| `refunded` | done / current / todo / todo，`halted=true` |
| `cancelled` | todo × 4，`halted=true` |

页面固定展示一句：

> 进度由当前订单状态推导，不是服务端返回的历史事件记录。

同时：

- 终止态额外显示「订单已取消，交易终止」/「订单已退款，交易终止」；
- 时间信息只渲染**后端返回非 null** 的字段，不做「预计时间」之类的补全。

---

## 9. 倒计时：本期未实现，以及为什么

### 9.1 先核对「是否有可信时间依据」

| 检查项 | 结论 | 依据 |
| --- | --- | --- |
| 后端是否提供截止时间 | 是，`expire_at` | `api/order/v1/order.go:49` |
| 是否有明确的超时配置 | 是，`order.pay_timeout` 默认 900s | `manifest/config/config.yaml`、`internal/logic/order/order.go:32-33,946-963` |
| 是否有超时取消机制 | 是：详情/支付懒取消 + 后台扫描器每 60s | `order.go:683-761`、`internal/worker/scanner.go:50-68` |
| 超时如何反映到详情 | `GET /orders/:id` 触发懒取消，返回 `status=cancelled`、`cancel_reason=2` | `order.go:422-444` |

按上表看，`expire_at` 是存在的。但真机联调发现**同一响应内的时间字段时区不一致**：

```
orders 表实际存储（真实 MySQL 8.0 + 真实服务）
  id=1  created_at=2026-11-10 08:15:03   paid_at=2026-11-10 16:15:03
  id=2  created_at=2026-11-10 08:15:03   expire_at=2026-11-10 16:30:03
        cancelled_at=2026-11-10 16:16:12
宿主/业务时区：CST +0800
```

同一笔订单，`created_at` 比 `paid_at` 早 8 小时——不是业务事实，而是写入路径不同：

1. 应用启动时把 MySQL **会话时区固定为 `dashboard.timezone`（默认 Asia/Shanghai，+08:00）**：
   `internal/boot/boot.go:176-201` 生成 DSN 参数 `time_zone='+08:00'`；
2. `expire_at` 用 `DATE_ADD(NOW(), INTERVAL 900 SECOND)` 由 **MySQL 会话**计算
   （`internal/logic/order/order.go:322-323`），得到 +08 墙钟；
3. `created_at` / `updated_at` 由 GoFrame 在 Go 侧自动填充，驱动按 `loc`（UTC）格式化，
   得到 UTC 墙钟；
4. 结果：**同一响应里 `created_at` 是 UTC 墙钟，`expire_at` / `paid_at` 是 +08 墙钟**。

### 9.2 决策

**不实现倒计时**，只展示后端返回的静态「支付截止时间」，原因：

- 客户端无法从同一响应里建立自洽的时间基准：要么假设 `expire_at` 是 +08
  （对 `created_at` 又解释不通），要么假设是 UTC（则倒计时整体偏 8 小时）；
- 任何一侧的硬编码偏移，都会在后端修正时立刻变成「二次偏移」错误，
  等于把数据缺陷固化成前端逻辑；
- W4-00 审查报告已把「时间统一为带偏移的 RFC3339」列为需要后端决策的问题。

同时页面明确写着「超时后由服务端自动取消订单，页面不提供前端倒计时与取消操作」，
并且**不提供任何支付/取消/确认收货按钮**（属于 W4-02）。

> 若后端后续把时间字段统一为 `2026-11-10T16:30:03+08:00` 形式，
> 只需在 `utils/orders.ts` 增加解析 + 一个 `useOrderCountdown` hook 即可启用倒计时，
> 页面结构无需改动。

---

## 10. 关键技术决策

| # | 决策 | 理由 |
| --- | --- | --- |
| D1 | 列表请求**不带任何参数** | 后端会静默忽略 `status/page/size`；带上反而会造成"服务端已筛选"的错觉 |
| D2 | 状态筛选 + 分页全部本地实现，并在 UI 上写明 | 与后端真实能力一致，避免虚假承诺 |
| D3 | 本地分页每页 10 条 | 订单数量可能较大，全量渲染会拖慢首屏；本地分页不影响请求次数 |
| D4 | 状态徽标用后端原文，分组标签用业务名 | 徽标必须忠实；分组是用户视角 |
| D5 | 金额零运算 | 避免与后端事务口径分叉 |
| D6 | 详情页 404 与网络错误分开处理 | 404 是「不存在/无权」，重试无意义；网络错误才给「重新加载」 |
| D7 | `data-status` 属性 | 「待支付」同时是状态徽标与第一个进度节点，测试需要精确选择 |
| D8 | 只对网络/超时重试 | 4xx/5xx 重试会延后错误态，也会拖慢测试 |
| D9 | 成功页新增「查看订单」入口 | 下单后最短路径进入订单详情 |
| D10 | 复用既有 `Order` DTO / `apiClient` / `envelope` | 不引入第二套请求封装或重复类型 |

---

## 11. 实际问题与排查过程

### 11.1 真实后端无法启动：七牛 bucket 校验失败

`serve` 启动时会 `GetBucketInfo` 校验七牛 bucket（`internal/logic/upload/upload.go:242-260`），
本地没有真实凭据 → `bad token` 直接退出。

排查：仓库已有 `test/container/qiniu_mock`（UC + bucketInfo Mock）和
`test/serveproc`（`-tags serve_lifecycle` 的测试入口，仅改写 UC endpoint）。
按 `scripts/test-container-runtime.sh` 的思路本地复现：

```bash
openssl req -x509 -newkey rsa:2048 -nodes -keyout server.key -out server.crt \
  -days 2 -subj "/CN=127.0.0.1" -addext "subjectAltName=IP:127.0.0.1"
go build -o /tmp/qiniu-mock ./test/container/qiniu_mock
go build -tags serve_lifecycle -o /tmp/my-shop-w4 ./test/serveproc

QINIU_MOCK_ADDR=127.0.0.1:9443 QINIU_MOCK_CERT=... QINIU_MOCK_KEY=... /tmp/qiniu-mock
SSL_CERT_FILE=<server.crt> SERVE_LIFECYCLE_QINIU_UC_HOST=https://127.0.0.1:9443 \
  DATABASE_DEFAULT_PORT=3307 DATABASE_DEFAULT_PASS=... /tmp/my-shop-w4 serve
```

**未修改任何 Go 代码**：用的是仓库自带的测试能力，生产二进制不读取该变量。

### 11.2 发现 created_at 与 expire_at 相差 8 小时

见第 9 节。定位过程：

1. 联调时发现列表「下单时间」是 08:15，而当前本地时间 16:15；
2. 直接查 MySQL：`created_at=08:15:03`、`expire_at=16:30:03`（存储值本身就不同）；
3. 确认 `@@session.time_zone` 在应用连接里被设为 `+08:00`（`boot.go` DSN 参数）；
4. 确认 `expire_at` 走 `NOW()`（+08），`created_at` 走 Go 侧自动填充（UTC）。

结论：**是后端字段级时区不一致，不是前端解析问题**。
前端选择原样展示 + 文档记录，不用 +8 偏移"修正"。

### 11.3 错误态测试超时

首版 `retry` 写成「失败就重试 1 次」，导致 500 用例里错误态要等一次退避（约 1s）
才出现，`findByText` 默认 1s 超时直接失败。
改为「仅网络/超时重试」后错误态立即出现，也更符合业务语义。

### 11.4 金额断言匹配不到

`PriceDisplay` 把 `¥` / 整数 / 小数渲染成三个 `span`，`getByText('¥19.98')` 必然失配。
改用容器级 `toHaveTextContent('¥19.98')`（基于 `textContent`，可跨子节点）。

### 11.5 「待支付」出现两次

状态徽标与进度第一步文案相同，`getByText` 报 "Found multiple elements"。
给徽标加 `data-status`（保留后端枚举），测试用
`document.querySelector('[data-status="pending_payment"]')` 精确定位。

### 11.6 lint 警告：useMemo 依赖每次渲染都变

`const orders = ordersQuery.data?.items ?? []` 每次渲染都是新数组，
导致 `useMemo` 依赖失效。改为 `useMemo(() => data?.items ?? [], [data])`。

---

## 12. 测试验证

### 12.1 单元 / 组件测试（Vitest + MSW，Mock 环境）

| 文件 | 覆盖 |
| --- | --- |
| `src/utils/orders.test.ts` | 7 状态的唯一分组、分组顺序、跨终态分组、进度推导、色调、取消原因、地址拼接、件数、时间空值 |
| `src/api/orders.test.ts` | 列表 401、仅本人、id 倒序、含明细、空数组；创建/详情既有用例保持通过 |
| `src/pages/account/orders-page.test.tsx` | 空态、卡片字段、详情链接、三种筛选、筛选空态回全部、加载失败重试、未登录跳转 |
| `src/pages/account/order-detail-page.test.tsx` | 正常渲染、金额三项、收货快照、支付截止时间、缺失不渲染、终止态、404、500、非法 id、未登录、进度说明 |

### 12.2 端到端（Playwright + MSW）

`tests/e2e/orders.spec.ts`：

1. 未登录访问 `/account/orders` → 跳登录 → 登录后回列表 → 空态；
2. 建地址 → 加购 → 结算 → 下单 → 成功页「查看订单」→ 详情 → 返回列表 →
   筛选 → 再进详情。

### 12.3 真实后端联调（非 Mock）

在真实 MySQL 8.0 + Redis + 真实 Go 服务的环境下，用 Playwright 驱动**不带 MSW** 的
前端（Vite dev + `/api` 代理到真实后端），实测 17 项全部通过：

- 登录、列表加载（2 笔）、卡片数量；
- 待支付 0 / 待发货 1 / 待收货 0 / 已完成 0 / 已关闭 1 的分组计数正确；
- 「已关闭」筛出 cancelled、「已完成」为空、「待发货」筛出 paid；
- 已支付订单详情金额 `¥39.98`、状态「已支付」、收货快照、进度说明；
- 已取消订单详情展示终止态与「超时未支付自动取消」；
- 返回列表、未登录跳登录、浏览器控制台无错误；
- 另一账号（w4buyer2）列表为空 → 用户隔离成立。

---

## 13. 已知限制

1. **无服务端筛选/分页**：`GET /orders` 返回全部订单，数据量大时首屏成本随时间线性增长。
2. **列表接口是 N+1**：后端逐单查询 `order_items`（`order.go:405-420`）。
3. **列表不触发懒取消**：可能展示已过 `expire_at` 的 `pending_payment`。
4. **时间字段时区不一致**（第 9 节）：`created_at`/`updated_at` 为 UTC 墙钟，
   `expire_at`/`paid_at`/`cancelled_at` 为 +08 墙钟，页面原样展示。
5. **无倒计时**：受第 4 条限制，本期不做。
6. **无订单操作**：支付、取消、确认收货、退款均属 W4-02/W4-03，本页只有查询。
7. **Token 仅存内存**：刷新页面需重新登录，因此真实后端联调不能直接用 URL 深链进入详情，
   必须从登录后的 SPA 内导航进入。
8. **秒杀订单不在本列表**：秒杀订单写 `flash_sale_orders`，不进入 `orders`。

---

## 14. 后续维护注意事项

- **若后端补 `status` / `page` / `size`**：
  `api/orders.ts` 的 `fetchOrders` 需要加参数，`queryKeys.orders.list` 需要带参数，
  列表页的本地过滤/分页要同步下线，否则会出现"双重筛选"。
- **若后端统一时间格式（RFC3339 带偏移）**：
  删除 `utils/orders.ts` 里关于时区的说明，补 `parseBackendDateTime` 与倒计时 hook；
  页面结构无需改动。
- **若新增订单状态**：
  必须同时更新 `ORDER_STATUS_LABELS`、`ORDER_STATUS_GROUPS`、`buildOrderProgress`，
  `orders.test.ts` 的「7 个状态唯一分组」用例会立刻报错，起到守门作用。
- **若把 `received` 改为可观测**：需要把它从「已完成」分组拆出，并补一个进度节点。
- **不要在详情页用地址簿覆盖收货信息**，这会被快照用例直接捕获。

---

## 15. 技术学习与面试要点

1. **状态机与展示的边界**：后端 7 态是事实，前端「待发货/待收货」是分组视图，
   两者不能混为一谈；分组必须在文档里写清楚映射，否则下一个维护者会以为后端状态改了。
2. **快照（snapshot）设计**：订单为什么要把价格、名称、地址都复制一份？
   因为交易单据必须自足，不能随主数据变化而漂移。
3. **时区陷阱**：`DATETIME` 不带时区，存储的是"墙钟"。
   同一张表里混用 `NOW()`（会话时区）与 Go 侧时间（驱动 loc）会直接产出 8 小时偏差。
   生产系统应统一存 UTC，或在 API 层输出带偏移的 RFC3339。
4. **本地筛选 vs 服务端筛选**：本地筛选不需要接口改动、响应快，但无法分页、数据量受限；
   服务端筛选可扩展但需要索引与契约。选择哪种取决于后端能力，且必须在 UI 上说明。
5. **缓存失效时机**：创建订单后如果不失效列表 key，用户会看到"刚下的单不见了"。
   Query Key 的分层（`all` / `list` / `detail`）就是为了精确失效。
6. **测试分层**：单测锁纯函数、组件测试锁交互与错误态、MSW e2e 锁用户路径、
   真实联调锁契约（字段名、时区、状态码）。四层都不能互相替代。
7. **不要用前端"修正"后端数据**：发现 8 小时偏差时，正确做法是暴露问题并记录，
   而不是加偏移把界面"修好看"——后者会在后端修复时变成新的 bug。
