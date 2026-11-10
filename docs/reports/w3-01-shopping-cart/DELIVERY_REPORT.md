# W3-01 购物车完整业务：交付报告

- 任务：SurgeCart 用户商城前端 W3-01（购物车完整业务）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W3-01-shopping-cart.md`
- 提交说明：代码、开发知识文档与本报告属于同一个提交；提交 SHA 由提交后生成，
  见交付汇报与 `git log -1 --format=%H`（报告无法包含自身的提交 SHA）。

---

## 0. 摘要

本期在 W2 认证与用户中心之上完成购物车：

- 商品详情：显式选择 SKU、数量输入、加入购物车、未登录引导登录并回跳；
- `/cart` 购物车页：列表、图片、SKU、单价、数量、小计、勾选、全选、删除、金额预览、空/错/加载/不可购买状态；
- Header 与移动端导航购物车入口 + 数量徽标（与页面共享同一份缓存，不额外请求）；
- 登录保护、退出登录缓存清理、账号隔离、401 续期沿用既有认证体系；
- 连续修改数量的请求竞争处理（按条目串行 + 目标值合并）。

接口全部来自真实 Go 源码；未修改 Go 后端、SQL、contracts、管理端、Docker、Nginx、Kubernetes。
已执行全部前端质量门禁，并完成真实 Go 后端（真实 MySQL/Redis）的接口级与浏览器级联调。
订单试算、结算与订单创建（W3-02）未实现，购物车「去结算」入口按设计系统要求保持禁用并说明原因。

---

## 1. 执行前检查

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| W2-02 `93d916b` | 在当前 HEAD 历史中 |
| W2-01 `ea0b025` | 在历史中 |
| 初始工作区 | clean |
| 已有 W3-01 文档 | 不存在，本次新建，未覆盖 |
| 购物车 API | 5 个接口全部存在（列表/加购/改数量/勾选/删除） |
| 购物车数据模型 | `cart_items`（`uk_user_sku`、`price_snapshot`、`selected`） |
| 后端批量勾选/批量删除 | **不存在** |
| 后端购物车计数接口 | **不存在** |
| 后端公开库存接口 | **不存在**（库存只在购物车列表返回） |
| 后端订单试算/结算 | 不在本期范围（W3-02） |

---

## 2. 实际完成的页面与功能

| 路由 / 位置 | 功能 |
| --- | --- |
| `/products/:id` | 规格选择（不默认选中，必选校验）、数量步进器（1..999）、加入购物车、加载/成功/失败反馈、未登录「登录后加入购物车」→ 登录 → 回跳详情、无可用 SKU 时禁用并说明 |
| `/cart` | 购物车列表（图片/名称/规格/单价/数量/小计）、勾选与全选、数量修改、删除、金额汇总（预估）、空/加载/错误状态、不可购买三种原因、库存不足、价格变动提示、结算入口禁用说明 |
| Header | 购物车入口（图标 + 数量徽标 + 桌面文案），点击进入 `/cart` |
| 移动端导航抽屉 | 购物车入口（带数量），替换原「W3 开放」占位 |
| 登录态联动 | `/cart` 路由 `RequireAuth`；未登录加购跳登录并回跳；登出/会话失效清理购物车缓存与徽标 |

其他：

- `/cart` 懒加载（独立 chunk，构建产物 `cart-page-*.js` 约 8.1 kB）；
- 购物车页面复用 W0 设计系统（`PageContainer`、`PriceDisplay`、`Button`、`EmptyState`、`ErrorState`、`LoadingState`、语义 Token），未新增设计 Token、未引入第三方依赖；
- 详情页「显示库存状态」说明保留，因为后端没有公开库存接口，不虚构库存数字。

### 2.1 有意的行为变更

商品详情页原先默认选中第一个 SKU，W3-01 改为**默认不选中**：

- 后端 `AddReq.SkuId` 必填（缺失或 ≤0 → 400）；
- 默认选中会让「SKU 必选校验」不可观测，也容易误加购错误规格；
- 已同步更新既有单元测试与 E2E 断言（`product-detail-page.test.tsx`、`product-detail.spec.ts`）。

---

## 3. API Method + Path

| 功能 | Method + Path | 鉴权 |
| --- | --- | --- |
| 购物车列表 | `GET /cart` | Bearer |
| 加入购物车 | `POST /cart/items` | Bearer |
| 修改数量 | `PUT /cart/items/:id` | Bearer |
| 勾选 / 取消勾选 | `PUT /cart/items/:id/selected` | Bearer |
| 删除条目 | `DELETE /cart/items/:id` | Bearer |

前端统一经 `/api` 前缀访问（`src/api/client.ts`），开发环境由 Vite 代理剥离 `/api`
（`vite.config.ts`）；后端路由本身不带前缀（`internal/cmd/routes_frontend.go`）。

---

## 4. 请求参数与响应结构

统一响应包装（`internal/middleware/response.go`）：`{ code, message, data }`；
成功 `code=0`；失败为非 2xx + `code=<业务码>` + `data=null`。

### 4.1 `GET /cart`

无参数。`data`：

```json
{
  "items": [
    {
      "id": 18,
      "sku_id": 12,
      "quantity": 3,
      "selected": false,
      "price_snapshot": 129900,
      "product_id": 12,
      "product_name": "联调测试商品 5G 手机",
      "product_main_image": "http://img.example/w301.png",
      "sku_name": "标准款",
      "current_price": 129900,
      "price_changed": false,
      "available": true,
      "unavailable_reason": "",
      "stock": 20,
      "insufficient": false,
      "created_at": "2026-11-10 15:22:53",
      "updated_at": "2026-11-10 07:22:54"
    }
  ]
}
```

字段说明：

- `available=false` 时 `unavailable_reason` 为 `sku_deleted` / `disabled` / `off_shelf`；
- `insufficient = available && quantity > stock`，无库存记录视为 0；
- `price_changed = current_price !== price_snapshot`；
- SKU 被删除时 `product_id`、`current_price`、`stock` 为 `null`，名称字段为空串。

### 4.2 `POST /cart/items`

请求：

```json
{ "sku_id": 12, "quantity": 2 }
```

- `sku_id` 必填，`≤0` → 400/1001；
- `quantity` 可缺省（默认 1），必须为 1..999 的整数，否则 400/8002；
- 重复加购同一 SKU 在数据库层原子累加，累加后 >999 整体回滚并返回 400/8002。

`data`：单个 `CartItem`（结构同上，扁平对象）。

### 4.3 `PUT /cart/items/:id`

请求：

```json
{ "quantity": 3 }
```

- `quantity` 必须为 1..999 的整数，否则 400/8002；
- 按 `id AND user_id` 定位，未命中 → 404/8001；值未变化也视为成功。

`data`：单个 `CartItem`。

### 4.4 `PUT /cart/items/:id/selected`

请求：

```json
{ "selected": false }
```

`data`：单个 `CartItem`。未命中 → 404/8001；值未变化幂等成功。

### 4.5 `DELETE /cart/items/:id`

无请求体。`data`：

```json
{}
```

不存在 / 已删除 / 非本人 → 404/8001。

### 4.6 错误码

| code | HTTP | 含义 |
| --- | --- | --- |
| 1002 | 401 | 未授权（无/失效 Token） |
| 5001 | 404 | SKU 不存在 |
| 8001 | 404 | 购物车条目不存在或不属于当前用户 |
| 8002 | 400 | 数量非法（非正整数 / 超 999） |
| 8003 | 409 | 商品下架或 SKU 禁用，不可加购 |

---

## 5. 对应 Go 源码位置

| 内容 | 位置 |
| --- | --- |
| 接口契约（CartItem / 5 组 Req & Res） | `api/cart/v1/cart.go` |
| 前台路由注册（`middleware.Auth` 分组） | `internal/cmd/routes_frontend.go` |
| 控制器（Principal → userID，缺失 401） | `internal/controller/cart/cart.go` |
| 业务逻辑（加购原子累加、可购性、库存、价格快照） | `internal/logic/cart/cart.go` |
| 服务接口与注册 | `internal/service/cart.go` |
| 表结构迁移 | `internal/migrations/sql/20261001000006_cart_items.up.sql` |
| 业务错误码 | `internal/codes/codes.go`（8001/8002/8003、5001、1002） |
| 设计说明 | `docs/design/cart.md` |

前端未修改上述任何文件。

---

## 6. 真实后端联调情况

环境：Linux + 本机 Docker（`mysql:8.0`、`redis:7-alpine`）+ Go 1.26 + 真实仓库路由。
说明：`go run . serve` 启动阶段会校验七牛 bucket（无真实凭据时 fail-fast），因此**未修改后端**，
改用以下两种方式完成真实联调，临时文件在验证后删除，未进入提交。

### 6.1 接口级联调（真实 MySQL/Redis + 真实路由）

使用仓库 `test/e2e` 的真实初始化（`migrations.Up` + `boot.Bootstrap` + `RegisterFrontendRoutes`），
编写临时 Go 用例 4 个并全部通过：

| 用例 | 覆盖内容 | 结果 |
| --- | --- | --- |
| `TestW301CartRequiresAuth` | 无 Token `GET /cart` → 401/1002 | PASS |
| `TestW301CartAddAccumulateAndLimits` | 加购字段/快照价/联查字段/库存、重复加购累加、累加超 999 回滚、数量 0 与 1000 拒绝、修改成功 | PASS |
| `TestW301CartUnavailableAndDynamicFlags` | 库存不足、价格变化、SKU 禁用、商品下架、禁用/下架加购 409/8003、SKU 删除后悬空条目 | PASS |
| `TestW301CartSelectionDeleteAndIsolation` | 跨用户不可见、跨用户改数量/勾选/删除 404/8001、勾选持久化、删除后列表为空、重复删除 8001 | PASS |

curl 级真实响应（节选）：

```text
POST /cart/items {"sku_id":12,"quantity":2} → 200 code=0 data.quantity=2 data.stock=20
POST /cart/items {"sku_id":12,"quantity":1} → 200 data.quantity=3（表内仍 1 行）
PUT  /cart/items/18 {"quantity":0}          → 400 code=8002 message=数量非法
DELETE /cart/items/18                       → 200 data={}
DELETE /cart/items/18（再次）                → 404 code=8001
GET  /cart（无 Token）                       → 401 code=1002
```

### 6.2 浏览器级联调（真实后端 + 真实数据库）

临时启动一个仅注册前台路由、跳过七牛校验的临时入口（未修改仓库后端代码），
配合未启用 MSW 的 Vite dev server（`/api` 代理到 8080），执行临时 Playwright 用例：

- 详情页 → 「登录后加入购物车」→ 登录 → 回跳 `/products/12`；
- 选择规格 → 加购 → 成功提示 → Header 徽标 1 件；
- 进入 `/cart` → 单价/小计/预估金额 1,299；
- 数量 +1 → 2,598；取消勾选 → 0 件；全选 → 2,598；
- 删除 → 空购物车。

结果：`1 passed`。临时入口、临时 Playwright 配置与用例均已删除。

### 6.3 联调中观察到的后端现象

- `created_at` 与 `updated_at` 时区表现不一致（如 `15:22:53` / `07:22:54`）；
  购物车页面不展示这两个字段，对功能无影响，记录为后端观察项；
- `inventories.sku_id` 存在外键 `fk_inventories_sku`（ON DELETE RESTRICT），
  删除 SKU 前必须先删除库存记录；这也意味着真实环境「SKU 被删除」场景较少出现，
  但前端仍按后端字段完整支持 `sku_deleted` 展示。

---

## 7. 仅通过 Mock 验证的功能

以下功能在 **MSW（Vitest / Playwright Mock 环境）** 中验证，不构成真实后端联调证据：

| 功能 | 说明 |
| --- | --- |
| 商品下架 / SKU 停售 / SKU 删除的不可购买展示 | 真实环境难以稳定构造，Mock 提供 `markMockProductOffShelf` / `markMockSkuDisabled` / `markMockSkuDeleted` |
| 库存不足提示 | Mock 提供 `setMockSkuStock` |
| 数量连续修改的请求竞争 | Mock 提供可配置响应延迟（`setMockCartRequestDelay`） |
| 加载失败与重试 | MSW 覆盖 `GET /cart` 返回 500 |
| 退出登录缓存清理 | 前端缓存行为，Mock 环境即可完整验证 |
| 全选部分失败回滚 | 依赖 Mock 组合场景（当前用例覆盖成功路径与失败提示逻辑） |

`npm run test:e2e` 同样是 Mock 环境（`VITE_ENABLE_MOCK=true`），用于验证 UI 流程与交互，
不能等同真实后端联调。

---

## 8. 后端尚未提供的能力（本期未实现，也不在前端伪造）

| 缺口 | 影响 | 当前前端处理 |
| --- | --- | --- |
| 无批量勾选接口 | 全选需 N 次 `PUT /cart/items/:id/selected` | `Promise.allSettled` 并发 + 失败聚合 + 刷新回滚 |
| 无批量删除 / 清空购物车接口 | 只能逐条删除，无「清空」入口 | 不提供清空按钮，避免 N 次删除的误导性交互 |
| 无购物车计数接口 | 徽标数量需由列表汇总 | Header 与页面共享 `GET /cart` 缓存，只请求一次 |
| 无公开库存接口 | 详情页无法展示库存、无法预校验库存 | 详情页保留「库存以下单时校验为准」，购物车展示后端 `stock/insufficient` |
| 无订单试算 / 结算接口接入 | 无法计算最终应付金额 | 「去结算」禁用并说明 W3-02 开放；预估金额明确标注为预览 |
| SKU 无规格维度 | 只有单层 SKU 名称 | 单层规格选择，不虚构颜色/尺寸矩阵 |
| `updated_at` 时区表现异常 | 无（前端未使用） | 不展示时间字段，记录观察项 |

---

## 9. 关键文件及职责

| 文件 | 职责 |
| --- | --- |
| `web/src/api/cart.ts` | 5 个接口封装、`CART_QUANTITY_MIN/MAX`、`CartListResult` |
| `web/src/api/types.ts` | `CartItem` DTO（字段与 Go JSON tag 一致） |
| `web/src/api/query-keys.ts` | `queryKeys.cart.all` / `cart.list` |
| `web/src/hooks/use-cart.ts` | 查询、徽标计数、加购、数量串行队列、勾选/全选、删除、缓存合并策略 |
| `web/src/utils/cart.ts` | 单价/小计/汇总纯函数、不可购买原因文案（整数分） |
| `web/src/pages/cart-page.tsx` | `/cart` 页面（列表 + 汇总 + 状态） |
| `web/src/pages/cart-errors.ts` | 购物车错误码 → 用户文案 |
| `web/src/components/cart/quantity-stepper.tsx` | 数量步进器（详情页/购物车共用） |
| `web/src/components/cart/cart-item-row.tsx` | 购物车商品行（勾选/单价/数量/小计/删除/状态） |
| `web/src/components/cart/cart-summary.tsx` | 金额汇总 + 结算占位说明 |
| `web/src/components/product/add-to-cart-panel.tsx` | 详情页加购区域（SKU 必选/登录引导/反馈） |
| `web/src/components/layout/cart-entry.tsx` | Header 购物车入口 + 数量徽标 |
| `web/src/components/layout/site-header.tsx` | 用真实入口替换禁用占位 |
| `web/src/components/layout/mobile-nav.tsx` | 移动端导航购物车入口 |
| `web/src/pages/product-detail-page.tsx` | 规格不默认选中、价格面板、接入加购面板 |
| `web/src/auth/use-auth.ts` / `use-auth-cache-reset.ts` | 登出/会话失效清理购物车缓存 |
| `web/src/router/index.tsx` / `lazy-pages.tsx` | `/cart` 路由（`RequireAuth` + 懒加载） |
| `web/src/mocks/data/cart.ts` / `mocks/handlers.ts` / `test/setup.ts` | MSW 购物车状态、处理器、逐用例重置 |
| `web/src/api/cart.test.ts` | 接口契约测试（Mock） |
| `web/src/pages/cart-page.test.tsx` | 页面行为、并发、认证清理、共享请求测试 |
| `web/src/utils/cart.test.ts` | 金额与汇总纯函数测试 |
| `web/src/pages/product-detail-page.test.tsx` | 加购、SKU 必选、登录回跳、无 SKU 禁用 |
| `web/tests/e2e/cart.spec.ts` | 购物车 E2E（Mock 环境，含移动端） |
| `web/tests/e2e/product-detail.spec.ts` | 详情页 E2E（同步 SKU 选择变更） |
| `.agent/tasks/dev/W3-01-shopping-cart.md` | 开发知识文档 |
| `docs/reports/w3-01-shopping-cart/DELIVERY_REPORT.md` | 本报告 |

---

## 10. 测试命令与真实结果

在 `web/` 目录执行：

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error / 0 warning） |
| `npm run typecheck` | 通过（`tsc -b` 无输出） |
| `npm run test` | 通过：29 个测试文件 / 149 个用例全部通过 |
| `npm run build` | 通过：`tsc -b && vite build`，产出含独立 `cart-page-*.js` chunk |
| `npm run test:e2e` | 通过：33 个 Playwright 用例（Mock 环境），42.5s |

新增/更新的测试文件：

- 新增：`web/src/api/cart.test.ts`、`web/src/utils/cart.test.ts`、`web/src/pages/cart-page.test.tsx`、`web/tests/e2e/cart.spec.ts`
- 更新：`web/src/pages/product-detail-page.test.tsx`、`web/tests/e2e/product-detail.spec.ts`

覆盖情况（对任务要求逐项）：

| 要求 | 覆盖 |
| --- | --- |
| 加入购物车 | 单元（详情页 + API）、E2E、真实后端浏览器 |
| SKU 必选校验 | 单元（未选择规格不发请求） |
| 数量修改 | 单元 + E2E + 真实后端 |
| 商品删除 | 单元 + E2E + 真实后端 |
| 商品选择 / 全选 | 单元 + E2E + 真实后端 |
| 金额展示 | 纯函数 + 页面 + 真实后端 |
| 空购物车 | 单元 + E2E + 真实后端 |
| 商品不可购买状态 | 单元（下架/停售/SKU 删除/库存不足） |
| 登录保护 | 单元 + E2E + 真实后端 |
| 退出登录缓存清理 | 单元 |
| Header 购物车数量 | 单元（含共享请求断言）+ E2E + 移动端 E2E |
| 连续修改数量请求竞争 | 单元（延迟 Mock + 请求序列断言） |

---

## 11. 技术要点

- **数量并发**：按条目串行 + 目标值合并；旧响应与当前目标不一致时丢弃；只合并本次操作字段；
  失败回滚并 `invalidateQueries`。连续点击通常 2 次请求即可收敛到最终值。
- **缓存共享**：Header 与页面共用 `queryKeys.cart.list()`，页面内只发生 1 次 `GET /cart`
  （测试断言 `getMockCartListRequestCount() === 1`）。
- **认证联动**：查询 `enabled: hasSession`；登出与会话失效都 `removeQueries(cart)`；
  `patchCartData` 在缓存不存在时不创建，防止在途响应把上一账号数据写回。
- **金额边界**：整数分、`PriceDisplay` 展示；仅汇总「已勾选 + available」；
  明确提示「以订单试算为准」，不计算优惠/运费/成交价。

---

## 12. 已知限制

1. 全选为 N 次单条请求（后端无批量接口），条目多时请求数线性增长；
2. 无「清空购物车」能力；
3. 详情页无法展示库存（后端无公开库存接口）；
4. 「去结算」禁用，订单试算/结算/订单创建属于 W3-02，未实现；
5. 详情页不再默认选中 SKU（有意变更，已记录并同步测试）；
6. 真实后端联调用例为临时文件，验证后删除；仓库中未新增 Go 测试（遵守「不修改 Go 后端」）。

---

## 13. 交付物清单

- 生产代码：新增 10 个文件（API/类型 key/hooks/utils/页面/组件/入口），修改 9 个文件
  （query-keys、types、认证缓存两处、Header、移动端导航、详情页、路由表、懒加载）
- 测试与 Mock 基础设施：新增 4 个测试文件 + `mocks/data/cart.ts`，
  修改 `mocks/handlers.ts`、`test/setup.ts`、`product-detail-page.test.tsx`、
  `tests/e2e/product-detail.spec.ts`
- 开发知识文档：`.agent/tasks/dev/W3-01-shopping-cart.md`
- 正式交付报告：`docs/reports/w3-01-shopping-cart/DELIVERY_REPORT.md`
- 分支：`feat/phase2-user-frontend`（单次提交，未 push、未合并、未 force push）
