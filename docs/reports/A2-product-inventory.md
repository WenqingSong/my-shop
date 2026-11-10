# A2 商品与库存管理 — 交付报告

- 阶段：A2（商品管理、商品分类、SKU 与规格、库存管理、商品图片上传）
- 分支：`feat/phase2-admin-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 开发知识文档：`.agent/tasks/dev/A2-product-inventory.md`
- 后端改动：无（未修改 `internal/**`、`api/**`、`migrations/**`、`contracts/**`）

---

## 1. 实现范围

本次只在现有 Vue 3 管理后台内实现 A2，复用 A0/A1 的布局、认证、Axios、
权限快照、路由与 MSW 体系，未新增运行时依赖。

未进入 A3/A4/A5 范围：没有订单、退款、优惠券、秒杀、用户、管理员、角色、
财务或数据大屏功能。

---

## 2. 实际完成的功能

状态说明：

- **IMPLEMENTED**：代码已实现。
- **VERIFIED**：通过 MSW + Vitest 验证前端业务逻辑。
- **PARTIALLY_VERIFIED**：部分路径验证（例如只验证了前端分支）。
- **NOT_VERIFIED**：未与真实后端/第三方联调。
- **NOT_IMPLEMENTED**：后端无能力或本次未做。

### 2.1 商品管理

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 商品列表 | IMPLEMENTED / VERIFIED | 真实 `GET /admin/products`，分页、关键词、分类筛选 |
| 商品名称搜索 | IMPLEMENTED / VERIFIED | 后端 `keyword` 模糊匹配 name/brand |
| 商品分类筛选 | IMPLEMENTED / VERIFIED | 后端 `category_id` |
| 商品状态筛选 | IMPLEMENTED / VERIFIED | 后端**无** `status` 参数，改为跨页拉取真实数据后前端过滤，页面有说明与截断告警 |
| 商品图片 / 名称 / ID / 分类 / 价格 | IMPLEMENTED / VERIFIED | 主图带预览 |
| SKU 数量 | IMPLEMENTED / VERIFIED | 列表接口不返回，逐条请求商品详情补全（并发 4，失败显示 `—`） |
| 库存信息 | PARTIALLY_VERIFIED | 列表接口无库存字段；商品详情抽屉中逐 SKU 查询真实库存 |
| 创建 / 编辑时间 | IMPLEMENTED / VERIFIED | `created_at` / `updated_at` |
| 查看详情 | IMPLEMENTED / VERIFIED | 抽屉：商品信息 + 图片 + SKU + 逐 SKU 库存 |
| 创建商品 | IMPLEMENTED / VERIFIED | 后端强制 `draft` |
| 编辑商品 | IMPLEMENTED / VERIFIED | 表单回显，金额元 ⇄ 分 |
| 上架 / 下架 | IMPLEMENTED / VERIFIED | 真实状态迁移接口 + 二次确认 |
| 删除商品 | NOT_IMPLEMENTED | 后端无删除接口，未提供无效按钮 |

### 2.2 商品分类

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 树形列表 | IMPLEMENTED / VERIFIED | `GET /categories`，层级、父级、排序、状态 |
| 新增 / 编辑 | IMPLEMENTED / VERIFIED | Dialog；父级下拉禁用自身与后代 |
| 删除 | IMPLEMENTED / VERIFIED | 二次确认；3003/3005 展示后端原文 |
| 级联删除 | NOT_IMPLEMENTED | 后端明确拒绝，前端不擅自级联 |
| 禁用分类查看 | NOT_IMPLEMENTED | 后端列表仅返回启用项，页面已注明 |

### 2.3 SKU 与规格

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 规格组 | IMPLEMENTED / VERIFIED | 前端 provisional，不持久化 |
| 规格值 | IMPLEMENTED / VERIFIED | 去重、去空 |
| 多规格组合生成 | IMPLEMENTED / VERIFIED | 笛卡尔积，显式生成 + 二次确认 |
| SKU 列表 / 编码 | IMPLEMENTED / VERIFIED | 编码字段后端不存在，未提供假字段 |
| SKU 价格 / 状态 | IMPLEMENTED / VERIFIED | 整数分；启用/禁用 |
| SKU 图片 | NOT_IMPLEMENTED | 后端无字段，未做假上传入口 |
| SKU 同步 | IMPLEMENTED / VERIFIED | create → update → delete，失败明细可见 |
| 规格结构持久化 | NOT_IMPLEMENTED | 后端 SKU 为平铺模型，仅持久化名称/价格/状态 |

### 2.4 库存

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 库存查询 | IMPLEMENTED / VERIFIED | 商品列表/详情 + 逐 SKU `GET /admin/inventories/:sku_id` |
| 商品名称 / 商品 ID 查询 | IMPLEMENTED / VERIFIED | 列表 `keyword`；ID 精确查询走商品详情 |
| SKU 编码搜索 | NOT_IMPLEMENTED | 后端无 SKU 编码字段，页面改为按 SKU 名称过滤 |
| 当前库存 | IMPLEMENTED / VERIFIED | 后端真实值；无记录显示 0 |
| 锁定库存 / 可售库存 | NOT_IMPLEMENTED | 后端未提供，页面不展示、不推导 |
| 增加 / 扣减库存 | IMPLEMENTED / VERIFIED | 真实 increase / deduct，正整数校验，二次确认，防重复提交 |
| 库存不足 | IMPLEMENTED / VERIFIED | 展示后端 6001「库存不足」，无乐观更新 |
| 库存流水 | IMPLEMENTED / VERIFIED | 分页、筛选 SKU、只读 |
| 调整原因 | NOT_IMPLEMENTED | 后端请求无 reason 字段；UI 明确提示原因不会保存 |

### 2.5 图片上传

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 七牛凭证 | IMPLEMENTED / VERIFIED | 复用 `GET /admin/qiniu/upload/token` |
| 浏览器直传 | IMPLEMENTED / PARTIALLY_VERIFIED | 独立 Axios 实例；真实七牛 `NOT_VERIFIED` |
| 主图 / 轮播图 | IMPLEMENTED / VERIFIED | 1 张 / 8 张 |
| 上传进度 / 失败重试 | IMPLEMENTED / VERIFIED | 失败项可重试，不生成假 URL |
| 预览 / 删除 | IMPLEMENTED / VERIFIED | `el-image` 预览，删除同步表单 |
| 类型 / 大小校验 | IMPLEMENTED / VERIFIED | jpg/jpeg/png/gif/webp，≤10MB |

---

## 3. 重要修改文件

新增（均在 `admin/**`）：

- `src/api/types/{product,category,sku,inventory,upload}.ts`
- `src/api/modules/{product,category,sku,inventory,upload}.ts`
- `src/api/adapters/{product,category,sku,inventory}-adapter.ts`
- `src/composables/useSubmitGuard.ts`
- `src/utils/error-message.ts`
- `src/views/product/{index.vue,form.vue,components/*,composables/*}`
- `src/views/category/{index.vue,components/*}`
- `src/views/inventory/{index.vue,transactions.vue,components/*}`
- `src/router/modules/{product,inventory}.ts`
- `src/mocks/handlers/{mock-db,shared,product,category,inventory,upload}.ts`
- `tests/unit/{product,category,sku,inventory,upload}.test.ts`
- `tests/helpers/business-error.ts`

修改：

- `src/utils/permission.ts` — 增加后端真实权限码常量（A1 角色映射未改动）。
- `src/utils/http/index.ts`、`src/utils/http/types.d.ts` — 非 2xx 响应附加
  `businessCode/businessMessage`，用于展示后端业务错误；未改变既有 reject 类型。
- `src/utils/datetime.ts` — 新增 `formatServerDateTime`。
- `src/router/enums.ts` — 新增菜单 rank。
- `src/mocks/handlers/index.ts` — 注册 A2 handlers。
- `locales/zh-CN.yaml`、`locales/en.yaml` — 菜单文案。

---

## 4. 已接入的真实 API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/admin/products` | 商品列表 |
| GET | `/admin/products/:id` | 商品详情（含 SKU） |
| POST | `/admin/products` | 创建商品 |
| PUT | `/admin/products/:id` | 更新商品 |
| POST | `/admin/products/:id/on-shelf` | 上架 |
| POST | `/admin/products/:id/off-shelf` | 下架 |
| GET | `/categories` | 分类树 |
| GET | `/categories/:id` | 分类详情 |
| POST | `/categories` | 创建分类 |
| PUT | `/categories/:id` | 更新分类 |
| DELETE | `/categories/:id` | 删除分类 |
| POST | `/admin/skus` | 创建 SKU |
| PUT | `/admin/skus/:id` | 更新 SKU |
| DELETE | `/admin/skus/:id` | 删除 SKU |
| GET | `/admin/inventories/:sku_id` | 查询库存 |
| GET | `/admin/inventories/:sku_id/logs` | 库存流水 |
| POST | `/admin/inventories/:sku_id/increase` | 增加库存 |
| POST | `/admin/inventories/:sku_id/deduct` | 扣减库存 |
| GET | `/admin/qiniu/upload/token` | 上传凭证 |

权限码（前端按钮控制，全部来自后端）：

`product:create`、`product:update`、`product:on_shelf`、`product:off_shelf`、
`category:create`、`category:update`、`category:delete`、
`sku:create`、`sku:update`、`sku:delete`、
`inventory:increase`、`inventory:deduct`。

后端没有 `product:read` / `category:read` / `sku:read` / `inventory:read`，
因此菜单与页面访问只依赖登录态，未编造读权限码。

---

## 5. Provisional API / 前端组合

不存在"新增后端接口"或"虚假契约"，以下均为前端组合或暂定前端模型：

| 项目 | 类型 | 边界 |
| --- | --- | --- |
| 商品状态筛选 | 前端组合 | 跨页拉取真实数据后过滤（≤5 页 × 100） |
| 商品列表 SKU 数量 | 前端组合 | 逐条商品详情补全 |
| 商品详情 / 库存页库存 | 前端组合 | 逐 SKU 查询库存 |
| 规格组 / 规格值 | PROVISIONAL | 仅前端，持久化为 SKU 名称 |
| SKU 编码 / SKU 图片 | PROVISIONAL | 未提供 UI，后端无字段 |
| 库存调整原因 | PROVISIONAL | 不提交，UI 明确提示 |
| 上传凭证 / 直传地址 | MSW Mock | 仅本地演示与测试，真实七牛未联调 |

未在 `generated/` 目录放置任何手写或暂定类型；`contracts/openapi.yaml` 目前不存在，
本次也未创建。

---

## 6. 测试结果

实际执行命令与结果：

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过（vue-tsc，0 error） |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 通过：12 个测试文件、162 个用例（含 A1 全部回归） |
| `pnpm build` | 通过，产物约 3.16 MB |

A2 新增 5 个测试文件：

- `tests/unit/product.test.ts`：列表/搜索/分页/详情/创建/编辑/上下架/表单校验/错误状态/权限码。
- `tests/unit/category.test.ts`：树形列表/层级/新增/编辑/删除/3003/3005/禁用分类限制。
- `tests/unit/sku.test.ts`：规格组合/名称编解码/编辑回显/同步计划/价格换算/5005 失败明细。
- `tests/unit/inventory.test.ts`：库存查询/增减/6001/数量校验/防重复提交/流水分页/接口失败。
- `tests/unit/upload.test.ts`：文件校验/凭证/直传成功/失败/预览列表/删除/上限/上传中删除。

### 真实后端联调

**NOT_VERIFIED。** 本次未启动 Go 后端，所有 HTTP 请求由 MSW 拦截；
真实接口的路径、字段与权限码来自仓库内的 Go 源码只读核查，未做运行时联调。

### 真实七牛上传

**NOT_VERIFIED。** 无七牛账号 / bucket 配置，仅验证了前端凭证 → 直传 → `final_url` 的代码路径。

### 界面验收

**NOT_VERIFIED。** 无图形浏览器环境，未做 1366 / 1920 像素级检查；
布局按 Element Plus 栅格与桌面优先实现，生产构建通过。

---

## 7. 未完成事项与已知限制

1. 商品删除、库存列表接口、SKU 独立列表、分类后台全量列表：后端不存在，未实现。
2. 分类禁用后无法在后台列出（后端列表仅启用项），只能通过接口按 ID 读取。
3. 商品状态筛选为前端跨页过滤，超过 500 条时提示结果可能不完整。
4. 修改规格值会导致 SKU 名称变化，从而删除旧 SKU、创建新 SKU；
   若旧 SKU 有库存记录，后端拒绝删除并在页面展示错误。
5. 非超管角色的写按钮默认隐藏：`/admin/me` 当前不返回 `permissions[]`，
   A1 的角色映射只登记了 `super_admin`。真正授权仍由后端 `RequirePermission` 保证。
6. 库存调整原因无法保存（后端请求无该字段）。
7. 列表页「库存」列显示 `—`（后端未返回），实际库存在详情/库存页查看。

---

## 8. Git

- 功能提交 SHA：见本节末尾（提交后回填）
- 目标分支：`feat/phase2-admin-frontend`
- 远端：`origin/feat/phase2-admin-frontend`
- 推送方式：普通 `git push`（未 force push，未改写历史）

---

## 9. A3 启动条件

1. A2 提交已推送至 `feat/phase2-admin-frontend`，工作区干净。
2. Go 后端订单 / 售后接口与 OpenAPI 契约具备可核对的定义
   （当前仅确认存在 `/admin/orders`、`/admin/refunds` 路由，未纳入 A2）。
3. A2 的真实后端联调完成，至少确认：商品/SKU 写接口、库存增减、
   分类删除错误码、上传凭证在真实环境可用。
4. A2 的 provisional 边界（规格结构、库存原因、状态筛选）在后端补齐后
   通过 `api/modules` + adapter 替换，页面无需重写。
5. Owner 明确下发 A3 任务书后再启动，不自动进入下一阶段。
