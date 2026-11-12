# A4 营销与内容管理 — 开发知识文档

> 交付阶段：A4（优惠券、秒杀活动、轮播图、推荐位、文章）
> 目标分支：`feat/phase2-admin-frontend`
> 依据：最终代码 + 对 Go 后端的只读核查；未修改任何后端文件。

---

## 1. 整体架构

```
admin/src/
├── api/
│   ├── types/{coupon,flash-sale,banner,recommendation,article}.ts
│   ├── modules/{coupon,flash-sale,banner,recommendation,article}.ts
│   └── adapters/{coupon,flash-sale,banner,recommendation,article}-adapter.ts
├── views/
│   ├── marketing/
│   │   ├── coupons/{index.vue,components/CouponFormDialog.vue}
│   │   └── flash-sales/{index.vue,components/SkuBindingPicker.vue}
│   └── content/
│       ├── banners/{index.vue,components/BannerFormDialog.vue}
│       ├── recommendations/{index.vue,components/PositionFormDialog.vue}
│       └── articles/index.vue
├── router/modules/{marketing,content}.ts
└── mocks/handlers/{coupon,flash-sale,banner,recommendation,article}.ts
```

调用方向：页面 → API 模块 → Axios；字段映射、金额换算、状态文案、
校验与请求体构造全部在 adapter，页面不拼 URL、不直接做金额运算。

路由：

| 路径 | 路由名 | 菜单 |
| --- | --- | --- |
| `/marketing/coupons` | CouponList | 营销管理 / 优惠券管理 |
| `/marketing/flash-sales` | FlashSaleManagement | 营销管理 / 秒杀活动 |
| `/content/banners` | BannerList | 内容管理 / 轮播图管理 |
| `/content/recommendations` | RecommendationManagement | 内容管理 / 商品推荐位 |
| `/content/articles` | ArticleList | 内容管理 / 文章内容 |

---

## 2. 优惠券

### 2.1 后端事实

| 接口 | 权限 | 说明 |
| --- | --- | --- |
| `POST /admin/coupons` | `coupon:create` | 创建 |
| `GET /admin/coupons` | `coupon:list` | 全部模板，按 id 倒序，**无分页参数** |
| `POST /admin/coupons/:id/stop` | `coupon:stop` | 停止领取（1→0，幂等） |

不存在：详情、编辑、删除、重新启用。

### 2.2 业务模型

- 只有**满减固定金额**：`threshold_amount`（0=无门槛）+ `discount_amount`。
- 没有折扣率、商品范围、每人限领、领取后有效期。
- 状态：`1` 可领取 / `0` 已停止领取。
- `total_quantity`（发放总量）与 `issued_quantity`（已发放，后端真实计数）。
- 领取/使用时间**必填**，各自 start < end。
- 创建时 `status=1`、`issued_quantity=0`；金额上限 99,999,999 分。

### 2.3 页面实现

- 列表：一次加载全部模板，在**完整数据集**上做关键词/状态筛选与前端分页。
- 详情：抽屉展示完整模板字段（后端无详情接口，数据来自列表）。
- 新建：Dialog，金额以元输入、提交转整数分；时间格式化为 `YYYY-MM-DD HH:mm:ss`。
- 停止领取：二次确认，成功后刷新；按钮仅 status=1 时显示。
- 未提供编辑/删除/重新启用（后端无接口）。

---

## 3. 秒杀活动

### 3.1 后端事实

| 接口 | 权限 | 说明 |
| --- | --- | --- |
| `POST /admin/flash-sales` | `flash_sale:create` | 创建（默认 enabled） |
| `PUT /admin/flash-sales/:id` | `flash_sale:update` | 部分更新 |

不存在：活动列表、详情、删除、独立启停；也没有活动统计（Dashboard 只有聚合计数）。

更新规则：

- `name/status/start_time/end_time` 未提交则保持不变；
- `skus` 提供时只更新**已绑定**的 SKU，不允许新增/删除绑定；
- `total_stock` 不得小于 `sold`；
- 秒杀价整数分、>0、≤99,999,999；库存正整数；SKU 必须存在。

### 3.2 与 Kafka 秒杀系统的关系

管理端只写 `flash_sale_activities` / `flash_sale_activity_skus` 配置：
- 不改 Redis Lua 准入、不写 Kafka/Outbox、不修改请求状态；
- 不直接改 MySQL/Redis 库存（库存更新是后端 UpdateActivity 的内部行为）；
- 无秒杀订单查询/统计页面。

### 3.3 页面实现

- 「创建活动」：名称 + 时间区间 + SKU 绑定（复用 A2 商品搜索/详情选 SKU，
  填写秒杀价与库存）→ 真实 POST。
- 「按 ID 更新」：ID + 可选 name/status/时间 + 可选 SKU 价格/库存更新 → 真实 PUT。
  由于没有详情接口，无法回填历史值，页面已明确提示。
- 「最近操作结果」：展示本次接口返回的活动快照（含 sold），刷新后不可回读。
- 未提供活动列表（后端无接口）。

---

## 4. 轮播图

### 4.1 后端事实

| 接口 | 权限 |
| --- | --- |
| `GET /admin/banners` | AdminAuth（全部状态） |
| `GET /admin/banners/:id` | AdminAuth |
| `POST /admin/banners` | `banner:create` |
| `PUT /admin/banners/:id` | `banner:update`（部分更新，status 在此变更） |
| `DELETE /admin/banners/:id` | `banner:delete` |

字段：title（≤64）、image_url（≤255）、link_url（≤512，可空）、sort、status（1/0）。
没有展示位置、有效期、独立启停接口。

### 4.2 图片上传调用链

```
BannerFormDialog
  → A2 ProductImageUploader（views/product/components）
  → validateImageFile（类型/大小）
  → GET /admin/qiniu/upload/token
  → 独立 Axios 实例直传凭证里的 upload_url
  → 成功后写回 image_url
```

管理员 Token 不会发往七牛域名；前端不接触 AK/SK。
真实七牛上传未联调（NOT_VERIFIED），MSW 覆盖成功/失败交互。

### 4.3 页面实现

- 列表：全部状态、sort 升序；前端分页（后端无分页参数）。
- 新增/编辑：标题、图片、跳转、排序、状态；图片上传组件复用 A2。
- 启停：通过 `PUT status` 完成，二次确认。
- 删除：二次确认，删除后不可恢复。

---

## 5. 推荐位

### 5.1 数据模型

两层：`recommend_positions (code, name, status)` →
`recommend_items (position_id, product_id, sort)`。

- code 创建后不可变，规则 `[a-z0-9][a-z0-9-]{0,63}`；
- 推荐关系**没有商品快照**，只存 product_id；
- 排序接口是全量覆盖：`product_ids` 必须非空、无重复、恰好覆盖全部已加入商品；
- 删除推荐位级联删除关系；
- 添加商品只要求商品存在（任意状态）。

### 5.2 页面实现

- 推荐位列表（全部状态，id 升序），点击行加载详情。
- 商品信息通过 A2 `getProduct` 逐条补全（并发 4）；失败只显示 `商品 #id`。
- 新增/编辑推荐位（编辑时 code 只读）。
- 启停通过 update，删除有级联提示。
- 添加商品：复用 A2 商品搜索；移除需确认。
- 上移/下移：本地交换后提交**完整有序 product_ids**，成功重新加载。
- 商品状态来自商品表实时数据，与推荐关系本身的状态区分展示。

---

## 6. 文章

### 6.1 后端事实

- 只有公开只读：`GET /articles`（分页，不含正文）、`GET /articles/:id`（含正文）。
- 写接口（`POST/PUT/DELETE /articles`）属于**用户侧**，需要用户 token 且校验作者本人，
  管理员 token 不适用。
- 表结构只有 id/author_id/title/content/created_at/updated_at：
  **没有** status、封面、分类、发布时间。
- 正文为 `TEXT` 纯文本，无 HTML 渲染约定。

### 6.2 页面实现

- 列表：服务端分页（page/size），展示标题、作者、创建/更新时间。
- 详情：抽屉展示纯文本正文（`white-space: pre-wrap`），不注入 HTML。
- 明确不提供创建/编辑/发布/下架/删除（后端无管理端能力）。
- 没有标题搜索/状态筛选（后端无参数、无 status 字段）。

---

## 7. Adapter 设计

| Adapter | 职责 |
| --- | --- |
| `coupon-adapter` | 金额分/元、状态文案、表单校验、创建载荷、全量筛选与分页 |
| `flash-sale-adapter` | 状态文案、绑定校验、创建/部分更新载荷、视图汇总 |
| `banner-adapter` | 状态、表单回显/校验、创建/更新载荷 |
| `recommendation-adapter` | 状态、关系+商品合成视图、code/name 校验、移动与全量排序载荷 |
| `article-adapter` | 列表/详情视图、纯文本摘要 |

DTO 全部来自 Go 源码核对，**没有 provisional 字段**；仓库仍不存在 OpenAPI 契约。

金额统一整数分；新增 `utils/money.parseYuanToCents` 做精确元→分转换，
新增 `utils/datetime.toServerDateTime` 生成 `YYYY-MM-DD HH:mm:ss` 请求体时间。

---

## 8. 技术决策

1. **不为后端缺失能力造接口**：秒杀无列表就不做列表；文章无管理端写接口就只读；
   优惠券无更新就不做编辑按钮。
2. **全量数据上的前端筛选是真实筛选**：优惠券/轮播图列表接口返回完整数据集，
   前端分页与筛选不会造成"只看当前页"的误导；页面已注明。
3. **秒杀页采用"创建/按 ID 更新 + 会话内结果"**：没有详情接口无法回填，
   页面明确说明刷新后不可回读，避免用 MSW 假列表冒充真实列表。
4. **推荐排序全量提交**：严格遵循后端 15006 覆盖校验。
5. **跨模块复用**：SKU 选择复用 A2 商品/详情；轮播图图片复用 A2 上传组件；
   推荐商品信息复用 A2 商品详情。
6. **权限码真实**：`coupon:*`、`flash_sale:*`、`banner:*`、`recommend:*`；
   文章没有管理端权限码。

---

## 9. 遇到的问题与排查

1. **秒杀没有列表/详情接口**：原计划做活动列表，核查 `routes_admin.go` 后确认只有
   Create/Update，改为创建/按 ID 更新 + 本次响应快照，并在页面与报告中标注。
2. **文章没有管理端接口**：写接口是用户侧作者操作，管理员 token 无法调用；
   改为公开只读列表/详情，写入能力标记 NOT_IMPLEMENTED。
3. **优惠券没有更新接口**：任务要求编辑，但后端只有 create/list/stop；
   不做编辑按钮，停止领取是唯一状态变更。
4. **推荐关系无商品快照**：列表只返回 product_id；用 A2 商品详情并发补全，
   失败时降级显示 ID，不伪造名称。
5. **菜单图标空白**：`useRenderIcon` 对不含 `:` 的图标走离线注册表，
   易漏注册；A4 在 `offlineIcon.ts` 注册了营销/内容图标。
6. **请求体时间格式**：查询参数用 RFC3339，body 用 `YYYY-MM-DD HH:mm:ss`；
   新增 `toServerDateTime` 区分两种场景。

---

## 10. 测试与未验证

已执行：

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过 |
| `pnpm test` | 19 文件 / 279 用例通过（A4 新增 62） |
| `pnpm build` | 通过（3.29 MB） |

A4 测试：

- `marketing-coupon.test.ts`（15）：列表/金额/筛选/分页/创建/停止/校验/403。
- `marketing-flash-sale.test.ts`（14）：创建/视图/绑定校验/更新部分字段/
  未绑定 SKU/库存低于已售/时间校验/防重复提交。
- `content-banner.test.ts`（11）：列表/详情/创建/更新/删除/字段校验/403。
- `content-recommendation.test.ts`（15）：列表/详情/CRUD/添加去重/移除/全量排序
  覆盖校验/移动工具/表单校验。
- `content-article.test.ts`（7）：分页/详情/404/视图映射/摘要/无写接口断言。

未验证：

- 真实 Go 后端联调：**NOT_VERIFIED**（全部 MSW）。
- 真实七牛上传：**NOT_VERIFIED**。
- 1366 / 1920 浏览器视觉验收：**NOT_VERIFIED**。

---

## 11. 后端不支持的能力（A4 相关）

- 优惠券：详情、编辑、删除、重新启用；折扣率、商品范围、每人限领。
- 秒杀：活动列表、详情、删除、独立启停、活动统计、请求列表。
- 轮播图：展示位置、有效期、列表分页/搜索。
- 推荐位：商品快照、推荐算法、点击/转化统计。
- 文章：管理端增删改、发布/下架状态、封面、分类、标题搜索。

---

## 12. 后续维护约束

1. 不要为秒杀活动伪造列表/统计数据；后端提供读接口后再加。
2. 不要通过前端改库存/请求状态，或直接操作 Redis/Kafka。
3. 优惠券只支持满减固定金额，新增规则必须先有后端字段。
4. 推荐排序必须全量提交，缺项会被 15006 拒绝。
5. 文章正文按纯文本渲染，不要 `v-html`。
6. 金额保持整数分；新增金额字段继续走 `parseYuanToCents` / `formatCentsAsCny`。
7. 轮播图上传复用 A2 组件；禁止把 AK/SK 或签名逻辑放前端。
8. `/admin/me` 若返回 `permissions[]`，本地角色映射会自动失效。
