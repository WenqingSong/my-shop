# A2 商品与库存管理 — 开发知识文档

> 交付阶段：A2（商品管理、商品分类、SKU 与规格、库存、图片上传）
> 目标分支：`feat/phase2-admin-frontend`
> 记录依据：最终代码 + 对 Go 后端的只读核查，未修改任何后端文件。

---

## 1. 模块前端架构

```
admin/src/
├── api/
│   ├── types/                 # 后端 DTO（snake_case，字段与 Go 结构体一致）
│   │   ├── product.ts
│   │   ├── category.ts
│   │   ├── sku.ts
│   │   ├── inventory.ts
│   │   └── upload.ts
│   ├── modules/               # 请求方法（只拼 URL 与参数，不含 UI 逻辑）
│   │   ├── product.ts
│   │   ├── category.ts
│   │   ├── sku.ts
│   │   ├── inventory.ts
│   │   └── upload.ts
│   └── adapters/              # DTO ⇄ 视图模型 / 表单模型 / 同步计划
│       ├── product-adapter.ts
│       ├── category-adapter.ts
│       ├── sku-adapter.ts
│       └── inventory-adapter.ts
├── composables/useSubmitGuard.ts
├── utils/
│   ├── permission.ts          # 真实权限码常量（A1 机制扩展）
│   ├── error-message.ts       # 请求异常 → 可展示文案
│   └── datetime.ts            # 新增 formatServerDateTime
├── views/
│   ├── product/
│   │   ├── index.vue                      # 商品列表
│   │   ├── form.vue                       # 新建 / 编辑（同一组件，按 route.name 区分）
│   │   ├── components/ProductDetailDrawer.vue
│   │   ├── components/ProductImageUploader.vue
│   │   ├── components/SkuSpecEditor.vue
│   │   └── composables/{useProductList,useImageUploader}.ts
│   ├── category/
│   │   ├── index.vue
│   │   └── components/CategoryFormDialog.vue
│   └── inventory/
│       ├── index.vue                      # 库存查询 + 调整入口
│       ├── transactions.vue               # 库存流水
│       └── components/InventoryAdjustDialog.vue
├── router/modules/{product,inventory}.ts
└── mocks/handlers/{mock-db,shared,product,category,inventory,upload}.ts
```

调用关系（单向）：

```
页面组件
  → composable（列表状态 / 上传状态 / 提交锁）
  → api/modules/*（真实请求）
  → api/adapters/*（DTO 转换、校验、同步计划）
  → api/types/*（类型）
```

页面组件**不拼接** API 路径，也不做 `* 100` / `/ 100` 的金额换算。

路由：

| 路径 | 组件 | 说明 |
| --- | --- | --- |
| `/products` | `views/product/index.vue` | 商品列表 |
| `/products/create` | `views/product/form.vue` | 新建（不在菜单显示） |
| `/products/:id/edit` | `views/product/form.vue` | 编辑（不在菜单显示） |
| `/categories` | `views/category/index.vue` | 分类管理 |
| `/inventory` | `views/inventory/index.vue` | 库存查询 |
| `/inventory/transactions` | `views/inventory/transactions.vue` | 库存流水 |

菜单父级：`/product`（商品管理）、`/stock`（库存管理）。父路径仅用于菜单分组，实际页面路径保持任务要求的形式。

---

## 2. 后端事实（只读核查结论）

核查位置：`internal/cmd/routes_admin.go`、`api/*/v1/*.go`、`internal/logic/*`、
`internal/boot/seed.go`、`internal/migrations/sql/*`。

### 2.1 已有真实接口

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| GET | `/admin/products` | AdminAuth（无读权限码） |
| GET | `/admin/products/:id` | AdminAuth |
| POST | `/admin/products` | `product:create` |
| PUT | `/admin/products/:id` | `product:update` |
| POST | `/admin/products/:id/on-shelf` | `product:on_shelf` |
| POST | `/admin/products/:id/off-shelf` | `product:off_shelf` |
| GET | `/categories` | 公开（仅启用项，树形） |
| GET | `/categories/:id` | 公开 |
| POST | `/categories` | `category:create` |
| PUT | `/categories/:id` | `category:update` |
| DELETE | `/categories/:id` | `category:delete` |
| POST | `/admin/skus` | `sku:create` |
| PUT | `/admin/skus/:id` | `sku:update` |
| DELETE | `/admin/skus/:id` | `sku:delete` |
| GET | `/admin/inventories/:sku_id` | AdminAuth |
| GET | `/admin/inventories/:sku_id/logs` | AdminAuth |
| POST | `/admin/inventories/:sku_id/increase` | `inventory:increase` |
| POST | `/admin/inventories/:sku_id/deduct` | `inventory:deduct` |
| GET | `/admin/qiniu/upload/token` | AdminAuth（无独立权限码） |

### 2.2 后端不存在的能力（前端未伪造）

- 商品删除接口（`DELETE /admin/products/:id` 不存在）。
- 独立的 SKU 列表接口（只能从商品详情的 `skus` 读取）。
- 库存列表 / 搜索 / 汇总接口（只能按 `sku_id` 查询）。
- 设置库存总量的接口（只有 increase / deduct 增量语义）。
- 锁定库存、可售库存字段。
- 商品副标题（`subtitle`）、商品级 SKU 编码、SKU 图片字段。
- 规格组 / 规格值数据表。
- 后台分类列表（禁用分类无法列出）。
- 商品列表的 `status` 查询参数。
- 库存调整的原因字段（`IncreaseReq/DeductReq` 只有 `quantity`，流水 `reason` 恒为空）。

### 2.3 数据结构要点

- 金额：`price` 为 **int64 整数分**，范围 0 ~ 99,999,999。
- 商品状态：`draft / on_shelf / off_shelf`；创建强制 `draft`；普通 PUT 忽略 `status`。
- SKU 状态：`enabled / disabled`（DB 1/0）。
- SKU 唯一性：`uk_product_name (product_id, name)`。
- 分类：`parent_id = 0` 为顶级，最大 3 级；同级重名 `uk_parent_name`。
- 库存：`inventories.uk_sku_id`，无记录时查询返回 `quantity = 0`、时间为 `null`。
- 图片：`products.main_image` + 独立表 `product_images`（`sort` 升序）。

---

## 3. 商品规格与 SKU

### 3.1 数据结构（前端）

```ts
interface SpecGroupDraft {
  key: string;        // 前端行标识，不提交
  name: string;       // 「颜色」——PROVISIONAL，不持久化
  values: string[];   // ["黑", "白"]
}

interface SkuDraft {
  key: string;        // 前端行标识
  values: string[];   // ["黑", "40"]
  name: string;       // 提交给后端的 SKU 名称
  priceCents: number; // 整数分
  status: "enabled" | "disabled";
}
```

映射规则：

| 前端字段 | 是否持久化 | 说明 |
| --- | --- | --- |
| `name` | ✅ 是 | 规格值以 `" / "` 拼接，例如 `黑 / 40` |
| `priceCents` | ✅ 是 | 提交时保持整数分 |
| `status` | ✅ 是 | `enabled/disabled` |
| `values` / 规格组名 | ❌ 否 | 仅用于生成与回显 |
| SKU 编码 / SKU 图片 | ❌ 未实现 | 后端无字段，未在 UI 中提供假输入 |

### 3.2 组合生成

`generateCombinations(groups)` 做笛卡尔积，跳过没有规格值的组：

```
颜色 [黑, 白] × 尺码 [40, 41, 42]
→ 黑/40 黑/41 黑/42 白/40 白/41 白/42
```

生成是**显式动作**（按钮 + 二次确认），避免用户改一个规格值就冲掉已填价格。
生成时按 SKU 名称复用已有行的价格与状态。

### 3.3 编辑回显

`buildSkuEditorState(detail.skus, detail.price)`：

1. 无 SKU → 一行默认 SKU（名称「默认规格」）。
2. 有 SKU → **逐行映射后端 SKU**，不重新生成组合，保证不多不少。
3. 规格组由 SKU 名称反推（按 `" / "` 分段）：
   - 段数一致且 > 1 → 生成 `规格 1..N`（组名不可持久化）；
   - 段数不一致 → 退化为单组，值为完整名称，不做猜测。

### 3.4 同步机制

`planSkuSync(drafts, existing)` 以 **trim 后的 name** 为匹配键：

- 草稿有、后端无 → `create`；
- 两边都有且价格/状态不同 → `update`；
- 后端有、草稿无 → `remove`。

`syncProductSkus()` 按 create → update → delete 顺序逐条调用真实接口，
单条失败不中断，返回 `{ created, updated, removed, skipped, failures[] }`。
页面会展示失败明细，不会把失败计入成功数。

已知语义：

- 修改规格值等于修改 SKU 名称 → 旧 SKU 被删除、新 SKU 被创建。
- 若旧 SKU 已有库存记录，后端返回 5005，删除失败并在页面提示（不会静默忽略）。
- 重命名商品不会影响 SKU 名称。

---

## 4. 库存调整流程

```
库存页选择商品 → GET /admin/products/:id → 得到 SKU 列表
  → 并发 GET /admin/inventories/:sku_id（上限 4 并发）
  → 点击「调整库存」
  → Dialog：类型（increase/deduct）+ 正整数数量 + 可选原因
  → validateAdjustQuantity()
  → ElMessageBox 二次确认
  → POST /admin/inventories/:sku_id/{increase|deduct}  { quantity }
  → 用后端返回的 quantity 更新表格；失败展示后端 message
```

约束：

- 没有"覆盖库存"路径；`buildAdjustmentRequest` 只返回 `{ type, skuId, quantity }`。
- 不在前端做乐观更新；库存数字只来自后端响应或重新查询。
- 扣减不足由后端返回 6001「库存不足」；前端不预先拦截（避免展示值过期导致误判）。
- 调整原因放在 `provisional` 返回值的独立字段中，UI 明确提示"不会提交、不会出现在流水"。

---

## 5. 图片上传流程

```
ProductImageUploader
  → validateImageFile（扩展名 / MIME / 10MB）
  → GET /admin/qiniu/upload/token?filename=&content_type=
  → 独立 Axios 实例 POST {upload_url}（FormData: token/key/file）
  → 使用凭证中的 final_url 作为图片地址
```

安全边界：

- AK/SK 只在后端；前端从不生成签名。
- 直传使用 `Axios.create()` 的独立实例，**不会**把管理员 Bearer Token 发往第三方域名。
- 不手动设置 `Content-Type`，由浏览器补 multipart boundary。
- 失败项保留在列表中可重试，不会生成占位 URL 冒充成功。
- 组件支持：上传进度、失败提示、`el-image` 预览、删除、数量上限。

---

## 6. API 适配边界

### 6.1 真实接入（契约来自 Go 结构体）

商品、分类、SKU、库存、上传的全部读写接口均按后端真实路径与字段接入。
DTO 位于 `api/types/*`，字段名与 Go JSON tag 一致，未放入 `generated/`。

### 6.2 前端组合（不是新后端接口）

| 页面能力 | 实现方式 | 说明 |
| --- | --- | --- |
| 商品列表 SKU 数量 | 列表 + 逐条 `GET /admin/products/:id` | 并发 4；失败显示 `—` |
| 商品详情库存 | 详情 + 逐 SKU `GET /admin/inventories/:sku_id` | 失败显示 `—` |
| 库存列表 / 按商品查看 | 商品列表/详情 + 逐 SKU 库存查询 | 后端无库存列表接口 |
| 商品状态筛选 | `scanProducts()` 跨页拉取真实数据后前端过滤 | 后端无 `status` 参数；上限 5 页 × 100 |
| 按商品 ID 查库存 | `GET /admin/products/:id` | 后端列表不支持 id 查询 |

### 6.3 PROVISIONAL（前端暂定、后端未持久化）

- 规格组名称、规格值结构（由 SKU 名称反推）。
- SKU 编码、SKU 图片（未做 UI，避免假字段）。
- 库存调整原因（收集但不提交）。
- 图片上传的 mock 凭证与 mock 上传地址（仅 MSW）。

---

## 7. 核心技术决策

1. **SKU 先保存商品、再同步 SKU**：后端创建商品的请求体没有 SKU 字段，
   必须先拿到 `product_id`。因此保存分为两步，并对"商品成功、SKU 部分失败"单独提示。
2. **创建后部分失败切到编辑模式**：避免用户再次点击保存又创建一个新商品；
   同时刷新 `existingSkus` 快照，重试时不会重复创建已成功的 SKU。
3. **不展示后端没有的字段**：锁定库存、可售库存、商品副标题、SKU 编码均不出现。
4. **业务错误透传**：后端对商品/库存业务错误使用 HTTP 400/404/409，
   Axios 走 error 分支会丢失 envelope message。在 `utils/http` 中**附加**
   `businessCode/businessMessage`（不改变 reject 类型与既有提示逻辑），
   `resolveErrorMessage()` 优先展示后端原文。
5. **权限码全部取自后端**：`product:create/update/on_shelf/off_shelf`、
   `category:create/update/delete`、`sku:create/update/delete`、
   `inventory:increase/deduct`。没有读权限码，所以菜单/页面访问只依赖登录态，
   不为页面编造 `*:read`。
6. **提交锁**：`useSubmitGuard` 合并并发点击，任务结束后自动解锁。

---

## 8. 实际遇到的问题与排查

1. **SKU 平铺模型**：最初按"规格组表 + 规格值表"设计，核查 `skus` DDL 后确认只有
   `name/price/status`，改为"名称即规格组合"的适配方案，并把规格结构标记为 provisional。
2. **业务错误文案丢失**：MSW 复现 409 时页面只显示"数据状态冲突"。
   排查发现 Axios 对非 2xx 不走成功拦截器，`BusinessError` 不产生。
   解决：在 error 拦截器提取 envelope 到 `businessCode/businessMessage`，
   UI 使用后端原文；测试用 `tests/helpers/business-error.ts` 同时覆盖两种形态。
3. **图片上传在 jsdom 中网络错误**：两个原因——
   (a) 手动设置 `Content-Type: multipart/form-data` 丢失 boundary，后端/ MSW 解析失败；
   (b) 跨域 XHR 在 jsdom 下无 CORS 响应头会被判定为网络错误。
   解决：移除手动头；MSW 凭证返回**同源** `upload_url`（真实七牛仍是第三方域名）。
4. **状态筛选无后端支持**：没有伪造 `status` 参数，改为跨页拉取 + 前端过滤，并在页面
   显示实现说明与截断告警。
5. **SKU 删除 5005**：库存用例复现后，同步结果保留失败明细，页面不回滚也不会假报成功。

---

## 9. 测试与未验证事项

### 已执行

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 12 个测试文件、162 个用例全部通过（含 A1 回归） |
| `pnpm build` | 通过，产物 3.16 MB |

A2 新增测试：`tests/unit/{product,category,sku,inventory,upload}.test.ts`。

覆盖点：列表/搜索/分页/详情/创建/编辑/上下架/表单校验/错误状态；分类树/新增/编辑/
删除及 3003/3005；规格组合生成/SKU 映射/价格换算/编辑回显/同步计划/5005；
库存查询/增减/6001/数量校验/重复提交/流水分页；图片校验/上传成功/失败/预览列表/删除。

### 未验证

- **真实 Go 后端联调**：本次未启动后端，所有请求由 MSW 拦截，状态 `NOT_VERIFIED`。
- **真实七牛上传**：无账号与 bucket 配置，`NOT_VERIFIED`；仅验证了凭证 → 直传 → final_url 的代码路径。
- **浏览器视觉验收**：无图形浏览器环境，1366 / 1920 布局未做像素级检查；
  实现使用 Element Plus 栅格与桌面优先布局，`pnpm build` 通过。

---

## 10. 后续维护约束

1. 不要在前端补 `product:read` / `inventory:read` 等后端不存在的权限码。
2. 不要把规格结构写入 `generated/` 或声称后端已持久化。
3. 库存只能走 increase / deduct；不要新增"设置库存 = N"的实现。
4. 商品状态只能通过上下架接口；`PUT` 携带 `status` 会被后端忽略。
5. 修改 SKU 名称等价于删除旧 SKU + 创建新 SKU，需考虑库存记录导致的 5005。
6. 删除分类前必须依赖后端 3003/3005，不做前端级联。
7. 上传必须经后端签发凭证；禁止把 AK/SK 或签名逻辑放进前端。
8. 若后端后续提供库存列表 / 商品状态筛选 / SKU 列表接口，
   只需替换 `api/modules/*` 与对应 adapter，页面无需改动。
9. 契约冻结后，用 `pnpm api:types` 生成类型替换 `api/types/*`，
   保持函数签名与组件调用不变。
