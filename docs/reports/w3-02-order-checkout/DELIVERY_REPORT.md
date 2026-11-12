# W3-02 订单试算、结算与订单创建：交付报告

- 任务：SurgeCart 用户商城前端 W3-02（订单试算、结算与订单创建）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W3-02-order-checkout.md`
- 提交说明：代码、开发知识文档与本报告属于同一个提交；提交 SHA 由提交后生成，
  见交付汇报与 `git log -1 --format=%H`（报告无法包含自身提交 SHA）。

---

## 0. 摘要

本期在 W3-01 购物车之上完成结算与下单：

- 新增 `/checkout` 结算页：已勾选商品清单、收货地址选择、金额预估、提交订单、业务错误处理；
- 新增 `/checkout/success` 下单成功页：订单号、服务端应付金额、状态、收货快照、商品明细；
- 购物车「去结算」入口启用；
- 接入真实 `POST /orders`，服务端定价、单事务扣库存并清理已购购物车条目；
- 接入后端幂等键（`uk_user_idempotency` + `request_hash`），超时重试复用同一键；
- 前端不计算、不上送任何订单金额字段。

**关键结论：Go 后端没有独立订单试算接口。** 结算页金额是购物车口径的「预估」，
已明确标注不是后端试算；最终金额以 `POST /orders` 返回的 `total_amount` 为准。
未修改 Go 后端、SQL、contracts、管理端、Docker、Nginx、Kubernetes。

---

## 1. 执行前检查

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| 初始工作区 | clean |
| W3-01 `ad21be2` | 在 HEAD 历史中 |
| 已有 W3-02 文档 | 不存在，本次新建 |
| 独立订单试算接口 | **不存在**（源码无 Preview/Calculate/Quote；真实请求 `/orders/preview` 返回 404） |
| 订单创建接口 | `POST /orders`（服务端定价 + 单事务 + 幂等） |
| 订单详情接口 | `GET /orders/:id`（仅本人，9001 防枚举） |
| 订单状态机 | `pending_payment → paid → shipped → received → completed`；`cancelled/refunded` 终态 |
| 库存扣减 | 订单事务内条件更新 `quantity >= qty`，不足 6001 整体回滚 |
| 购物车处理 | 同事务删除已勾选（已购）条目，未勾选保留 |
| 优惠券 | 有领券/我的券与 `coupon_id` 计算，但无「可用券查询/优惠试算」接口 |

---

## 2. 完成的页面与功能

| 路由 / 位置 | 功能 |
| --- | --- |
| `/checkout`（RequireAuth + 懒加载） | 商品清单（图片/名称/SKU/单价/数量/小计）、收货地址选择（默认优先、可切换、无地址引导、加载/错误态）、金额预估与「非试算」说明、不可购买阻断、库存不足提示、提交中状态、防重复提交、业务错误/网络异常提示 |
| `/checkout/success?order_id=N`（RequireAuth + 懒加载） | 订单提交成功反馈、订单编号、服务端应付金额、订单状态、商品件数、下单时间、收货快照、商品明细、继续购物/查看购物车；缺失/无权查看/加载失败状态 |
| `/cart` | 「去结算」由禁用占位改为真实入口（有不可购买商品或未勾选时保持禁用并说明） |
| 账号隔离 | 结算与成功页均要求登录；未登录跳登录并回跳原目标 |

工程细节：

- `/checkout` 独立 chunk 13.3 kB，`/checkout/success` 6.66 kB；
- 复用 W0 设计系统组件（`PageContainer`/`PriceDisplay`/`Button`/`EmptyState`/`ErrorState`/`LoadingState`），
  未新增设计 Token、未引入第三方依赖。

---

## 3. 使用的订单 API Method + Path

| 功能 | Method + Path | 鉴权 |
| --- | --- | --- |
| 创建订单 | `POST /orders` | Bearer |
| 订单详情（成功页） | `GET /orders/:id` | Bearer |

未使用：`GET /orders`（列表）、`POST /orders/:id/pay`、`cancel`、`receive`、后台接口
——均属于后续阶段能力，本期不接入。

---

## 4. 请求参数与响应结构

### 4.1 `POST /orders`

前端实际发送（`source=cart`，不传价格、不传 sku/qty）：

```json
{
  "source": "cart",
  "address_id": 9,
  "idempotency_key": "1794296699562a1b2c3d4e5f6a7b8c9d"
}
```

字段约束（`api/order/v1/order.go`）：

| 字段 | 约束 | 前端行为 |
| --- | --- | --- |
| `source` | 必填，`cart`/`direct` | 固定 `cart` |
| `address_id` | 必填，必须属于当前用户 | 取结算页选中地址 |
| `idempotency_key` | 必填，≤64 字符 | UUID 去连字符（32 位），同内容重试复用 |
| `sku_id`/`quantity` | `source=direct` 时必填 | 本期不传 |
| `coupon_id` | 可选，0/缺省=不用券 | 本期不传（无试算/可用券接口） |

响应 `data`（真实后端示例，节选）：

```json
{
  "id": 14,
  "order_no": "17942966995628c14e9238f0f",
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
  "created_at": "2026-11-10 15:44:59",
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
  ]
}
```

订单号规则（真实源码）：13 位毫秒时间戳 + 12 位随机 hex。

### 4.2 `GET /orders/:id`

无参数；`data` 为同一 `Order` 结构。不存在或非本人 → 404/9001。

### 4.3 错误码

| code | HTTP | 含义 | 前端文案 |
| --- | --- | --- | --- |
| 9004 | 400 | 购物车无勾选商品 | 购物车中没有勾选的商品，请返回购物车重新选择 |
| 9003 | 409 | 商品下架 / SKU 停售 | 部分商品已下架或规格已停售，请返回购物车调整 |
| 6001 | 409 | 库存不足 | 库存不足，请返回购物车调整数量后重试 |
| 9005 | 400 | 数量非法 | 购买数量不合法，请返回购物车调整 |
| 9006 | 409 | 幂等键冲突 | 订单内容已变化，请刷新结算页后重新提交 |
| 5001 | 404 | SKU 不存在 | 商品规格不存在，请返回购物车调整 |
| 7001 | 404 | 地址不存在/非本人 | 收货地址不存在或已被删除，请重新选择 |
| 1001 | 400 | 参数错误 | 请求参数有误，请检查收货地址与商品后重试 |
| 1002 | 401 | 未授权 | 登录状态已失效，请重新登录 |
| 9001 | 404 | 订单不存在/非本人 | 订单不存在或无权查看 |

---

## 5. Go 后端对应源码位置

| 内容 | 位置 |
| --- | --- |
| 订单契约（Order/OrderItem/CreateReq/DetailReq） | `api/order/v1/order.go` |
| 前台路由（Auth 分组） | `internal/cmd/routes_frontend.go` |
| 控制器 | `internal/controller/order/order.go` |
| 创建逻辑（服务端定价、事务、幂等） | `internal/logic/order/order.go` |
| 服务接口 | `internal/service/order.go` |
| 库存扣减 | `internal/logic/inventory/inventory.go`（`DeductInTx`） |
| 优惠券折扣计算 | `internal/logic/coupon/*`、`api/coupon/v1/coupon.go` |
| 订单表与幂等唯一键 | `internal/migrations/sql/20261001000007_orders.up.sql` |
| 错误码 | `internal/codes/codes.go`（9001–9006、6001、7001、5001、1002） |

前端未修改上述任何文件。

---

## 6. 订单试算接口是否存在

**不存在。**

证据：

1. `api/order/v1/order.go` 只有 Create/List/Detail/Pay/Cancel/Receive 与后台接口，无试算请求类型；
2. `internal/cmd/routes_frontend.go` 未注册任何试算路由；
3. 真实后端调用：
   ```text
   GET /orders/preview   → 404 {"code":9001,"message":"订单不存在"}
   GET /orders/calculate → 404
   GET /orders/quote     → 404
   ```
   （`preview` 被 `/orders/:id` 按 id 解析后查不到订单，返回 9001；语义上仍是无试算能力）

因此：

- 结算页只展示购物车口径的「商品合计（预估）/应付金额（预估）」，并显式标注
  「不是后端订单试算结果」；
- 不调用 `POST /orders` 进行试算；
- 成功页展示的金额来自创建订单响应，标注「由服务端订单事务计算并返回」。

---

## 7. 最终金额由谁计算

**全部由 Go 后端计算，前端不参与。**

`internal/logic/order/order.go` → `Create`：

1. `source=cart` 时读取本人**全部已勾选**购物车条目；
2. 每条重新读取 `skus.price`（不信任购物车快照、不信任客户端价格），
   并校验 SKU enabled、商品 on_shelf；
3. `original_amount = Σ skus.price × quantity`；
4. 若传 `coupon_id`，由 `service.Coupon().GetDiscount` 计算 `discount_amount`；
5. `total_amount = original_amount - discount_amount`；
6. 金额、商品/SKU 名称与主图、地址全部写入订单快照。

真实联调验证：加购后把 SKU 价格从 1000 改到 1500，下单金额为 1500（按最新价），
而不是购物车里的 1000 快照。

前端只展示：

- 结算页：`current_price × quantity` 的预估（标注非试算）；
- 成功页：`order.total_amount`（权威）。

---

## 8. 订单创建事务与库存处理

`insertOrder` 在**单个数据库事务**内完成：

1. `INSERT orders`（订单号、状态 `pending_payment`、金额快照、地址快照、`expire_at = NOW()+支付超时`）；
2. `INSERT order_items`（SKU/商品/图片/成交价/数量快照）；
3. 每个订单项 `Inventory().DeductInTx`：
   ```sql
   UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?
   ```
   受影响行数为 0 → `6001 库存不足`，事务整体回滚；
4. 若使用优惠券：`available → locked` 预占，失败整体回滚；
5. `source=cart`：`DELETE cart_items WHERE user_id=? AND sku_id IN (已购 SKU)`；
6. 唯一键冲突分流：
   - `uk_user_idempotency` → 读回既有订单：`request_hash` 相同返回既有订单，不同 → 9006；
   - `uk_order_no` → 重新生成订单号（最多 3 次）。

真实联调验证：

- 正常下单：订单金额 8000，库存 10→8（SKU1）、10→7（SKU2），未勾选 SKU3 库存保持 10；
- 库存不足（库存 1、数量 2）：返回 409/6001，订单 0 条、库存仍为 1、购物车条目保留；
- 他人地址：404/7001，且不产生订单。

---

## 9. 幂等机制与重试策略

### 9.1 后端

- 唯一键：`uk_user_idempotency(user_id, idempotency_key)`；
- 内容指纹：`request_hash = sha256(source|address_id|coupon_id|按 sku 升序的 sku:qty)`；
- 同键同指纹 → 返回既有订单（200），不重复扣库存/清购物车；
- 同键不同指纹 → 409/9006。

### 9.2 前端

- 幂等键：`crypto.randomUUID()` 去连字符（32 位），无该 API 时用时间戳+随机数兜底；
- 复用条件：地址与已勾选商品行（sku_id + quantity，按 sku 升序）不变；
  内容变化则生成新键，避免 9006；
- 防重复点击：按钮 `isLoading` + `disabled` + 同步 `ref` 锁；
- 超时/网络异常：提示「订单结果未知，订单可能已创建，请勿重复下单；可重试（相同幂等键不会重复创建订单）」，
  同时失效购物车缓存帮助判断；
- 9006：提示刷新结算页，并失效购物车。

### 9.3 边界说明（如实记录）

后端在幂等判定**之前**读取购物车。成功下单后已购条目在同一事务被删除，
因此「成功后直接用同键再提交」会先命中 **9004（购物车无勾选商品）**，
而不是返回既有订单。真实联调验证了「重新放入相同内容后同键重放 → 返回同一订单、库存只扣一次」。

前端不把 9004 解释为「订单一定没创建」，超时提示要求用户同时检查购物车；
不自动重试，避免在结果未知时制造额外订单。

---

## 10. 创建成功后的购物车行为

- 后端在订单事务内删除**已勾选（已购买）**的购物车条目；
- **未勾选条目保留**（真实联调：下单 2 条已选 + 1 条未选，下单后剩 1 条未选）；
- 前端在创建成功后 `invalidateQueries(queryKeys.cart.all)`：
  Header 徽标与购物车页面立即与服务端一致；
- 创建失败（库存不足等）时后端事务回滚，购物车保持不变；前端不做本地删除。

---

## 11. 真实后端联调与 Mock 验证

### 11.1 真实后端（真实 MySQL 8.0 + Redis 7 + 真实 Go 路由）

`go run . serve` 因七牛启动校验（无真实凭据）无法启动，未修改后端；
改用仓库真实初始化 harness + 临时入口完成验证，临时文件均已删除，未进入提交。

临时 Go 用例（5 个，全部 PASS）：

| 用例 | 覆盖 |
| --- | --- |
| `TestW302NoPreviewEndpoint` | 三个候选试算路径均 404 |
| `TestW302CreateFromCartServerPricing` | 服务端定价 8000、状态、库存扣减、已购清理/未购保留、详情查询 |
| `TestW302Idempotency` | 同键同内容同订单、库存只扣一次、订单 1 条；不同内容 9006 |
| `TestW302InsufficientStockRollback` | 6001 整体回滚（订单/库存/购物车） |
| `TestW302ServerPricingAndAddressOwnership` | 最新价计价、他人地址 7001 且不建单 |

临时浏览器用例（真实后端 + 真实数据库，1 个 PASS）：
登录 → 购物车 → 结算 → 默认地址 → 提交 → 成功页（订单号/金额/状态/收货人）→ 购物车为空。

接口记录（真实响应）：

```text
POST /orders → 200 id=14, order_no=17942966995628c14e9238f0f,
                status=pending_payment, total_amount=129900, discount_amount=0
POST /orders（同键同内容）→ 200 id=14（同一订单）
GET  /orders/14 → 200 total_amount=129900
```

### 11.2 Mock（MSW）验证

- 所有 Vitest 与 `npm run test:e2e` 用例运行在 MSW 上；
- Mock 订单实现了服务端定价、幂等键、地址归属、购物车清理与错误注入，
  **但它是测试替身，不构成真实后端联调证据**；
- 仅 Mock 覆盖的场景：提交中状态与防连点、网络异常后同键重试、
  9006 冲突提示、不可购买阻断、成功页缺失/无权查看。

---

## 12. 后端尚未支持的能力（本期未实现，也不伪造）

| 缺口 | 影响 | 当前处理 |
| --- | --- | --- |
| 无独立订单试算接口 | 无法在下单前获得正式金额/优惠/运费 | 仅展示购物车预估并明确标注非试算 |
| 无「订单可用券」查询 | 不知道哪些券适用于当前购物车 | 不接入优惠券选择 |
| 无优惠试算接口 | 无法提前算出优惠后金额 | 不展示优惠金额，页面标注「暂未接入」 |
| 无运费模型/字段 | 无法展示运费 | 不展示虚构运费 |
| 无支付能力（本期范围外） | 成功页不能支付 | 提示支付将在后续阶段开放 |
| 无订单列表/管理（W4） | 超时后无支付中心可核对订单 | 提示用户检查购物车，避免重复下单 |

---

## 13. 关键文件及职责

| 文件 | 职责 |
| --- | --- |
| `web/src/api/orders.ts` | 创建/详情 API 与 `Order`/`OrderItem` 类型 |
| `web/src/api/query-keys.ts` | `orders.all/details/detail` |
| `web/src/hooks/use-checkout.ts` | 下单 mutation、幂等键复用、订单缓存写入、购物车失效 |
| `web/src/utils/idempotency.ts` | UUID 幂等键 + 下单内容指纹 |
| `web/src/utils/orders.ts` | 状态文案、地址/时间格式化、件数 |
| `web/src/pages/checkout-page.tsx` | 结算页（商品/地址/预估/提交/错误） |
| `web/src/pages/checkout-success-page.tsx` | 下单成功页 |
| `web/src/pages/checkout-errors.ts` | 订单错误码文案与错误分类 |
| `web/src/components/checkout/checkout-address-section.tsx` | 收货地址选择 |
| `web/src/components/checkout/checkout-item-list.tsx` | 结算商品清单 |
| `web/src/components/checkout/checkout-summary.tsx` | 金额预估与提交按钮 |
| `web/src/components/cart/cart-summary.tsx` | 购物车「去结算」入口 |
| `web/src/router/index.tsx`、`lazy-pages.tsx` | `/checkout`、`/checkout/success`（RequireAuth + 懒加载） |
| `web/src/mocks/data/orders.ts` | MSW 订单（服务端定价/幂等/清理/延迟与错误注入） |
| `web/src/mocks/handlers.ts`、`test/setup.ts` | 订单接口处理器与逐用例重置 |
| `web/src/api/orders.test.ts` | 订单 API 契约测试 |
| `web/src/pages/checkout-page.test.tsx` | 结算/下单/成功页测试 |
| `web/tests/e2e/checkout.spec.ts` | 结算 E2E（Mock 环境） |
| `.agent/tasks/dev/W3-02-order-checkout.md` | 开发知识文档 |
| `docs/reports/w3-02-order-checkout/DELIVERY_REPORT.md` | 本报告 |

---

## 14. 测试命令与真实执行结果

在 `web/` 目录执行：

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error / 0 warning） |
| `npm run typecheck` | 通过（`tsc -b` 无输出） |
| `npm run test` | 通过：31 个测试文件 / 169 个用例 |
| `npm run build` | 通过：`checkout-page` 13.3 kB、`checkout-success-page` 6.66 kB 独立 chunk |
| `npm run test:e2e` | 通过：36 个 Playwright 用例（Mock 环境），47.8s |

新增测试：

- `web/src/api/orders.test.ts`（6）
- `web/src/pages/checkout-page.test.tsx`（14）
- `web/tests/e2e/checkout.spec.ts`（3）
- 更新：`web/src/pages/cart-page.test.tsx`、`web/tests/e2e/cart.spec.ts`

覆盖对照：

| 要求 | 覆盖 |
| --- | --- |
| 从购物车进入结算 | 单元 + E2E |
| 未选择商品 | 单元 + E2E |
| 地址加载/默认/切换/无地址 | 单元 + E2E |
| 商品信息展示 | 单元 + E2E |
| 正常试算 | **未实现**（后端无试算接口，已在报告标注） |
| 价格变化/库存不足/不可购买 | 真实后端 + 单元 |
| 网络异常 | 单元（同键重试） |
| 正常创建订单 | 单元 + E2E + 真实后端 + 真实浏览器 |
| 提交中/重复点击 | 单元 |
| 服务端业务错误 | 单元（6001/9006） |
| 创建成功页面/金额展示 | 单元 + E2E + 真实后端 |
| 购物车缓存更新 | 单元 + E2E + 真实后端 |
| 失败后状态恢复 | 单元（库存不足保持购物车） |

---

## 15. 已知限制

1. 无试算接口：结算页金额为预估，不含优惠/运费；
2. 优惠券未接入（缺可用券查询与试算）；
3. 运费未展示（后端无字段）；
4. 支付/退款/订单列表未实现（后续阶段）；
5. 成功后响应丢失时，同键重试会先返回 9004（购物车已清空），详见文档；
6. 真实联调用例为临时文件，已删除；仓库未新增 Go 测试（遵守「不修改 Go 后端」）。

---

## 16. 交付物与提交

- 生产代码：新增 10 个文件（订单 API/类型 key/hooks/utils/结算与成功页/地址与清单与摘要组件），
  修改 4 个文件（购物车汇总、路由表、懒加载、query-keys）；
- Mock/测试：新增 `mocks/data/orders.ts` 与 3 个测试文件，
  修改 `mocks/handlers.ts`、`mocks/data/cart.ts`（下单后清理辅助）、`test/setup.ts`、
  `pages/cart-page.test.tsx`、`tests/e2e/cart.spec.ts`；
- 开发知识文档：`.agent/tasks/dev/W3-02-order-checkout.md`
- 正式交付报告：`docs/reports/w3-02-order-checkout/DELIVERY_REPORT.md`
- 分支：`feat/phase2-user-frontend`；单次提交，未 push、未合并、未 force push
- 提交后工作区干净（见交付汇报）
