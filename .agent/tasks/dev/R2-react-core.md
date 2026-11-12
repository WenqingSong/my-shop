# R2：React 商城核心业务契约修复（开发知识文档）

- 分支：`feat/p2-r2-react-core`
- Worktree：`/root/projects/my-shop-worktrees/r2-react-core`
- 基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`（`feat/phase2-ecommerce-business`）
- 范围：订单 DTO、订单金额展示、订单试算、SKU 规格与库存展示、相关 Mock/测试、过期文案
- 不在范围：Go 后端、后台 Admin、Nginx/部署、评价/资料/会话/领券/钱包等新功能、视觉重构
- 事实来源：`api/order/v1/order.go`、`api/product/v1/product.go`、`api/sku/v1/sku.go`、
  `internal/logic/order/order.go`、`internal/logic/pricing/pricing.go`、
  `internal/logic/product/product.go`、`internal/cmd/routes_frontend.go`
- 本文只记录**当前实现与实测证据**，不复制后端设计文档。

---

## 1. 金额口径（唯一事实）

后端唯一计价入口是 `internal/logic/pricing/pricing.go`，六槽位固定顺序：

```
OriginalAmount 商品原价
  - CouponDiscount 优惠券优惠
  - MemberDiscount 会员优惠
  - PointsDiscount 积分优惠（当前恒 0）
  + ShippingFee 运费
= PayableAmount 应付金额
```

前端必须遵守的映射：

| 订单字段 | 含义 | 不要误解为 |
| --- | --- | --- |
| `original_amount` | Σ 订单项 `price × quantity` | 划线原价合计 |
| `discount_amount` | **仅优惠券优惠** | 会员折扣 / 总优惠 |
| `member_discount_amount` | 会员等级折扣 | 优惠券 |
| `shipping_fee` | 运费 | 商品金额 |
| `total_amount` | 应付金额（已含运费、已扣会员优惠） | `original - discount` |
| `pay_type` | `mock` / `balance`；未支付为空串 | 订单状态 |

`total_amount = original_amount - discount_amount - member_discount_amount + shipping_fee`
（积分当前恒 0，orders DTO 也没有积分字段）。

会员折扣由后端 `resolveMemberDiscount` 计算：`floor(original × (10000 - rate_bp) / 10000)`，
并夹紧到 `original - couponDiscount`；前端不参与计算。

订单详情页、成功页、订单卡片统一直接展示上表字段，禁止前端加减。

## 2. 订单试算接入（POST /orders/quote）

契约：`api/order/v1/order.go` QuoteReq / QuoteRes；路由见
`internal/cmd/routes_frontend.go`（`POST /orders/quote` 在 Auth 分组内）。

请求（本次只接购物车链路）：

```ts
{ source: 'cart', address_id, coupon_id? }
```

响应：`original_amount / coupon_discount / member_discount / points_discount /
shipping_fee / payable_amount / items[]`。

实现要点：

1. `web/src/api/orders.ts` 提供 `quoteOrder`，经 `authPostData` 走统一 API Client（组件内无 fetch）。
2. `web/src/hooks/use-order-quote.ts` 用 TanStack Query 管理：
   - Query Key = `['orders','quote',{addressId,couponId,lines}]`；
   - `lines` = 已勾选行 `sku_id:quantity:current_price` 按 sku_id 排序后的字符串；
   - 地址、券、勾选集合、数量、当前单价任一变化 → 新 key → 重新试算；
   - 不使用 `keepPreviousData`：新输入返回前 `isSuccess=false`，页面展示「试算中」，
     不会把上一份金额当成最新结果；
   - 4xx 不自动重试（确定性业务结果），网络/5xx 最多重试一次。
3. 不发起试算的条件：未选地址、没有勾选商品、勾选商品含不可购买项。
4. 提交闸门：必须 `quote != null && !isFetching`；试算失败/进行中时按钮禁用。
5. 失败：金额面板展示 `describeQuoteError` 文案 + 「重新试算」；选券时额外提供
   「不使用优惠券并重试」。不会回退到购物车金额。
6. 成功下单后 `removeQueries(['orders','quote'])`；最终金额使用 `POST /orders` 响应，
   成功页再以 `GET /orders/:id` 校正。
7. 幂等键仍由「地址 + 券 + 商品行」指纹生成，与试算 key 无关。

试算语义（页面文案已明确）：**不检查库存、不锁定优惠券、不创建订单**；
试算成功不代表库存已锁定。下单事务会重新读取权威价格并复核金额。

## 3. SKU 规格与库存

后端商品详情的规格结构（`api/product/v1/product.go`）：

- `attr[]`：`{ attrName, attrValueArr[] }`，规格组与可选值文本；
- `productValue`：键=按 attr 顺序用英文逗号连接的规格值文本（如 `黑色,M`），
  值=`{ unique, sku, sku_id, sku_name, price, original_price, stock, image }`；
- `skus[]`：`api/sku/v1/sku.go` 的 Sku，含 `sku_code / image_url / original_price /
  spec_key / spec_values[]`，**不含 stock**；
- 无规格商品：`attr=[]`、`productValue={}`（`buildSkuSelector` 在 `len(groups)==0` 时直接返回），
  因此无规格商品**没有库存数据**。

前端映射（`web/src/utils/sku.ts`）：

| 需求 | 实现 |
| --- | --- |
| 规格组展示 | 渲染 `attr`，每组一组按钮 |
| SKU 选择 | 选满一组值 → 用 attr 顺序拼 key → `productValue[key].sku_id` → `skus[]` 中真实 SKU |
| 库存 | `buildSkuStockMap(productValue)` 按 `sku_id` 建 Map；`getSkuStock` 返回数字或 null |
| 库存 0 | 后端返回数字 0 → 展示「无货」并禁用加购 |
| 库存未知 | productValue 无该 SKU 条目（含无规格商品）→ 展示「暂无可用的库存数据」，**不当作 0** |
| 价格 / 划线价 | 当前 SKU 的 `price`；`original_price > price` 时展示划线价 |
| 规格图 | `image_url` 非空时作为图册主图，否则回退商品主图 |
| 无规格商品 | 退化为 SKU 列表（按真实 sku_id 选择），不虚构规格维度 |
| 组合缺失 | 选满但 productValue 未命中 → onSelect(null) + 「该规格组合暂不可售」 |
| 加购 | 继续提交真实 `sku_id`；库存明确为 0 时禁用 |

实现细节：`ProductDetailContent` 用 `key={product.id}` 隔离商品；SKU 切换时图册用
`key={selectedSku.id}` 重置，确保规格图优先；`AddToCartPanel` 在 SKU 变化时用
「渲染期调整 state」重置数量，不额外引入 effect。

## 4. Mock 与测试策略

Mock 只用于 Vitest / Playwright（`VITE_ENABLE_MOCK=true`），不进入生产逻辑。

- `src/mocks/data/catalog.ts`：
  - 所有 Product 增加 `freight_template_id`；所有 Sku 补齐 `sku_code / image_url /
    original_price / spec_key / spec_values`；
  - 商品 1 等无规格商品保持 `attr=[] / productValue={}`（与后端一致，库存未知）；
  - 商品 11 增加「颜色 × 尺码」4 个组合，其中 `黑色,L` 库存 0，用于覆盖无货语义；
  - 规格图使用本地 `/mock/product-*.svg`，不依赖外网。
- `src/mocks/data/orders.ts`：
  - 抽出 `resolveSelectedLines` / `computeMockAmounts` / `mockMemberDiscount`，
    订单创建与试算共用同一套金额公式；
  - 新增 `quoteMockOrder`（cart + direct；只读：不锁券、不扣库存、不落库、不清购物车）；
  - 新增测试钩子：`setMockQuoteErrorOnce`、`setMockQuoteRequestDelay`、
    `setMockPricingBaseline`（模拟会员折扣率/运费）、`getMockQuote*`；
  - 订单创建补齐 `member_discount_amount / shipping_fee / pay_type` 与订单项
    `sku_code / spec_text / original_price`；支付后 `pay_type=mock`。
- `src/mocks/handlers.ts`：新增 `POST /api/orders/quote`，支持延迟钩子。
- 测试覆盖（新增/修改）：
  - Quote 成功（六槽位、items、Mock 无副作用、券不锁定）；
  - Quote 失败（4xx 错误文案 + 重试、无客户端回退）；
  - 地址变化重算 + 延迟期间不展示旧金额；
  - 优惠券选择/取消重算；试算拒绝券时可取消用券重试；
  - 购物车数量变化重算；
  - SKU 规格组合、真实 sku_id、库存 0 / 库存未知、原价/现价、规格图、组合缺失；
  - 订单金额明细（优惠券/会员/运费/应付）与订单项快照。

## 5. 已知边界与坑

1. **不能把 productValue 缺失当作 0**：无规格商品后端不返回库存，这是数据缺失，不是无货。
2. **不能用 sku.name 解析规格**：选择必须走 attr 顺序 + productValue 的 sku_id。
3. **试算不是库存校验**：页面不能写「已锁定库存」；下单仍可能因库存不足失败（6001）。
4. **试算金额非最终**：价格可能在试算与下单之间变化，最终以创建订单响应为准。
5. **订单 DTO 无积分字段**：积分只在 Quote 响应中出现且恒 0，订单页不能凭空展示积分优惠。
6. **W4 能力审计报告已过期**：`docs/reports/w4-backend-capability-audit/REPORT.md`
   仍写「没有订单试算接口 / 用户端无库存」，与当前 Go 源码不一致；本次以源码为准，
   未修改该历史报告（不在 R2 文件所有权内）。
7. **数量重置不要用 key 重挂载加购面板**：会导致用户已点按的按钮引用失效；
   使用渲染期 state 调整。
8. **不要在 render 中读 ref**：ESLint `react-hooks/refs` 会报错；改用 state。
