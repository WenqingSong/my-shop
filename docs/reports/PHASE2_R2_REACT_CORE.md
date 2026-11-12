# Phase 2 · R2：React 商城核心业务契约修复报告

- 任务：R2（SurgeCart / my-shop）
- 分支：`feat/p2-r2-react-core`
- Worktree：`/root/projects/my-shop-worktrees/r2-react-core`
- 集成分支基线：`feat/phase2-ecommerce-business` @ `ddfd5ab77ced617ae0b0020c46a8220396b52145`
- 交付提交：`e96dd9ae31d2031c3e303d03ac2da887f2c9f552`
- 报告范围：仅 `web/**`、本报告与 `.agent/tasks/dev/R2-react-core.md`

> 本文所有后端事实均以当前集成分支中的 Go 源码为准：
> `api/order/v1/order.go`、`api/product/v1/product.go`、`api/sku/v1/sku.go`、
> `internal/logic/order/order.go`、`internal/logic/pricing/pricing.go`、
> `internal/logic/product/product.go`、`internal/cmd/routes_frontend.go`。

---

## 1. 修复前问题

1. **订单 DTO 缺字段**
   - `Order` 缺 `member_discount_amount`、`shipping_fee`、`pay_type`；
   - `OrderItem` 缺 `sku_code`、`spec_text`、`original_price`；
   - 前端注释仍写 `total = original - discount`，与后端六槽位公式冲突；
   - 订单详情/成功页只展示「商品总额、优惠金额、应付金额」，把优惠券优惠与会员优惠混为一谈，
     运费完全缺失。
2. **试算能力遗漏**
   - 前端注释与页面声明「后端没有独立订单试算接口」，但实际已有
     `POST /orders/quote`；
   - 结算页所有金额都是客户端「购物车单价 × 数量」的估算，优惠券优惠也是前端估算；
   - 地址、优惠券、购物车变化不会触发任何服务端金额刷新；
   - 试算失败没有可展示的错误与重试入口（因为根本没有请求）。
3. **SKU 规格与库存展示缺失**
   - `Sku` 类型只有 `id/product_id/name/price/status`；
   - 商品详情只用 `sku.name` 平铺一层按钮，没有 `attr[]` / `productValue` / `spec_values[]`；
   - 页面写死「当前版本未提供公开库存查询接口」；
   - 没有原价/划线价、没有 SKU 规格图。
4. **过期文案**
   - 「后端没有库存查询能力」「SKU 没有规格维度」「后端没有独立试算接口」
     「运费暂未接入」「支付与售后操作将在后续阶段开放」等陈述均与当前源码冲突。
5. **Mock/测试缺口**
   - MSW 的订单与商品 DTO 缺少上述字段；
   - 没有 Quote handler，也没有试算相关测试；
   - 订单金额测试只覆盖「券优惠」一项。

## 2. 修复后行为

### 2.1 订单 DTO 与金额展示

- `Order` 完整声明：`original_amount`、`discount_amount`（**仅券**）、
  `member_discount_amount`、`shipping_fee`、`total_amount`、`pay_type`、
  `user_coupon_id`、全部时间字段与 `items[]`。
- `OrderItem` 完整声明：`sku_code`、`spec_text`、`original_price` 等快照字段。
- 订单详情页金额区固定为五行：
  - 商品总金额（`original_amount`）
  - 优惠券优惠（`-discount_amount`）
  - 会员优惠（`-member_discount_amount`）
  - 运费（`+shipping_fee`）
  - 应付金额（`total_amount`）
  并注明守恒公式和「服务端快照，前端不运算」。
- 下单成功页同步展示券优惠、会员优惠、运费与应付金额（0 值时明确写「未使用优惠券」
  「无会员优惠」，不伪造优惠）。
- 订单列表卡片仍只展示 `total_amount`（应付金额），与详情/成功页语义一致。
- 订单项规格优先展示 `spec_text`，无规格回退 `sku_name`；同时展示 `sku_code`；
  `original_price > price` 时展示划线价。
- 已支付订单展示 `pay_type` 文案（`mock` → Mock 支付，`balance` → 钱包余额支付）。

### 2.2 服务端订单试算

- 结算页 `POST /orders/quote` 接入完成：
  - 未选收货地址、无勾选商品、勾选商品含不可购买项时**不发起**试算；
  - 地址、优惠券、勾选集合、数量、当前单价变化 → Query Key 变化 → 重新试算；
  - 试算进行中展示「正在向服务端试算金额…」，应付金额显示「待服务端试算」；
  - 试算失败展示后端错误文案 + 「重新试算」；选券时提供「不使用优惠券并重试」；
  - 商品清单的单价/小计也只使用 Quote 明细，试算中/失败时不展示客户端金额；
  - 提交按钮只有在「当前输入试算成功且不在刷新中」时才可用；
  - 下单成功后最终金额使用 `POST /orders` 返回的订单快照，成功页再以详情接口校正；
  - 页面明确声明：试算**不检查库存、不锁定优惠券、不创建订单**，成功不代表买到。
- 金额面板固定展示：商品原价、优惠券优惠、会员优惠、积分优惠（后端恒 0）、
  运费、应付金额，并展示守恒公式。

### 2.3 SKU 规格、库存与图片

- 商品详情类型补齐 `attr[]`、`productValue`、`skus[].spec_values[]`、
  `sku_code`、`image_url`、`original_price`、`spec_key`。
- 规格选择改为结构化多维选择：
  - 按 `attr` 顺序渲染规格组（颜色、尺码等）；
  - 选满组合后按 `规格值1,规格值2` 查 `productValue`，取 `sku_id` 映射到真实 SKU；
  - 组合缺失时明确提示「该规格组合暂不可售」；
  - 无规格商品（`attr=[]` / `productValue={}`）退化为 SKU 列表，仍按真实 `sku_id` 选择。
- 加购继续提交真实 `sku_id`；库存明确为 0 时禁用加购并提示无货；
  库存未提供时显示「暂无可用的库存数据」，不当作 0。
- 当前 SKU 价格、划线价（`original_price > price`）、规格图（`image_url` 非空优先，
  否则回退商品主图）均已接入。
- 切换 SKU 时数量与选中规格同步重置；图册以规格图为主图。

## 3. API 契约映射

### 3.1 订单

| 前端 | 后端 | 说明 |
| --- | --- | --- |
| `Order`（`api/orders.ts`） | `api/order/v1/order.go` `Order` | 字段名与 JSON tag 一一对应 |
| `OrderItem` | `OrderItem` | 新增 `sku_code/spec_text/original_price` |
| `PayType` | `pay_type` 字符串枚举 | `'' / mock / balance` |
| `createOrder` | `POST /orders` | 请求/响应不变，仅类型补齐 |
| `quoteOrder` | `POST /orders/quote` | 新增；`OrderQuotePayload / OrderQuote / OrderQuoteItem` |
| `fetchOrders` / `fetchOrder` | `GET /orders` / `GET /orders/:id` | 不变 |
| `payOrder` / `cancelOrder` / `receiveOrder` | `POST /orders/:id/{pay,cancel,receive}` | 不变 |

Quote 请求（当前只调用购物车模式）：

```json
{ "source": "cart", "address_id": 1, "coupon_id": 12 }
```

`source=direct` 的 `sku_code/sku_id/quantity` 已在类型与 Mock 中支持，但当前结算页仍固定
`source=cart`（本期只要求接入购物车结算链路）。

### 3.2 商品 / SKU

| 前端 | 后端 | 说明 |
| --- | --- | --- |
| `Product.freight_template_id` | `Product.FreightTemplateId` | null=未绑定（零运费） |
| `Sku` | `api/sku/v1/sku.go` `Sku` | 新增 `sku_code/image_url/original_price/spec_key/spec_values` |
| `SkuSpecValue` | `SkuSpecValue` | `spec_id/spec_name/spec_value_id/value/sort` |
| `SkuAttr` | `SkuAttr` | `attrName/attrValueArr` |
| `SkuProductValue` | `SkuProductValue` | `unique/sku/sku_id/sku_name/price/original_price/stock/image` |
| `ProductDetail` | `DetailRes` | `skus/attr/productValue` |

## 4. 金额字段映射及公式

后端实际公式（`internal/logic/pricing/pricing.go` + `internal/logic/order/order.go`）：

```
payable_amount = original_amount
               - coupon_discount
               - member_discount
               - points_discount(恒 0)
               + shipping_fee
```

落库到订单：

```
total_amount          = payable_amount
original_amount       = Σ order_item.price × quantity
discount_amount       = coupon_discount        （仅券，不包含会员优惠）
member_discount_amount= member_discount
shipping_fee          = shipping_fee
```

会员折扣：`min(floor(original × (10000 - rate_bp) / 10000), original - coupon)`，
无等级权益（rate_bp=10000）时为 0。

前端展示规则：

| 位置 | 展示字段 |
| --- | --- |
| 结算金额面板 | Quote 的六槽位（含恒定 0 的积分优惠，标注「暂未开放」） |
| 订单详情 | 订单的 `original/discount/member/shipping/total` |
| 下单成功页 | 同上；券/会员为 0 时明确展示「未使用优惠券 / 无会员优惠」 |
| 订单列表 | 仅 `total_amount`（应付金额） |
| 订单项 | `price`、`original_price`（大于现价时划线）、`spec_text`、`sku_code` |

金额仍全部为整数分；前端没有任何浮点货币运算，也没有用 `original - discount`
推导应付金额。

## 5. SKU 规格与库存映射

### 5.1 组合键

后端 `buildSkuSelector`（`internal/logic/product/product.go`）：

- `attr[i].attrName` 为规格组名；
- `productValue` 的键为 `strings.Join(规格值, ",")`，顺序与 `spec_values`（按组 sort）一致；
- 值为真实 SKU 的 `sku_id / price / original_price / stock / image`；
- 无规格商品：`attr=[]`、`productValue={}`；
- 规格值不完整的历史 SKU 不进入 `productValue`，但仍保留在 `skus[]`。

前端 `SkuSelector`：

1. 按 `attr` 顺序维护每组选中值；
2. 选满后 `buildProductValueKey(values)` 拼接；
3. 命中 `productValue[key]` → `skus.find(id === sku_id)` → `onSelect(sku)`；
4. 未选满/未命中 → `onSelect(null)`，不按名称解析、不猜测。

### 5.2 库存

- `skus[]` 没有 `stock`；库存只在 `productValue[].stock`。
- `buildSkuStockMap(productValue)` 以 `sku_id` 为键建立 Map，只收录数字库存。
- `getSkuStock`：有值返回数字（0 也是有效值）；无条目返回 `null`（未知）。
- 无规格商品 `productValue={}` → 所有 SKU 库存未知，页面明确显示「暂无可用的库存数据」，
  **不显示 0 件、不显示无货、不禁用加购**（是否可买以下单校验为准）。
- 已知库存 0 → 显示「该规格当前无货」，禁用加购与数量步进器。
- 已知库存 > 0 → 显示「当前规格库存 N 件」，数量上限取 `min(N, 999)`。

### 5.3 价格、图片、无规格商品

- 价格：选中 SKU 的 `price`；`original_price > price` 时展示划线价。
- 图片：`sku.image_url` 非空 → 图册主图；否则回退 `product.main_image`。
- 无规格商品：`attr` 为空或 `productValue` 为空 → 单层 SKU 列表，标签用
  `spec_values` 拼接（无值回退 `sku.name`），选择仍映射真实 `sku_id`。
- 规格值组合未命中：显示「该规格组合暂不可售，请更换规格」。

## 6. 修改文件清单

新增：

- `web/src/hooks/use-order-quote.ts`
- `web/src/utils/sku.ts`
- `.agent/tasks/dev/R2-react-core.md`
- `docs/reports/PHASE2_R2_REACT_CORE.md`

修改（`web/**`）：

- API/类型：`src/api/orders.ts`、`src/api/orders.test.ts`、`src/api/types.ts`、
  `src/api/query-keys.ts`、`src/api/coupons.ts`
- 结算：`src/pages/checkout-page.tsx`、`src/pages/checkout-page.test.tsx`、
  `src/pages/checkout-coupon.test.tsx`、`src/pages/checkout-errors.ts`、
  `src/components/checkout/checkout-summary.tsx`、
  `src/components/checkout/checkout-item-list.tsx`、
  `src/components/checkout/checkout-coupon-section.tsx`
- 商品详情：`src/pages/product-detail-page.tsx`、`src/pages/product-detail-page.test.tsx`、
  `src/components/product/sku-selector.tsx`、`src/components/product/add-to-cart-panel.tsx`
- 订单展示：`src/pages/account/order-detail-page.tsx`、
  `src/pages/account/order-detail-page.test.tsx`、`src/pages/account/orders-page.test.tsx`、
  `src/pages/checkout-success-page.tsx`、`src/components/order/order-card.tsx`、
  `src/utils/orders.ts`、`src/utils/orders.test.ts`
- 文案/购物车：`src/components/cart/cart-summary.tsx`、`src/utils/cart.ts`、`src/utils/coupons.ts`
- Mock：`src/mocks/data/catalog.ts`、`src/mocks/data/orders.ts`、`src/mocks/handlers.ts`
- E2E：`tests/e2e/checkout.spec.ts`、`tests/e2e/coupons.spec.ts`、
  `tests/e2e/product-detail.spec.ts`
- 文档：`web/README.md`

未修改：`admin/**`、`internal/**`、`api/**`、`migrations/**`、`nginx/**`、部署文件。

## 7. 测试命令及结果

在 `web/` 下执行：

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error / 0 warning） |
| `npm run typecheck` | 通过 |
| `npm run test` | 通过：45 个测试文件、336 个用例 |
| `npm run build` | 通过（tsc -b + vite build） |
| `npx playwright test` | 通过：47 个用例（MSW 环境） |

关键新增覆盖：

1. Quote 成功：六槽位、明细、会员折扣/运费基线、无副作用（不扣库存、不锁券、不建单）。
2. Quote 失败：4xx 业务错误展示、重试成功；失败时不展示客户端金额。
3. 地址变化：重新试算；新试算返回前不展示旧金额。
4. 优惠券变化：选券/取消券都重新试算，金额由 Quote 返回。
5. 购物车变化：数量变化触发新 Quote，行金额与应付同步更新。
6. SKU 规格组合：按组选择、映射真实 `sku_id`、组合缺失提示。
7. SKU 库存：已知库存、0 库存、无库存数据（未知）三种表现。
8. 原价/现价：划线价展示。
9. 订单金额明细：券优惠、会员优惠、运费、应付金额与守恒文案。
10. 无库存数据不误判为 0。

## 8. 未验证内容

- **未做真实后端联调**：本任务运行环境未启动 Go 服务与 MySQL/Redis/Nginx；
  所有前端测试均为 MSW Mock，结果不能描述为真实后端联调结果。
- 未运行 Go 单元测试（任务明确不要求）。
- `source=direct` 的试算仅完成类型/Mock/API 层测试，结算页 UI 仍固定 `source=cart`。
- 会员折扣与运费在 Mock 中通过测试基线模拟；真实值依赖会员等级、运费模板与地区规则，
  未在真实数据上验证。
- 真实库存并发、下单时价格复核与试算差异未在真实环境验证。
- Playwright 使用 `VITE_ENABLE_MOCK=true`，不是真实后端。

## 9. Git 分支和最终 SHA

- 分支：`feat/p2-r2-react-core`
- Worktree：`/root/projects/my-shop-worktrees/r2-react-core`
- 基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`
- 交付提交（代码/测试/文档）：`e96dd9ae31d2031c3e303d03ac2da887f2c9f552`
- 说明：本报告的 SHA 回填提交是紧随其后的元数据提交；分支最终 HEAD 以
  `git rev-parse HEAD` 与远端分支为准。

## 10. Push 状态

- 远端分支：`origin/feat/p2-r2-react-core`
- 已推送，且已校验远端 HEAD 与本地交付提交一致：
  `e96dd9ae31d2031c3e303d03ac2da887f2c9f552`
- SHA 回填元数据提交随后推送（只修改本报告第 9/10 节）。
- 未 force push，未 rebase，未合并其他分支。

## 11. 工作区状态

- 交付时工作区 clean（`git status --short` 无输出），仅存在被 `.gitignore` 忽略的
  `web/node_modules`、`web/dist`、`web/test-results` 等本地产物。
- 未修改 `main`，未修改集成分支，未修改其他 worktree。

## 12. 剩余问题

1. **历史审计报告过期**：`docs/reports/w4-backend-capability-audit/REPORT.md`
   仍写「不存在订单试算/预览」「用户端没有库存查询」等。该报告是 W4 时点的审计记录，
   不在 R2 文件所有权内，本次未修改；以当前 Go 源码与本报告为准。
2. **无「可用券查询」接口**：结算页的券可选性仍是前端预筛选（状态/有效期/门槛），
   权威优惠金额已由 Quote 返回；券被拒绝时提供明确的取消用券重试入口。
3. **无规格商品没有库存数据**：后端 `productValue` 为空，前端只能显示库存未知；
   若业务需要无规格商品库存展示，需要后端扩展商品详情 DTO。
4. **订单 DTO 无积分字段**：`points_discount` 只在 Quote 中出现且恒 0；
   订单详情/成功页无法展示积分抵扣（当前也没有该业务）。
5. **直接购买结算 UI 未接入**：Quote 与 Mock 已支持 `source=direct`，但当前页面没有
   「立即购买」链路（任务要求至少完成购物车结算）。
6. **真实后端验收待执行**：需要在具备 Go 服务与数据的独立环境重跑商品详情、试算、
   下单、订单详情与购物车变化后的重算。
