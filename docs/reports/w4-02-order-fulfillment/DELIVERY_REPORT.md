# W4-02 订单支付与履约操作：交付报告

- 任务：SurgeCart 用户商城 W4-02（模拟支付 / 取消订单 / 确认收货）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W4-02-order-fulfillment.md`
- 前置依赖：W4-01 订单中心（`a0dc9b0`）
- 后端能力依据：`docs/reports/w4-backend-capability-audit/REPORT.md`
- 提交说明：代码与两份文档属同一提交；报告无法包含自身提交 SHA，见交付汇报与 `git log -1 --format=%H`

---

## 0. 摘要

在 W4-01 订单中心之上实现三个用户订单操作：**模拟支付**、**取消订单**、**确认收货**，
并把操作结果同步到订单详情、订单列表与个人中心入口。

- 三个接口都是后端已实现且有测试的能力，Method/Path/请求体/状态闸门逐项核对源码后才接入；
- 支付在 UI 与文案上**明确标注为项目模拟支付**，不出现任何真实支付品牌；
- 全部状态变更以服务端响应为准，**没有任何乐观更新或本地伪造状态**；
- 未实现退款（W4-03）、优惠券中心（W4-04）、秒杀，也未修改任何后端代码。

---

## 1. 实际实现的功能

| 功能 | 入口条件 | 交互 | 结果 |
| --- | --- | --- | --- |
| 模拟支付 | `status = pending_payment` | 确认弹层（声明模拟支付、展示订单号与应付金额）→ 确认 → 请求中 → 成功/失败反馈 | 订单变为「已支付」，`paid_at` 回填，支付/取消入口消失 |
| 取消订单 | `status = pending_payment` | 确认弹层（说明不可撤销、恢复库存、释放优惠券）→ 确认 | 订单变为「已取消」，展示「用户主动取消」，入口全部消失 |
| 确认收货 | `status = shipped` | 确认弹层（说明确认后直接完成、不可撤销）→ 确认 | 订单变为「已完成」，入口消失 |

配套：

- 操作区固定在订单详情页顶部（状态徽标下方），任何时候都能立刻看到"当前能做什么"；
- 每个状态都有对应说明文案（已支付=等待发货、已关闭=不可操作等）；
- 请求在途时三个按钮与弹层确认按钮统一 `disabled + spinner`，弹层不可关闭；
- 成功后详情缓存立即更新、列表缓存立即失效，返回列表时状态与分组计数已同步。

---

## 2. 使用的 API

| Method | Path | 请求体 | 响应 | 认证 |
| --- | --- | --- | --- | --- |
| POST | `/orders/:id/pay` | `{}`（后端不读 body） | 完整 Order | Bearer 用户 token |
| POST | `/orders/:id/cancel` | `{}` | 完整 Order | 同上 |
| POST | `/orders/:id/receive` | `{}` | 完整 Order | 同上 |

契约来源：`api/order/v1/order.go:112-143`（三个 Req 只有 `Id` in path）；
控制器 `internal/controller/order/order.go:48-72`；
路由 `internal/cmd/routes_frontend.go:95-97`（Auth 分组）。

**未使用**：退款、支付流水、支付查询、支付回调、优惠券接口。

---

## 3. 请求与响应结构

### 3.1 请求

```http
POST /orders/1/pay
Authorization: Bearer <access_token>
Content-Type: application/json

{}
```

三个接口路径不同，请求体完全一致（空对象）。

### 3.2 成功响应（以支付为例，真实后端实测）

```json
{
  "code": 0,
  "message": "OK",
  "data": {
    "id": 1,
    "order_no": "17943035273564f588b953213",
    "user_id": 2,
    "status": "paid",
    "total_amount": 2599,
    "original_amount": 2599,
    "discount_amount": 0,
    "user_coupon_id": null,
    "idempotency_key": "w402-pay",
    "recipient_name": "张三",
    "phone": "13800138000",
    "province": "广东省",
    "city": "深圳市",
    "district": "南山区",
    "detail": "科技园1号",
    "address_id": 1,
    "expire_at": "2026-11-10 17:49:07",
    "cancel_reason": null,
    "paid_at": "2026-11-10 17:34:19",
    "shipped_at": null,
    "received_at": null,
    "completed_at": null,
    "cancelled_at": null,
    "refunded_at": null,
    "created_at": "2026-11-10 09:34:07",
    "updated_at": "2026-11-10 09:34:07",
    "items": [ /* OrderItem[] */ ]
  }
}
```

取消响应中 `status="cancelled"`、`cancel_reason=1`、`cancelled_at` 有值；
收货响应中 `status="completed"`、`received_at` 与 `completed_at` 均有值。

### 3.3 失败响应

| 场景 | HTTP | code | 前端文案 |
| --- | --- | --- | --- |
| 未登录 / 会话失效 | 401 | `1002` | 登录状态已失效，请重新登录后再试 |
| 订单不存在或非本人 | 404 | `9001` | 订单不存在或无权操作，请返回订单列表查看 |
| 状态不允许（含超时已取消） | 409 | `9002` | 按操作区分：支付「订单状态已变化（可能已超时自动取消），已为你刷新最新状态」；取消「订单当前不是待支付状态…」；收货「订单当前不是已发货状态…」 |
| 参数异常 | 400 | `1001` | 请求参数异常，请刷新页面后重试 |
| 服务端错误 | 500 | `1000` | 服务暂时不可用，请稍后重试 |
| 网络/超时 | — | — | 网络异常，请检查网络后重试 / 请求超时 |

---

## 4. 各订单状态允许的操作

| 后端状态 | 模拟支付 | 取消订单 | 确认收货 | 说明 |
| --- | --- | --- | --- | --- |
| `pending_payment` | ✅ | ✅ | ❌ | 后端 `pay` / `cancel` 的 WHERE 均为 `status=10` |
| `paid` | ❌（幂等成功但不展示入口） | ❌ | ❌ | 等商家发货 |
| `shipped` | ❌ | ❌ | ✅ | 后端 `receive` 的 WHERE 为 `status=30` |
| `received` | ❌ | ❌ | ❌ | 后端同事务 30→40→50，实际不可观测 |
| `completed` | ❌ | ❌ | ❌ | 终态 |
| `cancelled` | ❌ | ❌ | ❌ | 终态 |
| `refunded` | ❌ | ❌ | ❌ | 终态 |

前端白名单（`order-actions.tsx`）：

```ts
const canPay = order.status === 'pending_payment'
const canCancel = order.status === 'pending_payment'
const canReceive = order.status === 'shipped'
```

不存在任何"其他状态兜底放开"的分支。

---

## 5. 模拟支付的实际业务机制

`internal/logic/order/order.go:447-516`：

1. 按 `id + user_id` 查订单，未命中 → 9001；
2. 若 `status=10` 且 `expire_at < NOW()` → 先执行懒取消（恢复库存、释放券）→ 返回 9002；
3. 若 `status=20` → **幂等成功**，原样返回（不改状态、不重复核销券）；
4. 若 `status=10` → 单事务：
   `UPDATE orders SET status=20, paid_at=NOW() WHERE id=? AND user_id=? AND status=10`；
   `RowsAffected=0` → 9002；若 `user_coupon_id` 非空 → `user_coupons` `locked→used`；
5. 其它状态 → 9002。

**这不是真实支付**：

| 检查项 | 结论 |
| --- | --- |
| 第三方支付 SDK / 网关 | 不存在 |
| `payments` / 支付流水表 | 不存在 |
| 支付回调路由 | 不存在 |
| 支付状态查询接口 | 不存在 |
| 支付金额校验 | 不存在（请求体为空） |
| 支付渠道 / 交易号 | 不存在 |

前端对应处理：入口按钮写「模拟支付」；确认弹层固定展示
「当前为项目模拟支付，不会产生真实资金扣款；本页面不对接任何第三方支付渠道，也没有支付流水与回调。」；
**不出现支付宝/微信支付/银联等品牌**（组件测试与 e2e 均有断言）。

---

## 6. 取消订单的库存与优惠券处理

`internal/logic/order/order.go:729-761`（`cancelInTx`）：

```
UPDATE orders SET status=60, cancel_reason=1, cancelled_at=NOW()
  WHERE id=? AND status=10          -- 条件更新即并发闸门
RowsAffected=0 → 9002
逐条 order_items → Inventory.IncreaseInTx(sku_id, quantity)   -- 恢复库存
Coupon.ReleaseInTx(order_id)        -- locked→available，清空 order_id / locked_at
（以上全部在同一事务内）
```

真实联调实测：

| 验证 | 结果 |
| --- | --- |
| 库存 | 取消前 95 → 取消（数量 2 的订单）→ 取消后 **97** |
| 优惠券 | `available → locked(order_id=5） → available(order_id=NULL)` |
| 订单 | `status=60`、`cancel_reason=1`、`cancelled_at` 有值 |

并发安全：后端 e2e `TestOrderCancelRestoresInventoryOnce` 断言 8 个并发取消只有 1 个成功、
库存只恢复一次（本次未重复执行该测试，属既有后端保障）。

**已支付订单不允许取消**：`cancelInTx` 的 WHERE 只有 `status=10`，前端也不渲染取消按钮。

---

## 7. 确认收货的业务逻辑

`internal/logic/order/order.go:551-595`：

```
UPDATE orders SET status=40, received_at=NOW() WHERE id=? AND user_id=? AND status=30
RowsAffected=0 → 9002
UPDATE orders SET status=50, completed_at=NOW() WHERE id=? AND status=40
（同一事务）
```

- 语义：买家确认已收到货 → 订单**直接完成**，没有"待评价"等中间态；
- 不可撤销，前端确认框明确写「确认后订单状态将变为「已完成」，不可撤销」；
- 后端不做库存/优惠券动作（库存下单时已扣、券支付时已核销）；
- 因为 30→40→50 同事务，响应恒为 `completed`，`received` 对外不可观测；
- **仅 `shipped` 可入**：待支付、待发货、已完成、已关闭都不展示入口。

---

## 8. 订单超时处理机制

| 读取/操作 | 是否触发懒取消 | 说明 |
| --- | --- | --- |
| `GET /orders/:id` | **是** | `status=10` 且过期 → 取消后返回 cancelled |
| `POST /orders/:id/pay` | **是** | 取消后返回 9002 |
| `GET /orders` | 否 | 列表可能短暂显示已过期的「待支付」 |
| 后台扫描器 | 每 60s 扫 100 条 | 与懒取消同一 `cancelInTx` 实现（`cancel_reason=2`）|

前端策略（严格遵守任务约束）：

1. 不实现倒计时；
2. 不硬编码支付时限；
3. 不由前端主动取消超时订单；
4. 不用本地时间推断是否过期；
5. 以重新请求后端返回的状态为准。

落地方式：支付失败（9002）时同时失效详情与列表缓存，页面自动回到服务端真实状态；
列表/详情展示的状态永远来自接口，不出现"前端认为已过期"的推断。

真实联调实测（页面已缓存「待支付」→ 服务端将 `expire_at` 改到过去 → 点击模拟支付）：

```
错误提示：订单状态已变化（可能已超时自动取消），已为你刷新最新状态
刷新结果：status=cancelled，取消原因=超时未支付自动取消，支付/取消入口全部消失
数据库：id=4 status=60 cancel_reason=2，库存已恢复（95→97→98）
```

---

## 9. 真实后端联调结果

### 9.1 环境（真实，非 Mock）

- MySQL 8.0（独立容器 3307，全新库，`migrate up` 到 `20261001000024`）
- Redis 7
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + 仓库自带 `test/container/qiniu_mock`
  （绕过启动期七牛 bucket 校验；**未修改任何 Go 代码**）
- 前端：`vite` dev + `/api` 代理到真实后端，**未设置 `VITE_ENABLE_MOCK`**
- 浏览器：Playwright Chromium，真实交互

### 9.2 UI 级验证（22 项全 PASS）

| 验证项 | 结果 |
| --- | --- |
| 订单列表加载 4 笔 | PASS |
| 支付确认框声明「不会产生真实资金扣款」 | PASS |
| 确认框无支付宝/微信/银联品牌 | PASS |
| 支付后 `status=paid` | PASS |
| 支付后无支付入口 | PASS |
| 支付时间已回填 | PASS |
| 取消确认框说明库存与优惠券处理 | PASS |
| 取消原因=用户主动取消 | PASS |
| 取消后无支付/取消入口 | PASS |
| 取消释放库存（95 → 97） | PASS |
| 已发货订单展示「确认收货」 | PASS |
| 已发货订单无支付/取消入口 | PASS |
| 确认收货后 `status=completed` | PASS |
| 超时订单初始为待支付 | PASS |
| 超时支付返回 9002 + 提示已刷新 | PASS |
| 超时订单自动变为已取消 | PASS |
| 超时取消原因=超时未支付自动取消 | PASS |
| 超时后无支付入口 | PASS |
| 列表分组计数同步（全部4/待支付0/待发货1/待收货0/已完成1/已关闭2） | PASS |
| 控制台无非预期错误 | PASS |
| 预期内的 409 资源日志（主动触发的超时支付拒绝） | PASS |

### 9.3 接口级补充验证（优惠券释放）

```
创建模板 → 领取（available）
→ 用券下单：2599 - 500 = 2099，券变为 locked(order_id=5)
→ 取消订单：券回到 available，order_id=NULL
```

### 9.4 数据库终态（联调结束）

```
inventories.quantity = 98        -- 100 - 6 单 + 取消释放(2+1+1)
orders: 1=paid(20) 2=cancelled(60,cancel_reason=1) 3=completed(50) 4=cancelled(60,cancel_reason=2) 5=cancelled(60,cancel_reason=1)
```

---

## 10. Mock 测试结果（与真实联调严格区分）

| 层 | 环境 | 覆盖 |
| --- | --- | --- |
| 单元/组件 | Vitest + jsdom + MSW | `order-actions.test.tsx` 13 个用例 + `orders.test.ts` 7 个操作用例 |
| e2e | Playwright + `VITE_ENABLE_MOCK=true`（MSW） | 4 个用例，其中 2 个为 W4-02 新增（支付、取消） |

Mock 侧新增能力（仅测试/Local Mock 存在，真实后端没有）：

- `payMockOrder` / `cancelMockOrder` / `receiveMockOrder`：状态迁移与 9002 闸门对齐后端；
- `setMockOrderActionDelay`：构造"请求中"状态，验证按钮禁用与防重复点击；
- `getMockOrderActionRequestCount`：断言重复点击只发出一次请求。

**声明**：e2e 使用 MSW，不代表真实后端；真实后端结果见第 9 节。

---

## 11. 后端尚未支持的能力

| 能力 | 现状 | 影响 |
| --- | --- | --- |
| 真实支付（扣款/流水/回调/对账） | 不存在 | 只能做模拟支付，不能宣称已收银 |
| 支付状态查询 / 支付记录 | 不存在 | 不开发支付详情页 |
| 支付金额校验 | 不存在 | 后端不校验"付了多少" |
| 已支付订单取消 | 不支持（9002） | 已支付只能走退款（W4-03） |
| 确认收货后撤销 | 不支持 | 文案必须写"不可撤销" |
| 退货退款 / 部分退款 | 不存在 | 本任务不涉及 |
| 物流/快递单号/轨迹 | 无字段 | 详情页不展示物流 |
| 服务端倒计时依据 | 时间字段时区不一致 | 不做倒计时（W4-01 已记录） |
| 订单列表状态筛选/分页 | 不支持 | 仍为本地筛选/分页 |

---

## 12. 关键文件及职责

| 文件 | 类型 | 职责 |
| --- | --- | --- |
| `web/src/api/orders.ts` | 改 | 新增 `payOrder` / `cancelOrder` / `receiveOrder` |
| `web/src/hooks/use-orders.ts` | 改 | `OrderActionKind`、`useOrderAction`（mutation + 缓存策略） |
| `web/src/components/order/order-actions.tsx` | 新增 | 操作区、三个确认 Dialog、成功/失败反馈、防重复点击 |
| `web/src/pages/account/order-action-errors.ts` | 新增 | 9001/9002/1002/网络/5xx 文案映射 |
| `web/src/pages/account/order-detail-page.tsx` | 改 | 挂载 `<OrderActions />`、更新页脚说明 |
| `web/src/mocks/data/orders.ts` | 改 | Mock 状态迁移、延迟注入、请求计数 |
| `web/src/mocks/handlers.ts` | 改 | 三个 POST 路由的 MSW handler |
| `web/src/pages/account/order-actions.test.tsx` | 新增 | 操作区组件测试 |
| `web/src/api/orders.test.ts` | 改 | 三个操作的 API 层用例 |
| `web/src/pages/account/order-detail-page.test.tsx` | 改 | 适配 W4-02 后详情页的新按钮 |
| `web/tests/e2e/orders.spec.ts` | 改 | 新增支付、取消两条 e2e |

未修改：Go 后端、SQL、管理端、Docker、Nginx、Kubernetes、其他分支。

---

## 13. 测试结果

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error / 0 warning） |
| `npm run typecheck` | 通过 |
| `npm run test` | **226 passed / 35 files** |
| `npm run build` | 成功；`order-detail-page` 16.89 kB（gzip 5.17 kB）、`orders-page` 5.71 kB |
| `npm run test:e2e` | **40 passed**（含 W4-02 新增 2 条） |

对照任务要求的测试点：

| 要求 | 覆盖 |
| --- | --- |
| 待支付订单模拟支付 | 组件 + e2e + 真实联调 |
| 已支付订单不能重复支付 | API 幂等用例（`paid_at` 不变）+ 已支付不展示入口 |
| 已关闭订单不能支付 | `order-actions.test.tsx`（已取消/已退款无入口）+ API 9002 |
| 支付失败 | 9002 与网络异常两条用例 |
| 重复点击 | 延迟注入 + 断言仅 1 次请求 + 按钮 disabled |
| 支付后状态更新 | 组件 + e2e + 真实联调 |
| 明确的模拟支付提示 | 组件断言文案 + 断言无真实支付品牌 |
| 合法状态取消 / 不允许取消 | 组件 + API 9002 |
| 取消成功 / 取消失败 | 组件 + 真实联调 |
| 取消后列表同步 | e2e（已关闭分组）+ 真实联调分组计数 |
| 待收货确认 / 不允许确认 | 组件（shipped vs paid） |
| 确认成功 / 失败 | 组件 + API 9002 |
| 确认后状态更新 | 组件 + 真实联调 |
| 超时懒取消后的状态展示 | 真实联调 9002 自动刷新为 cancelled |
| 超时订单不能继续执行操作 | 真实联调：支付入口消失 |
| 重新请求后的缓存同步 | `useOrderAction` 失败失效 + 真实联调 |
| 不依赖前端倒计时 | 无倒计时代码；页面展示后端原值 |

---

## 14. 两份文档路径

| 文档 | 路径 |
| --- | --- |
| 开发知识文档 | `.agent/tasks/dev/W4-02-order-fulfillment.md` |
| 交付报告（本文） | `docs/reports/w4-02-order-fulfillment/DELIVERY_REPORT.md` |

---

## 15. Git 提交

代码 + 开发知识文档 + 本交付报告为**同一提交**。
报告中无法写入自身 SHA，提交后由交付汇报给出，可用 `git log -1 --format=%H` 复核。

前置提交：`a0dc9b0 feat(web): implement user order center with list and detail`（W4-01）。

---

## 16. 工作区状态

- 分支：`feat/phase2-user-frontend`
- 仅修改 `web/`（前端）与 `docs/`、`.agent/`（文档）
- 未执行：push、force push、合并、切分支
- 交付后工作区应为 clean
