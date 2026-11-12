# W4-02 订单支付与履约操作：开发知识文档

- 任务：SurgeCart 用户商城 W4-02（模拟支付 / 取消订单 / 确认收货）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 上游依赖：W4-01 订单中心（`/account/orders`、`/account/orders/:id`）
- 后端能力依据：`docs/reports/w4-backend-capability-audit/REPORT.md`

---

## 1. SurgeCart 的「Mock 支付」到底是什么

### 1.1 后端机制

`POST /orders/:id/pay` 是项目内**唯一**的支付入口，它做的事情只有一件：
把订单状态从「待支付」推进到「已支付」，并写入 `paid_at`。

```
internal/logic/order/order.go:447-516
  → 校验订单归属（非本人 9001）
  → status=10 且 expire_at < NOW()  → 先懒取消，返回 9002
  → status=20                       → 幂等成功，原样返回（不改状态、不重复核销券）
  → status=10                       → 单事务：
        UPDATE orders SET status=20, paid_at=NOW()
          WHERE id=? AND user_id=? AND status=10
        RowsAffected=0 → 9002
        若 user_coupon_id 非空 → user_coupons locked→used
  → 其它状态（30/40/50/60/70）      → 9002
```

全仓库事实（W4-00 审查已确认，本次复核未变）：

| 能力 | 现状 |
| --- | --- |
| 第三方支付 SDK / 网关 | **不存在** |
| `payments` / 支付流水表 | **不存在**（37 张表中无支付表） |
| 支付回调路由 | **不存在** |
| 支付状态查询接口 | **不存在** |
| 支付金额校验 | **不存在**（请求体为空，后端不校验金额） |
| 支付方式 / 渠道参数 | **不存在** |

因此前端必须：

1. 在入口与确认框显式声明「项目模拟支付，不会产生真实资金扣款」；
2. 不出现支付宝 / 微信支付 / 银联等真实支付品牌；
3. 不展示"支付渠道选择""支付流水""交易号"等后端并不存在的东西。

### 1.2 前端实现

`web/src/components/order/order-actions.tsx`：

- 只有 `status === 'pending_payment'` 才渲染「模拟支付」按钮；
- 点击打开确认 Dialog，内含警示块（`不会产生真实资金扣款`、`不对接任何第三方支付渠道`）、
  订单号、应付金额（`PriceDisplay` 展示后端 `total_amount`）；
- 确认后调用 `payOrder(orderId)` → `POST /orders/:id/pay`；
- 成功后用**服务端返回的订单**覆盖缓存，界面立刻变为「已支付」，按钮消失。

前端**没有任何**「本地把状态改成已支付」的代码路径。

---

## 2. 订单状态转换（本次涉及的三个操作）

```
                       ┌── cancel（用户，cancel_reason=1）──► cancelled(60)
pending_payment(10) ───┤
                       └── pay（模拟）──► paid(20) ── admin ship ──► shipped(30)
                                                                        │
                                                                        └── receive ──► completed(50)
                                                                             （同一事务 30→40→50）
```

| 操作 | 前置状态 | 目标状态 | 失败码 |
| --- | --- | --- | --- |
| `POST /orders/:id/pay` | `pending_payment` | `paid`（+`paid_at`） | 9001 / 9002 |
| `POST /orders/:id/cancel` | `pending_payment` | `cancelled`（+`cancel_reason=1`、`cancelled_at`） | 9001 / 9002 |
| `POST /orders/:id/receive` | `shipped` | `completed`（+`received_at`、`completed_at`） | 9001 / 9002 |

关键点：

- **`paid` 不能取消**：`cancelInTx` 的 WHERE 只有 `status=10`，已支付订单调用取消返回 9002；
- **`pending_payment` 不能收货**：`Receive` 的 WHERE 只有 `status=30`；
- **`received(40)` 对外不可观测**：`Receive` 在同事务内 30→40、40→50，响应恒为 `completed`；
- **取消是终态**，`cancelled`/`refunded` 之后没有任何用户可执行的操作。

前端的按钮可见性严格由 `order.status` 决定（`order-actions.tsx`）：

```ts
const canPay = order.status === 'pending_payment'
const canCancel = order.status === 'pending_payment'
const canReceive = order.status === 'shipped'
```

没有 `else`、没有"兜底放行"，状态不在白名单里就不渲染按钮。

---

## 3. 取消订单与库存释放

`internal/logic/order/order.go:729-761`（`cancelInTx`）：

```
BEGIN
  UPDATE orders SET status=60, cancel_reason=1, cancelled_at=NOW()
    WHERE id=? AND status=10                    -- 条件更新即闸门
  RowsAffected=0 → 返回 false（上层映射 9002）
  for each order_item:
      Inventory.IncreaseInTx(sku_id, quantity)  -- 恢复库存
  Coupon.ReleaseInTx(order_id)                  -- locked→available，清空 order_id/locked_at
COMMIT
```

要点：

- **条件更新是并发闸门**：8 个并发取消只有 1 个 `RowsAffected=1`，库存只恢复一次
  （后端 e2e `TestOrderCancelRestoresInventoryOnce` 覆盖）；
- **库存恢复与状态变更同事务**，不会出现"取消了但库存没还"；
- **优惠券释放是 no-op 安全的**：只更新 `status=20 AND order_id=?` 的行，重复调用无副作用；
- 取消**不会**删除订单、不会改地址快照、不会影响 `order_items`。

真实联调实测（见交付报告）：

```
取消前库存 95 → 取消（数量 2 的订单）→ 取消后库存 97
user_coupons: available → locked(order_id=5) → available(order_id=NULL)
```

---

## 4. 确认收货的业务语义

`internal/logic/order/order.go:551-595`：

```
BEGIN
  UPDATE orders SET status=40, received_at=NOW() WHERE id=? AND user_id=? AND status=30
  RowsAffected=0 → 9002
  UPDATE orders SET status=50, completed_at=NOW() WHERE id=? AND status=40
COMMIT
```

- 语义是「买家确认已收到货」→ 订单直接进入**完成态**，不经过"待评价"等中间态；
- 业务上不可撤销，前端确认框必须写明「确认后不可撤销」；
- 后端不做库存/优惠券动作（货已经发出，库存早在下单时扣掉）；
- 因为 30→40→50 是同事务，**前端永远看不到 `received`**，进度条直接跳到「已完成」。

---

## 5. 订单超时懒取消

### 5.1 机制

| 触发点 | 代码 | 行为 |
| --- | --- | --- |
| `GET /orders/:id`（详情） | `order.go:431-435` | `status=10` 时先 `cancelExpired`，`expire_at < NOW()` 则取消（`cancel_reason=2`） |
| `POST /orders/:id/pay` | `order.go:456-465` | 同上，取消后返回 **9002** |
| 后台扫描器（每 60s，每批 100 条） | `worker/scanner.go:50-68` + `order.go:683-711` | 批量取消过期订单 |
| `GET /orders`（列表） | `order.go:405-420` | **不触发**懒取消 |

### 5.2 前端怎么处理（W4-02 的约束）

W4-01 已确认时间字段存在时区不一致（`created_at` UTC 墙钟 vs `expire_at` +08 墙钟），
因此 W4-02 继续遵守：

1. **不实现倒计时**；
2. **不硬编码支付时限**；
3. **不由前端主动取消超时订单**（没有这样的接口调用）；
4. **不用本地时间推断订单是否过期**；
5. **以重新请求后端返回的状态为准**。

具体落地：

- 详情页只展示后端给的静态「支付截止时间」；
- 用户点「模拟支付」时，如果订单其实已过期，后端会先懒取消再返回 9002，
  前端展示错误文案并**同时失效详情与列表缓存**，页面自动变成「已取消 + 超时未支付自动取消」；
- 列表可能短暂显示"已过期但仍待支付"的订单（后端列表不懒取消），
  这是已知行为，进入详情即以服务端为准。

真实联调实测（页面已缓存"待支付"→ 服务端把 `expire_at` 改到过去 → 点击支付）：

```
错误提示：订单状态已变化（可能已超时自动取消），已为你刷新最新状态
刷新后：status=cancelled，取消原因=超时未支付自动取消，支付按钮消失
```

---

## 6. 前端状态同步机制

### 6.1 数据流

```
OrderActions（组件）
  └─ useOrderAction(orderId)          hooks/use-orders.ts
       ├─ mutationFn: ACTION_REQUEST[action](orderId)
       │     ├─ payOrder  → POST /orders/:id/pay
       │     ├─ cancelOrder → POST /orders/:id/cancel
       │     └─ receiveOrder → POST /orders/:id/receive
       ├─ onSuccess: setQueryData(detail) + invalidate(list)
       └─ onError(http): invalidate(detail) + invalidate(list)
```

### 6.2 为什么不用乐观更新

- 交易状态不能靠前端猜测：支付/收货的结果取决于后端状态闸门，猜错会让用户以为成功；
- 失败时要"回滚"，而回滚本身会产生一次状态闪变（先已支付再跳回待支付），体验更差；
- 因此所有操作都等**服务端响应**：成功用响应覆盖，失败则失效缓存回到真实状态。

### 6.3 缓存更新的三个落点

| 落点 | 做法 | 效果 |
| --- | --- | --- |
| 订单详情 | `setQueryData(orders.detail(id), order)` | 当前页立刻显示新状态，无需再发 GET |
| 订单列表 | `invalidateQueries(orders.list())` | 返回列表时重新拉取，状态/分组计数同步 |
| 个人中心入口 | 无独立状态 | 订单列表是唯一数据源，列表更新即入口更新 |

> 列表 key 不带参数（后端无筛选/分页参数），所以一次失效就覆盖所有本地分组。

### 6.4 并发保护

- `mutation.isPending` 同时驱动三个按钮与弹层确认按钮的 `disabled` / `isLoading`；
- 弹层在请求在途时不允许关闭（`handleOpenChange` 直接 return），
  避免用户以为操作被取消而重复发起；
- 单测用 Mock 延迟（`setMockOrderActionDelay(120)`）断言：
  请求在途时确认按钮 `disabled` 且 `aria-busy=true`，最终只发出 **1** 次请求。

---

## 7. 关键文件与调用链

| 文件 | 职责 |
| --- | --- |
| `web/src/api/orders.ts` | `payOrder` / `cancelOrder` / `receiveOrder`（POST + 空对象 body），返回完整 Order |
| `web/src/hooks/use-orders.ts` | `useOrderAction`：mutation、缓存更新、失败失效 |
| `web/src/components/order/order-actions.tsx` | 操作区、三个确认 Dialog、成功/失败反馈、防重复点击 |
| `web/src/pages/account/order-action-errors.ts` | 9001/9002/1002/网络/5xx 的错误文案 |
| `web/src/pages/account/order-detail-page.tsx` | 详情页装配（操作区位于顶部） |
| `web/src/mocks/data/orders.ts` | Mock 状态迁移（pay/cancel/receive）+ 延迟注入 + 请求计数 |
| `web/src/mocks/handlers.ts` | 三个 POST 路由的 MSW handler |

后端调用链（只读核对）：

```
routes_frontend.go:95-97
  → controller/order.go:48-72（currentUserID）
    → service/order.go:19-24（IOrder 接口）
      → logic/order/order.go:447-516 / 518-549 / 551-595
        → Inventory.IncreaseInTx / Coupon.MarkUsedInTx / Coupon.ReleaseInTx
```

---

## 8. 技术决策与权衡

| # | 决策 | 理由 |
| --- | --- | --- |
| D1 | 三个操作共用一个 `useOrderAction` mutation | 三者互斥、同一订单、同一缓存更新逻辑；共用一个 `isPending` 天然防并发 |
| D2 | 成功用 `setQueryData` 而非 `invalidate(detail)` | 后端返回的就是最新完整订单，省一次往返；且详情 GET 会触发懒取消副作用，不宜额外调用 |
| D3 | 失败时对 HTTP 错误失效缓存 | 9002 意味着"本地缓存已过期"，必须立刻回到服务端状态 |
| D4 | 网络错误不失效缓存 | 请求可能没到服务端，保留原状态让用户重试更合理 |
| D5 | 弹层在成功/失败后都关闭 | 失败时页面已刷新为新状态，停在旧确认框只会诱导重复点击 |
| D6 | 请求体发送 `{}` | 后端不读 body，但显式空对象避免"空 body 无 Content-Type"被代理改写 |
| D7 | 操作区放在详情页顶部 | 用户进入详情最需要知道"现在能做什么"，避免长页面下滚 |
| D8 | 不新增路由/页面 | 任务要求不加独立支付中心 |

---

## 9. 实际问题与排查过程

### 9.1 W4-01 的旧测试断言失效

W4-01 的用例曾断言"待支付页不出现任何支付/取消按钮"。W4-02 有意引入操作入口后该断言必然失败。
处理：把断言改为「不提供前端倒计时」+「提供模拟支付与取消入口」，
并新增 `order-actions.test.tsx` 专门覆盖操作行为，避免两个任务的责任混在一个用例里。

### 9.2 9002 竞态用例一开始无法复现

最初在渲染前就把 Mock 订单改成 cancelled，导致页面首次加载就是"已取消"，根本没有支付按钮。
修正：**先渲染出"待支付"**，再改 Mock 状态并覆盖 handler，才能复现
「页面显示待支付 / 服务端已取消」的真实竞态。

### 9.3 真实联调时库存读不到

脚本用 `data.stock` 读取库存，实际响应是内嵌 `Inventory`，字段名是 `quantity`
（`api/inventory/v1/inventory.go:44-46`），导致 `undefined`。修正后得到 `95 → 97`。

### 9.4 真实联调控制台出现 409

这是**主动触发**的超时支付拒绝（9002 对应 HTTP 409），浏览器会打印资源加载失败。
不是应用缺陷。脚本改为区分「预期内 409」与「非预期错误」，后者为 0。

### 9.5 数据库重建后服务连接失效

`DROP DATABASE` 后连接池仍持有旧连接，`/health` 可能正常但业务报错。
处理：重建数据库后**重启服务进程**，让连接池重新建立。

---

## 10. 测试验证

### 10.1 单元 / 组件（Vitest + MSW）

| 文件 | 覆盖 |
| --- | --- |
| `src/api/orders.test.ts` | 三个操作未登录 401；支付幂等（`paid_at` 不变）；已取消不能支付；取消写入 `cancel_reason=1`；已支付不能取消；收货 shipped→completed；未发货不能收货；他人订单 9001 |
| `src/pages/account/order-actions.test.tsx` | 按钮可见性（待支付/已支付/已取消/已退款/待发货）；支付确认框文案与无支付品牌；支付成功状态更新；请求在途禁用 + 只发一次请求；9002 后刷新为 cancelled；网络异常不改状态；取消成功 + 取消原因；确认收货成功；待发货无收货入口 |

### 10.2 e2e（Playwright + MSW）

`tests/e2e/orders.spec.ts` 新增：
- 待支付订单完成模拟支付 → 状态与列表分组同步；
- 待支付订单取消 → 取消原因展示 + 列表「已关闭」同步。

### 10.3 真实后端联调（非 Mock）

真实 MySQL 8.0 + Redis + 真实 Go 服务 + 无 MSW 前端，Playwright 驱动，**22 项全 PASS**：

- 4 笔订单列表；
- 支付确认框声明模拟支付、无真实支付品牌；
- 支付后 `status=paid`、`paid_at` 回填、支付入口消失；
- 取消确认框声明库存与券处理；取消后 `cancel_reason=1`、入口消失；
- **库存 95 → 97**（取消释放 2 件）；
- 已发货订单出现「确认收货」，且无支付/取消入口；确认后 `completed`；
- 超时订单：页面缓存待支付 → 服务端过期 → 点击支付返回 9002 → 自动刷新为 `cancelled` + `超时未支付自动取消`；
- 列表分组计数：全部 4 / 待支付 0 / 待发货 1 / 待收货 0 / 已完成 1 / 已关闭 2；
- 控制台无非预期错误。

另有接口级验证：用券下单 → 券 `locked` → 取消订单 → 券 `available`（`order_id` 清空），
证明取消确实释放优惠券。

---

## 11. 已知限制

1. **没有真实支付**：无扣款、无流水、无回调、无退款；支付成功仅代表订单状态推进。
2. **支付接口没有金额校验**：不是"校验通过才扣款"，而是根本没有资金环节。
3. **无法在前端展示支付渠道/交易号**：后端不存在这些字段。
4. **确认收货不可撤销**，也没有"延长收货时间"之类的接口。
5. **`received` 状态不可观测**，进度条直接从「待收货」跳到「已完成」。
6. **列表不会懒取消**：可能短暂显示已过期的待支付订单。
7. **时间字段时区不一致**（W4-01 遗留）：不做倒计时、不做本地过期推断。
8. **无退款**：`paid` 之后的售后属于 W4-03。
9. **超时后的库存/券释放依赖后端**：前端只负责重新拉取状态。

---

## 12. 后续维护注意事项

- **若接入真实支付**：`payOrder` 需要改为"创建支付单 → 跳转/轮询 → 回调确认"，
  当前 `useOrderAction` 的"一次 POST 定成败"假设将不再成立；
  同时 `order-actions.tsx` 里的模拟支付声明必须删除。
- **若后端新增「待支付可取消」之外的可取消状态**：只需改 `canCancel` 与文案，
  `useOrderAction` 与错误映射无需变动。
- **若后端把 `received` 变成可观测状态**：进度映射与按钮白名单都要同步（`canReceive` 仍只认 `shipped`）。
- **若后端统一时间格式**：可以补倒计时，但要重新评估"超时自动取消"与倒计时的交互。
- **新增订单操作时**：必须同时扩展 `OrderActionKind`、`ACTION_REQUEST`、`SUCCESS_TEXT`、
  `DIALOG_TITLE` 与错误映射，缺一处会出现"按钮能点但没反馈"。

---

## 13. 技术学习与面试要点

1. **"Mock 支付"和"支付"是两件事**：前者只是一个状态机迁移，后者涉及资金、对账、回调、幂等。
   把 Mock 说成支付，是典型的能力虚报。
2. **条件更新即分布式锁**：`UPDATE ... WHERE status=10` + `RowsAffected` 判断，
   比"先查再改"更安全，天然抵御并发重复提交。
3. **同事务保证补偿一致性**：取消订单时"改状态 + 还库存 + 释放券"必须同事务，
   否则会出现"取消了但库存没还"的资损。
4. **幂等的不同层次**：支付接口对"已支付"幂等成功；取消对"已取消"返回 9002（非幂等），
   因为重复取消是用户的错误操作，应该明确告知而不是假装成功。
5. **缓存失效比缓存更新更重要**：成功时能拿到权威响应，失败时只能靠失效回到真实状态。
6. **不要在交易链路做乐观更新**：用户对"钱/货"的容忍度远低于点赞收藏。
7. **前端不能修正后端数据**：时区不一致时，正确做法是记录问题、限制功能（不做倒计时），
   而不是加偏移把界面"修好看"。
