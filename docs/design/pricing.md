# 计价与金额设计（Pricing）

本文说明 SurgeCart 的金额单位、统一计价入口、当前已实现的计价公式、目标公式与历史兼容边界。

自 B1-04-03 起，项目建立**唯一计价入口** `internal/logic/pricing.Quote`：商品原始金额、优惠券折扣、运费与应付金额必须经该入口得出，禁止各业务模块各自实现一套金额公式。会员折扣与积分抵扣在 `Breakdown` 中已预留独立槽位（当前恒 0），由 B1-04-08 / B1-04-05 通过同一入口扩展。

当前状态需严格区分：

- **模块级已实现**：`pricing.Quote`（计价公式与守恒约束）、运费计算（`service.IFreight.Quote`，见 `freight.md`）；
- **订单链路尚未接入**：`/orders/quote`、下单事务内复核与 `orders.shipping_fee` 落库待 B1-04-02 进入共同基线后实施，订单侧仍按既有 `total_amount = original_amount - discount_amount` 运行。

## 1. 职责与边界

- 回答「一笔订单的商品金额、优惠、运费与最终应付分别是什么、按什么顺序计算、金额如何取整」。
- 事实来源：`orders`（金额快照）、`order_items`（SKU 价格/数量快照）、`skus.price`（下单时价格事实）、`coupon_templates`/`user_coupons`（券优惠）、`freight_templates`/`freight_template_rules`（运费）。
- 不负责：支付渠道、退款到账、发票、优惠分摊、计价规则引擎、运费模板管理（见 `order.md`、`wallet.md`、`refund.md`、`freight.md`）。
- 会员优惠、积分抵扣为 **Deferred**；其槽位已存在但当前不产生业务值。

## 2. 金额单位与表示

| 项 | 约定 |
| --- | --- |
| 单位 | **整数分**（1 元 = 100 分），不使用 `decimal`、`float`、字符串金额 |
| DB 类型 | `INT UNSIGNED`（上限 99,999,999 分 = ¥999,999.99）；积分用 `BIGINT` |
| Go 类型 | `int64`，JSON 数字；解析/校验在 API 层，拒绝小数、负数、超上限 |
| 舍入 | 当前只有固定金额券，无比例计算，**不产生舍入**；比例折扣/积分换算的取整规则未定（Deferred），实现时必须落到整数分且不得出现负应付 |
| 时间 | 金额快照与业务时间均落 `DATETIME`，不依赖 Go 进程时区 |

**禁止**：把历史 Java 库的 `decimal(8,2)` 元金额直接搬入；用浮点累计金额；在展示层反算并回写订单金额。

## 3. 商品金额（已实现）

```text
original_amount = Σ (order_items.price × order_items.quantity)
order_items.price = 下单时重读的 skus.price（整数分快照）
```

- 定价只信服务端：不信任客户端提交价格，也不信任购物车 `price_snapshot`。
- `products.price` 只是 SPU 展示价，不参与订单计算；订单粒度为 SKU。
- 下单后改 SKU 价格/商品状态不影响已存在的订单快照（`skus`/`products` 删除或改名亦不影响）。

## 4. 优惠券优惠（已实现，固定金额）

```text
discount_amount = min(coupon_templates.discount_amount, original_amount)
                   （仅当券可用：归属本人、未使用/未预占、在有效期内、original ≥ threshold_amount）
```

- 只支持平台通用固定金额满减券；不使用百分比、不叠加多券、不分摊到订单项。
- 券的资格与金额由服务端在预占事务内判定；`orders.discount_amount` 与 `orders.user_coupon_id` 为下单快照。
- 退款不返券（券保持已使用），见 `refund.md`。

## 5. 会员优惠（B1-04-08 已实现）

**等级与折扣配置**见 `docs/design/member.md`（`member_levels.discount_rate_bp`，万分比）。

已实现语义：

```text
raw_member_discount = floor(original_amount × (10000 - discount_rate_bp) / 10000)
member_discount     = min(raw_member_discount, original_amount - coupon_discount)
```

- 会员折扣按「商品金额」`original_amount` 计算，不折扣运费；不折扣订单项级别，不分摊到订单项。
- 结果**向下取整到分**，全部为整数分运算，无浮点。
- 折扣结果写入订单独立快照列 `orders.member_discount_amount`（migration `20261110000015`），
  不改写 `orders.discount_amount` 的券语义。
- **叠加顺序（B1-04-08 冻结）**：先算券（券资格与金额基于 `original_amount`，保持既有已交付语义不变），
  再算会员折扣并按「券后剩余金额」夹紧。夹紧保证 `discount_amount + member_discount_amount ≤ original_amount`
  且 `total_amount ≥ 0`——**会员折扣不突破订单金额边界**；券已抵满时会员折扣为 0。
- 会员折扣在**下单时计算一次**并快照；等级变更、配置变更、事后查询都不重算（INV-P03）。
- 无适用等级（平台未配置启用等级）或等级为原价（`discount_rate_bp=10000`）时折扣为 0。

## 6. 积分抵扣（Deferred）

尚未实现。已知约束：

- 积分是整数（`BIGINT`），账户/流水见 `member.md`；不得出现负积分；
- 抵扣换算比例（多少积分抵 1 分）属配置项，当前未定义；
- 单笔抵扣上限、是否与券叠加、取消/退款返还规则均需在实现前明确；
- 抵扣金额必须落到整数分，且累计优惠不得超过商品金额。

## 7. 运费（计算已实现，订单接入 Deferred）

- `orders.shipping_fee`（INT UNSIGNED，默认 0）为运费快照，由 `20261110000004` 新增；历史订单保持 0，不重算。
- 运费模板、区域计费、满额免邮、包邮规则由 B1-04-03 建模与实现，见 `freight.md`；金额统一整数分。
- 运费计算入口为 `service.IFreight.Quote`，结果作为 `pricing.Quote` 的 `ShippingFee` 槽位输入。
- 运费不参与优惠券门槛判断（门槛只比较商品金额），也不被券/积分抵扣：

```text
PayableAmount = OriginalAmount - CouponDiscount - MemberDiscount - PointsDiscount + ShippingFee
```

- 运费与折扣互不折抵：优惠只作用于商品金额，运费始终按模板规则加在应付上。
- 会员折扣由会员模块按等级折扣率产出金额，作为 `pricing.Quote` 的 `MemberDiscount` 槽位参与统一计算（见 `member.md`）。

## 8. 最终应付金额与统一计价入口

### 8.1 统一入口 `pricing.Quote`

```go
type Breakdown struct {
    OriginalAmount  int64 // 商品原始金额
    CouponDiscount  int64 // 优惠券折扣
    MemberDiscount  int64 // 会员折扣（当前恒 0）
    PointsDiscount  int64 // 积分抵扣（当前恒 0）
    ShippingFee     int64 // 运费
    PayableAmount   int64 // 应付金额
}

func Quote(ctx context.Context, req *QuoteRequest) (*Breakdown, error)
```

公式与约束：

```text
PayableAmount = OriginalAmount
              - CouponDiscount
              - MemberDiscount
              - PointsDiscount
              + ShippingFee

约束：
  1. 所有输入与输出非负；
  2. 任一项优惠不得超过「扣减到该项时的剩余商品金额」，因此累计优惠 ≤ OriginalAmount；
  3. PayableAmount ≥ 0，且 ≤ INT UNSIGNED 可写区间（超出返回 20012，而不是等到写库失败）；
  4. 全程 int64 整数运算，无 float64/decimal；
  5. 金额守恒：OriginalAmount - CouponDiscount - MemberDiscount - PointsDiscount + ShippingFee == PayableAmount。
```

`Breakdown.Conserved()` 提供守恒自检，测试与调用方可用于断言。

### 8.2 槽位顺序不可变

扣减顺序固定为「券 → 会员 → 积分 → 加运费」。顺序影响「某项优惠是否超过剩余金额」的判定，任何调整都属于计价语义变更，必须先更新本文与测试。

### 8.3 当前生效范围

| 环节 | 状态 |
| --- | --- |
| `pricing.Quote` 公式、守恒与溢出保护 | **已实现** |
| 运费计算（`IFreight.Quote`） | **已实现** |
| 优惠券折扣（`ICoupon.GetDiscount`） | **已实现**（既有） |
| 订单使用 `pricing.Quote` 落库 | **已接入**（`POST /orders`，事务内复核） |
| `/orders/quote` 算价接口 | **已实现**（`POST /orders/quote`，预览金额非最终可信金额） |
| 会员折扣产生实际值 | **已实现**（`IMember.DiscountRateBp` → `MemberDiscount` 槽位） |
| 积分抵扣产生实际值 | **未实现**（B1-04 无积分账本分支，槽位恒 0） |

订单落库公式：

```text
original_amount        = Σ(下单时重读的 skus.price × quantity)
discount_amount        = coupon_discount
member_discount_amount = floor(original × (10000 - rate_bp) / 10000)，且 ≤ original - coupon
shipping_fee           = IFreight.Quote(收货地址 region_id, 商品/SKU/数量/金额)
total_amount           = original - discount - member_discount + shipping
```

约束：各项优惠累计 ≤ 商品金额；运费不参与折扣；结果 ≥ 0 且为整数分；历史订单永不重算。

## 9. 支付与退款金额

- 支付金额 = `orders.total_amount`（Mock 支付不额外计算手续费/渠道费）。
- 退款金额 = `orders.total_amount`（V1 整单全额，服务端派生，不接受客户端金额），见 `refund.md`。
- 当前 Mock 支付/退款不产生钱包流水；余额支付与退款入账见 `wallet.md`。

## 10. 历史订单兼容

| 场景 | 规则 |
| --- | --- |
| 迁移前订单 | `original_amount` 已在 `20261001000022` 回填为 `total_amount`；`discount_amount=0`；`total_amount` 不重算 |
| 新增 `shipping_fee` | 默认 0，不回填、不参与历史 `total_amount` 重算 |
| 新增券快照 | 历史 `user_coupons` 四列为 NULL，读取回退模板；不回填 |
| 新增 `member_discount_amount`（`20261110000015`） | 默认 0、不回填；历史订单 `total_amount = original_amount - discount_amount` 仍成立，不重算 |
| 新增支付/履约字段 | 默认空串，历史订单保持空；不推断历史渠道或运单号 |
| 新增 `spec_text` | 默认空串，历史订单不回填；历史 `sku_name` 已含规格文本 |

## 11. 不变量

- INV-P01（单位唯一）：所有金额一律整数分；任何新列/接口不得引入元或小数金额。
- INV-P02（服务端定价）：订单商品金额只来自服务端重读的 `skus.price`；客户端金额不参与计算。
- INV-P03（快照自足）：订单金额/优惠/运费一旦写入即为快照，改价、改模板、改地址不回溯修改。
- INV-P04（金额守恒）：计价入口必须满足 `PayableAmount = OriginalAmount - CouponDiscount - MemberDiscount - PointsDiscount + ShippingFee`；订单落库满足 `total_amount = original_amount - discount_amount - member_discount_amount + shipping_fee`，且 `discount_amount + member_discount_amount ≤ original_amount`、`total_amount ≥ 0`。历史订单永不重算。
- INV-P05（历史不伪造）：迁移只加列/加表，不回填无法从现有数据无歧义推导的金额与优惠。
- INV-P06（入口唯一）：任何模块不得自行实现第二套金额公式；新增优惠类型必须扩展现有槽位，不得复制计价逻辑。
- INV-P07（无浮点、无溢出）：金额计算禁止 float64/decimal；加、乘与累加前必须做上界检查，溢出返回明确错误码而非回绕。

## 12. Deferred / 已知留白

- 积分抵扣比例、上限与账本实现（B1-04 无对应分支，`PointsDiscount` 恒 0）。
- 会员折扣与运费/积分抵扣的叠加顺序中「运费是否计入会员折扣基数」的业务口径（当前运费不参与折扣）。
- 优惠分摊到订单项、部分退款金额拆分、发票与税费。
