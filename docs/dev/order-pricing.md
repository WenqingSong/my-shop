# 订单计价开发指南（Order Pricing）

本文说明如何围绕统一计价入口 `pricing.Quote` 扩展金额能力，以及当前订单链路的真实状态。设计语义以 `docs/design/pricing.md`、`docs/design/order.md`、`docs/design/freight.md` 为准。

## 1. 当前真实状态（务必先读）

| 能力 | 状态 |
| --- | --- |
| `pricing.Quote` 六槽位公式与守恒 | 已实现，有单元测试 |
| 运费计算 `IFreight.Quote` | 已实现，有单元 + 真实库集成测试 |
| 优惠券折扣 `ICoupon.GetDiscount` | 已实现（既有） |
| 订单调用 `pricing.Quote` 落库 | **未接入** |
| `/orders/quote` | **未接入** |
| 下单事务内复核 / 幂等指纹扩展 / 状态流水 | **未接入** |

原因：B1-04-02（SKU 规格快照）与 B1-04-03 共同修改 `internal/logic/order/order.go`，在 B1-04-02 进入 `feat/p2-backend` 共同基线前，B1-04-03 不得修改该文件，以避免并行分支冲突。

因此当前订单仍执行：

```text
original_amount = Σ (skus.price × quantity)
discount_amount = min(coupon.discount_amount, original_amount)
total_amount    = original_amount - discount_amount
shipping_fee    = 0
```

**不要在文档或注释里把「订单已按六槽位计价」写成既定事实。**

## 2. `pricing.Quote` 用法

```go
breakdown, err := pricing.Quote(ctx, &pricing.QuoteRequest{
    OriginalAmount: original, // 服务端重读 skus.price × quantity 的合计
    CouponDiscount: coupon,   // ICoupon 在事务内校验后返回
    ShippingFee:    shipping, // IFreight.Quote 返回
})
if err != nil {
    return err
}
// breakdown.PayableAmount 写入 orders.total_amount
// breakdown.CouponDiscount 写入 orders.discount_amount
// breakdown.ShippingFee   写入 orders.shipping_fee
```

要点：

- **输入必须来自服务端权威数据**。客户端提交的价格、金额、运费一律不参与；
- `MemberDiscount`/`PointsDiscount` 当前必须传 0（没有产生这两个值的业务规则）；
- 输入非法（负数、优惠超商品金额、溢出、超出 `INT UNSIGNED`）会返回 `20010`/`20011`/`20012`，调用方直接透传错误，不要 try-fix；
- `Breakdown.Conserved()` 可用于断言守恒，生产代码不需要重复校验。

## 3. 扩展新折扣类型的正确姿势

以会员折扣（B1-04-08）为例：

1. 在 `pricing.QuoteRequest` 已有 `MemberDiscount` 槽位上产出值，**不要**新增第二套公式；
2. 会员折扣的基数与顺序必须与 `docs/design/pricing.md` §8 一致（当前顺序：券 → 会员 → 积分 → 运费）；
3. 若会员折扣需要读取等级配置，在 `Quote` 内部按需扩展（`ctx` 已预留），不要改调用签名；
4. 补测试：折扣等于商品金额、累计折扣超限、与券叠加、溢出。

反例（禁止）：

```go
// ❌ 在会员模块内自己算应付
payable := original - coupon - original*rate/10000 + shipping
```

```go
// ✅ 通过统一入口
b, err := pricing.Quote(ctx, &pricing.QuoteRequest{
    OriginalAmount: original, CouponDiscount: coupon, MemberDiscount: member, ShippingFee: shipping,
})
```

## 4. 订单接入清单（B1-04-02 进入基线后执行）

按顺序完成，每一步都要有测试：

1. `api/order/v1` 增加 `QuoteReq`/`QuoteRes`（路径 `POST /orders/quote`），响应字段与 `pricing.Breakdown` 对齐；
2. `internal/service/order.go` 增加 `Quote` 方法声明；
3. `internal/logic/order` 实现预览：解析商品/购物车 → 服务端定价 → 券校验 → 运费 → `pricing.Quote`；
4. `Create` 改造：
   - 把 `resolveLine`、券校验、运费读取搬进 `insertOrder` 事务内重新执行；
   - 落库金额使用事务内 `pricing.Quote` 的结果；
   - 事务外只做参数校验与幂等键读取，不产出最终金额；
5. 幂等指纹：`requestHash` 纳入 `source`、`address_id`、`coupon_id`、`sku_id:qty`（已有），并把未来会员/积分参数以稳定顺序追加，保证「同键不同内容」必然冲突；
6. 状态流水：在创建/支付/取消/发货/收货的事务内写 `order_status_logs`，与状态更新同事务；
7. 回归：券预占/释放、库存扣减/恢复、超时取消、退款推进全部重跑。

## 5. 幂等指纹的注意点

当前 `requestHash(source, addressID, couponID, lines)` 的格式是 `source|address_id|coupon_id|sku:qty|...`（按 `sku_id` 升序）。

- 新增计价参数时**追加到固定位置**，不要插在中间（会让历史指纹全部失配，导致同一幂等键在升级后误判冲突）；
- 参数必须包含「会改变最终金额」的全部输入：地址（影响运费）、券（影响折扣）、SKU 与数量（影响商品金额）；
- 不要包含时间、随机数、请求 id 等不稳定值。

## 6. 金额字段与落库

| 字段 | 语义 | 当前是否写入 |
| --- | --- | --- |
| `orders.original_amount` | 商品原始金额 | 是 |
| `orders.discount_amount` | **仅优惠券**折扣 | 是 |
| `orders.shipping_fee` | 运费 | 否（恒 0） |
| `orders.total_amount` | 应付金额 | 是（= original - discount） |

`discount_amount` 的语义**不得**被会员折扣或积分抵扣复用；后续任务需各自新增独立列（若需要快照）。

## 7. 测试

```bash
go test ./internal/logic/pricing/... -count=1
go test ./internal/logic/freight/... -count=1
```

`pricing_test.go` 覆盖：无优惠、仅券、仅运费、多槽位、折扣等于/超过商品金额、累计折扣超限、负金额、nil 请求、溢出、数据库列边界、零金额、守恒篡改检测。

订单集成测试目前**无法执行**（`order.go` 未接入、且需要隔离数据库），接入时按 §4 清单补 `orders/quote` 与事务复核用例。
