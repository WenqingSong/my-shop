# 计价与金额设计（Pricing）

本文说明 SurgeCart 的金额单位、当前已实现的计价公式、目标公式与历史兼容边界。它是**规则说明**，不是已完成的计价引擎：
订单/优惠券模块只实现了其中的商品金额与固定金额券部分；运费、会员优惠、积分抵扣尚未实现。

## 1. 职责与边界

- 回答「一笔订单的商品金额、优惠、运费与最终应付分别是什么、按什么顺序计算、金额如何取整」。
- 事实来源：`orders`（金额快照）、`order_items`（SKU 价格/数量快照）、`skus.price`（下单时价格事实）、`coupon_templates`/`user_coupons`（券优惠）。
- 不负责：支付渠道、退款到账、发票、优惠分摊、计价规则引擎、运费模板（见 `order.md`、`wallet.md`、`refund.md`）。
- 目标公式中的会员优惠、积分抵扣、运费计算均为 **Deferred**；B1-04 实现时必须先更新本文并补测试。

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

## 5. 会员优惠（Deferred）

**等级与折扣配置已建模**（`member_levels.discount_rate_bp`，见 `docs/design/member.md`），但**计价尚未实现**。
目标语义（B1-04 及以后）：

- 会员折扣按「商品金额」计算，不折扣运费；
- 折扣结果写入订单独立快照列（不得直接改写 `discount_amount` 的券语义）；
- 折扣与券的叠加顺序、是否互斥由 Owner 决策后写入本文，当前不做假设。

## 6. 积分抵扣（Deferred）

尚未实现。已知约束：

- 积分是整数（`BIGINT`），账户/流水见 `member.md`；不得出现负积分；
- 抵扣换算比例（多少积分抵 1 分）属配置项，当前未定义；
- 单笔抵扣上限、是否与券叠加、取消/退款返还规则均需在实现前明确；
- 抵扣金额必须落到整数分，且累计优惠不得超过商品金额。

## 7. 运费（结构已建，计算 Deferred）

- `orders.shipping_fee`（INT UNSIGNED，默认 0）为运费快照，由 `20261110000004` 新增；历史订单与当前阶段均为 0。
- 运费模板、区域计费、满额免邮、包邮规则**未建模**；B1-04/B1-03-03 引入时须新增独立结构，不修改本列语义。
- 运费不参与优惠券门槛判断（门槛只比较商品金额），也不被券/积分抵扣。

## 8. 最终应付金额

**当前已实现（B1-04 之前）**：

```text
total_amount = original_amount - discount_amount
             = Σ(price×qty) - coupon_discount
shipping_fee = 0
```

**目标公式（Deferred，按实现顺序落地）**：

```text
应付金额 = 商品金额(original)
          - 优惠券优惠(coupon_discount)
          - 会员优惠(member_discount)
          - 积分抵扣(points_discount)
          + 运费(shipping_fee)

约束：各项优惠累计 ≤ 商品金额；运费不参与折扣；结果 ≥ 0 且为整数分。
```

在任一项目落地前，订单金额仍以数据库现有三列 + `shipping_fee` 快照为准；**不得**在未实现对应优惠时把目标公式写进代码或文档当作既定行为。实现顺序需保证：每次只引入一项，同步更新本文、`order.md` INV 与金额守恒测试。

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
| 新增支付/履约字段 | 默认空串，历史订单保持空；不推断历史渠道或运单号 |
| 新增 `spec_text` | 默认空串，历史订单不回填；历史 `sku_name` 已含规格文本 |

## 11. 不变量

- INV-P01（单位唯一）：所有金额一律整数分；任何新列/接口不得引入元或小数金额。
- INV-P02（服务端定价）：订单商品金额只来自服务端重读的 `skus.price`；客户端金额不参与计算。
- INV-P03（快照自足）：订单金额/优惠/运费一旦写入即为快照，改价、改模板、改地址不回溯修改。
- INV-P04（金额守恒）：当前 `total_amount = original_amount - discount_amount`；`shipping_fee` 非零后扩展为 `total = original - discount + shipping`。历史订单永不重算。
- INV-P05（历史不伪造）：迁移只加列/加表，不回填无法从现有数据无歧义推导的金额与优惠。

## 12. Deferred / 已知留白

- 运费模板与运费计算、包邮规则。
- 会员等级折扣、积分抵扣比例与上限、优惠叠加顺序。
- 比例折扣/积分换算的舍入规则（当前无比例计算）。
- 优惠分摊到订单项、部分退款金额拆分、发票与税费。
