# W4-03：SurgeCart 用户退款中心（开发知识文档）

- 分支：`feat/phase2-user-frontend`
- 范围：用户端退款中心（退款申请 / 我的退款记录 / 退款详情 / 撤销申请 / 状态展示 / 与订单中心联动）
- 不在范围：Go 后端、SQL、后台退款审核、真实第三方资金退款、优惠券中心（W4-04）
- 事实来源：`api/refund/v1/refund.go`、`internal/logic/refund/refund.go`、`internal/controller/refund/refund.go`、`internal/cmd/routes_frontend.go`、`internal/codes/codes.go`、`internal/migrations/sql/20261001000020_refund.up.sql`、`docs/design/refund.md`
- 本文只记录**当前实现与实测证据**，不复制设计文档原文；设计文档解释"为什么这样设计"，本文解释"前端如何对接、踩过什么坑"。

---

## 1. SurgeCart 退款业务模型

退款是「已支付未发货普通订单」的**整单全额仅退款**闭环，不是售后工单系统。

| 维度 | 事实 |
| --- | --- |
| 订单类型 | 仅普通订单；秒杀订单（`flash_sale_orders`）无支付与库存恢复语义，不进入退款 |
| 退款粒度 | **整单**，没有商品级/部分退款，没有多次退款 |
| 退款金额 | 恒等于 `orders.total_amount`（下单时快照），服务端派生 |
| 触发条件 | 订单属于本人 且 `orders.status = paid(20)` |
| 审核 | 需要管理员人工审核（同意 → 待退款；拒绝 → 审核拒绝） |
| 执行 | 管理员人工确认结果（成功 / 失败），**没有支付网关** |
| 资金 | **无真实资金退回**：没有支付流水、没有回调、没有渠道、没有到账时间 |
| 审计 | `refund_audits` append-only；**只有后台接口**（需 `refund:view`），用户端不可见 |
| 单一有效申请 | 同一订单至多一条有效申请（10/20/30），DB 生成列 + 唯一索引兜底 |

对应数据表：

- `refund_requests`：退款主数据（状态、金额、原因、审核/执行字段）；
- `refund_audits`：只追加的审计流水（apply / review_approve / review_reject / execute_success / execute_failure / close / revoke）。

## 2. 退款与订单的关系

```
orders 1 ─── n refund_requests (软引用 order_id，无 FK)
                     └── refund_audits (软引用 refund_id)
```

- 退款是订单的**附属业务单据**，订单结构里**没有** `refund` / `refund_status` 字段；
  因此前端无法从 `GET /orders` 或 `GET /orders/:id` 直接拿到退款信息。
- 订单状态只在**退款执行成功**时变化：`paid(20) → refunded(70)` + 写 `refunded_at`。
- 申请、审核、撤销、退款失败、关闭都**不改变订单状态**，订单保持 `paid`。
- 退款列表只能通过 `GET /refunds?order_id=` 反查（后端真实支持的过滤）。
- 退款与发货互斥：存在有效退款申请时发货被拒绝；退款成功与发货由订单行锁串行化，保证不会出现「已发货 + 已退款」。

由此产生一个前端约束：**订单详情页如果要展示退款，必须额外请求退款接口**；订单列表页如果要展示退款徽标，必须额外请求退款列表。

## 3. 退款状态机（DB TINYINT ↔ API 字符串）

| API 字符串 | DB | 含义 | 用户可撤销 | 终态 | 订单状态 |
| --- | --- | --- | --- | --- | --- |
| `applying` | 10 | 申请中（待审核） | ✅ | ❌ | paid |
| `pending_refund` | 20 | 待退款（审核通过） | ❌ | ❌ | paid |
| `refund_failed` | 30 | 退款失败 | ❌ | ❌ | paid |
| `review_rejected` | 40 | 审核拒绝 | ❌ | ✅ | paid |
| `revoked` | 50 | 已撤销 | ❌ | ✅ | paid |
| `refund_succeeded` | 60 | 退款成功 | ❌ | ✅ | **refunded** |
| `closed` | 70 | 已关闭 | ❌ | ✅ | paid |

合法迁移（其余一律 `18002`）：

```
10 → 20（审核同意）   10 → 40（审核拒绝）   10 → 50（用户撤销）
20 → 60（执行成功）   20 → 30（执行失败）
30 → 60（重试成功）   30 → 70（人工关闭）
```

有效状态（阻塞发货、阻止重复申请）= `{10, 20, 30}`。

前端映射位置：`web/src/utils/refunds.ts` 的 `REFUND_STATUS_LABELS` / `describeRefundStatusTone` / `ACTIVE_REFUND_STATUSES` / `canRevokeRefund` / `canApplyRefund`。**没有新增任何后端不存在的状态。**

## 4. 退款申请调用链

前端链路：

```
OrderDetailPage
  └─ OrderRefundSection（GET /refunds?order_id=）
       └─ ApplyRefundDialog（react-hook-form + zod）
            └─ useApplyRefund(orderId)
                 └─ applyRefund(orderId, reason)
                      └─ POST /orders/:id/refund  body: { reason }
```

后端链路（`internal/logic/refund/refund.go:125-196`）：

1. `reason` TrimSpace 后为空或 >255 字符 → `1001`（400）；
2. 事务开始，`SELECT ... FOR UPDATE` 锁定 `orders` 行（与发货互斥）；
3. 订单不存在或非本人 → `18001`（404，防枚举）；
4. `orders.status ≠ paid` → `18003`（409）；
5. `INSERT refund_requests`，`amount = orders.total_amount`；
6. `uk_active` 唯一冲突 → `18004`（409，同一订单已有有效申请）；
7. 写 `refund_audits(action=apply)`；
8. 提交事务后回读并返回完整 `Refund`。

请求/响应示例（真实后端实测）：

```json
// POST /orders/2/refund
{ "reason": "真实后端 UI 联调退款" }

// 200
{ "code": 0, "message": "OK", "data": {
  "id": 8, "order_id": 2, "user_id": 1, "status": "applying",
  "amount": 209900, "reason": "真实后端 UI 联调退款",
  "review_reason": "", "reviewed_by": null, "reviewed_at": null,
  "execute_reason": "", "executed_by": null, "executed_at": null,
  "created_at": "2026-11-10 18:31:05", "updated_at": "2026-11-10 18:31:05"
}}
```

**请求体没有金额字段**：前端不提交、也不允许用户修改退款金额。

## 5. 退款撤销机制

```
RefundDetailPage（status === 'applying'）
  └─ RevokeRefundDialog
       └─ useRevokeRefund()
            └─ POST /refunds/:id/revoke  body: {}
```

后端语义（`internal/logic/refund/refund.go:199-255`）：

- 仅 `applying(10) → revoked(50)`；
- 非本人/不存在 → `18001`（404）；
- 其他状态 → `18002`（409）；
- **不恢复库存、不改订单状态**，撤销后可以再次申请；
- 写 `refund_audits(action=revoke)`。

前端不做"本地改状态伪装撤销"：成功后用服务端返回的 `Refund` 覆盖详情缓存并失效列表。
撤销确认框明确写出「订单状态保持已支付、不涉及库存或优惠券变动、撤销后可重新申请」。

## 6. 退款金额计算依据

- 唯一权威来源：`orders.total_amount`（下单时由服务端计算并写入的订单快照 = `original_amount - discount_amount`）。
- `refund_requests.amount` 在申请事务内由服务端复制该快照，**不接受客户端金额**。
- 使用优惠券的订单同样按实付金额退款（联调实测：`209900 - 50000 = 159900`，退款 `amount = 159900`）。
- 前端只通过 `<PriceDisplay value={refund.amount} />` 展示整数分，**不做任何金额运算**，也没有金额输入框。
- 申请弹层展示的「订单实付金额」来自 `order.total_amount`，仅作为确认信息；最终金额以响应中的 `refund.amount` 为准。

## 7. 退款与库存、优惠券的关系

| 动作 | 库存 | 优惠券 |
| --- | --- | --- |
| 申请退款 | 不变 | 不变 |
| 审核同意 / 拒绝 | 不变 | 不变 |
| 用户撤销 | 不变 | 不变 |
| 退款失败 | 不变 | 不变 |
| 关闭退款 | 不变 | 不变 |
| **退款成功** | **恢复一次**（同一事务，`WHERE orders.status=20` 闸门保证只补偿一次） | **不返还**，券保持 `used` |

联调实测（SKU 初始 100）：

```
订单1 下单 → 99；退款成功 → 100
订单2 下单 → 99；撤销 → 99（不变）
订单3 下单 → 98；执行失败 → 98；关闭 → 98
订单4（用券）下单 → 97；退款成功 → 98
终态 inventories.quantity = 98
```

优惠券联调实测：用券下单后券 `used`（`order_id=4`）→ 退款成功 → 仍为 `used` 且 `order_id` 不变。

> 注意：订单**取消**会恢复库存并释放优惠券（W4-02 已验证），但退款**成功**只恢复库存、不返还券。两者语义不同，前端文案不能混用。

## 8. 前端退款状态展示

- 状态徽标：`RefundStatusBadge`，`data-refund-status` 保留后端原始枚举，文案来自 `REFUND_STATUS_LABELS`；
- 状态说明：`describeRefundStatusHint`，严格对应状态机，不描述资金动作；
- 状态筛选：`REFUND_STATUS_FILTERS` = 全部 + 后端全部 7 个状态（**纯前端本地筛选**）；
- 用户可见时间：仅 `created_at` / `reviewed_at` / `executed_at` 三个真实字段，未发生则不渲染；
- 不展示：`reviewed_by` / `executed_by`（管理员内部 id）、退款渠道、流水号、预计到账时间；
- 不构造状态时间轴：用户侧没有审计记录接口，页面明确说明「后端没有面向用户的退款历史/审计接口，因此不提供状态时间轴」；
- 资金真实性声明出现在三处：列表卡片页脚、详情页独立区块、申请确认框。

## 9. TanStack Query 缓存同步

Query Key（`web/src/api/query-keys.ts`）：

```ts
refunds: {
  all: ['refunds'],
  lists: () => ['refunds', 'list'],
  list: (orderId?) => ['refunds', 'list', orderId ? String(orderId) : 'all'],
  details: () => ['refunds', 'detail'],
  detail: (id) => ['refunds', 'detail', String(id)],
}
```

| 操作 | 缓存动作 |
| --- | --- |
| 申请成功 | `setQueryData(detail(id), refund)` + `invalidateQueries(lists)` |
| 申请失败（HTTP 业务错误） | `invalidateQueries(lists)` + 订单详情 + 订单列表（本地视图已过期） |
| 撤销成功 | `setQueryData(detail(id), refund)` + `invalidateQueries(lists)` |
| 撤销失败（HTTP 业务错误） | 失效退款详情 + 退款全部列表 |
| 列表/详情查询失败 | 网络类错误重试 1 次；4xx/5xx 不重试 |

设计取舍：

- **不做乐观更新**：退款状态由管理员在后台推进，前端无法预测；乐观更新会在失败回滚时闪回错误状态。
- **申请不失效订单缓存**：申请阶段后端不改订单，订单列表上的退款徽标来自退款列表，已随 `lists` 失效。
- **撤销需要失效列表**：订单列表卡片徽标、订单详情「关联退款记录」都来自退款列表。

## 10. 关键技术决策

### 10.1 退款列表页同时请求订单列表（仅用于订单号映射）

退款结构只有 `order_id`，没有 `order_no` 快照。任务要求展示「关联订单编号」，因此：

- 用 `useOrderList()` 的既有查询（与订单页共享缓存，不会重复请求）；
- 建立 `order_id → order_no` 的本地 Map；
- 订单接口失败时降级为「订单 #id」，不影响退款列表主体。

### 10.2 订单列表卡片展示退款徽标

用一次 `GET /refunds`（本人全部，id 倒序）建立 `order_id → 最新退款` 映射，取首次出现者（即 id 最大）。
避免对每个订单发起 N 次 `GET /refunds?order_id=`。

### 10.3 订单详情页的申请入口门禁

前端门禁 = 后端门禁的**子集**，只用于入口可见性：

```
showApplyEntry = 退款列表已加载 && order.status === 'paid' && 无有效退款(status ∈ 10/20/30)
```

- 退款列表未加载完成时不展示入口，避免用户点击一个大概率 18004 的按钮；
- 已退款订单不展示（`status=refunded` 不满足 `paid`）；
- 后端仍是最终裁决者，18003/18004 都有对应文案。

### 10.4 撤销反馈提升到详情页

第一版把成功/失败反馈放在 `RevokeRefundDialog` 内部。实测发现：撤销成功后 `refund.status` 变为 `revoked`，`canRevokeRefund` 为 false → 组件返回 `null` 被卸载 → 内部 `feedback` state 一起丢失，用户看不到任何反馈。
修正：`RevokeRefundDialog` 通过 `onResult` 回调把反馈交给 `RefundDetailContent` 持有，入口卸载后反馈仍可见。

### 10.5 状态筛选使用逐状态而非自行分组

订单列表用了「待支付/待发货/待收货」等业务分组，但退款状态没有对应的业务分组定义。
若自行把 7 个状态合并成「处理中/已结束」，等于发明后端不存在的业务分类，因此筛选器直接列出 7 个真实状态。

### 10.6 表单校验与后端对齐

`refundApplySchema`：TrimSpace 后非空 + `Array.from(value).length ≤ 255`（与 Go `utf8.RuneCountInString` 对齐）。
前端校验只做提前反馈，实际错误仍按 `1001` 处理。

## 11. 实际遇到的问题与排查过程

| # | 问题 | 原因 | 处理 |
| --- | --- | --- | --- |
| 1 | ESLint 报 `react-hooks/incompatible-library` | `react-hook-form` 的 `form.watch()` 无法被 React Compiler 安全 memo | 改用 `useWatch({ control, name })` |
| 2 | 撤销成功后没有成功提示 | 成功后状态变化导致入口组件卸载，内部 state 丢失 | 反馈提升到详情页（见 10.4） |
| 3 | 组件测试找不到金额 `¥2,099.00` | `getByText` 只匹配元素的直接文本节点，`PriceDisplay` 把金额拆成多个子 span | 改为 `toHaveTextContent` / 父容器断言 |
| 4 | 详情页 `getByText('申请中')` 报 multiple elements | 状态同时出现在徽标与「当前状态」字段 | 用 `[data-refund-status]` 精确定位徽标 |
| 5 | 「网络异常」用例进入不了错误态 | hook 对网络错误重试 1 次，默认 retryDelay 1s，超过默认 1s 查询超时 | 前两次都返回错误 + `findByText` timeout 放宽到 3s |
| 6 | 弹层打开时断言不到背后的「订单详情」标题 | Radix Dialog 给背景加 `aria-hidden`，role 查询忽略 | 改为断言 `router.state.location.pathname` |
| 7 | 256 字符输入用例超时 | `userEvent.type` 逐字符触发 256 次事件 | 改用 `user.paste()` |
| 8 | 真实联调「支付后库存 100→99」失败（实际 100） | 脚本重构时把库存快照移到了下单之前 | 快照移到 `createPaidOrder()` 之后，并按每次下单重算基线 |
| 9 | 真实联调「退款失败不恢复库存」失败 | 断言写死 99，实际此时已因新订单扣到 98 | 改为按订单流转计算期望值（98），并逐笔验证 |
| 10 | 真实 UI 联调金额断言失败 | Playwright `innerText` 对 `inline-flex` 子元素会插入换行 | 改用 `textContent()` |
| 11 | 真实 UI 联调进入错误订单，撤销按钮超时 | 订单列表按 id 倒序，第一笔不是目标订单 | 从退款卡片的「关联订单」链接进入，精确定位 |
| 12 | 真实 UI 联调「退款成功筛选为空」失败 | 该用户还有其他历史退款成功记录 | 改为断言「申请中」筛选后只剩本次申请，且历史原因消失 |
| 13 | 本地重建数据库后服务报错 | 连接池持有旧连接 | 重建库后重启服务进程 |

## 12. 测试验证

### 12.1 单元 / 组件（Vitest + jsdom + MSW）

| 文件 | 用例数 | 覆盖 |
| --- | --- | --- |
| `web/src/api/refunds.test.ts` | 11 | 未登录 401、申请成功与服务端金额、非 paid 订单 18003、原因校验 1001、重复申请 18004、撤销后可重申请、越权/不存在 18001、列表倒序与 order_id 过滤、详情、撤销成功/18002/越权 |
| `web/src/pages/account/refunds-page.test.tsx` | 5 | 空状态、字段完整展示、状态筛选、加载失败与重试、刷新重新请求 |
| `web/src/pages/account/refund-detail-page.test.tsx` | 17 | 详情字段、关联订单、审核/处理信息、7 状态映射、404、非法 ID、网络异常重试、撤销成功/不可撤销/18002 刷新/防重复 |
| `web/src/pages/account/refund-actions.test.tsx` | 17 | 入口可见性（paid / 4 种非 paid / 已有有效申请 / 已撤销）、弹层文案与无真实渠道、空原因、超长原因、成功跳转、防重复、18003、18004、网络异常、订单列表徽标、登出清缓存 |

全量：39 个测试文件 / 276 个用例，全部通过。

### 12.2 E2E（Playwright + MSW，**Mock，不是真实后端**）

`web/tests/e2e/refunds.spec.ts`：3 个用例

1. 未登录访问退款中心跳转登录，登录后回到列表并展示空状态；
2. 已支付订单申请整单退款 → 跳转详情 → 列表可见 → 状态筛选；
3. 申请中撤销 → 已撤销反馈 → 订单列表退款徽标与「已支付」同步。

全量 E2E：43 个用例全部通过。

### 12.3 真实 Go 后端联调（**非 Mock**）

环境：

- MySQL 8.0.46（独立容器 3307，全新库，`migrate up` 到 `20261001000024`）
- Redis 7（独立容器 6380，DB 9）
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + `test/container/qiniu_mock`（绕过启动期七牛 bucket 校验，**未修改任何 Go 代码**）
- 前端：`vite` dev（5174）+ `/api` 代理到真实后端 8000，**未设置 `VITE_ENABLE_MOCK`**
- 浏览器：Playwright Chromium，真实交互

接口级：48 项全部 PASS，覆盖申请/列表/详情/审核/执行成功/执行失败/关闭/撤销/重复申请/空原因/状态前置/越权隔离/库存/优惠券不返还。

UI 级：30 项全部 PASS，覆盖未登录保护、申请入口、弹层文案、金额展示、状态展示、列表与筛选、订单联动、撤销、登出后重新保护。

联调数据库终态：

```
refund_requests: 1=60(order1) 5=70(order3) 6=60(order4) 8=50(order2)
orders:          1=70 2=20 3=20 4=70
inventories:     sku 1 quantity=98
refund_audits:   refund 8 → apply(1→10), revoke(10→50)
```

**声明**：E2E 与组件测试使用 MSW，不代表真实后端；真实后端结果以上述两节为准。

## 13. 已知限制

1. **没有真实资金退回**：`refund_succeeded` 只代表内部业务处理完成 + 订单状态推进，没有支付网关、流水、回调与到账时间。
2. **只支持整单全额退款**：没有商品级、部分金额、多次退款。
3. **用户侧没有审计接口**：无法展示状态时间轴与审核操作记录。
4. **用户不能审核/执行**：退款必须由管理员在后台推进；前端不实现后台能力。
5. **退款列表无服务端分页/状态筛选**：只能一次性取回全部，本地筛选与本地分页。
6. **退款金额不可编辑**：由服务端派生，前端不提供金额输入。
7. **退款成功后不可逆**：`refund_succeeded` 是终态。
8. **退款不返还优惠券**：券保持 `used`，与「取消订单释放券」不同。
9. **订单号映射依赖订单接口**：退款结构没有 `order_no`，订单接口不可用时降级为「订单 #id」。
10. **会话失效（非主动登出）只隐藏不清缓存**：401 时 `clearAuthTokens()` 让查询 `enabled=false`，界面不展示旧数据；缓存对象会保留到 GC。主动登出已显式清理 `orders`/`refunds`。
11. **无退款凭证上传**：后端没有面向退款的图片上传接口，前端不提供。
12. **无退款申请次数统计**：后端不返回该维度。

## 14. 后续维护注意事项

- **新增退款状态时**：必须同步 `RefundStatus` 联合类型、`REFUND_STATUS_LABELS`、`describeRefundStatusTone`、`REFUND_STATUS_FILTERS`、`describeRefundStatusHint`；如果新状态属于"有效申请"，还要加入 `ACTIVE_REFUND_STATUSES`。
- **后端若开放用户侧审计接口**：在详情页增加时间轴，但必须使用后端返回的真实事件，不能由前端推导。
- **后端若支持部分退款**：`amount` 将不再恒等于订单金额，申请表单、列表金额语义、"整单退款"文案都要重写。
- **若接入真实支付/退款渠道**：`DescribeRefundStatusHint` 与三处资金声明必须删除或改写；当前所有"不代表真实资金退回"的文案都基于无支付 SDK 的事实。
- **订单详情页入口门禁依赖退款列表**：若退款列表接口失败，申请入口不会显示（保守策略）。若产品要求"始终可点、由后端报错"，需要调整 `showApplyEntry`。
- **登出缓存清理清单**：新增任何账号私有 Query Key（如 W4-04 优惠券）时，必须同步加入 `useLogout` 的 `removeQueries` 列表，否则会跨账号残留。
- **修改撤销反馈结构**：反馈由详情页持有，`RevokeRefundDialog` 只负责触发；不要把它移回弹层内部。
- **不要在前端计算退款金额**：所有金额直接展示 `refund.amount`。

## 15. 技术学习与面试准备

### 15.1 可讲清楚的核心点

1. **状态机驱动 UI，而不是 UI 猜测业务**：7 个状态、2 个可操作条件（申请=`paid`、撤销=`applying`）全部来自后端源码；前端只做可见性，不做业务判定。
2. **服务端派生金额的安全边界**：请求体没有金额字段，前端不计算、不提交、不可编辑；这是"不信任客户端"的直接体现。
3. **单一有效申请的唯一索引设计**：生成列 `active_key = (status IN (10,20,30) ? order_id : NULL)` + `UNIQUE`，用数据库机械保证"一单一条有效申请"，允许拒绝/撤销/关闭后重新申请。前端据此设计"已有有效申请则隐藏入口"。
4. **退款与发货的并发互斥**：申请与发货锁同一 `orders` 行；退款成功与发货靠 `WHERE status=20` 条件更新闸门，避免"既发货又退款成功"。
5. **缓存同步的因果链**：退款列表是订单退款徽标与订单详情关联记录的**唯一数据源**，因此写操作只需失效退款列表；订单缓存只在申请失败（可能订单已变化）时失效。
6. **不做乐观更新的理由**：状态由后台人工推进，前端无法预测；失败回滚会造成状态闪回。
7. **组件卸载与状态丢失**：把反馈放在"会因成功而卸载"的组件里是典型陷阱。

### 15.2 常见追问

- **Q：为什么退款成功后库存恢复了，优惠券却不返还？**
  A：后端在退款成功事务里调用 `Order().CompleteRefundInTx`，只做订单状态推进 + 库存恢复；优惠券的释放逻辑只存在于订单取消路径。联调实测券保持 `used`。
- **Q：为什么退款列表页要多请求一次订单列表？**
  A：退款记录只有 `order_id`，没有订单号快照；为了展示可读的「关联订单编号」，复用订单列表缓存做映射，失败时降级。
- **Q：为什么详情页没有状态时间轴？**
  A：`refund_audits` 只有后台接口（`refund:view`），用户端拿不到事件流；用当前状态推导时间轴等于伪造数据。
- **Q：退款金额会不会被前端改大？**
  A：不会，请求体只有 `reason`；金额由后端从订单快照派生，且 `amount` 字段在 API 契约里不是请求字段。
- **Q：如何保证撤销不是"假成功"？**
  A：撤销调用真实 `POST /refunds/:id/revoke`，成功响应是服务端返回的 `RevokeRes`（status=revoked），并写入详情缓存；失败按 18002 提示并刷新。
- **Q：账号切换会不会看到别人的退款？**
  A：`useLogout` 显式 `removeQueries` 掉 `refunds.all` 与 `orders.all`；查询 key 不含 user id，所以这一步是必需的。
