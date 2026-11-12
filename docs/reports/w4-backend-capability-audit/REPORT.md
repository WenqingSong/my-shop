# W4-00：订单、支付与营销后端能力审查报告

- 任务：SurgeCart 用户商城 W4-00（只读后端能力审查）
- 执行模型：DeepSeek-V4.1-Flash
- 分支：`feat/phase2-user-frontend`
- HEAD SHA：`1186162b2a07d5792d00f4ae73a588e2de189c6c`
- 起始工作区：clean
- 审查日期：2026-11-10
- 审查性质：**只读**。未修改 Go 代码、React 代码、SQL、配置、分支；未提交、未 push。
- 报告路径：`docs/reports/w4-backend-capability-audit/REPORT.md`（审查前不存在，本次新建）

---

## 0. 结论摘要（先看这段）

1. **订单域能力最完整**：创建、列表、详情、Mock 支付、取消、确认收货、超时自动取消、退款与退款查询全部有真实 HTTP API。但**列表没有状态筛选、没有分页**，状态筛选只能在浏览器本地做。
2. **支付不存在任何真实支付能力**。全仓库没有支付网关 SDK、没有 `payments` 表、没有支付回调、没有支付记录、没有支付状态查询接口。唯一的"支付"是 `POST /orders/:id/pay`，它只做一件事：把 `orders.status` 从 10 改成 20 并写 `paid_at`。**前端必须如实标注为"模拟支付"，不得出现微信/支付宝/银行卡等真实支付文案。**
3. **优惠券只有 2 个用户端 API**：`POST /coupons/:template_id/claim` 与 `GET /my/coupons`。**没有"可领取优惠券列表"接口**（模板列表 `GET /admin/coupons` 需要管理员权限），因此"领券中心"页面在后端补齐前无法实现。
4. **秒杀前台 API 严重不完整**：只有 `POST /flash-sales/:id/orders` 与 `GET /flash-sales/:id/orders/result`。**没有任何公开的活动列表/活动详情/秒杀商品展示接口**，前端拿不到 `activity_id` 与 `sku_id`，**秒杀页面无法作为 W4 前端交付项**。
5. **W3-02 报告的幂等缺陷在源码层被确认成立**，且比报告描述的更广：不只是"购物车清空导致 9004"，地址被删、SKU 下架、券过期都会在幂等查询之前中断重试，导致"订单已创建但客户端永远拿不回订单号"。
6. 另发现一处**契约级缺陷**：`POST /orders` 缺少必填字段时（`source`/`address_id`/`idempotency_key`），框架校验错误码 `51` 未映射到 `1001`，会返回 **HTTP 500 + code 51 + message「内部错误」**，而不是 400/1001。

---

## 1. 审查方法与证据分级

### 1.1 事实来源（全部来自当前 HEAD 的实际源码，不引用历史报告结论）

| 类别 | 实际路径 |
| --- | --- |
| 前台路由 | `internal/cmd/routes_frontend.go` |
| 后台路由 | `internal/cmd/routes_admin.go` |
| 路由装配 | `internal/cmd/cmd.go:166-182` |
| API 契约 | `api/order/v1/order.go`、`api/refund/v1/refund.go`、`api/coupon/v1/coupon.go`、`api/flashsale/v1/flash_sale.go`、`api/cart/v1/cart.go`、`api/product/v1/product.go` |
| 控制器 | `internal/controller/{order,refund,coupon,flashsale}/` |
| 业务逻辑 | `internal/logic/{order,refund,coupon,flashsale}/` |
| 服务接口 | `internal/service/{order,refund,coupon,flashsale}.go` |
| 错误码 | `internal/codes/codes.go` |
| 认证 | `internal/middleware/auth.go`、`internal/middleware/response.go` |
| 迁移 | `internal/migrations/sql/*.up.sql` |
| 后台任务 | `internal/worker/scanner.go` |
| 集成测试 | `test/e2e/*.go` |
| 路由锁定测试 | `internal/cmd/routes_test.go` |

### 1.2 状态定义

| 状态 | 含义 |
| --- | --- |
| IMPLEMENTED | 代码中真实存在该能力（有对外 HTTP API 或明确内部实现） |
| VERIFIED | 存在真实测试并对该行为做了断言 |
| PARTIALLY_VERIFIED | 只验证了部分行为或部分分支 |
| NOT_VERIFIED | 代码存在，但没有任何测试覆盖该能力 |
| NOT_IMPLEMENTED | 代码中不存在该能力 |

> 本报告对所有能力同时给出「代码状态」与「验证状态」两列，避免把"代码存在"等同于"功能可用"。

### 1.3 本次审查的执行限制（如实声明）

- e2e 测试需要真实 MySQL + Redis。本次审查环境 **3306/6379 均未监听**，因此**未执行任何测试**。所有 VERIFIED 判定基于测试源码中对断言的实际编写内容，而非本次运行结果。
- 未启动 HTTP 服务，未做真实请求探测。
- 涉及框架行为（参数校验错误码）的结论已下钻到 `go.mod` 声明的 GoFrame v2.10.3 模块源码，属静态确认，标注为"未运行时复验"。

---

## 2. 用户端 API 总览

前台路由注册见 `internal/cmd/routes_frontend.go:52-157`，并由 `internal/cmd/routes_test.go:43` 的 `TestRouteTable` 锁定路由表结构。

| # | Method | Path | 认证 | 能力域 | 代码 | 验证 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | POST | `/orders` | 用户 | 订单创建 | IMPLEMENTED | VERIFIED（不含购物车重试场景） |
| 2 | GET | `/orders` | 用户 | 订单列表 | IMPLEMENTED | NOT_VERIFIED |
| 3 | GET | `/orders/:id` | 用户 | 订单详情 | IMPLEMENTED | VERIFIED |
| 4 | POST | `/orders/:id/pay` | 用户 | Mock 支付 | IMPLEMENTED | VERIFIED |
| 5 | POST | `/orders/:id/cancel` | 用户 | 取消订单 | IMPLEMENTED | VERIFIED |
| 6 | POST | `/orders/:id/receive` | 用户 | 确认收货 | IMPLEMENTED | VERIFIED |
| 7 | POST | `/orders/:id/refund` | 用户 | 申请退款 | IMPLEMENTED | VERIFIED |
| 8 | GET | `/refunds` | 用户 | 退款列表 | IMPLEMENTED | VERIFIED |
| 9 | GET | `/refunds/:id` | 用户 | 退款详情 | IMPLEMENTED | VERIFIED |
| 10 | POST | `/refunds/:id/revoke` | 用户 | 撤销退款 | IMPLEMENTED | VERIFIED |
| 11 | POST | `/coupons/:template_id/claim` | 用户 | 领取优惠券 | IMPLEMENTED | VERIFIED |
| 12 | GET | `/my/coupons` | 用户 | 我的优惠券 | IMPLEMENTED | VERIFIED |
| 13 | POST | `/flash-sales/:id/orders` | 用户 | 秒杀下单 | IMPLEMENTED | VERIFIED |
| 14 | GET | `/flash-sales/:id/orders/result` | 用户 | 秒杀结果查询 | IMPLEMENTED | VERIFIED |

**不存在**的用户端接口（已逐条确认）：订单试算/预览、订单状态筛选参数、订单分页、支付确认、支付回调、支付记录、支付状态查询、优惠券模板列表、可用券查询、优惠券过期状态、秒杀活动列表/详情、秒杀订单列表、拼团、砍价。

统一响应封装（`internal/middleware/response.go:12-45`）：

```json
{ "code": 0, "message": "OK", "data": { } }
```

失败时 `data` 为 `null`，HTTP 状态按 `internal/codes/codes.go:270-275` 的码表映射，客户端必须按 `code` 判型，不依赖 `message`。

统一认证（`internal/middleware/auth.go:26-63`）：`Authorization: Bearer <access_token>`，JWT `type=user`，且每个请求都会校验 Redis 会话 + `users.status=1` + `users.auth_epoch` 一致性，任一不满足即 401（fail-closed）。

---

## 3. 订单管理能力清单

| 能力 | 代码状态 | 验证状态 | 说明 |
| --- | --- | --- | --- |
| 用户订单列表 | IMPLEMENTED | NOT_VERIFIED | `GET /orders`；仅本人；按 id 倒序；**无状态筛选、无分页**；每条订单单独查明细（N+1）；**不触发超时懒取消** |
| 订单详情 | IMPLEMENTED | VERIFIED | `GET /orders/:id`；不存在/非本人统一 9001；**待支付且已过期时会先懒取消再返回**（GET 带写副作用） |
| 订单状态筛选 | NOT_IMPLEMENTED | — | `ListReq` 只有 `g.Meta`（`api/order/v1/order.go:91-99`），`List(ctx, userID)` 签名不接收筛选条件；传 `?status=` 会被忽略且不报错 |
| 订单取消 | IMPLEMENTED | VERIFIED | `POST /orders/:id/cancel`；仅 10→60；同事务恢复库存 + 释放券（locked→available） |
| 确认收货 | IMPLEMENTED | VERIFIED | `POST /orders/:id/receive`；仅 30 可入；同一事务内 30→40→50，**响应永远返回 `completed`，`received` 对外不可观测** |
| 订单超时取消 | IMPLEMENTED | VERIFIED | 两条通道：①详情/支付时懒取消（SQL `NOW()` 判定）；②后台扫描器每 60s 扫 100 条（`internal/worker/scanner.go:50-68`） |
| 订单状态流转 | IMPLEMENTED | VERIFIED | 条件更新 + `RowsAffected` 闸门，非法迁移统一 9002 |
| 订单退款申请 | IMPLEMENTED | VERIFIED | 复用 `POST /orders/:id/refund`；**仅 `paid(20)` 且未发货可申请** |
| 退款进度查询 | IMPLEMENTED | VERIFIED | `GET /refunds?order_id=` 与 `GET /refunds/:id`；订单对象本身**不携带**退款状态，必须二次查询 |

### 3.1 关键订单业务规则（源码依据）

- **服务端定价**：`resolveLine` 重读 `skus.price`，校验 SKU `enabled` 且商品 `on_shelf`，不信任客户端金额、也不信任购物车 `price_snapshot`（`internal/logic/order/order.go:261-288`）。
- **金额三列快照**：`original_amount = Σ price×quantity`，`discount_amount` 由服务端按券计算，`total_amount = original - discount`（`order.go:158-176`）。
- **单事务**：写 orders + order_items + 扣库存 + 预占券 + 删除已购购物车条目，任一失败整体回滚（`order.go:292-384`）。
- **支付截止**：`expire_at = NOW() + order.pay_timeout`（默认 900 秒，`order.go:32-33, 946-963`），由 MySQL 计算，避免时区漂移。
- **购物车清理**：`source=cart` 时按 `user_id + sku_id IN (已下单项)` 删除，未勾选项保留（`order.go:364-375`）。
- **列表性能**：`List` 对每一行调用 `toOrder` → 单独 `SELECT order_items`，是明确的 N+1 查询（`order.go:405-420`）。

---

## 4. 支付能力清单

### 4.1 结论

> **本项目不存在真实支付能力。** 全仓库检索无支付网关、无支付 SDK、无支付回调路由、无 `payments` 表（37 张表中无支付相关表）。唯一"支付"接口是订单状态翻转。

| 能力 | 代码状态 | 验证状态 | 说明 |
| --- | --- | --- | --- |
| 用户支付接口 | IMPLEMENTED（Mock） | VERIFIED | `POST /orders/:id/pay`，无请求体、无金额、无支付方式、无渠道参数 |
| Mock 支付接口 | IMPLEMENTED | VERIFIED | 即上一条，项目内**唯一**支付入口；不是"额外提供 Mock"，而是"只有 Mock" |
| 真实第三方支付 | NOT_IMPLEMENTED | — | 无网关客户端、无签名验签、无商户号/证书配置项 |
| 支付状态查询 | NOT_IMPLEMENTED | — | 无独立接口；只能读 `orders.status`/`paid_at` |
| 支付结果确认 | NOT_IMPLEMENTED | — | 无"支付中/待确认"中间态，`pay` 直接返回 200 + `status=paid` |
| 支付超时处理 | IMPLEMENTED | VERIFIED | `expire_at` + 懒取消 + 扫描器；过期后 pay 返回 9002 |
| 支付回调 | NOT_IMPLEMENTED | — | 无 `/callback`、`/notify` 类路由；`grep -rn "callback"` 在非测试代码中无命中 |
| 支付记录查询 | NOT_IMPLEMENTED | — | 无支付流水表、无交易号、无收据 |
| 支付幂等 | IMPLEMENTED | VERIFIED | 状态幂等：`paid` 再支付直接返回既有订单，不重复改状态、不重复核销券 |
| 支付失败及重试 | NOT_IMPLEMENTED | — | 没有"支付失败"状态；未支付订单重试 = 再次调用同一个 `pay` |
| 订单支付状态流转 | IMPLEMENTED | VERIFIED | 仅 `10 → 20`（`order.go:475-499`） |

### 4.2 支付的具体实现（避免被误读为真实支付）

`internal/logic/order/order.go:447-516`：

1. 校验订单归属（非本人 → 9001）。
2. 若 `status=10` 且已过 `expire_at` → 懒取消 → 返回 9002。
3. 若 `status=20` → 幂等成功，原样返回。
4. 若 `status=10` → 单事务：`UPDATE orders SET status=20, paid_at=NOW() WHERE id=? AND status=10`；`RowsAffected=0` → 9002；若订单用了券 → `user_coupons` `locked→used`。
5. 其它状态（30/40/50/60/70）→ 9002。

**没有任何金额校验、没有外部调用、没有支付单据。** 请求体为空，因此前端"选择支付方式"只能是 UI 装饰。

---

## 5. 优惠券能力清单

| 能力 | 代码状态 | 验证状态 | 说明 |
| --- | --- | --- | --- |
| 用户可领取优惠券列表 | NOT_IMPLEMENTED | — | 无前台模板列表接口；`GET /admin/coupons` 需 `coupon:list` 管理员权限。用户端**拿不到 `template_id`** |
| 用户领取优惠券 | IMPLEMENTED | VERIFIED | `POST /coupons/:template_id/claim`；原子闸门 `issued_quantity+1` + `uk_user_template` 唯一约束 |
| 用户优惠券列表 | IMPLEMENTED | VERIFIED | `GET /my/coupons`；LEFT JOIN 模板带出 `name/threshold/discount/use_start/use_end`；**返回全部状态，不过滤** |
| 可用优惠券查询 | NOT_IMPLEMENTED | — | 没有"传金额返回可用券"的接口；前端只能用 `threshold_amount` + 时间窗口本地估算 |
| 订单优惠券选择 | IMPLEMENTED | VERIFIED | `POST /orders` 的 `coupon_id`（0/缺省=不用券），一笔订单最多一张 |
| 优惠金额计算 | IMPLEMENTED | VERIFIED | 服务端 `min(discount_amount, original_amount)`（`internal/logic/coupon/coupon.go:309-313`） |
| 优惠券使用与核销 | IMPLEMENTED | VERIFIED | 下单 `available→locked`（同事务），支付 `locked→used`，取消/超时 `locked→available` |
| 优惠券过期处理 | NOT_IMPLEMENTED | — | **无 `expired` 状态、无过期任务**；`user_coupons.status` 只有 10/20/30；过期仅在"下单用券时"由 SQL `NOW()` 拒绝（19007）。「我的券」里过期券仍显示 `available` |

### 5.1 前端必须知道的券规则

- **券码只能从 `template_id` 领**：没有列表接口意味着前端无法发现模板。当前唯一可行路径是由运营在活动页/文章/轮播图里硬编码深链。
- **`status` 只有三态**：`available` / `locked` / `used`。`locked` 表示"已被某笔待支付订单占用"，该券此时不能用（再用会被 19007 拒绝）。
- **退款不返券**：退款成功后券保持 `used`（`test/e2e/coupon_test.go:794-849` 断言）。
- **取消/超时返券**：`cancelInTx` 调 `ReleaseInTx`，`locked→available` 并清空 `order_id/locked_at`（`order.go:756-759`）。
- **同一用户同一模板只能领一次**（`uk_user_template`），重复领 → 19004。
- `GetDiscount` 刻意**不校验券的可用状态**，把状态判定交给下单事务内的 `LockInTx` 条件更新，这是为了让幂等重试时"已被占用的券"不被预检误拒（`coupon.go:274-277` 注释）。

---

## 6. 秒杀与营销能力清单

| 能力 | 代码状态 | 验证状态 | 说明 |
| --- | --- | --- | --- |
| 秒杀活动列表 | NOT_IMPLEMENTED | — | 无 `GET /flash-sales`；后台也只有创建/更新，**连后台都没有活动列表接口** |
| 秒杀活动详情 | NOT_IMPLEMENTED | — | 无 `GET /flash-sales/:id` |
| 秒杀商品展示 | NOT_IMPLEMENTED | — | 无任何接口把 `activity × sku` 的秒杀价/剩余库存/商品信息暴露给前台 |
| 秒杀资格校验 | PARTIALLY | — | 无预校验接口；时间窗、一人一单、库存、SKU 可用性都在下单时隐式校验 |
| 用户秒杀请求 | IMPLEMENTED | VERIFIED | `POST /flash-sales/:id/orders`；V3 异步：Redis Lua 闸门 → 持久入队 → 后台消费落单 |
| 秒杀订单查询 | PARTIALLY | VERIFIED | 只能 `GET /flash-sales/:id/orders/result?idempotency_key=...` 按单键查询；**无列表**，键丢失即无法找回 |
| 秒杀状态展示 | PARTIALLY | VERIFIED | 返回 `queued/success/failed/dead` + 失败码；无活动级倒计时/剩余量 |
| 拼团 / 砍价 / 其他营销 | NOT_IMPLEMENTED | — | 全仓库无 `拼团/砍价/groupbuy/bargain/promotion` 相关代码 |

### 6.1 秒杀不能被当作"订单"对待

- 秒杀订单写的是独立表 `flash_sale_orders`，**不写入 `orders` 表**，因此**不会出现在 `GET /orders` 列表里**。
- 秒杀**不扣减**普通 `inventories`，只扣 `flash_sale_activity_skus.sold`（迁移 `20261001000011_flash_sale.up.sql` 头注释明确"V1 抢购事务内不联动普通 inventories"）。
- 秒杀订单"下单即成交"：**无支付、无取消、无退款、无发货、无物流**。行存在即成功。
- 一人一单的粒度是 `(activity_id, sku_id, user_id)`，同一活动买不同 SKU 是允许的。
- 前台必须依赖 Redis：Redis 不可用 → fail-closed 503（`internal/logic/flashsale/flashsale.go:296-313`，`test/e2e/flashsale_v2_test.go:253`）。
- 幂等键全局唯一 `(user_id, idempotency_key)`，跨活动复用同一键且内容不同 → 12005。

---

## 7. 用户端 API 契约（逐接口）

> 所有金额均为**整数分**；所有时间为 `gtime.Time`，JSON 输出为 `"YYYY-MM-DD HH:mm:ss"` 或 `null`；`data` 为对象时字段平铺（Go 嵌入结构体，无 `json` 标签）。

### 7.1 `POST /orders` — 创建订单

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /orders` |
| 认证 | 需要，`Authorization: Bearer <user access_token>` |
| 请求体 | `source` (string, required, `cart`\|`direct`)<br>`address_id` (int64, required, 必须属于本人)<br>`idempotency_key` (string, required, ≤64 字符，建议 UUID 去连字符 32 位)<br>`sku_id` (int64, `direct` 必填)<br>`quantity` (int64, `direct` 必填，1~999)<br>`coupon_id` (int64, 可选，0/缺省=不用券) |
| 响应 | `data` = 完整 Order 对象（字段见 7.15），含 `items[]` |
| 主要错误码 | `1001` 参数错误、`7001` 地址不存在/非本人、`5001` SKU 不存在、`4001` 商品不存在、`9003` SKU 禁用/商品下架、`9004` 购物车无勾选项、`9005` 数量非法、`9006` 幂等键冲突、`19003` 券不存在/非本人、`19007` 券不可用（过期/未达门槛/已被占用）、`6001` 库存不足、`1000` 内部错误 |
| 契约缺陷 | 缺少 `source`/`address_id`/`idempotency_key` 时，框架校验码 `51` 未映射 → **HTTP 500 + code 51 + message「内部错误」**（静态确认，未运行时复验，详见 §10.4） |
| 源码 | `api/order/v1/order.go:76-89`；`internal/controller/order/order.go:22-28`；`internal/logic/order/order.go:132-217, 292-384` |
| 测试 | `test/e2e/order_test.go:211,269,318,336,378,424,681,727`；`test/e2e/coupon_test.go:427,473,505,535,562,598,653` |
| 关键规则 | 服务端定价与快照；单事务写单+扣库存+预占券+清购物车；`source=cart` 下单**全部已勾选**条目并忽略 `sku_id/quantity`；`expire_at = NOW()+900s` |
| React 接入 | **有条件可直接接入**。创建本身可靠，但**重试恢复不可靠**（见 §10），前端必须把"提交超时"当作"结果未知"，不得自动重试后直接提示失败 |

### 7.2 `GET /orders` — 本人订单列表

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /orders` |
| 认证 | 需要（用户） |
| 请求参数 | **无**。`ListReq` 只有 `g.Meta`，传 `status`/`page`/`size` 会被静默忽略 |
| 响应 | `data.items: Order[]`（每条含 `items[]`），按 `id DESC`，无 `total` |
| 主要错误码 | `1002` 未授权、`1000` 内部错误 |
| 源码 | `api/order/v1/order.go:91-99`；`internal/controller/order/order.go:31-37`；`internal/logic/order/order.go:405-420` |
| 测试 | **无**（`test/e2e` 中不存在任何请求 `GET /orders` 的调用） |
| 关键规则 | 仅本人；无分页；无筛选；逐单查明细（N+1）；**不做超时懒取消**，列表可能长期显示过期的 `pending_payment` |
| React 接入 | **有条件**。订单量小的场景可全量拉取 + 本地筛选；数据量增长后必须由后端补分页/筛选，前端不能假装有分页 |

### 7.3 `GET /orders/:id` — 本人订单详情

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /orders/:id` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，int64，required） |
| 响应 | `data` = Order（含 `items[]`） |
| 主要错误码 | `9001` 订单不存在或非本人（404，防枚举）、`1002`、`1000` |
| 源码 | `api/order/v1/order.go:101-110`；`internal/logic/order/order.go:422-444` |
| 测试 | `test/e2e/order_test.go:336,595,727` |
| 关键规则 | **GET 带写副作用**：待支付且 `expire_at < NOW()` 时先执行取消（恢复库存、释放券）再返回，返回状态为 `cancelled` + `cancel_reason=2` |
| React 接入 | **可直接接入**。注意：该 GET 会改变服务端状态，前端不要把它当纯缓存读取；React Query 的 `refetchOnWindowFocus` 可能"顺手"取消用户订单（对用户是正确行为，但需在 UI 上解释） |

### 7.4 `POST /orders/:id/pay` — Mock 支付

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /orders/:id/pay` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required）；**无请求体** |
| 响应 | `data` = Order（`status=paid`，`paid_at` 已填） |
| 主要错误码 | `9001`、`9002` 非法状态迁移（已取消/已过期/已发货/已退款）、`1002`、`1000` |
| 源码 | `api/order/v1/order.go:112-121`；`internal/logic/order/order.go:447-516` |
| 测试 | `test/e2e/order_test.go:458,562`；`test/e2e/coupon_test.go:702` |
| 关键规则 | 状态幂等（`paid` 再调返回成功）；过期先懒取消再 9002；同事务核销券 `locked→used`；**无金额/渠道/交易号** |
| React 接入 | **可直接接入，但必须标注"模拟支付"**。不得展示支付渠道图标、不得声称已收银 |

### 7.5 `POST /orders/:id/cancel` — 取消订单

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /orders/:id/cancel` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required）；无请求体 |
| 响应 | `data` = Order（`status=cancelled`，`cancel_reason=1`，`cancelled_at`） |
| 主要错误码 | `9001`、`9002`（仅待支付可取消）、`1002`、`1000` |
| 源码 | `api/order/v1/order.go:123-132`；`internal/logic/order/order.go:518-549, 729-761` |
| 测试 | `test/e2e/order_test.go:458,509`；`test/e2e/coupon_test.go:751` |
| 关键规则 | 同事务恢复库存 + 释放券；并发重复取消只有一个成功（条件更新 + `RowsAffected`） |
| React 接入 | **可直接接入**。需处理"用户点取消时订单恰好过期被自动取消 → 返回 9002"的情况，应以 9002 后重新拉详情为准 |

### 7.6 `POST /orders/:id/receive` — 确认收货

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /orders/:id/receive` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required）；无请求体 |
| 响应 | `data` = Order（`status=completed`，`completed_at`） |
| 主要错误码 | `9001`、`9002`（仅 `shipped` 可确认）、`1002`、`1000` |
| 源码 | `api/order/v1/order.go:134-143`；`internal/logic/order/order.go:551-595` |
| 测试 | `test/e2e/order_test.go:458` |
| 关键规则 | 同一事务内 30→40→50，**`received(40)` 永远不会被前端看到** |
| React 接入 | **可直接接入**。状态映射表里不需要 `received` 的可视化分支（保留兼容即可） |

### 7.7 `POST /orders/:id/refund` — 申请退款

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /orders/:id/refund` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required）；body：`reason`（string，必填，trim 后 1~255 字符） |
| 响应 | `data` = Refund（`status=applying`） |
| 主要错误码 | `1001` 参数错误、`18001` 不存在/非本人、`18003` 订单非"已支付未发货"、`18004` 已存在有效退款申请、`1002`、`1000` |
| 源码 | `api/refund/v1/refund.go:50-60`；`internal/controller/refund/refund.go`；`internal/logic/refund/refund.go:123-196` |
| 测试 | `test/e2e/refund_test.go:156,177,199,234,317,364` |
| 关键规则 | 事务内 `SELECT ... FOR UPDATE` 锁订单；金额 = `orders.total_amount`（服务端派生）；`uk_active` 保证同一订单至多一条有效申请；与发货互斥 |
| React 接入 | **可直接接入**。注意：**只有 `paid` 状态可申请**，`shipped/received/completed` 一律 18003，前端不要给已发货订单显示"申请退款"按钮 |

### 7.8 `GET /refunds` — 本人退款列表

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /refunds` |
| 认证 | 需要（用户） |
| 请求参数 | `order_id`（query，int64，可选，≤0 视为不过滤） |
| 响应 | `data.items: Refund[]`，按 `id DESC`，无分页 |
| 主要错误码 | `1002`、`1000` |
| 源码 | `api/refund/v1/refund.go:84-93`；`internal/logic/refund/refund.go:272-287` |
| 测试 | `test/e2e/refund_test.go:507`（`refundList` 调用 `/refunds`） |
| 关键规则 | 仅本人；可按订单过滤；无状态筛选、无分页 |
| React 接入 | **可直接接入**；订单详情页用 `?order_id=` 拉该单退款进度 |

### 7.9 `GET /refunds/:id` — 本人退款详情

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /refunds/:id` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required） |
| 响应 | `data` = Refund（含 `review_reason`/`execute_reason`/时间戳） |
| 主要错误码 | `18001`、`1001`（id≤0）、`1002`、`1000` |
| 源码 | `api/refund/v1/refund.go:73-82`；`internal/logic/refund/refund.go:257-270` |
| 测试 | `test/e2e/refund_test.go:507` |
| 关键规则 | 不存在/非本人统一 18001（防枚举）；不返回审计明细（审计仅后台可见） |
| React 接入 | **可直接接入** |

### 7.10 `POST /refunds/:id/revoke` — 撤销退款申请

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /refunds/:id/revoke` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，required）；无请求体 |
| 响应 | `data` = Refund（`status=revoked`） |
| 主要错误码 | `18001`、`18002`（仅 `applying` 可撤销）、`1002`、`1000` |
| 源码 | `api/refund/v1/refund.go:62-71`；`internal/logic/refund/refund.go:198-255` |
| 测试 | `test/e2e/refund_test.go:483` |
| 关键规则 | 撤销**不恢复库存、不改订单状态**，用户可再次申请 |
| React 接入 | **可直接接入**。撤销后需重新拉订单详情（订单仍是 `paid`） |

### 7.11 `POST /coupons/:template_id/claim` — 领取优惠券

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /coupons/:template_id/claim` |
| 认证 | 需要（用户） |
| 请求参数 | `template_id`（path，int64，required）；无请求体 |
| 响应 | `data` = UserCoupon（`status=available`，含模板快照字段） |
| 主要错误码 | `1001`、`19001` 模板不存在、`19004` 已领取、`19005` 未开始/已结束/已停止、`19006` 已领完、`1002`、`1000` |
| 源码 | `api/coupon/v1/coupon.go:95-104`；`internal/logic/coupon/coupon.go:179-250` |
| 测试 | `test/e2e/coupon_test.go:224,263,314` |
| 关键规则 | 事务内原子 `issued_quantity+1`（含状态/时间窗/总量判定）+ 插入券；`uk_user_template` 兜底；一人一模板一张 |
| React 接入 | **能力可用，但入口缺失**：没有模板列表接口，前端无法展示"可领的券"。只能在已知 `template_id` 的深链场景使用 |

### 7.12 `GET /my/coupons` — 我的优惠券

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /my/coupons` |
| 认证 | 需要（用户） |
| 请求参数 | 无 |
| 响应 | `data.items: UserCoupon[]`，按 `id DESC`；字段见 7.16 |
| 主要错误码 | `1002`、`1000` |
| 源码 | `api/coupon/v1/coupon.go:106-114`；`internal/logic/coupon/coupon.go:252-272` |
| 测试 | `test/e2e/coupon_test.go:224,383` |
| 关键规则 | 返回**全部状态**（available/locked/used）与全部券，不排序、不筛选、不标记过期 |
| React 接入 | **可直接接入**；过期判定必须由前端用 `use_end_time < now` 自行计算，且不得声称"后端判定" |

### 7.13 `POST /flash-sales/:id/orders` — 秒杀下单（异步受理）

| 项 | 内容 |
| --- | --- |
| Method / Path | `POST /flash-sales/:id/orders` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，活动 id，required）；body：`sku_id`（int64，必须是该活动绑定的 SKU）、`idempotency_key`（string，required，≤64） |
| 响应 | `data` = `{status:"queued", idempotency_key, activity_id, sku_id}`；**不同步返回订单** |
| 主要错误码 | `1001`、`12001` 活动不存在/已下架、`12002` 不在时间窗、`12003` 库存不足、`12004` 已购买、`12005` 幂等键冲突、`12006` SKU 不可购买、`12007` 活动参数非法、`12009` 用户/活动限流（429）、`12010` 排队已满（429）、`1005` Redis 不可用（503）、`1002`、`1000` |
| 源码 | `api/flashsale/v1/flash_sale.go:103-117`；`internal/logic/flashsale/flashsale.go:266-342`；入队 `internal/logic/flashsale/request.go:61-92` |
| 测试 | `test/e2e/flashsale_v3_test.go:60,110,139`；`test/e2e/flashsale_test.go:432,477`；`test/e2e/flashsale_v5_test.go:19` |
| 关键规则 | 限流 → 熔断 → Redis Lua 闸门（库存/已购/幂等）→ 持久入队；Redis 故障 fail-closed 503；**无地址、无支付、无取消** |
| React 接入 | **不可直接接入为完整功能**：缺少活动数据来源，前端无法构造 `activity_id/sku_id`，也无法展示秒杀价与剩余库存 |

### 7.14 `GET /flash-sales/:id/orders/result` — 秒杀结果查询

| 项 | 内容 |
| --- | --- |
| Method / Path | `GET /flash-sales/:id/orders/result` |
| 认证 | 需要（用户） |
| 请求参数 | `id`（path，活动 id）；`idempotency_key`（query，string，required） |
| 响应 | `data` = `{status, order?, fail_code?}`；`status ∈ queued/success/failed/dead`；`order` 为 FlashOrder（仅 success） |
| 主要错误码 | `1001`（键为空）、`1004` 请求不存在/非本人/活动不匹配（404）、`1002`、`1000` |
| 源码 | `api/flashsale/v1/flash_sale.go:119-131`；`internal/logic/flashsale/request.go:117-150` |
| 测试 | `test/e2e/flashsale_v3_test.go:60`（含归属隔离与 404 断言） |
| 关键规则 | 按 `(user_id, activity_id, idempotency_key)` 定位；**必须同时持有活动 id 与幂等键**；`success` 才返回订单 |
| React 接入 | **可接入作为轮询端**，但必须先把 `activity_id + idempotency_key` 持久化到本地（localStorage），否则刷新后永远查不回结果 |

### 7.15 Order 对象结构（`api/order/v1/order.go:32-72`）

```json
{
  "id": 1,
  "order_no": "1762...",
  "user_id": 1001,
  "status": "pending_payment",
  "total_amount": 8000,
  "original_amount": 9000,
  "discount_amount": 1000,
  "user_coupon_id": 12,
  "idempotency_key": "....",
  "recipient_name": "张三", "phone": "138...",
  "province": "广东省", "city": "深圳市", "district": "南山区", "detail": "...",
  "address_id": 3,
  "expire_at": "2026-11-10 16:05:00",
  "cancel_reason": null,
  "paid_at": null, "shipped_at": null, "received_at": null,
  "completed_at": null, "cancelled_at": null, "refunded_at": null,
  "created_at": "2026-11-10 15:50:00",
  "updated_at": "2026-11-10 15:50:00",
  "items": [
    { "id": 1, "sku_id": 7, "product_id": 3, "sku_name": "红色/L",
      "product_name": "纯棉T恤", "product_main_image": "https://...",
      "price": 4000, "quantity": 2 }
  ]
}
```

`status` 枚举：`pending_payment` / `paid` / `shipped` / `received` / `completed` / `cancelled` / `refunded`。
`cancel_reason`：`1`=用户取消、`2`=超时取消、`null`=未取消。

### 7.16 UserCoupon 对象结构（`api/coupon/v1/coupon.go:41-54`）

```json
{
  "id": 12, "template_id": 5,
  "status": "available",
  "order_id": null,
  "received_at": "2026-11-10 10:00:00",
  "locked_at": null, "used_at": null,
  "name": "满100减10",
  "threshold_amount": 10000, "discount_amount": 1000,
  "use_start_time": "2026-11-01 00:00:00",
  "use_end_time": "2026-11-30 23:59:59"
}
```

### 7.17 Refund 对象结构（`api/refund/v1/refund.go:33-48`）

```json
{
  "id": 3, "order_id": 1, "user_id": 1001,
  "status": "applying",
  "amount": 8000,
  "reason": "不想要了",
  "review_reason": "", "reviewed_by": null, "reviewed_at": null,
  "execute_reason": "", "executed_by": null, "executed_at": null,
  "created_at": "...", "updated_at": "..."
}
```

`status` 枚举：`applying` / `pending_refund` / `refund_failed` / `review_rejected` / `revoked` / `refund_succeeded` / `closed`。

### 7.18 FlashOrder 对象结构（`api/flashsale/v1/flash_sale.go:48-63`）

```json
{
  "id": 9, "order_no": "FS1762...", "user_id": 1001,
  "activity_id": 2, "sku_id": 7, "product_id": 3,
  "sku_name": "红色/L", "product_name": "纯棉T恤",
  "product_main_image": "https://...",
  "flash_price": 1000, "quantity": 1,
  "idempotency_key": "...",
  "created_at": "...", "updated_at": "..."
}
```

---

## 8. 关键业务状态机

### 8.1 订单状态机（`internal/logic/order/order.go:36-44, 896-914`）

```
                    ┌─────────────── user cancel (9002 for others) ──────────────┐
                    │                                                            ▼
pending_payment(10) ├── timeout(lazy/scan) ────────────────────────────► cancelled(60)
        │           └────────────────────────────────────────────────────────────┘
        │ POST /orders/:id/pay  (Mock)
        ▼
     paid(20) ── POST /admin/orders/:id/ship ──► shipped(30)
        │                                          │
        │                                          │ POST /orders/:id/receive
        │                                          ▼
        │                                  received(40) ──同事务──► completed(50)
        │
        └── refund execute success ──► refunded(70)
```

| 迁移 | 触发方 | 失败码 |
| --- | --- | --- |
| 10 → 20 | 用户 Mock 支付 | 9002 |
| 10 → 60 | 用户取消 / 超时（懒取消或扫描器） | 0 行则静默不生效 |
| 20 → 30 | 管理员发货 | 9002 / 18005（存在有效退款申请） |
| 30 → 40 → 50 | 用户确认收货（同事务） | 9002 |
| 20 → 70 | 管理员执行退款成功 | 9002 / 18002 |

**不可发生的迁移**：60/70 为终态，不可再变更；`paid` 不能直接 `completed`；`shipped` 不能退款（18003）。

### 8.2 退款状态机（`internal/logic/refund/refund.go:29-41`）

```
applying(10) ──approve──► pending_refund(20) ──execute success──► refund_succeeded(60)
     │                          │
     │                          └──execute failure──► refund_failed(30) ──close──► closed(70)
     │                                                      │
     │                                                      └──execute success──► refund_succeeded(60)
     ├──reject──► review_rejected(40)
     └──revoke──► revoked(50)
```

有效状态集合 `{10, 20, 30}` 会阻塞发货（`HasActiveRequestInTx`），并阻止重复申请。

### 8.3 优惠券状态机（`internal/logic/coupon/coupon.go:22-27`）

```
available(10) ──下单预占（LockInTx）──► locked(20) ──支付核销（MarkUsedInTx）──► used(30)
      ▲                                     │
      └──取消/超时释放（ReleaseInTx）────────┘
```

**没有 `expired` 终态**。`used(30)` 是终态，退款不回退。

### 8.4 秒杀请求状态机（`internal/logic/flashsale/request.go:19-25`）

```
queued(0) ──消费成功──► success(1)   （与 flash_sale_orders 插入同事务）
    │
    ├──业务失败（库存不足/时间窗结束/已购）──► failed(2)   （补偿 Redis）
    └──技术失败重试超限──► dead(3) ──管理员 repair──► queued(0)
```

秒杀订单本身**无状态机**：`flash_sale_orders` 行存在即成功订单。

---

## 9. 支付与订单的数据关系

### 9.1 实际数据模型

```
orders (1) ────< order_items (N)          [FK, ON DELETE CASCADE]
orders (1) ────< refund_requests (N)      [软引用 order_id，uk_active 保证 ≤1 条有效]
orders.user_coupon_id ──► user_coupons.id [软引用，下单快照，不随释放改变]
user_coupons.order_id ──► orders.id       [仅 locked 期间有值]
flash_sale_orders                          [独立表，与 orders 无任何关联]
```

**不存在**：`payments` / `payment_transactions` / `payment_callbacks` / `order_payments` 表（37 张表已逐条核对）。

### 9.2 结论

- 订单与"支付"的关系只有 3 个字段：`orders.status`、`orders.paid_at`、`orders.expire_at`。
- 没有支付流水，因此**无法做"支付记录""交易号""退款原路返回""对账"**。
- 退款金额直接取 `orders.total_amount`，与"实际支付金额"在模型上是同一个值（因为没有真实支付，这个等价关系当前成立；接入真实支付后会立刻不成立）。
- 秒杀订单完全在另一条数据链上：不占普通库存、不进订单列表、不参与支付/退款。

---

## 10. 幂等与失败恢复分析（W3-02 重点复核）

### 10.1 结论

> **W3-02 报告的缺陷成立，且影响面比报告描述的更大。**
> 幂等记录**没有预查询**，唯一的幂等闸门是 `orders` 表的 `uk_user_idempotency` 唯一约束；而它的触发点在"解析地址 → 解析行项目 → 算价 → 查券折扣"**之后**。任何前置条件在重试时不再成立，都会在触达幂等闸门之前直接返回业务错误。

### 10.2 逐条回答 6 个问题

**① 幂等记录在哪个阶段查询？**

只在**订单 INSERT 命中唯一约束之后**查询：

- `internal/logic/order/order.go:183-198`：`insertOrder` 返回 `duplicateKeyErr` 且键名含 `uk_user_idempotency` 时，才调用 `handleIdempotency`。
- `internal/logic/order/order.go:386-403`：`handleIdempotency` 用 `findByIdempotencyKey(user_id, key)` 读回既有订单，比较 `request_hash`，一致返回订单，不一致返回 9006。

`Create` 开头**没有任何** `SELECT ... WHERE user_id=? AND idempotency_key=?` 的预查询。

**② 购物车校验和幂等查询的先后顺序？**

顺序是：**地址 → 行项目（购物车）→ 算价 → 券折扣 → request_hash → INSERT（幂等闸门）→ 冲突后回查**。

| 步骤 | 代码位置 | 可能提前失败 |
| --- | --- | --- |
| 参数校验 | `order.go:133-146` | 1001 |
| 地址归属 | `order.go:148-151` → `Address().Detail` | 7001 |
| 行项目（`source=cart` 读购物车） | `order.go:153-156` → `buildLines` `order.go:234-253` | **9004（购物车无勾选）** |
| 行项目（`resolveLine` 重读 SKU/商品） | `order.go:261-288` | 5001 / 4001 / 9003 |
| 券折扣 | `order.go:167-175` → `GetDiscount` | 19003 / 19007 |
| 指纹 | `order.go:178` | — |
| 幂等闸门 | `order.go:183-198` | 9006 / 返回既有订单 |

**③ `request_hash` 如何计算？**

`internal/logic/order/order.go:916-937`：

```
sha256hex( source + "|" + address_id + "|" + coupon_id(缺省 0) + ("|" + sku_id + ":" + qty)* )
```

行项目**按 `sku_id` 升序**排序后拼接，保证顺序无关。注意：**`lines` 来自"当前"购物车/SKU 解析结果**，不是客户端原始输入。

**④ 成功订单如何与幂等记录关联？**

不通过独立幂等表，而是直接把 `idempotency_key` 与 `request_hash` 写进 `orders` 行（`order.go:313-314`），由 `UNIQUE KEY uk_user_idempotency (user_id, idempotency_key)` 保证唯一（迁移 `20261001000007_orders.up.sql`）。

**⑤ 相同幂等键重试时实际经过的调用路径**

`source=cart` 且首次已成功（购物车已清空）：

```
Create
 → 参数校验（通过）
 → Address().Detail（通过，假设地址未删）
 → buildLines(cart)
    → Cart().List(userID) 返回 0 条已勾选
    → len(lines)==0
    → return 9004  ❌ 在此终止
 （永远到不了 insertOrder，也就永远到不了 handleIdempotency）
```

前端拿到 `400/9004`，**无法区分"从未下单"与"已下单但购物车被清空"**。

**⑥ 其他可能导致重复订单或订单结果无法恢复的问题**

| 编号 | 问题 | 后果 | 依据 |
| --- | --- | --- | --- |
| R1 | 购物车已清空 → 9004 | 订单已创建，客户端拿不回订单号 | `order.go:153, 250-252` |
| R2 | 地址在重试前被删除/改归属 | 7001，同上 | `order.go:148-151` |
| R3 | SKU 被禁用 / 商品下架 | 9003，同上 | `order.go:269-278` |
| R4 | 券在使用期外 / 券被删 | 19007 / 19003，同上 | `order.go:167-175` |
| R5 | 购物车内容变化（增删改勾选） | `request_hash` 变化 → 9006，用户被提示"内容已变化"，但原订单其实已存在 | `order.go:178, 395-397` |
| R6 | 事务提交成功但随后 `loadOwnedOrder` 失败 | 返回 1000，订单已落库；重试又被 R1 拦截 | `order.go:203-209` |
| R7 | `source=direct` 重试 | 只要地址/SKU/券仍有效，可正常回查既有订单（**direct 路径相对安全**，现有幂等测试也只覆盖了 direct） | `test/e2e/order_test.go:378`、`test/e2e/coupon_test.go:653` |
| R8 | `GET /orders` 不懒取消 | 列表中长期出现已过期但仍显示 `pending_payment` 的订单，用户点"支付"才收到 9002 | `order.go:405-420` vs `423-444` |
| R9 | 前端无法通过"再下一单"自救 | 同键必冲突（9006/9004），换键会真的创建第二单并二次扣库存 | 唯一约束 + 无预查询 |
| R10 | 秒杀幂等键全局唯一 `(user_id, key)` | 同一键用于不同活动 → 12005；前端必须"一活动一 SKU 一键" | 迁移 `20261001000015` + `flashsale.go:371-390` |

**是否存在真正的重复订单？**
在数据库层面**不会**：`uk_user_idempotency` 是硬约束，同一 `(user_id, key)` 不可能写入两行，且库存扣减在同一事务内位于订单 INSERT 之后，重复插入会在扣库存之前被唯一键打断。
风险集中在**结果不可恢复**（R1-R6），而不是重复下单。

### 10.3 对前端的直接含义（W4 必须遵守）

1. 下单超时/网络断开后，**不得**直接提示"下单失败，请重试"。
2. 正确的恢复顺序是：`GET /orders` 拉列表 → 用 `order_no`/时间/金额比对，或按 `idempotency_key` 在返回的 `Order` 对象里比对（列表返回该字段）。
3. 因为列表没有筛选，前端需要在前端侧做匹配；这也是"订单列表必须可全量拉取"的现实理由。
4. **不要用 `POST /orders` 做试算**（会真实建单）。

### 10.4 额外发现的契约缺陷：必填字段校验返回 500

- `CreateReq` 的 `source` / `address_id` / `idempotency_key` 带 `v:"required"`（`api/order/v1/order.go:78-80`）。
- GoFrame v2.10.3 在 `r.Parse` 阶段执行 `gvalid`，失败错误码为 `gcode.CodeValidationFailed = 51`（`ghttp_request_param.go:111`、`gcode.go:32`）。
- 本仓库 `codes.FromError` 直接返回 `Code(51)`（`internal/codes/codes.go:297-306`），`codeTable` 无 51 条目 → `HTTPStatus` 回落 500、`Message` 回落「内部错误」（`codes.go:269-283`）。
- 结论：缺字段时返回 **HTTP 500 + code 51**，而非 400/1001。前端若按 `code` 分支处理会落入"未知错误"。
- 该行为**没有任何测试覆盖**，本次未启动服务复验（静态确认）。
- 对照：购物车接口刻意不写 `v` 标签、由 service 显式返回 1001（`api/cart/v1/cart.go:46` 注释），说明项目内存在两种风格。

---

## 11. 后端缺失能力（W4 前端需要但后端没有）

| # | 缺失能力 | 影响的前端页面 | 严重度 |
| --- | --- | --- | --- |
| M1 | 订单列表状态筛选 | 订单中心"待付款/待发货/待收货"Tab | 中（可本地过滤绕过） |
| M2 | 订单列表分页 | 订单中心（历史订单多时） | 中 |
| M3 | 订单试算 / 金额预览 | 结算页权威金额 | 中（W3-02 已用"预估"绕过） |
| M4 | 订单上的退款状态 | 订单卡片直接显示"退款中" | 低（二次查询可绕过） |
| M5 | 真实支付（网关/回调/流水） | 收银台 | **高**（只能做 Mock） |
| M6 | 支付结果确认 / 支付状态查询 | 支付中轮询 | 低（Mock 同步返回） |
| M7 | 可领取优惠券列表（模板列表） | 领券中心 | **高**（无法实现） |
| M8 | 可用券查询（按金额） | 结算页"可用券"选择 | **高**（只能本地估算） |
| M9 | 优惠券过期状态 | 券包"已过期"分组 | 中（前端算时间） |
| M10 | 秒杀活动列表 | 秒杀频道 | **高** |
| M11 | 秒杀活动详情（含商品/价格/剩余量/倒计时） | 秒杀详情页 | **高** |
| M12 | 秒杀订单列表 | 我的秒杀记录 | 高 |
| M13 | 拼团 / 砍价 / 满减活动 | 营销页 | —（无需求来源） |
| M14 | 物流/快递单号/轨迹 | 订单物流卡片 | 中（字段不存在） |
| M15 | 退货退款（shipped 之后） | 售后 | 中（当前仅"仅退款"） |
| M16 | 发票 | 订单详情 | 低 |

---

## 12. 前端可以直接接入的能力

| 能力 | 接口 | 前置条件 / 注意事项 |
| --- | --- | --- |
| 订单创建 | `POST /orders` | 必须复用幂等键；超时按"结果未知"处理；不要用做试算 |
| 订单列表 | `GET /orders` | 状态筛选/分页在前端做；数据量假设需显式写进任务文档 |
| 订单详情 | `GET /orders/:id` | 会触发懒取消；9001 需映射为"订单不存在或无权查看" |
| Mock 支付 | `POST /orders/:id/pay` | UI 必须标注"模拟支付/演示环境" |
| 取消订单 | `POST /orders/:id/cancel` | 9002 后重新拉详情 |
| 确认收货 | `POST /orders/:id/receive` | 返回直接是 `completed` |
| 申请退款 | `POST /orders/:id/refund` | 仅 `paid` 且未发货；`reason` 必填 |
| 退款列表 | `GET /refunds?order_id=` | 用于订单详情页展示退款进度 |
| 退款详情 | `GET /refunds/:id` | — |
| 撤销退款 | `POST /refunds/:id/revoke` | 仅 `applying` |
| 我的优惠券 | `GET /my/coupons` | 过期由前端按 `use_end_time` 判定 |
| 结算选券 | `POST /orders` 的 `coupon_id` | 金额以服务端返回为准；前端只做预估 |
| 领券（已知 template_id） | `POST /coupons/:template_id/claim` | 无列表接口，只能深链 |
| 秒杀下单 + 结果轮询 | `POST /flash-sales/:id/orders` + `GET .../result` | 仅在后端补齐活动查询、或运营提供固定入口后可用 |

---

## 13. 前端不能假装实现的能力

> 以下能力如果在前端"做出来"，就是虚假实现，必须在任务文档与 UI 中明确排除。

1. **真实支付**：不得出现微信支付 / 支付宝 / 银联 / 信用卡 / 扫码付 / 收银台跳转；不得显示"支付成功，已扣款"。
2. **服务端订单状态筛选**：不能声称"按状态查询订单"；只能说明"本地筛选当前已加载订单"。
3. **订单分页**：不能显示页码/总数；后端无 `total`、无 `LIMIT`。
4. **退款到账时间 / 退款原路退回**：后端只有审核与人工"执行成功"标记，没有资金动作。
5. **退款实时推送**：无 webhook / SSE / WebSocket，只能手动刷新或轮询。
6. **领券中心**：无模板列表接口，不能凭空生成券列表（Mock 数据也不行，会与真实后端产生事实冲突）。
7. **优惠券"已过期"标签由服务端给出**：后端不返回 `expired`，前端计算必须标注为"本地判定"。
8. **秒杀活动页 / 秒杀商品列表 / 秒杀倒计时 / 秒杀剩余库存**：无任何公开活动 API。
9. **秒杀订单列表 / 秒杀订单进入订单中心**：秒杀订单不在 `orders` 表。
10. **秒杀支付**：秒杀"下单即成交"，没有支付环节，不能画支付按钮。
11. **拼团 / 砍价 / 满减 / 积分**：代码中不存在。
12. **物流轨迹 / 快递单号**：`orders` 无相关字段。
13. **退货退款 / 部分退款 / 换货**：退款金额恒等于订单总额，且仅 `paid` 可申请。
14. **订单发票**：无字段、无接口。

---

## 14. W4-01 / W4-02 / W4-03 推荐开发范围

> 推荐原则：**只做后端已实现且有测试的能力**；把"后端缺口"作为显式依赖上抛，不用前端 Mock 掩盖。

### 14.1 W4-01：订单中心（列表 + 详情）

**范围**

- `/orders` 订单列表页：全量拉取、按 `status` 本地分组（全部 / 待付款 / 待发货 / 待收货 / 已完成 / 已取消 / 退款）、空态、加载态、错误态。
- 订单卡片：订单号、创建时间、首件商品图 + 件数、应付金额、状态标签、可用操作入口。
- `/orders/:id` 订单详情页：状态时间轴、收货快照、商品明细、金额三联（`original/discount/total`）、`expire_at` 倒计时。
- 状态映射：7 态全覆盖，`received` 仅作兼容映射（后端不产出）。
- 明确标注"列表不分页、筛选为本地行为"。

**依赖**：`GET /orders`（NOT_VERIFIED，建议先补一条 e2e 或由前端做容错）、`GET /orders/:id`（VERIFIED）。

**不做**：状态筛选接口、分页、物流、发票。

### 14.2 W4-02：支付与履约操作

**范围**

- 待付款订单的"去支付"：调用 `POST /orders/:id/pay`，成功后刷新订单与券状态。
- **模拟支付确认弹层**：明确文案"当前为演示环境，不会产生真实扣款"，无渠道选择。
- 倒计时与超时：基于 `expire_at` 本地倒计时；到点后重新拉详情（详情会懒取消）。
- 取消订单：二次确认 + `POST /orders/:id/cancel`；9002 处理。
- 确认收货：仅 `shipped` 展示；`POST /orders/:id/receive`。
- 幂等提交与失败恢复：按 §10.3 执行，超时后走"订单列表比对"而不是盲目重试。

**依赖**：7.4 / 7.5 / 7.6 三个接口（均 VERIFIED）。

**不做**：真实支付、支付方式、支付回调、支付中轮询。

### 14.3 W4-03：退款闭环（售后）

**范围**

- 订单详情页"申请退款"：仅 `status=paid` 显示；`reason` 必填（1~255 字）；`POST /orders/:id/refund`。
- 退款进度：`GET /refunds?order_id=` 展示该单退款记录与状态；`GET /refunds/:id` 详情。
- 撤销申请：仅 `applying` 可撤销，`POST /refunds/:id/revoke`。
- 退款状态文案：7 态全覆盖，其中 `review_rejected/revoked/refund_succeeded/closed` 为终态。
- 错误处理：18003（订单不可退）、18004（已有申请）需给出明确引导。

**依赖**：7.7 / 7.8 / 7.9 / 7.10（均 VERIFIED）。

**不做**：退款到账时间、退货退款、部分退款、退款协商。

### 14.4 关于优惠券与秒杀

- **优惠券**（`/my/coupons` + 结算选券）后端已就绪（VERIFIED），工作量约为 1 个页签 + 1 个选择器。建议作为 **W4-04**；若只能排 3 个任务，可与 W4-03 合并为"售后与优惠券"，但需在任务文档中拆清两个独立验收项。
- **秒杀不能排进 W4 前端任务**：缺少活动列表与详情 API（M10/M11），前端无法获得 `activity_id`/`sku_id`/秒杀价/库存/倒计时。若主规划坚持要做，只有两条路：
  1. 后端先补 `GET /flash-sales`（前台可用的活动列表）与 `GET /flash-sales/:id`（含 SKU 绑定、秒杀价、已售/剩余、时间窗）；
  2. 降级为"运营固定深链 + 只做下单与结果轮询"，且在 UI 上不承诺活动氛围与库存展示。
- **拼团/砍价**：无后端，直接排除。

---

## 15. 需要后端主规划 Chat 决策的问题

| # | 问题 | 为什么必须决策 | 建议 |
| --- | --- | --- | --- |
| Q1 | 是否修复 `POST /orders` 的幂等顺序（把幂等查询提到地址/购物车/SKU/券校验之前）？ | 这是 W4 支付与重试体验的地基；不修则"响应丢失"必然导致用户困惑 | 强烈建议修复：`Create` 开头先按 `(user_id, key)` 回查，命中且 hash 一致直接返回既有订单（hash 需要用客户端原始输入或可复现的输入计算，或改为"先查键、再校验前置条件"） |
| Q2 | `GET /orders` 是否补 `status` 筛选与分页？ | 决定 W4-01 是否要做"本地筛选 + 全量加载"的临时方案 | 建议补 `status` + `page/size`，并顺便消除 N+1 |
| Q3 | 是否新增"用户可领取优惠券列表"接口？ | 决定"领券中心"是否存在 | 建议新增 `GET /coupons`（仅 `status=1` 且在领取窗口内，返回剩余量） |
| Q4 | 是否新增"可用券查询/优惠试算"接口（入参金额，返回可用券与优惠额）？ | 决定结算页能否权威展示可用券 | 建议新增，至少返回 `available[]` 与 `unavailable[] + reason` |
| Q5 | 是否新增秒杀活动前台列表/详情接口？ | 决定秒杀能否进入 W4 | 若不补，W4 明确不做秒杀 |
| Q6 | 秒杀订单是否需要进入订单中心（或提供"我的秒杀记录"）？ | 用户买完秒杀后在订单列表看不到，是明显的体验断裂 | 建议至少提供 `GET /flash-sales/orders`（本人） |
| Q7 | W4 是否接受"模拟支付"作为交付？是否需要预留真实支付接入位？ | 决定收银台 UI 的文案与结构 | 建议明确写"演示支付"，并在前端抽象一层 `payOrder()`，便于后续替换 |
| Q8 | 退款是否需要覆盖 `shipped` 之后（退货退款）？ | 决定售后页面的可操作范围 | 当前仅"仅退款"，建议 W4 如实限制 |
| Q9 | 参数校验错误码是否统一映射为 `1001/400`（含 `gcode 51`）？ | 影响所有写接口的前端错误分支 | 建议在 `codes.FromError` 增加 `gcode.CodeValidationFailed → CodeInvalidArgument` 映射 |
| Q10 | 优惠券是否需要"已过期"状态或过期任务？ | 决定券包"已过期"分组的事实来源 | 建议前端本地判定 + 文档注明；若要服务端权威，需加 `expired` 派生字段 |
| Q11 | 订单详情 `GET` 触发懒取消（GET 有副作业）是否可接受？ | 影响缓存策略与"刷新即取消"的体验 | 建议保留（业务正确），但前端需在 UI 说明"超时将自动取消" |
| Q12 | `GET /orders` 无测试覆盖，是否先补后端 e2e？ | 这是 W4-01 的主接口，却没有任何断言 | 建议补 `TestOrderList`（排序、归属隔离、items 完整性） |

---

## 16. 附录

### 16.1 证据文件清单

| 主题 | 文件 | 关键位置 |
| --- | --- | --- |
| 前台路由 | `internal/cmd/routes_frontend.go` | 91-108（订单/退款/秒杀）、140-142（优惠券） |
| 路由装配 | `internal/cmd/cmd.go` | 166-182 |
| 路由锁定测试 | `internal/cmd/routes_test.go` | 43-160 |
| 订单契约 | `api/order/v1/order.go` | 9-17（状态）、31-72（Order）、74-143（前台接口） |
| 订单逻辑 | `internal/logic/order/order.go` | 132-217（Create）、292-384（insertOrder）、386-403（幂等）、405-420（List）、422-444（Detail）、447-516（Pay）、518-549（Cancel）、551-595（Receive）、657-679（退款推进）、683-761（超时/取消）、916-937（request_hash） |
| 退款契约 | `api/refund/v1/refund.go` | 9-18（状态）、32-93（前台接口） |
| 退款逻辑 | `internal/logic/refund/refund.go` | 123-196（Apply）、198-255（Revoke）、257-287（查询）、454-566（Execute） |
| 优惠券契约 | `api/coupon/v1/coupon.go` | 10-15（状态）、40-114（UserCoupon 与前台接口） |
| 优惠券逻辑 | `internal/logic/coupon/coupon.go` | 179-250（Claim）、252-272（ListMine）、274-315（GetDiscount）、319-357（Lock/Used/Release） |
| 秒杀契约 | `api/flashsale/v1/flash_sale.go` | 12-24（枚举）、47-63（FlashOrder）、103-131（前台接口） |
| 秒杀逻辑 | `internal/logic/flashsale/flashsale.go` | 266-342（CreateOrder）、463-542（落单事务） |
| 秒杀请求 | `internal/logic/flashsale/request.go` | 19-25（状态）、61-113（入队/回查）、117-150（结果查询） |
| 错误码 | `internal/codes/codes.go` | 83-89（订单）、136-141（退款）、143-150（优惠券）、103-113（秒杀）、269-283（映射） |
| 认证/响应 | `internal/middleware/auth.go`、`internal/middleware/response.go` | 26-63、12-45 |
| 超时扫描 | `internal/worker/scanner.go` | 23-68 |
| 迁移 | `internal/migrations/sql/20261001000007_orders.up.sql`（订单+索引）<br>`20261001000011_flash_sale.up.sql`（秒杀+唯一约束）<br>`20261001000015_flash_sale_order_requests.up.sql`（异步请求）<br>`20261001000020_refund.up.sql`（退款）<br>`20261001000021_coupon.up.sql`（券）<br>`20261001000022_order_coupon_amounts.up.sql`（订单券字段） | 全文 |

### 16.2 测试清单（判定 VERIFIED 的依据）

| 测试文件 | 覆盖函数 | 断言要点 |
| --- | --- | --- |
| `test/e2e/order_test.go` | `TestOrderCreateDirect`(211)、`TestOrderCreateFromCart`(269)、`TestOrderEmptyCart`(318)、`TestOrderServerPricingSnapshot`(336)、`TestOrderIdempotency`(378)、`TestOrderInsufficientStockRollback`(424)、`TestOrderStateMachine`(458)、`TestOrderCancelRestoresInventoryOnce`(509)、`TestOrderPayIdempotent`(562)、`TestOrderUserIsolation`(595)、`TestOrderConcurrentNoOversell`(681)、`TestOrderTimeoutAutoCancel`(727) | 定价快照、事务回滚、幂等（direct）、状态机、并发取消、懒取消+扫描器 |
| `test/e2e/order_admin_test.go` | 173-568 | 后台分页/筛选/排序/统计/只读权限（后台域，W4 不直接使用） |
| `test/e2e/coupon_test.go` | 150-895 | 模板管理、领取与拒绝、并发领取、归属隔离、下单用券、门槛、过期券、回滚、并发同券、幂等、支付核销、取消释放、退款不返券、快照稳定 |
| `test/e2e/refund_test.go` | 156-587 | 申请金额、归属、状态前置条件、唯一有效申请、审核不等于退款、非法迁移、发货互斥、成功一致性、失败与关闭、失败后重试、撤销、查询与审计、权限 |
| `test/e2e/flashsale_test.go` | 267-800 | 活动创建/权限、时间窗、定价快照、一人一单、幂等、并发不超卖、失败分支、更新、修复审计、不可增删绑定 |
| `test/e2e/flashsale_v2_test.go` | 65-253 | 预热/失效、售罄快速失败、Redis 预扣不超卖、对账、fail-closed |
| `test/e2e/flashsale_v3_test.go` | 60-188 | 入队快速响应、结果查询与归属隔离、消费幂等、失败补偿 |
| `test/e2e/flashsale_v5_test.go` | 19-89 | 限流 429、指标端点 |
| `internal/cmd/routes_test.go` | `TestRouteTable`(43) | 路由表结构与生产入口一致 |

**关键空白**：`GET /orders`（用户订单列表）**没有任何测试**；`POST /orders` 的 `source=cart` 幂等重试（购物车已清空）**没有任何测试**。

### 16.3 审查结束时的仓库状态

| 项 | 结果 |
| --- | --- |
| Git 分支 | `feat/phase2-user-frontend` |
| HEAD SHA | `1186162b2a07d5792d00f4ae73a588e2de189c6c` |
| 审查开始时工作区 | clean |
| 审查动作 | 只读（read_file / grep / ls / git log / git status / 读取模块缓存源码） |
| 代码修改 | 无 |
| 新增文件 | 仅本报告 `docs/reports/w4-backend-capability-audit/REPORT.md` |
| 分支操作 / 提交 / push | 无 |

**结论：本次为纯只读审查，未开始 W4 任何业务实现。**
