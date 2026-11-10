# W3-02 订单试算、结算与订单创建：开发知识文档

- 任务：SurgeCart 用户商城前端 W3-02（订单试算、结算与订单创建）
- 分支：`feat/phase2-user-frontend`
- 代码位置：`web/`
- 对应正式报告：`docs/reports/w3-02-order-checkout/DELIVERY_REPORT.md`
- 文档依据：接口、事务、金额与幂等行为全部来自仓库内真实 Go 源码，并经真实 MySQL/Redis 联调验证；
  文档中的每条结论都能对应到代码或真实请求记录。
- 边界：本期只做结算与创建；支付、退款、订单列表/管理属于后续阶段，未实现。

---

## 1. 任务边界与前置核查

### 1.1 本期实现

- `/checkout` 结算页：商品清单、收货地址选择、金额预估、下单提交与错误处理；
- `/checkout/success` 下单成功页：订单号、服务端金额、状态、收货快照、商品明细；
- 购物车「去结算」入口启用；
- 订单 API 封装、TanStack Query 缓存（订单详情 + 购物车失效）；
- 幂等键生成、复用与失败重试策略。

### 1.2 明确不做

- 订单试算「接口」（后端没有，前端也不伪造）；
- 优惠券选择与优惠金额展示（缺少可用券查询/优惠试算接口）；
- 运费计算（后端无运费概念）；
- 支付、退款、订单列表/订单管理（W4 范围）；
- `source=direct` 直接购买（本期只从购物车下单）。

### 1.3 前置核查

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| 初始工作区 | clean |
| W3-01 Commit | `ad21be2 feat(web): implement shopping cart with sku add, quantity and header badge` 在 HEAD 历史中 |
| 购物车实现 | `GET /cart`、`POST /cart/items`、`PUT /cart/items/:id`、`selected`、`DELETE`，见 W3-01 文档 |
| 收货地址实现 | `GET /addresses` 等（W2-02），结算页直接复用，不重复开发 |
| 认证与 API Client | 内存 Token + `authGetData/authPostData/...`，401 自动 refresh 一次 |
| **独立订单试算接口** | **不存在**（`api/order/v1/order.go` 无 Preview/Calculate/Quote） |
| 订单创建接口 | `POST /orders`，服务端定价 + 单事务 + 幂等键 |
| 订单状态机 | `pending_payment(10) → paid(20) → shipped(30) → received(40) → completed(50)`；`cancelled(60)`/`refunded(70)` 为终态 |
| 幂等机制 | `uk_user_idempotency(user_id, idempotency_key)` + `request_hash` 比对 |
| 库存扣减 | 订单事务内 `UPDATE inventories SET quantity = quantity - ? WHERE sku_id=? AND quantity >= ?` |
| 购物车处理 | source=cart 时事务内删除**已勾选（已购）**条目，未勾选保留 |
| 已有 W3-02 文档 | 不存在，本次新建 |

Go 源码位置：

- 接口契约：`api/order/v1/order.go`
- 路由：`internal/cmd/routes_frontend.go`（`POST /orders`、`GET /orders`、`GET /orders/:id`、pay/cancel/receive 均在 Auth 分组）
- 控制器：`internal/controller/order/order.go`
- 业务：`internal/logic/order/order.go`
- 服务接口：`internal/service/order.go`
- 库存：`internal/logic/inventory/inventory.go`（`DeductInTx`）
- 优惠券：`internal/logic/coupon/*`、`api/coupon/v1/coupon.go`
- 迁移：`internal/migrations/sql/20261001000007_orders.up.sql`（订单与幂等唯一键）
- 错误码：`internal/codes/codes.go`（9001–9006、6001、7001、5001、1002）

---

## 2. 订单创建链路总览

```text
购物车（服务端 cart_items）
   │ 用户在 /cart 勾选商品
   ▼
/checkout（RequireAuth）
   ├─ GET /cart          展示已勾选商品与「非权威预估金额」
   ├─ GET /addresses     选择收货地址（默认地址优先）
   └─ 点击「提交订单」
        └─ POST /orders { source:"cart", address_id, idempotency_key }
             └─ Go 后端单事务：
                  1. 校验地址归属（7001）
                  2. 读取本人全部已勾选购物车条目
                  3. 重新读取 skus/products 定价与可售性（9003/5001）
                  4. original = Σ skus.price × quantity
                  5. 可选优惠券（coupon_id>0 时校验并计算 discount）
                  6. total = original - discount
                  7. INSERT orders（幂等唯一键）+ INSERT order_items
                  8. UPDATE inventories 扣库存（不足 6001，整体回滚）
                  9. 预占优惠券（可选）
                 10. DELETE 已购（已勾选）cart_items，未勾选保留
             └─ 返回完整 Order（order_no / total_amount / status / items / 地址快照）
        ▼
/checkout/success?order_id=N
   ├─ 先用创建响应写缓存即时渲染
   └─ GET /orders/:id 重新拉取，展示服务端最终数据
```

关键原则：

1. **前端不上送任何价格/金额字段**，只上送地址与幂等键；
2. **正式金额只由后端计算**，前端预告金额是购物车口径；
3. **购物车清理由后端事务完成**，前端只失效缓存；
4. **幂等由后端唯一键保证**，前端防重复点击只是补充。

---

## 3. 订单数据模型

### 3.1 `orders` 表关键字段（`20261001000007_orders.up.sql`）

| 字段 | 含义 | 前端用途 |
| --- | --- | --- |
| `order_no` | 全局唯一订单号（13 位毫秒时间戳 + 12 位随机 hex） | 成功页展示 |
| `status` | DB TINYINT，API 输出字符串枚举 | 成功页展示 |
| `total_amount` | 应付金额（整数分，快照） | 成功页展示（权威） |
| `original_amount` | 优惠前金额 = Σ price×qty | 未展示（后续订单详情可用） |
| `discount_amount` | 优惠金额（无券为 0） | 不展示（本期无券） |
| `idempotency_key` | 幂等键（唯一键 `uk_user_idempotency`） | 前端生成并复用 |
| `request_hash` | 请求内容指纹（sha256） | 判断同键是否同内容 |
| `recipient_name/phone/province/city/district/detail` | 地址快照 | 成功页展示 |
| `address_id` | 地址软引用 | — |
| `expire_at` | 支付截止时间 | 未展示（支付未开放） |
| `items` | `order_items` 商品/SKU/价格快照 | 成功页展示 |

### 3.2 API 出参（真实响应示例）

```json
{
  "id": 14,
  "order_no": "17942966995628c14e9238f0f",
  "user_id": 15,
  "status": "pending_payment",
  "total_amount": 129900,
  "original_amount": 129900,
  "discount_amount": 0,
  "user_coupon_id": null,
  "idempotency_key": "w302-doc-1",
  "recipient_name": "张三",
  "phone": "13800138000",
  "province": "广东省",
  "city": "深圳市",
  "district": "南山区",
  "detail": "科技园路 1 号 101 室",
  "address_id": 9,
  "items": [
    {
      "id": 16,
      "sku_id": 29,
      "product_id": 29,
      "sku_name": "标准款",
      "product_name": "W302联调商品 智能手表",
      "product_main_image": "http://img.example/w302.png",
      "price": 129900,
      "quantity": 1
    }
  ],
  "created_at": "2026-11-10 15:44:59"
}
```

---

## 4. 订单试算：为什么前端只做「预估」

### 4.1 后端没有试算接口（已核实）

- `api/order/v1/order.go` 只有 Create/List/Detail/Pay/Cancel/Receive/Ship/Admin*，没有 Preview/Calculate/Quote；
- 真实调用 `GET /orders/preview` 返回 **404**（响应体 `{code:9001,message:"订单不存在"}`，
  因为被 `/orders/:id` 路由按 id 解析后查不到订单）；
- 结论：**不存在独立试算能力**，任何「正式试算金额」都无法在不创建订单的前提下取得。

### 4.2 因此采用的展示口径

`/checkout` 展示：

```
商品合计（预估） = Σ 已勾选且可购买条目的 current_price × quantity（整数分）
应付金额（预估） = 同上
运费：暂未接入
优惠：暂未接入
```

页面明确标注：

- 「以上为购物车预估，不是后端订单试算结果」；
- 「最终应付金额、优惠与库存由服务端在提交订单时计算并返回」；
- 成功页金额标注「由服务端订单事务计算并返回」。

### 4.3 禁止事项（已遵守）

- 不调用 `POST /orders` 做试算（会真实创建订单、扣库存、清购物车）；
- 不把购物车小计当最终应付金额；
- 不伪造优惠/运费数字；
- 不与后端重复计算「最终价」。

---

## 5. 服务端金额计算与事务

`internal/logic/order/order.go` → `Create`：

1. **参数校验**：`source` 必须 cart/direct；`address_id > 0`；`idempotency_key` 非空；`coupon_id >= 0`；
2. **地址归属**：`service.Address().Detail(ctx, userID, addressID)`，非本人/不存在 → 7001；
3. **组装行项目**：
   - `source=cart`：`service.Cart().List` 取全部条目，过滤 `Selected`，逐条 `resolveLine`；无勾选 → 9004；
   - `resolveLine` 重新读取 `skus.price`，校验 SKU enabled 与商品 on_shelf，否则 9003；SKU 不存在 5001；
   - 快照 SKU 名、商品名、主图；
4. **金额**：`originalAmount = Σ line.Price × line.Quantity`；`totalAmount = originalAmount - discountAmount`；
5. **请求指纹**：`sha256(source|address_id|coupon_id|sku_id:qty,...,按 sku_id 升序)`；
6. **插入事务**（`insertOrder`，单个 `g.DB().Transaction`）：
   - `INSERT orders`（含地址快照、`expire_at = NOW()+支付超时`）；
   - `INSERT order_items`（价格/名称/主图快照）；
   - 逐行 `Inventory().DeductInTx`：`UPDATE ... WHERE quantity >= qty`，0 行 → 6001，整体回滚；
   - 可选优惠券预占（`available → locked`）；
   - `source=cart` 时 `DELETE cart_items WHERE user_id=? AND sku_id IN (已购 sku)`；
7. **唯一键冲突分流**：
   - `uk_user_idempotency` → 读回既有订单：`request_hash` 相同返回既有订单，不同 → 9006；
   - `uk_order_no` → 重新生成订单号（最多 3 次）；
8. 返回 `loadOwnedOrder` 的完整订单。

由此可知：

- **价格以 `skus.price` 为准**，购物车 `price_snapshot` 不参与成交价；
- **库存扣减与订单写入同事务**，库存不足不会留下半成品订单；
- **购物车清理同事务**，不会出现「订单成功但购物车没清」或反之。

---

## 6. 幂等机制与重试策略

### 6.1 后端机制

- 唯一键：`uk_user_idempotency(user_id, idempotency_key)`；
- 内容指纹：`request_hash`（source + address + coupon + 按 sku 升序的 sku:qty）；
- 同键同指纹 → 返回**既有订单**（HTTP 200，不重复扣库存/清购物车）；
- 同键不同指纹 → **409/9006**；
- 幂等键必填，长度受 `VARCHAR(64)` 约束。

### 6.2 前端策略（`src/hooks/use-checkout.ts` + `src/utils/idempotency.ts`）

```text
signature = cart|address_id|0|sku:qty,sku:qty...（按 sku 升序）
第一次提交：key = UUID(去连字符，32 位)，记录 { signature, key }
同 signature 重试：复用同一个 key
内容变化（换地址、改数量、增减商品）：signature 变化 → 生成新 key
```

- 提交中：按钮 `isLoading` + `disabled`，并用同步 `ref` 锁避免渲染延迟导致的连点；
- 超时/网络异常：提示「订单可能已创建，请勿重复下单；可重试（同键不会重复创建订单）」，
  同时失效购物车缓存（若订单已成功，服务端已删条目，用户能看到购物车变化）；
- 9006：提示「订单内容已变化，请刷新结算页后重新提交」，并失效购物车。

### 6.3 重要边界：成功下单后直接重试会得到 9004

按后端执行顺序，`buildLines`（读取购物车）在 `insertOrder`（幂等唯一键判定）之前：

- 第一次创建成功后，事务内已删除已购购物车条目；
- 若此时用同一个 key 再提交，后端首先发现「购物车无勾选条目」→ **9004**，
  而不是命中幂等键返回既有订单；
- 本文档如实记录该行为：前端不会把 9004 解释为「订单一定没创建」，
  超时后的提示语要求用户同时检查购物车。

真实后端联调中验证了「同键同内容（重新放入相同条目后）→ 返回同一订单 id、库存只扣一次」，
以及「同键不同内容 → 9006」。

---

## 7. 结算页实现

路由：`/checkout`（`RequireAuth`，懒加载）。

### 7.1 数据来源

| 区块 | 数据源 | 说明 |
| --- | --- | --- |
| 商品清单 | `GET /cart`（`useCartQuery`） | 只展示 `selected=true` 的条目 |
| 地址 | `GET /addresses`（复用 W2-02 API） | 默认地址优先，可切换 |
| 金额 | `summarizeCart(selectedItems)` | 整数分预估，明确标注非试算 |
| 提交 | `POST /orders` | 只传 `source/address_id/idempotency_key` |

### 7.2 地址选择

- 地址数据由 `useQuery(queryKeys.addresses.list())` 获取，与个人中心共享缓存；
- 选择逻辑（组件外）：
  ```ts
  addresses.find(a => a.id === chosenId) ??
  addresses.find(a => a.is_default) ??
  addresses[0] ?? null
  ```
  无需 effect，避免额外渲染；
- 无地址：展示「还没有收货地址」+ 跳转 `/account/addresses`；
- 地址加载失败：区块内错误 + 重新加载，禁止提交。

### 7.3 提交前置校验

| 条件 | 处理 |
| --- | --- |
| 购物车加载中/失败 | 整页 Loading / ErrorState |
| 没有已勾选商品 | EmptyState「没有勾选的商品」+ 返回购物车 |
| 已勾选商品含 `available=false` | 红色提示 + 禁止提交（后端会 9003） |
| 已勾选商品 `insufficient=true` | 黄色提示，允许提交（库存由后端事务裁决） |
| 未选择地址 | 提示 + 禁用提交 |

### 7.4 错误展示

- 提交错误展示在页面顶部 `role="alert"`（不放在金额卡片内，避免购物车被清空后提示消失）；
- 错误码文案见 `src/pages/checkout-errors.ts`；
- 超时/网络异常会失效购物车缓存，帮助用户判断订单是否已创建。

---

## 8. 下单成功页

路由：`/checkout/success?order_id=N`（`RequireAuth`，懒加载）。

数据流：

1. `POST /orders` 成功后，hook 把响应写入 `queryKeys.orders.detail(order.id)`；
2. 页面 `navigate(..., { replace: true })`，避免回退重复提交；
3. 成功页先用缓存即时渲染，同时 `GET /orders/:id`（`staleTime: 0`）重新拉取；
4. 展示：订单编号、状态、商品件数、下单时间、**服务端应付金额**、收货快照、商品明细；
5. 不提供支付按钮（支付未开放），提示「支付与订单管理将在后续阶段开放」。

异常：

- 缺少/非法 `order_id` → 「订单信息缺失」；
- 404/9001 → 「订单不存在或无权查看」；
- 其它错误 → ErrorState + 重试。

---

## 9. TanStack Query 缓存设计

| Key | 内容 | 失效/写入时机 |
| --- | --- | --- |
| `queryKeys.cart.list()` / `cart.all` | 购物车列表（Header 与页面共享） | 下单成功后 `invalidateQueries(cart.all)` |
| `queryKeys.addresses.list()` | 收货地址 | 复用 W2-02；结算页只读 |
| `queryKeys.orders.detail(id)` | 订单详情 | 创建成功 `setQueryData` + 成功页 `GET /orders/:id` |

要点：

- 下单成功后**必须**失效购物车：后端删除了已购条目，Header 徽标与购物车页要立即一致；
- 订单详情缓存按 id 归一化（`String(id)`），避免路由字符串与数字命中不同缓存；
- 不在组件里 fetch，所有请求封装在 `src/api/*` 与 hooks；
- 不建立第二套本地订单状态。

---

## 10. 优惠券与运费：能力缺口记录

后端已有：

- `GET /my/coupons`：本人已领取券（含状态、门槛、减免、有效期）；
- `POST /coupons/:template_id/claim`：领券；
- `POST /orders` 支持 `coupon_id`，由 `service.Coupon().GetDiscount` 校验归属/有效期/门槛并计算优惠；
- 无券时 `discount_amount=0`。

缺失：

- 没有「订单可用券」查询接口（无法知道哪些券适用于当前购物车金额）；
- 没有优惠试算接口（无法在下单前知道优惠后金额）。

因此本期**不接入优惠券选择**：

- 不传 `coupon_id`（等价于不使用优惠券）；
- 不展示优惠金额，不伪造「优惠已使用」；
- 页面标注「优惠：暂未接入」。

运费：后端订单模型没有运费字段，本期同样不展示虚构运费。

---

## 11. 技术决策与权衡

| 决策 | 选择 | 理由 / 代价 |
| --- | --- | --- |
| 无试算接口时的金额展示 | 购物车预估 + 明确标注 | 不伪造正式试算；代价是用户下单前无法看到最终优惠/运费 |
| 是否继续实现下单 | 实现 | `POST /orders` 在服务端独立完成定价、库存与事务，满足安全条件 |
| 下单来源 | 固定 `source=cart` | 只做购物车结算；direct 购买不在本期范围 |
| 幂等键 | 前端 UUID + 内容指纹复用 | 重试安全；代价是内容变化需换键，9006 需要用户刷新 |
| 提交按钮 | 同步锁 + isPending | 防连点；不宣称「前端等于服务端幂等」 |
| 超时提示 | 明确「结果未知」+ 建议重试 | 避免用户误以为失败后重新下单造成重复购买 |
| 购物车清理 | 依赖后端事务，前端只失效缓存 | 不在前端猜测哪些条目被删；与真实行为一致 |
| 成功页数据 | 先用创建响应，再 GET 详情 | 即时展示 + 服务端最终一致性 |
| 优惠券 | 不接入 | 缺可用券查询与试算接口，前端计算不可信 |

---

## 12. 实际问题与排查过程

1. **Mock 订单号格式与真实后端不一致**
   - 现象：真实联调时订单号是 `1794296658232...`，Mock 生成 `SC...`，导致 E2E 正则失败；
   - 原因：未核对 `generateOrderNo`（13 位毫秒时间戳 + 12 位 hex）；
   - 处理：Mock 改为同格式，测试正则改为 `/^\d{13}[0-9a-f]{12}$/`。

2. **默认地址断言偶发找不到**
   - 原因：购物车与地址是两个独立查询，先等到「确认订单」标题时地址可能仍在加载；
   - 处理：地址断言使用 `findByRole`（等待出现），而不是同步 `getByRole`。

3. **多个「返回购物车」链接导致 strict 匹配失败**
   - 原因：页面头部链接与空状态按钮同名；
   - 处理：空状态动作改为「去购物车勾选」，语义更明确且可区分。

4. **金额文本重复（单价 = 小计）**
   - 原因：数量为 1 时单价与小计相同，`PriceDisplay` 文本重复；
   - 处理：使用 `getAllByText` 断言数量，或用 `within` 限定区块。

5. **网络异常提示语与测试正则不一致**
   - 原因：先写测试正则 `/不会重复下单/`，实现文案是「不会重复创建订单」；
   - 处理：统一文案与测试，明确「不会重复创建订单」。

6. **超时后购物车被失效，页面切到空状态会吞掉错误提示**
   - 原因：错误原本渲染在金额卡片内，条目被清空后卡片不渲染；
   - 处理：把提交错误提升到页面顶部 `role="alert"`，空购物车下仍能看到「结果未知」提示。

7. **`GET /orders/preview` 返回 9001 而不是框架 404**
   - 原因：`/orders/:id` 路由把 `preview` 当作 id 解析，查不到订单返回 9001；
   - 结论：无论响应体如何，都没有试算能力；文档如实记录，避免误读为「有试算接口但参数错」。

8. **真实联调脚本把 refresh token 当 access token**
   - 原因：`isoFrontendLogin` 返回 `(access, refresh)`，多用户场景赋值顺序写错；
   - 处理：修正返回值接收顺序后通过（仅临时测试脚本问题，不涉及产品代码）。

---

## 13. 测试验证

### 13.1 单元测试（Vitest + MSW）

- `src/api/orders.test.ts`（6 个）：401、服务端金额与购物车清理、同键同内容/不同内容、
  9004/7001、订单详情归属与 9001、成功下单后重试 9004；
- `src/pages/checkout-page.test.tsx`（14 个）：
  - 未登录跳转回跳、无勾选空状态、商品与预估金额、默认地址与切换、无地址、
    不可购买阻断、正常创建 + 成功页 + 购物车清理、提交中防连点、
    6001、9006、网络异常后同键重试、成功页缺失/404/详情渲染；
- `src/pages/cart-page.test.tsx`：结算入口改为链接、不可购买时禁用。

### 13.2 E2E（Playwright + MSW）

`tests/e2e/checkout.spec.ts`（3 个）：

- 未登录访问 `/checkout` → 登录 → 回跳；
- 地址创建（两条、第二条默认）→ 商品加购 → 购物车 → 结算 → 切换地址 →
  提交 → 成功页（订单号/金额/状态/收货人）→ 购物车为空；
- 空购物车无结算入口。

全量：36 个 E2E 通过。

### 13.3 真实后端联调（真实 MySQL/Redis）

临时 Go 用例（运行后删除，未提交）：

| 用例 | 结论 |
| --- | --- |
| `TestW302NoPreviewEndpoint` | `/orders/preview|calculate|quote` 均 404，确认无试算接口 |
| `TestW302CreateFromCartServerPricing` | 8000 分订单、pending_payment、库存 8/7/10、购物车仅留未勾选、详情可查 |
| `TestW302Idempotency` | 同键同内容返回同一订单、库存只扣一次、订单 1 条；不同内容 9006 |
| `TestW302InsufficientStockRollback` | 6001 整体回滚：订单 0、库存不变、购物车保留 |
| `TestW302ServerPricingAndAddressOwnership` | 加购后改价按最新价 1500 计价；他人地址 7001 且不建单 |

临时浏览器用例（真实后端 + 真实数据库）：登录 → 购物车 → 结算 → 提交 → 成功页 →
购物车为空，全部通过。

curl/python 记录：

```text
GET  /orders/preview            → 404 {"code":9001,"message":"订单不存在"}
POST /orders (source=cart)      → 200 id=14, order_no=17942966995628c14e9238f0f,
                                  status=pending_payment, total_amount=129900,
                                  original_amount=129900, discount_amount=0
POST /orders（同键同内容重放）   → 200 id=14（同一订单）
GET  /orders/14                 → 200 total_amount=129900
```

---

## 14. 已知限制

1. 无后端试算接口：结算页金额为购物车预估，不含优惠/运费，最终以创建结果为准；
2. 无可用券查询与优惠试算：不接入优惠券，`coupon_id` 不传；
3. 无运费模型：不展示运费金额；
4. 无订单列表/支付/退款：成功页不可继续查看订单或支付（后续阶段）；
5. 成功下单后若响应丢失，直接重试同键会先返回 9004（购物车已清空），
   用户需通过购物车状态判断；前端已在超时文案中提示；
6. 支付截止时间 `expire_at` 未展示（支付未开放）；
7. 真实联调用例为临时文件，验证后删除，仓库未新增 Go 测试（遵守「不修改 Go 后端」）。

---

## 15. 后续维护注意事项

1. **不要新增前端金额计算作为订单金额**：任何最终金额只能取 `order.total_amount`；
2. 后端若新增试算接口：替换 `/checkout` 的预估区块，保留「以后端为准」文案；
3. 接入优惠券前，必须先有「可用券查询 + 优惠试算」接口，否则不要在前端算优惠；
4. 修改 `CreateReq` 时同步更新 `src/api/orders.ts` 与文档；
5. 幂等键策略不要改成「每次点击新 key」——会失去重试保护；
6. 下单成功后必须保留 `invalidateQueries(cart.all)`，否则 Header 徽标会显示已购条目；
7. 成功页只读取 `GET /orders/:id`，不要在页面里推导金额或状态；
8. 若未来实现订单列表/支付，复用 `queryKeys.orders.*`，避免 key 分裂。

---

## 16. 技术知识与面试问题

### 16.1 知识点

- **服务端定价**：客户端只提交「买什么」，价格由服务端重读 SKU 并在事务中快照；
- **事务边界**：订单、订单项、库存、优惠券、购物车清理必须同一事务，保证原子性；
- **条件更新扣库存**：`UPDATE ... WHERE quantity >= ?` 用受影响行数判断库存是否足够，
  避免「先查后扣」的并发超卖；
- **幂等键**：唯一键 + 请求指纹，解决「网络重试导致重复下单」；
- **幂等与购物车清理的顺序**：先读购物车再判幂等，导致成功后的盲目重试返回 9004；
- **缓存一致性**：写操作成功后失效相关查询，避免 Header 与页面数据不一致。

### 16.2 面试问题

1. **为什么不能把购物车小计当作订单应付金额？**
   购物车没有运费/优惠/库存裁决，成交价要由服务端在下单事务中重算并快照。

2. **后端没有试算接口时，前端应该怎么做？**
   只展示非权威预估并明确标注；不调用创建接口试算；记录能力缺口，等待后端补齐。

3. **如何保证重复点击不会创建两笔订单？**
   前端禁用按钮 + 同步锁只是体验层；真正保证是后端幂等键 + 请求指纹。

4. **超时后应该自动重试吗？**
   不应自动重试（结果未知，可能已创建）；应提供手动重试并复用同一幂等键，
   同时提示用户检查购物车/订单状态。

5. **下单事务里为什么要删除购物车条目？**
   避免已购商品留在购物车被重复下单；删除与扣库存同事务，保证「要么都成功，要么都回滚」。

6. **库存不足为什么不会留下半成品订单？**
   扣库存失败返回 6001，事务回滚，订单与订单项都不会落库。

7. **为什么订单号要「时间戳 + 随机段」？**
   可排序、可读，随机段降低撞号概率，唯一键再兜底重试。

8. **如何验证前端没有伪造金额？**
   检查请求体只包含 source/address_id/idempotency_key；页面金额只读接口返回字段；
   联调时对比服务端改价后订单金额是否变化。

---

## 17. 关键文件索引

| 文件 | 职责 |
| --- | --- |
| `web/src/api/orders.ts` | 订单创建/详情 API + 类型 |
| `web/src/api/query-keys.ts` | `orders.all/details/detail` |
| `web/src/hooks/use-checkout.ts` | 下单 mutation、幂等键复用、购物车失效 |
| `web/src/utils/idempotency.ts` | UUID 幂等键 + 下单内容指纹 |
| `web/src/utils/orders.ts` | 状态文案、地址/时间格式化、件数 |
| `web/src/pages/checkout-page.tsx` | 结算页 |
| `web/src/pages/checkout-success-page.tsx` | 下单成功页 |
| `web/src/pages/checkout-errors.ts` | 订单错误码文案 |
| `web/src/components/checkout/checkout-address-section.tsx` | 地址选择 |
| `web/src/components/checkout/checkout-item-list.tsx` | 商品清单 |
| `web/src/components/checkout/checkout-summary.tsx` | 金额预估 + 提交 |
| `web/src/components/cart/cart-summary.tsx` | 购物车「去结算」入口 |
| `web/src/router/index.tsx`、`lazy-pages.tsx` | `/checkout`、`/checkout/success` |
| `web/src/mocks/data/orders.ts`、`mocks/handlers.ts` | MSW 订单（服务端定价/幂等/清理/错误注入） |
| `web/src/api/orders.test.ts` | 订单 API 契约测试 |
| `web/src/pages/checkout-page.test.tsx` | 结算与成功页测试 |
| `web/tests/e2e/checkout.spec.ts` | 结算 E2E（Mock 环境） |
