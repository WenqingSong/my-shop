# W3-01 购物车完整业务：开发知识文档

- 任务：SurgeCart 用户商城前端 W3-01（购物车完整业务）
- 分支：`feat/phase2-user-frontend`
- 代码位置：`web/`
- 对应正式报告：`docs/reports/w3-01-shopping-cart/DELIVERY_REPORT.md`
- 文档依据：本文件描述的每个行为都对应仓库中真实可运行的代码与真实 Go 源码；接口字段、状态码、
  业务限制均以 Go 源码 + 真实后端（MySQL/Redis）联调结果为准。
- 边界：本期只做购物车；订单试算、结算、订单创建属于 W3-02，未实现。

---

## 1. 任务边界与前置核查

### 1.1 本期范围

实现：

- 商品详情：显式选择 SKU、输入数量、加入购物车（含未登录引导登录并回跳）；
- 购物车页面 `/cart`：列表、图片、名称、SKU、单价、数量、小计、勾选、全选、删除、金额汇总、空/错/加载/不可购买状态；
- Header 与移动端导航购物车入口 + 数量徽标；
- 购物车与认证状态协作：登录保护、登出清理、账号隔离、401 处理；
- 数量连续修改的请求竞争处理。

不实现：

- 订单试算、结算页、订单创建（W3-02）；
- 优惠券、运费、库存最终裁决（后端能力，前端不臆造）；
- 批量勾选/批量删除的后端接口（后端未提供，前端用 N 次单条请求实现全选）；
- 清空购物车（后端无该接口）。

### 1.2 前置核查结果

| 检查项 | 结果 |
| --- | --- |
| 当前分支 | `feat/phase2-user-frontend` |
| W2-02 Commit | `93d916b feat(web): implement user center, addresses and favorites` 在当前 HEAD 历史中 |
| W2-01 Commit | `ea0b025` 在历史中 |
| 初始工作区 | clean |
| 商品详情与 SKU | 已有 `product-detail-page.tsx` + `sku-selector.tsx`；原实现默认选中第一个 SKU，购物车按钮是禁用占位 |
| 认证与 Token | `useAuthStatus` + 内存 Token Store + `authGetData/authPostData/...` 的 401 refresh 包装 |
| TanStack Query | `src/api/query-client.ts`、`src/api/query-keys.ts`、测试用 `createTestQueryClient` |
| API Client | `src/api/client.ts`（统一 `ApiError`）+ `src/api/envelope.ts`（`{code,message,data}` 解包） |
| Go 购物车 API | 5 个接口全部存在：`GET /cart`、`POST /cart/items`、`PUT /cart/items/:id`、`PUT /cart/items/:id/selected`、`DELETE /cart/items/:id` |
| 购物车数据模型 | `cart_items` 表（`uk_user_sku` 唯一键，`price_snapshot` 快照价，`selected` 持久化） |
| 已有 W3-01 文档 | 不存在，本次新建，未覆盖任何已有成果 |

Go 源码位置：

- 接口契约：`api/cart/v1/cart.go`（`CartItem`、`ListReq/Res`、`AddReq/Res`、`UpdateQuantityReq/Res`、`UpdateSelectedReq/Res`、`DeleteReq/Res`）
- 路由：`internal/cmd/routes_frontend.go`（`/cart` 全部挂在 `middleware.Auth` 分组内）
- 控制器：`internal/controller/cart/cart.go`（从 `Principal` 取 `userID`，缺失返回 401）
- 业务：`internal/logic/cart/cart.go`
- 服务注册：`internal/service/cart.go`
- 迁移：`internal/migrations/sql/20261001000006_cart_items.up.sql`
- 错误码：`internal/codes/codes.go`（8001/8002/8003，另外用到 5001、1002）
- 设计说明：`docs/design/cart.md`

---

## 2. 购物车在 SurgeCart 中的业务位置

购物车是「浏览 → 加购 → 结算 → 下单」链路的中间态：

```text
商品列表 / 商品详情（公开接口）
        │  POST /cart/items（需登录）
        ▼
   cart_items（服务端权威数据）
        │  GET /cart（联查 SKU/商品/库存）
        ├──────────────► Header 徽标（同一份缓存）
        ├──────────────► /cart 页面（勾选/数量/删除/金额预览）
        ▼
   W3-02 订单试算（后端计算优惠/运费/最终价）→ 创建订单
```

关键定位：

- 购物车不是订单：它只保存「用户想买什么」，不锁库存、不锁价格、不产生应付金额；
- 购物车不是本地状态：这是后端持久化数据，刷新/换设备后仍在（同一账号）；
- 购物车是用户私有数据：所有接口都按 `user_id` 过滤，前端缓存也必须随登录态清理。

---

## 3. 后端数据模型

### 3.1 表结构（`20261001000006_cart_items.up.sql`）

```sql
CREATE TABLE cart_items (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        BIGINT UNSIGNED NOT NULL,
  sku_id         BIGINT UNSIGNED NOT NULL,
  quantity       INT UNSIGNED    NOT NULL,
  price_snapshot INT UNSIGNED    NOT NULL,
  selected       TINYINT         NOT NULL DEFAULT 1,
  created_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_sku (user_id, sku_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

设计要点：

| 设计 | 含义 | 前端后果 |
| --- | --- | --- |
| `uk_user_sku` | 同一用户同一 SKU 至多一条 | 重复加购是「累加」而不是新增行；列表没有重复 SKU |
| 无外键（软引用） | SKU 被物理删除后条目保留 | 列表可能出现 `sku_deleted` 的悬空条目，必须能展示「不可购买」 |
| `price_snapshot` | 加购时的 `skus.price`（整数分） | 只用于「加购价」提示，不参与小计与预估金额 |
| `selected` 持久化 | 勾选状态存服务端 | 前端勾选不是纯 UI 状态，刷新后保持一致 |
| `quantity` 上限 999 | 业务常量 `maxQuantity` | 前端步进器同上限，后端仍会校验 |

### 3.2 接口返回的 CartItem

`GET /cart`、`POST /cart/items`、`PUT /cart/items/:id`、`PUT /cart/items/:id/selected`
返回的都是同一种结构（`api/cart/v1/cart.go`），字段经服务端 LEFT JOIN 实时计算：

```json
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
```

字段语义（前端 `src/api/types.ts` 一一对应，不做 camelCase 映射）：

- `available` / `unavailable_reason`：`true/""`、`false/sku_deleted`、`false/disabled`、`false/off_shelf`；
- `insufficient`：`available && quantity > stock`；没有库存记录视为 0；
- `price_changed`：`current_price !== price_snapshot`；
- SKU 被删除时 `product_id`、`current_price`、`stock` 为 `null`，名称字段为空串。

> 观察：真实后端联调中 `created_at` 与 `updated_at` 的时区表现不一致（15:22 vs 07:22）。
> 购物车页面不展示这两个时间字段，因此不影响功能；已在交付报告记录为后端观察项。

### 3.3 五个接口与限制（真实源码 + 真实联调）

| 功能 | Method + Path | 请求体 | 成功响应 `data` | 业务限制 |
| --- | --- | --- | --- | --- |
| 列表 | `GET /cart` | 无 | `{ "items": [CartItem] }` | 无分页，按 `id` 升序返回本人全部条目 |
| 加购 | `POST /cart/items` | `{ "sku_id": 12, "quantity": 2 }` | `CartItem`（扁平） | `sku_id` 必填；`quantity` 缺省 1、范围 1..999；重复加购累加，累加后超 999 整体回滚 |
| 改数量 | `PUT /cart/items/:id` | `{ "quantity": 3 }` | `CartItem` | 1..999；按 `id AND user_id` 定位，未命中 404 |
| 勾选 | `PUT /cart/items/:id/selected` | `{ "selected": false }` | `CartItem` | 幂等（值未变化也返回成功） |
| 删除 | `DELETE /cart/items/:id` | 无 | `{}` | 物理删除；不存在/已删除/属他人统一 404 |

错误码（`internal/codes/codes.go`）：

| code | HTTP | 含义 | 前端文案（`src/pages/cart-errors.ts`） |
| --- | --- | --- | --- |
| 1002 | 401 | 未授权（无/失效 Token） | 登录状态已失效，请重新登录 |
| 5001 | 404 | SKU 不存在 | 商品规格不存在，请重新选择 |
| 8001 | 404 | 条目不存在或不属于当前用户 | 条目已不存在，可能已在其他页面删除 |
| 8002 | 400 | 数量非法（<1 / >999 / 非整数） | 数量需为 1–999 之间的整数 |
| 8003 | 409 | SKU 禁用或商品下架，不可加购 | 商品已下架或规格已停售 |

真实联调记录（本机真实 Go 路由 + 真实 MySQL/Redis）：

```text
POST /cart/items {"sku_id":12,"quantity":2} → 200 code=0，返回 item(id=18, quantity=2, stock=20)
POST /cart/items {"sku_id":12,"quantity":1} → 200，quantity 累加为 3，cart_items 仍只有 1 行
PUT  /cart/items/18 {"quantity":0}          → 400 code=8002 数量非法
PUT  /cart/items/18/selected {"selected":false} → 200，selected=false 持久化
DELETE /cart/items/18                       → 200 data={}
DELETE /cart/items/18（再次）                → 404 code=8001
GET  /cart（无 Token）                       → 401 code=1002
```

---

## 4. 商品、SKU 与库存的关系

- 商品（SPU）：`products`，含 `status`（`draft/on_shelf/off_shelf`）、`price`（SPU 展示价）、`main_image`。
- SKU：`skus`，含 `product_id`、`name`、`price`、`status`（`enabled/disabled`）。
- 库存：`inventories`，按 `sku_id` 一行，`quantity` 为整数。

前端的事实边界：

| 数据 | 来源 | 前端是否可以自行推断 |
| --- | --- | --- |
| 加购是否允许 | `POST /cart/items` 的 5001/8003 | 否，必须由后端判定 |
| 条目当前单价 | `CartItem.current_price` | 否，只做展示与整数分乘法 |
| 条目是否可购买 | `CartItem.available` / `unavailable_reason` | 否 |
| 库存是否足够 | `CartItem.stock` / `insufficient` | 否；页面只展示后端结论 |
| 最终成交价 | W3-02 订单试算 | 否，购物车金额只是预览 |

商品详情页没有公开库存接口（`GET /products/:id` 只返回 SPU + enabled SKU），
因此详情页不显示库存，也无法在加购前做库存校验；库存信息只在购物车列表出现。
这是当前后端的真实能力边界，页面文案「库存以下单时校验为准」保持不变。

SKU 只有 `name/price/status`，没有颜色/尺寸等规格维度，因此 `SkuSelector`
只渲染单层规格按钮，不虚构多维规格矩阵。

---

## 5. 加购调用链

```text
AddToCartPanel（商品详情）
  └─ 点击「加入购物车」
       ├─ 未选择 SKU → 本地提示「请先选择商品规格」，不发请求
       ├─ 未登录 → navigate('/login', { state: { from: '/products/:id' } })
       └─ useAddToCart().mutate({ skuId, quantity })
            └─ addCartItem() → POST /cart/items { sku_id, quantity }
                 └─ onSuccess: invalidateQueries(['cart'])
                      ├─ Header 徽标（共享 queryKeys.cart.list）
                      └─ /cart 页面（同一 key）
```

设计说明：

1. **不做本地合并**。`POST /cart/items` 是累加语义，客户端不知道条目原数量，
   也无法可靠推断累加后的结果，因此成功后让整份列表失效并重新拉取，由后端返回权威值。
2. **加购前后端都会校验数量**。前端步进器限制 1..999，后端 `normalizeAddQuantity`
   仍然会拒绝非法值（400/8002）。
3. **商品无可用 SKU**：`product.skus.length === 0` 时按钮禁用并给出说明，
   不发送空 `sku_id` 请求（后端会返回 400/1001）。
4. **显式选择规格**：W1 版本的详情页默认选中第一个 SKU，W3-01 改为默认不选中。
   原因是后端 `sku_id` 必填，默认选中会让「SKU 必选校验」形同虚设。
   这是一次有意的行为变更，已同步更新既有单元测试与 E2E。

未登录路径：

- 详情页按钮文案为「登录后加入购物车」，点击跳 `/login` 并携带 `from`；
- 登录成功后 `resolveRedirectTarget` 回跳商品详情，页面重新加载商品与规格（选择状态不保留，
  用户需要重新点规格——这是回跳重挂载的正常结果）；
- 已在真实后端浏览器联调中验证该路径。

---

## 6. 修改数量的调用链与并发协调

### 6.1 朴素实现的问题

用户连续点「+」时，如果每次点击都直接 `PUT /cart/items/:id`：

```text
点击1 → PUT qty=2 ──────────────► 响应 2（晚到）
点击2 → PUT qty=3 ─────► 响应 3
点击3 → PUT qty=4 ───────────────► 响应 4（先到？后到？）
```

HTTP 请求没有顺序保证，旧请求的响应晚到会覆盖新状态，界面「回跳」到旧数量；
数据库最终值也取决于到达顺序，与用户最后一次操作不一致。

### 6.2 采用的方案：按条目串行 + 合并目标值

实现位置：`src/hooks/use-cart.ts` → `useUpdateCartQuantity`。

数据结构（`useRef<Map<itemId, {desired, running}>>`）：

- `desired`：用户最新期望值；
- `running`：该条目是否有请求在途。

流程：

```text
setQuantity(itemId, n)
  ├─ 立即乐观写缓存（界面立刻显示 n）
  ├─ state.desired = n
  └─ drain(itemId)（若 running 则直接返回）

drain:
  while (true):
    target = state.desired
    serverItem = await PUT /cart/items/:id {quantity: target}
    if (state.desired === target) {
        合并 serverItem.quantity → 缓存（服务端权威）
        break
    }
    // 期间用户又改了目标：丢弃这次旧响应，继续发送最新目标
```

效果：

- 同一时间每个条目最多 1 个在途请求 → 不可能出现响应乱序；
- 连续点 5 次通常只发 2 次请求（首个目标 + 最终目标）；
- 最终缓存值一定来自「目标值 == 响应值」的那次响应。

配套保护：

- **字段级合并**：数量响应只合并 `quantity`，不整体覆盖条目对象。
  这样即使同一条目上还有并发的勾选请求，也不会把 `selected` 覆盖回旧值。
- **缓存不存在时不写入**：`patchCartData` 先读缓存，缓存不存在直接返回。
  否则登出后仍在途的响应会把上一账号的购物车重新写回缓存（跨账号数据泄漏）。
- **失败回滚**：任一请求失败 → 提示错误 + `invalidateQueries(['cart'])`，
  以服务端真实状态覆盖乐观值（同时刷新库存/可购状态）。

没有引入 `zustand`/`redux` 或第三方请求队列库：队列只是组件内的一个 `Map` + 循环。

### 6.3 为什么不用 debounce

debounce 只能减少请求数，不能解决「在途旧响应晚于新响应」的问题；仍需要版本号/序号比对。
串行队列本身已经合并了连续操作，逻辑更直接，也更容易测试。

---

## 7. 勾选、全选与删除

### 7.1 单条勾选

- 乐观写入 `selected`，调用 `PUT /cart/items/:id/selected`；
- 成功用响应值校正；失败回滚整份缓存并提示。

### 7.2 全选 / 取消全选

后端**没有批量勾选接口**，因此 `useCartSelection().setAllSelected`：

1. 先筛出「可购买且当前状态与目标不同」的条目；
2. 乐观把全部可购买条目写成目标状态；
3. `Promise.allSettled` 并发发送 N 次单条 PUT；
4. 全部成功 → 提示「已全选可购买商品 / 已取消全选」；
   有失败 → 提示失败数量并 `invalidateQueries(['cart'])` 回到服务端真实状态。

不可购买条目（`available=false`）不参与全选：勾选无意义，且后端下单只会处理可购买项。
全选复选框的 `indeterminate` 状态用原生 DOM 属性设置，表达「部分选中」。

请求量说明：全选 N 条 = N 次 PUT。当前后端没有批量能力，这是已知成本；
如果后续后端提供批量接口，只需替换 `setAllSelected` 内部实现，页面无需改动。

### 7.3 删除

- 乐观从缓存移除条目，调用 `DELETE /cart/items/:id`；
- 失败回滚并提示（错误码 8001 会提示「条目已不存在，可能已在其他页面删除」）；
- 成功提示「已将商品移出购物车」，Header 徽标同步减少（同一份缓存）。

删除没有二次确认弹窗：条目可重新加购，且设计系统要求「不过度设计」；
真正不可逆的订单操作（W3-02 之后）才需要确认。

---

## 8. TanStack Query 缓存设计

### 8.1 Query Key

```ts
// src/api/query-keys.ts
cart: {
  all: ['cart'] as const,
  list: () => ['cart', 'list'] as const,
}
```

- 只有一个列表 key：后端没有分页、没有筛选，参数为空；
- `all` 用于「整域失效 / 整域删除」；
- 组件里禁止手写 key 字符串。

### 8.2 读写位置

| 场景 | 操作 | key |
| --- | --- | --- |
| Header 徽标 | `useCartCount` → `useCartQuery` | `cart.list()` |
| 购物车页面 | `useCartQuery` | `cart.list()` |
| 加购成功 | `invalidateQueries({ queryKey: cart.all })` | 整域 |
| 数量/勾选/删除 | `setQueryData` 乐观写 + 响应校正 | `cart.list()` |
| 数量失败 / 全选部分失败 | `invalidateQueries` | 整域 |
| 登出 / 会话失效 | `removeQueries({ queryKey: cart.all })` | 整域 |

### 8.3 为什么 Header 不额外请求计数

后端没有购物车计数接口。如果 Header 单独调一个「计数」接口，需要新接口；如果 Header 自己
汇总列表，就必须和页面共享同一份数据，否则会出现「Header 显示 2 件、页面只有 1 条」的不一致。

因此 Header 与页面使用同一个 `useQuery(queryKeys.cart.list())`：

- TanStack Query 对相同 key 的并发 `useQuery` 天然去重 → 一次 `GET /cart`；
- `staleTime: 30_000`，页面内导航不会重复拉取；
- 真实测试断言：进入 `/cart` 后 `GET /cart` 只发生 1 次（`getMockCartListRequestCount() === 1`）。

`useCartCount` 汇总的是「所有条目数量之和」，未登录或数据未就绪时返回 0，
不展示上一位用户的数字；徽标超过 99 显示 `99+`。

### 8.4 缓存形态与一致性

- 缓存里存的是后端 `CartItem[]`，不做二次建模（不把商品/SKU 拆成实体表）；
- 所有写操作都以服务端响应或重新拉取为最终状态，不存在「本地权威购物车」；
- `CART_STALE_TIME = 30s`：足够覆盖页面切换，又不至于长时间展示过期价格/库存。

---

## 9. 购物车缓存与认证的关系

### 9.1 登录态判断

- `hasSession` 来自内存 Token Store（`useSyncExternalStore`）；
- `useCartQuery` 只在 `enabled: hasSession` 时请求，未登录不会产生 401 请求；
- `/cart` 路由由 `RequireAuth` 保护：未登录跳 `/login` 并携带 `from=/cart`，登录后回跳。

### 9.2 退出登录 / 会话失效

两条路径都必须清理购物车缓存：

| 路径 | 实现 |
| --- | --- |
| 主动退出 | `useLogout` 的 `finally` 中 `removeQueries(queryKeys.cart.all)` |
| 会话失效（refresh 失败被清 Token） | `useAuthCacheReset` 监听「有会话 → 无会话」转变，统一删除 auth/addresses/favorites/cart |

两者都保留 `addresses`、`favorites` 的既有行为，W3-01 只追加 cart，不改变原有语义。

### 9.3 账号切换

- 登出会删除缓存；下一个账号登录后重新 `GET /cart`，拿到自己的数据；
- 在途响应不会复活缓存：`patchCartData` 在缓存不存在时直接返回；
- 真实单元测试覆盖：退出登录后 `client.getQueryData(queryKeys.cart.list())` 为 `undefined`，
  Header 徽标消失；Mock API 测试覆盖两个账号购物车互相隔离。

### 9.4 401 与会话续期

购物车请求统一走 `authGetData/authPostData/authPutData/authDeleteData`：

- 首次 401 → 尝试一次 `POST /refresh` → 成功则用新 Token 重试一次；
- refresh 失败 → 清空 Token（触发上面的缓存清理）→ 抛出 401；
- 查询的 `retry` 对 401 直接放弃重试，避免无意义的重复请求。

---

## 10. 金额展示与订单金额的边界

### 10.1 计算口径

前端只做两件事（`src/utils/cart.ts`）：

```ts
条目小计 = 当前单价(current_price) × 数量(quantity)
购物车预估金额 = Σ 已勾选 && available 的条目小计
```

- 金额全程整数分，展示由 `PriceDisplay` → `formatPriceParts` 转成 `¥1,299.00`；
- 不使用浮点元参与运算，`formatPriceParts` 对非整数分直接抛错；
- `price_snapshot`（加购价）只用于提示「价格已变动，加购价 ¥X」，不参与小计。

### 10.2 明确不做

| 不做 | 原因 |
| --- | --- |
| 计算优惠 / 优惠券抵扣 | 优惠规则属于后端订单域，前端没有权威数据 |
| 计算运费 | 同上 |
| 判断最终库存 / 是否可下单 | 库存与可购性由后端返回，下单时还会再次校验 |
| 决定成交价 | 成交价以订单试算为准（W3-02） |
| 把预估金额写入订单 | 本期不创建订单 |

页面显式提示：

- 「商品合计（预估）」；
- 「运费、优惠与最终应付金额以订单试算结果为准，购物车金额仅供预览」；
- 「去结算」按钮在 W3-02 前禁用，并说明「订单试算、结算与下单将在 W3-02 开放」。

不可购买但已勾选的条目不计入预估金额，页面提示「已勾选商品中有 N 件不可购买，未计入预估金额」。

---

## 11. 页面与交互实现

### 11.1 路由

```text
/cart  →  RequireAuth  →  CartPage（懒加载，独立 chunk）
```

- 懒加载：`src/router/lazy-pages.tsx` 导出 `CartPage`，构建产物 `cart-page-*.js` 约 8 kB；
- 未登录：`RequireAuth` 跳 `/login`，`state.from = '/cart'`，登录后回跳；
- 加载中：`RequireAuth` 展示路由级 Loading，不闪登录页。

### 11.2 页面结构

```text
PageContainer
├─ h1 购物车 + 说明 + 「继续购物」
├─ 操作反馈条（role=status / role=alert）
├─ LoadingState / ErrorState / EmptyState
└─ grid（lg: 1fr + 320px）
   ├─ 全选工具栏（checkbox + 件数统计，aria-live）
   ├─ 商品行列表（CartItemRow × N）
   └─ CartSummaryPanel（sticky）
```

`CartItemRow`：

- 勾选（不可购买时禁用）、图片（`ProductImage` 兜底）、商品名（可购买时链到详情）、规格名；
- 单价（`PriceDisplay`）、数量步进器、小计、删除；
- 状态提示：不可购买原因、库存不足（含库存数）、价格变动（含加购价）。

`QuantityStepper`：

- 「-」「+」按钮 + 数字输入框，`aria-label` 带商品名，保证同页多个步进器可区分；
- 输入框允许临时为空，失焦回填最近合法值；合法范围（1..999）内边距输入边提交；
- 越界按钮自动禁用。

### 11.3 状态覆盖

| 状态 | 实现 |
| --- | --- |
| Loading | `LoadingState label="正在加载购物车"` |
| Empty | `EmptyState`「购物车还是空的」+「去挑选商品」 |
| Error | `ErrorState` + 重新加载（失败文案不暴露接口/堆栈） |
| Success | 操作反馈条（已全选 / 数量已更新 / 已移出购物车） |
| Disabled | 结算按钮禁用并说明 W3-02；不可购买条目禁用勾选与数量 |
| Unavailable | `sku_deleted` / `disabled` / `off_shelf` 三种原因 + 库存不足 + 价格变动 |

### 11.4 Header 与移动端

- `src/components/layout/cart-entry.tsx`：`Link` 到 `/cart`，图标 + 徽标（`bg-promo`），桌面端显示「购物车」文案；
- `site-header.tsx`：替换原来的禁用占位按钮；
- `mobile-nav.tsx`：抽屉内的「购物车」链接（带数量），替换「购物车（W3 开放）」占位；
- 两处都从 `useCartCount()` 读取同一份缓存，不额外请求。

---

## 12. 技术决策与权衡

| 决策 | 选择 | 理由 / 代价 |
| --- | --- | --- |
| 详情页默认选中 SKU | 改为不默认选中 | 后端 `sku_id` 必填，默认选中使「必选校验」不可观测；代价是用户多一次点击，既有测试需更新 |
| 加购成功后的缓存 | 整域失效重拉 | 累加语义无法在本地可靠推算；代价是多一次 `GET /cart` |
| 数量修改 | 按条目串行 + 合并 | 彻底避免响应乱序，请求数可控；代价是同一行操作变成异步队列，需要 pending UI |
| 数量响应合并 | 只合并 `quantity` | 避免覆盖并发勾选状态；代价是价格/库存标记要等下一次刷新 |
| 全选 | N 次单条 PUT | 后端无批量接口；代价是条目多时请求多，已有失败聚合与回滚 |
| 结算入口 | 禁用 + 说明 | 未实现 W3-02，不制造死链；代价是本期用户无法真正下单 |
| 购物车状态管理 | 纯 TanStack Query | 不引入第二套本地权威数据；代价是乐观更新代码集中在 hooks |
| 图片/金额组件 | 复用 W0/W1 组件 | 保持视觉一致；不新增设计 Token |
| 时间字段 | 不展示 | `created_at/updated_at` 对购物车无业务意义，也规避时区观察问题 |

---

## 13. 实际问题与排查过程

1. **ESLint `react-hooks/set-state-in-effect`**
   - 现象：数量步进器用 `useEffect` 同步受控值，lint 报 error；
   - 处理：改成 React 官方推荐的「渲染期比较 + 条件 setState」，去掉 effect。

2. **并发测试首次不稳定（请求数 3 > 2）**
   - 原因：Mock 响应延迟 30ms，而 `userEvent.click` 三次总耗时超过 30ms，第一次请求已完成；
   - 处理：把 Mock 延迟提高到 300ms，确保三次点击落在同一在途窗口，再断言「≤2 次请求」。

3. **错误态测试等不到「购物车加载失败」**
   - 原因：查询自带一次自动重试，TanStack Query 默认重试退避约 1s，而 `findByText` 默认只等 1s；
   - 处理：放宽等待到 3s，并让 Mock 前两次失败、第三次成功，验证「重新加载」按钮真的能恢复。

4. **`getByText('4,299')` 命中多个元素**
   - 原因：数量为 1 时单价与小计相同，`PriceDisplay` 把金额拆成多个 span，测试同时匹配到两处；
   - 处理：用 `getAllByText` 断言出现 2 次，或用 `within(row)` 限定范围。

5. **真实数据库插入中文乱码**
   - 现象：联调商品名返回 `è”è°ƒ...`；
   - 原因：`docker exec mysql` 默认连接字符集为 latin1，UTF-8 字节被当作 latin1 存储；
   - 处理：重建数据时加 `--default-character-set=utf8mb4`。这只是联调数据问题，与前后端代码无关。

6. **删除 SKU 失败（外键约束）**
   - 现象：`DELETE FROM skus` 报 `fk_inventories_sku`；
   - 原因：`inventories.sku_id` 外键 ON DELETE RESTRICT；
   - 处理：验证「SKU 被删除」场景时先删库存记录再删 SKU；也确认了真实环境里 SKU 删除受库存记录约束。

7. **`go run . serve` 无法启动**
   - 现象：启动阶段七牛 bucket 校验失败（无真实凭据）；
   - 处理：不改后端，改用「仓库 `test/e2e` 的真实路由 + 真实 MySQL/Redis harness」做接口联调，
     并用临时入口（仅注册前台路由，不执行七牛校验）做浏览器联调；临时文件验证后删除，未提交。

---

## 14. 测试验证与已知限制

### 14.1 覆盖矩阵

| 要求 | 用例位置 | 类型 |
| --- | --- | --- |
| 加入购物车 | `src/pages/product-detail-page.test.tsx`、`src/api/cart.test.ts`、`tests/e2e/cart.spec.ts` | Vitest + Mock / Playwright + Mock / 真实后端 |
| SKU 必选校验 | 同上「未选择规格时加购给出必选校验提示」 | Vitest |
| 数量修改 | `src/pages/cart-page.test.tsx`、E2E | Vitest + Playwright |
| 商品删除 | `src/pages/cart-page.test.tsx`、E2E | 同上 |
| 商品选择 / 全选 | `src/pages/cart-page.test.tsx`、E2E | 同上 |
| 金额展示 | `src/utils/cart.test.ts`、`src/pages/cart-page.test.tsx` | 纯函数 + 页面 |
| 空购物车 | `src/pages/cart-page.test.tsx`、E2E | Vitest + Playwright |
| 商品不可购买 | `src/pages/cart-page.test.tsx`（下架/停售/SKU 删除/库存不足） | Vitest |
| 登录保护 | `src/pages/product-detail-page.test.tsx`、`tests/e2e/cart.spec.ts` | Vitest + Playwright |
| 退出登录缓存清理 | `src/pages/cart-page.test.tsx` | Vitest |
| Header 购物车数量 | `src/pages/cart-page.test.tsx`（徽标 + 共享请求）、E2E、移动端 E2E | Vitest + Playwright |
| 连续修改数量竞争 | `src/pages/cart-page.test.tsx`（Mock 延迟 + 请求序列断言） | Vitest + Mock |

### 14.2 真实后端 vs Mock

- **真实后端联调（已执行）**：
  - 临时 Go e2e 用例 4 个（鉴权、加购累加/上限/回滚、可购性/库存/价格变化/SKU 删除、勾选/删除/账号隔离），
    对真实 MySQL + Redis + 真实路由全部 PASS；
  - 临时浏览器用例 1 个：真实后端 + 真实数据库，从详情页登录回跳、加购、购物车数量/勾选/全选/删除全流程 PASS；
  - curl 级接口序列记录见第 3.3 节。
- **仅 Mock 验证（MSW）**：
  - 所有 Vitest 与 `npm run test:e2e` 用例都运行在 MSW 上，Mock 数据是为了构造
    下架/停售/SKU 删除/库存不足/延迟响应等真实环境难以稳定复现的场景；
  - 这些用例不能当作真实后端联调证据。

### 14.3 已知限制

1. 无批量勾选/批量删除接口：全选 = N 次 PUT，条目多时请求数线性增长；
2. 无购物车计数接口：徽标数字由列表汇总，进入站内首屏会触发一次 `GET /cart`；
3. 详情页无法展示库存（无公开库存接口），只能在购物车看到库存与不足提示；
4. 「去结算」禁用，订单试算/结算/下单未实现（W3-02）；
5. 商品详情不再默认选中 SKU，用户需先选规格（有意行为变更）；
6. 数量队列在页面卸载后不主动取消在途请求，但响应只会合并到已存在的缓存，
   不会在登出后复活缓存；
7. 真实后端联调使用的临时 Go/Playwright 文件已在提交前删除，仓库中不保留；后续如需回归，
   建议由后端补齐 `test/e2e` 的购物车正式用例（本次受「不修改 Go 后端」约束未提交）。

---

## 15. 后续维护注意事项

1. **改后端契约前先改 `src/api/types.ts` 与 `src/api/cart.ts`**，再改 hooks；
   前端字段名必须与 JSON tag 一致，禁止在组件里做隐式映射。
2. **不要把购物车塞进全局 store**：服务端数据只走 TanStack Query，否则会出现两套权威状态。
3. **新增写操作时遵守两个规则**：
   - 只合并本次操作拥有的字段（避免覆盖并发操作）；
   - 缓存不存在时不要创建缓存（避免登出后复活）。
4. **登出清理要同步**：`useLogout` 与 `useAuthCacheReset` 两处都要删除 `cart` 缓存。
5. **全选逻辑与可购买性绑定**：只对 `available=true` 的条目发请求，改后端语义时要同步改前端。
6. **金额计算只允许整数分**：新增金额展示一律用 `PriceDisplay`/`formatPrice`；
   不要把预估金额写进任何订单请求（W3-02 起也一样，以后端试算为准）。
7. **W3-02 接入点**：`CartSummaryPanel` 的「去结算」按钮与 `summarizeCart` 的 `selectedCount`；
   结算页应只取「已勾选且可购买」条目 id，最终金额由后端试算返回。
8. **测试稳定性**：涉及并发/延迟的用例不要依赖真实时钟过短延迟；Mock 延迟要明显大于
   `userEvent` 的点击耗时。

---

## 16. 值得学习的技术知识

### 16.1 前端请求竞争

- HTTP 请求没有顺序保证；「后发先至」会让旧响应覆盖新状态；
- 解决思路有三类：
  1. 取消旧请求（`AbortController`）——但取消了也不保证服务端没执行；
  2. 版本号/序号比对——响应只在序号最新时生效；
  3. 串行队列 + 目标值合并——既保证顺序，又减少请求。
- 本项目选择第三种：实现简单、行为确定、可测试。

### 16.2 TanStack Query 缓存一致性

- `invalidateQueries` 会重新拉取并覆盖缓存；`setQueryData` 是同步写；
- 乐观更新必须处理回滚（`onError`）与最终校正（`onSettled`/响应合并）；
- 多组件共享同一 key 时，`useQuery` 自动去重，是「计数徽标」这类需求的最低成本实现；
- `removeQueries` 用于清除用户私有数据，`resetQueries` 会把状态重置为初始值（含 loading），
  本场景应使用 `removeQueries`。

### 16.3 电商金额

- 金额用整数分，避免 `0.1 + 0.2` 类浮点误差；
- 前端展示金额 ≠ 订单应付金额：优惠、运费、税费、库存与价格校验都必须在服务端完成；
- 「预估价」必须显式标注为预估，并在文案中说明以结算页为准。

### 16.4 可访问性

- 图标按钮必须有 `aria-label`，且要能区分同一页面的多个同类按钮（带商品名）；
- 操作反馈用 `role="status"`（非打断）与 `role="alert"`（打断）；
- 部分选中的全选复选框使用原生 `indeterminate`，而不是自造视觉状态；
- 禁用按钮要说明原因（`aria-describedby` + 可见文案），避免「点了没反应」。

### 16.5 面试问题（附要点）

1. **购物车数量连续修改如何避免旧响应覆盖新状态？**
   按条目串行 + 目标值合并；旧响应与当前目标不一致时丢弃，继续发送最新目标；
   只合并本次操作字段，失败回滚并重新拉取。

2. **为什么加购成功后不直接改本地缓存？**
   `POST /cart/items` 是累加语义，客户端无法知道最终数量；让列表失效重拉，服务端为权威。

3. **Header 徽标如何做到不重复请求？**
   Header 与页面共用 `['cart','list']` 查询 key，TanStack Query 自动去重；
   徽标由列表汇总，不新增计数接口。

4. **全选为什么不是一次请求？**
   后端只有单条勾选接口；前端用 `Promise.allSettled` 并发 N 次，失败时聚合提示并回滚。

5. **登出后如何保证看不到上一个账号的购物车？**
   登出/会话失效时 `removeQueries(['cart'])`；异步响应写入缓存前检查缓存是否存在；
   查询只在 `hasSession` 时启用。

6. **购物车金额能当作订单金额吗？**
   不能。它只是「当前单价 × 数量」的展示口径，不含优惠/运费，也不做库存裁决；
   订单应付金额以后端试算为准。

7. **如何测试请求竞争？**
   在 Mock 层注入延迟，连续触发操作，断言请求序列单调、次数被合并、
   最终缓存与服务端状态都等于最后一次目标值。

8. **为什么详情页不默认选中 SKU？**
   后端 `sku_id` 必填；默认选中会让必选校验不可观测，也无法避免用户误加购错误规格。

---

## 17. 关键文件索引

| 文件 | 职责 |
| --- | --- |
| `web/src/api/cart.ts` | 购物车 5 个接口封装 + 数量常量 |
| `web/src/api/types.ts` | `CartItem` DTO（与 Go JSON tag 一致） |
| `web/src/api/query-keys.ts` | `cart.all` / `cart.list` |
| `web/src/hooks/use-cart.ts` | 查询、徽标计数、加购、数量队列、勾选/全选、删除 |
| `web/src/utils/cart.ts` | 单价/小计/汇总纯函数 + 不可购买原因文案 |
| `web/src/pages/cart-page.tsx` | `/cart` 页面 |
| `web/src/pages/cart-errors.ts` | 8001/8002/8003/5001/1002 文案映射 |
| `web/src/components/cart/cart-item-row.tsx` | 购物车商品行 |
| `web/src/components/cart/cart-summary.tsx` | 金额汇总 + 结算占位 |
| `web/src/components/cart/quantity-stepper.tsx` | 数量步进器（详情页与购物车共用） |
| `web/src/components/product/add-to-cart-panel.tsx` | 详情页加购区域（SKU 校验/登录引导） |
| `web/src/components/layout/cart-entry.tsx` | Header 购物车入口 + 徽标 |
| `web/src/components/layout/site-header.tsx` | 替换禁用占位为真实入口 |
| `web/src/components/layout/mobile-nav.tsx` | 移动端购物车入口 |
| `web/src/auth/use-auth.ts`、`use-auth-cache-reset.ts` | 登出/会话失效清理购物车缓存 |
| `web/src/router/index.tsx`、`lazy-pages.tsx` | `/cart` 路由（RequireAuth + 懒加载） |
| `web/src/mocks/data/cart.ts`、`mocks/handlers.ts` | MSW 购物车 Mock（含测试钩子） |
| `web/src/api/cart.test.ts` | 接口契约测试（Mock） |
| `web/src/pages/cart-page.test.tsx` | 页面行为 + 并发 + 认证清理测试 |
| `web/src/utils/cart.test.ts` | 金额与汇总纯函数测试 |
| `web/tests/e2e/cart.spec.ts` | 购物车 E2E（Mock 环境） |
