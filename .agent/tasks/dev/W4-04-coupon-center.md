# W4-04：SurgeCart 用户优惠券中心（开发知识文档）

- 分支：`feat/phase2-user-frontend`
- 范围：我的优惠券页面、结算页优惠券选择、用券下单、状态同步、与订单/退款中心整合
- 不在范围：Go 后端、SQL、后台券管理、秒杀/拼团/砍价、真实营销活动
- 事实来源：`api/coupon/v1/coupon.go`、`internal/logic/coupon/coupon.go`、`internal/logic/order/order.go`、`internal/codes/codes.go`、`internal/migrations/sql/20261001000021_coupon.up.sql`、`docs/design/coupon.md`
- 本文只记录**当前实现与实测证据**，不复制设计文档。

---

## 1. SurgeCart 优惠券数据模型

优惠券是「平台通用固定金额满减券」，不是折扣券/品类券/运费券。

| 表 | 作用 | 关键字段 |
| --- | --- | --- |
| `coupon_templates` | 券模板（后台创建） | `name`、`threshold_amount`（门槛，分）、`discount_amount`（减免，分）、`total_quantity`、`issued_quantity`、领取期、使用期、`status`(1 可领/0 停止) |
| `user_coupons` | 用户已领取的券实例 | `user_id`、`template_id`、`status`(10/20/30)、`order_id`（当前占用订单）、`received_at`、`locked_at`、`used_at` |

约束：

- `uk_user_template(user_id, template_id)`：同一用户同一模板只能领一张；
- 领取用「原子闸门 `issued_quantity + 1` + 时间窗口/总量 SQL 判定」，防止超发；
- 券的展示字段（名称、门槛、减免、使用期）来自 `LEFT JOIN coupon_templates` 的**模板快照**，不是用户券表冗余列。

前端 `UserCoupon` 类型对应 `api/coupon/v1/coupon.go:40-54`，字段逐一映射，没有额外扩展。

## 2. 用户优惠券与模板的关系

```
coupon_templates 1 ─── n user_coupons
                          └── order_id（下单占用/核销时指向 orders.id，软引用）
```

- 用户只能看到自己的 `user_coupons`（`WHERE user_id = ?`）；
- 模板管理（创建/列表/停止）是**后台能力**：`/admin/coupons*` 需 `coupon:create|list|stop`；
- 普通用户 token 调后台接口返回 403（`routes_test`/e2e 已覆盖）；
- 因此前端拿不到「有哪些券可以领」，也无法凭空生成券。

## 3. 优惠券状态机

| API 字符串 | DB | 含义 | 触发 |
| --- | --- | --- | --- |
| `available` | 10 | 可使用 | 领取成功；订单取消/超时取消时释放 |
| `locked` | 20 | 已锁定 | 下单事务内 `LockInTx`（available → locked，写 `order_id`/`locked_at`） |
| `used` | 30 | 已使用 | 支付事务内 `MarkUsedInTx`（locked → used，写 `used_at`） |

**没有 `expired` 状态**：过期只体现为 `use_end_time` 已过，由服务端下单时按 `NOW()` 拒绝（19007）。
前端状态枚举严格只有三种，不新增「已过期」；页面只展示后端返回的有效期字段。

不可逆点：`used` 是终态，退款成功也不返还（见第 9 节）。

## 4. 优惠券领取机制与能力限制

后端存在用户领取接口：

```
POST /coupons/:template_id/claim   → UserCoupon（status=available）
```

但**没有面向普通用户的公开模板列表**：

- `GET /admin/coupons` 需要 `coupon:list` 权限；
- 没有 `/coupons/templates`、`/coupons/available` 之类的公开接口；
- 因此前端无法获得合法的 `template_id` 来源。

本期决策：**不实现领券中心**。

- 不调用 `/admin/coupons`（越权 + 契约不符）；
- 不硬编码 `template_id`；
- 不虚构券活动；
- 不把「我的优惠券」冒充「可领取优惠券」；
- 不设计「手动输入券 ID 领取」这种无业务价值的伪功能。

页面上明确写出：「本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口」。

## 5. 结算页优惠券选择流程

```
CheckoutPage
  ├─ useCartQuery()            已勾选商品与预估金额
  ├─ useCouponList()           GET /my/coupons
  └─ CheckoutCouponSection
       ├─ evaluateCouponForCheckout()  前端估算（状态/有效期/门槛）
       └─ onSelect(couponId | null)    只改本地选择
            └─ buildOrderSignature(addressId, lines, couponId)  指纹含券 id
                 └─ useCreateOrder → POST /orders { coupon_id }
```

- 默认「不使用优惠券」；
- 券按「可选优先、预估优惠从高到低」排序；
- 不可选券仍然展示（不隐藏后端数据），禁用并给出原因；
- 选中券如果因为列表刷新变得不可选（被占用/过期/不满足门槛），派生逻辑会自动回落到「不使用」，避免提交必然失败的请求；
- 提交后金额完全以服务端响应为准，前端不做任何权威计算。

## 6. 服务端优惠金额计算

`internal/logic/order/order.go:152-178`：

```
originalAmount = Σ (skus.price × quantity)          # 服务端定价，不信任客户端
discountAmount = Coupon().GetDiscount(userID, couponID, originalAmount)
totalAmount    = originalAmount - discountAmount
```

`GetDiscount`（`internal/logic/coupon/coupon.go:274-315`）：

1. 券必须存在且属于当前用户（19003）；
2. 模板使用期必须包含 `NOW()`（否则 19007）；
3. `originalAmount ≥ threshold_amount`（否则 19007）；
4. `discount = min(discount_amount, originalAmount)`（INV-C06，优惠不超过商品金额）；
5. **不校验 status**——可用状态由下单事务内的 `LockInTx`（`status=available` 条件更新）权威判定。

真实后端实测（商品 ¥4,299.00）：

```
original_amount = 429900
discount_amount = 50000
total_amount    = 379900
user_coupon_id  = 1
```

封顶实测：券面额 ¥5,299.00 > 商品 ¥4,299.00 时，`discount_amount = 429900`，`total_amount = 0`。

## 7. 前端适用性估算的边界

前端只做**粗筛**，不产生权威结论：

| 判据 | 来源 | 边界 |
| --- | --- | --- |
| 状态必须 `available` | 后端字段 | 服务端 `LockInTx` 才是权威闸门 |
| 当前时间在 `[use_start_time, use_end_time)` | 后端字段 + 浏览器时间 | 服务端用 SQL `NOW()`，时区以服务端为准 |
| 预估商品金额 ≥ `threshold_amount` | 购物车预估金额 | 服务端用订单 `original_amount` |
| 预估优惠 = `min(discount_amount, 预估金额)` | 券面额 | 最终以响应 `discount_amount` 为准 |
| 适用范围 | **无此字段** | 券是平台通用券，不存在商品/分类范围判断 |

UI 上必须出现：「优惠券适用性及最终优惠金额以服务端下单校验结果为准。」
摘要区标注「优惠（预估）」「应付金额（预估）」，成功页才展示服务端返回的三项金额。

## 8. 订单幂等键与优惠券选择的关系

后端 `requestHash`（`internal/logic/order/order.go:916-933`）：

```
sha256(source|address_id|coupon_id|sku_id:qty|...)
```

前端 `buildOrderSignature` 与之一一对应：

```ts
buildOrderSignature(addressId, lines, couponId = 0)
// → `cart|${addressId}|${couponId}|${sku:qty,...}`
```

`useCreateOrder` 的 `keyRef` 只在 **signature 相同**时复用旧 key：

| 操作 | signature | 幂等键 |
| --- | --- | --- |
| 同地址 + 同商品 + 同券重试 | 相同 | **复用**（网络超时重试安全） |
| 换券 | 变化 | **新键** |
| 取消用券 | 变化 | **新键** |
| 换地址 / 商品增减 | 变化 | **新键** |

W4-04 之前的实现固定 `coupon_id = 0`，本次修复为真实券 id。
不修复会导致「同键不同券」→ 后端 9006；或「换券仍用旧键」→ 用户以为换了券但实际命中原订单。

真实后端实测：

- 同键同内容重试 → 返回既有订单（`id=1`）；
- 同键不同券 → `9006 / 409`。

## 9. 优惠券锁定与释放

| 动作 | 券状态 | 实现位置 |
| --- | --- | --- |
| 下单成功 | available → locked（写 `order_id`/`locked_at`） | `order.insertOrder` 事务内 `LockInTx` |
| 支付成功 | locked → used（写 `used_at`） | `order.Pay` 事务内 `MarkUsedInTx` |
| 取消订单（用户取消） | locked → available（清空 `order_id`/`locked_at`） | `order.cancel` 事务内 `ReleaseInTx` |
| 超时自动取消 | locked → available | 同一 `cancel` 路径 |
| 退款申请/审核/执行/关闭 | **不变** | 退款模块不调用券服务 |
| 退款成功 | **不返还，保持 used** | 同上 |

`ReleaseInTx` 用 `WHERE order_id = ? AND status = locked` 条件更新，天然幂等（重复释放 no-op）。

## 10. 取消订单与退款的优惠券处理差异

| 维度 | 取消订单 | 退款成功 |
| --- | --- | --- |
| 库存 | 恢复 | 恢复 |
| 优惠券 | **释放为 available** | **不返还，保持 used** |
| 订单状态 | → cancelled | → refunded |
| 业务语义 | 交易未发生，券应可复用 | 券已随支付核销，退款不退券 |

这是两个不同的业务路径，前端文案不能混用：

- 取消订单成功：刷新券列表，券恢复「可使用」；
- 退款成功：**不能**把券改回 available，也不能提示"券已退回"。

## 11. TanStack Query 缓存同步

Query Key：

```ts
coupons: {
  all: ['coupons'],
  list: () => ['coupons', 'list'],
}
```

| 操作 | 缓存动作 |
| --- | --- |
| 下单成功 | `invalidateQueries(coupons.all)`（券变 locked）+ 订单缓存 + 购物车 |
| 下单失败 19003/19007/9006 | `invalidateQueries(coupons.all)`（本地券已过期） |
| 支付成功 | `invalidateQueries(coupons.all)`（locked → used） |
| 取消成功 | `invalidateQueries(coupons.all)`（locked → available） |
| 支付/取消失败（HTTP） | 同时失效券列表（状态可能已被其它请求改变） |
| 确认收货 | 不失效（不影响券） |
| 退出登录 | `removeQueries(coupons.all)` |

不做乐观更新：券状态由服务端事务决定，前端猜测会在失败时造成状态闪回。

## 12. 关键文件与调用链

| 文件 | 职责 |
| --- | --- |
| `web/src/api/coupons.ts` | `UserCoupon`/`UserCouponStatus`、`fetchMyCoupons`、领取能力边界说明 |
| `web/src/api/query-keys.ts` | 新增 `coupons` key |
| `web/src/hooks/use-coupons.ts` | `useCouponList`（登录态、staleTime、重试策略） |
| `web/src/utils/coupons.ts` | 状态文案/色调/筛选器、有效期展示、门槛文案、结算页估算 |
| `web/src/components/coupon/coupon-status-badge.tsx` | 状态徽标（`data-coupon-status`） |
| `web/src/components/coupon/coupon-card.tsx` | 我的优惠券卡片 |
| `web/src/pages/account/coupons-page.tsx` | `/account/coupons` 页面 |
| `web/src/components/checkout/checkout-coupon-section.tsx` | 结算页选券（radio group） |
| `web/src/components/checkout/checkout-summary.tsx` | 预估优惠/预估应付 |
| `web/src/pages/checkout-page.tsx` | 选券状态、signature 组装、19007 回落 |
| `web/src/hooks/use-checkout.ts` | 下单 mutation、幂等键复用、券缓存失效 |
| `web/src/utils/idempotency.ts` | `buildOrderSignature` 加入 couponId |
| `web/src/hooks/use-orders.ts` | 支付/取消后失效券缓存 |
| `web/src/pages/checkout-success-page.tsx` | 展示服务端 `original/discount/total` 与券 id |
| `web/src/auth/use-auth.ts` | 登出清理券缓存 |
| `web/src/mocks/data/coupons.ts` | MSW 券状态机（seed/expire/锁/核销/释放） |
| `web/src/mocks/data/orders.ts` | 下单用券、支付核销、取消释放 |

## 13. 技术决策与权衡

### 13.1 结算页不隐藏不可用券

隐藏会让用户困惑「我明明有券却看不到」。展示 + 禁用 + 原因，信息更完整；同时用排序把可选券放前面。

### 13.2 选中券失效后自动回落

券列表刷新（例如另一标签页下单占用）后，`selectedCoupon` 派生为 null，UI 回到「不使用优惠券」。
代价是用户的选择被静默取消；收益是绝不会提交一个必然 19007 的请求。页面上的券行会实时变为禁用并显示原因。

### 13.3 只在前端做「估算」，金额始终以服务端为准

没有订单试算/可用券接口，任何前端金额计算都可能与后端不一致（门槛、时区、封顶规则）。
因此：摘要区明确标注「预估」，成功页才展示服务端金额，且不做二次换算。

### 13.4 不实现领券中心

见第 4 节。这是**能力缺失**而不是取舍：没有合法数据来源，任何实现都需要伪造。

### 13.5 券下拉/选择用原生 radio group

复用现有设计系统（无新增依赖），原生 radio 自带键盘与可访问性，测试也可直接按 role 查询。

## 14. 实际问题排查

| # | 问题 | 原因 | 处理 |
| --- | --- | --- | --- |
| 1 | ESLint 报 `useMockCouponOfOrder` 违反 hooks 规则 | 函数名以 `use` 开头被当作 Hook | 重命名为 `markMockCouponUsedOfOrder` |
| 2 | React Compiler 报 `checkout-page` memo 无法保留 | `summary`/`coupons` 每次渲染都是新对象 | 用 `useMemo` 包裹 `items → selectedItems → summary` 与 `coupons` |
| 3 | 结算页现有测试「暂未接入」数量断言失败 | 优惠行从「暂未接入」改为「未使用优惠券」 | 更新断言（预期内的 UI 变化） |
| 4 | 成功页现有测试 `getByText('10,597')` 多元素 | 新增「商品总额」后同一金额出现两次 | 改为 `getAllByText` |
| 5 | Mock 幂等重试用例返回 19007 而不是既有订单 | Mock 的 `evaluateMockCoupon` 校验了 `status=available`，与后端 `GetDiscount` 不一致 | 移除状态校验，状态由幂等检查后的预占闸门判定 |
| 6 | 「商品不可购买」API 用例误判成功 | orders Mock 的 `resolveSku` 不读取 cart Mock 的下架集合 | 删除该 API 用例（UI 层已覆盖 9003） |
| 7 | 我的优惠券状态用例 `querySelector` 返回 null | 只等 heading，数据仍在 loading | 先 `findByText(券名)` 再断言徽标 |
| 8 | 结算页「不使用优惠券」radio 找不到 | label 内含两段文本，accessible name 是「不使用优惠券 按服务端原价计算」 | 改用正则匹配 |
| 9 | 真实联调 UI 找不到商品 | SQL 插入中文未指定字符集，商品名/SKU 名乱码 | `--default-character-set=utf8mb4` 重写数据 |
| 10 | 真实联调金额断言漂移 | 多轮加购同一 SKU 会累加数量 | 脚本每轮先清空购物车 |
| 11 | 真实联调「查看详情」后侧边栏超时 | 成功页不在 `/account` 布局内 | 先点「查看订单」进订单详情，再用侧边栏 |
| 12 | 取消订单后等待「订单已取消」文本 strict violation | 页面多处同义文本 | 改为等待 `[data-status="cancelled"]` |

## 15. 测试验证

### 15.1 单元 / 组件（Vitest + jsdom + MSW）

| 文件 | 用例数 | 覆盖 |
| --- | --- | --- |
| `web/src/api/coupons.test.ts` | 10 | 未登录 401、账号隔离与倒序、用券下单金额/券绑定、不用券、优惠封顶、门槛/已用/过期/他人券拒绝、同键同内容幂等、同键不同券 9006、支付核销、取消释放、退款不返还 |
| `web/src/utils/idempotency.test.ts` | 8 | key 长度与唯一性、指纹同内容/顺序无关/换券/取消券/换地址/换商品、格式与后端一致 |
| `web/src/pages/account/coupons-page.test.tsx` | 10 | 空状态与能力说明、字段展示、三状态映射与说明、本地筛选、加载失败重试、刷新、账号隔离、登出清缓存 |
| `web/src/pages/checkout-coupon.test.tsx` | 10 | 展示券/默认不使用、门槛不足禁用、锁定与过期禁用、选券下单与金额、成功页三项金额、换券新幂等键、取消券新幂等键、19007 回落、无券下单、防重复提交 |

全量：43 个测试文件 / 314 个用例全部通过。

### 15.2 E2E（Playwright + MSW，**Mock，不是真实后端**）

`web/tests/e2e/coupons.spec.ts`：3 个用例

1. 未登录跳登录 → 我的优惠券空状态 + 三种状态筛选（无「已过期」）；
2. 结算页无券提示 + 仍可下单 + 成功页展示三项金额；
3. 个人中心导航与账户概览入口。

浏览器侧无法「造出一张券」（没有公开模板列表，也没有领券 UI），因此有券的选择/换券/取消用券由组件测试与真实后端联调覆盖。

全量 E2E：46 个用例全部通过。

### 15.3 真实 Go 后端联调（**非 Mock**）

环境：MySQL 8.0（独立容器 3307，全新库，`migrate up` 到 `20261001000024`）+ Redis 7（6380, DB 9）+ `go build -tags serve_lifecycle ./test/serveproc` + `test/container/qiniu_mock`（未修改任何 Go 代码）+ vite dev（5174）代理到真实后端 8000，未启用 MSW。

接口级 35/35 PASS：

- 未登录 401；领取成功；我的券包含刚领取的券与模板快照；重复领取 19004；
- 用券下单：`original=429900`、`discount=50000`、`total=379900`、`user_coupon_id=1`；
- 下单后券 locked 且绑定订单；同键同内容重试返回既有订单；同键不同券 9006；
- 支付后券 used + `used_at`；券 B 下单后取消 → available、`order_id`/`locked_at` 清空；
- 退款申请 + 审核通过 + 执行成功 → 订单 refunded，券仍 used 且仍关联原订单；
- 未达门槛 19007；过期券 19007（过期模板仍可领取，领取期与使用期独立）；他人券 19003；他人券列表为空。

UI 级 32/32 PASS：

- 登录后优惠券列表展示减免/门槛/有效期/状态说明；「可使用」筛选生效；
- 结算页声明「以服务端下单校验结果为准」；默认不使用；高门槛券禁用；过期券禁用；可选券可选；
- 摘要展示「优惠（预估）¥300.00」「应付金额（预估）¥3,999.00」；
- 成功页展示商品总额 ¥4,299.00、优惠 ¥300.00、应付 ¥3,999.00、本单使用优惠券；
- 下单后券变「已锁定」并展示关联订单；取消订单后恢复「可使用」且关联入口消失；
- 再次下单并模拟支付后券变「已使用」并展示使用时间；
- 无未捕获异常、无非预期控制台错误。

联调数据库终态：

```
user_coupons: 1=used(order1) 3=used(order9) 4=used(order5,退款订单) 5=available(高门槛) 6=available(过期)
orders:       1=paid 4=cancelled 5=refunded 7=cancelled 8=cancelled 9=paid
inventories:  sku 1 quantity=96
```

**声明**：E2E 与组件测试使用 MSW，不代表真实后端；真实后端结果以本节为准。

## 16. 已知限制

1. **没有领券中心**：后端没有面向用户的公开券模板列表接口，前端不伪造。
2. **没有「订单可用券」查询/优惠试算接口**：结算页的适用性判断是前端估算。
3. **没有 expired 状态**：过期只体现为有效期字段，最终由服务端拒绝。
4. **没有商品/分类适用范围**：券是平台通用券，不存在范围匹配。
5. **券类型只有固定金额满减**：没有折扣率券、品类券、运费券。
6. **一人一模板一张**：`uk_user_template` 约束，重复领取 19004。
7. **退款不返还券**：`used` 为终态。
8. **我的优惠券无分页与状态筛选**：本地筛选。
9. **结算页只看当前购物车预估金额**：与最终订单金额可能存在差异（服务端定价）。
10. **不支持手动输入券码**：后端没有券码兑换接口。

## 17. 后续维护注意事项

- **新增券状态时**：同步 `UserCouponStatus`、`COUPON_STATUS_LABELS`、`describeCouponStatusTone`、`COUPON_STATUS_FILTERS`、`evaluateCouponForCheckout`；若新状态可用于下单，还要同步结算页候选逻辑。
- **后端若开放公开模板列表/领券中心**：新增 `fetchClaimableTemplates` + `claimCoupon`，页面复用 `CouponCard`；领取成功后失效 `coupons.all`。
- **若后端新增「可用券查询」接口**：用服务端结果替换 `evaluateCouponForCheckout` 的估算，删掉「以服务端为准」的降级文案。
- **若支持适用范围**：扩展 `UserCoupon` 与 `evaluateCouponForCheckout` 的判据，并在 UI 展示范围。
- **若退款改为返券**：必须同步 `useRefund` 相关缓存失效与文案；当前文档明确「不返还」。
- **新增账号私有 Query Key**：加入 `useLogout` 的 `removeQueries` 列表。
- **修改幂等指纹**：必须同时改 `buildOrderSignature` 与后端 `requestHash`，否则换券会 9006。
- **不要在前端计算权威优惠金额**：成功页只展示服务端返回值。

## 18. 技术学习与面试准备

### 18.1 核心讲点

1. **券状态机的三段式**：available → locked → used；取消是唯一回退路径（locked → available），退款不产生回退。
2. **领券的原子闸门**：`UPDATE ... SET issued_quantity = issued_quantity + 1 WHERE issued_quantity < total_quantity AND claim_start_time <= NOW() AND claim_end_time > NOW()` + `uk_user_template`，用数据库保证不超发、不重复。
3. **下单用券的两个阶段**：`GetDiscount`（只读校验：归属/有效期/门槛/封顶）与 `LockInTx`（原子预占：`status=available` 条件更新）。前者不做状态校验，避免幂等重试被误拒。
4. **幂等指纹必须含券 id**：否则「换券仍用旧键」会命中旧订单，或触发 9006。
5. **优惠金额的权威性**：`min(减免, 商品金额)` 由服务端计算，前端不参与。
6. **缓存失效的因果**：券状态由订单流程改变，因此失效点挂在订单 mutation（下单/支付/取消）而不是券自身 mutation。
7. **取消 vs 退款**：同样恢复库存，但一个释放券、一个不释放。

### 18.2 常见追问

- **Q：为什么不做领券中心？**
  A：后端只有 `POST /coupons/:template_id/claim`，没有公开模板列表。没有 `template_id` 来源，做出来只能靠硬编码或伪造活动，属于假功能。
- **Q：前端怎么判断券能不能用？**
  A：只做估算（状态 available、有效期、门槛），并在 UI 明确「以服务端为准」；真实判定在 `GetDiscount` + `LockInTx`。
- **Q：换券为什么必须换幂等键？**
  A：后端 `requestHash` 含 `coupon_id`。同键不同 hash 直接 9006；同键不换券则可能返回旧订单，用户以为换了券但实际没有。
- **Q：退款后券为什么不回来？**
  A：券在支付时已核销为 used，退款路径只恢复库存与订单状态，不调用券服务。这是当前业务定义，不是缺陷。
- **Q：下单成功但支付前取消，券怎么恢复？**
  A：取消事务内 `ReleaseInTx` 把 `locked` 改回 `available` 并清空 `order_id`/`locked_at`；前端在取消成功回调里失效券缓存。
- **Q：如何避免重复提交？**
  A：`mutation.isPending` + `submittingRef` 同步锁；幂等键在同一内容下复用，服务端唯一键兜底。
