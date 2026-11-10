# W1-01 商品浏览主链路：开发知识文档

- 任务：SurgeCart 用户商城前端 W1-01（商品浏览主链路）
- 分支：`feat/phase2-user-frontend`
- 代码位置：`web/`
- 对应正式报告：`docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md`
- 本文目标：说明本次实际实现、接口依据、数据流、SKU 模型、关键取舍、踩坑与维护注意事项，
  供后续 W2–W5 接手者直接复用，不必重新逆向工程。

本文只描述仓库中真实存在并可运行的代码。所有接口结论均可在对应 Go 源码或 OpenAPI
（本仓库当前不存在）中核对。

---

## 1. 任务边界与开始前核查

### 1.1 边界

本期只做商品浏览主链路：

- 商城首页；
- 商品分类、列表、搜索、排序、分页；
- 商品详情、图片、SKU 选择、评价；
- 加入购物车入口保持禁用；
- 路由级代码拆分与统一错误边界。

明确不做：登录/注册（W2）、购物车（W3）、下单支付（W4）、订单与账户（W5），
不修改 Go 后端、迁移、Docker、Nginx、K8s、Prometheus 配置。

### 1.2 开始前核查结果

| 检查项 | 结果 |
| --- | --- |
| 当前分支 | `feat/phase2-user-frontend` |
| W0 提交 `631dc032edd35cbf8fc5f73ad4ec57abfa71c300` | 已包含在当前 HEAD 历史中 |
| 工作区 | 开始时干净（`nothing to commit`） |
| `contracts/openapi.yaml` | **不存在**（仓库根目录与 `docs/` 均无 contracts 目录） |
| 后端 API 事实来源 | `api/*/v1/*.go`、`internal/controller/*`、`internal/logic/*`、`internal/cmd/routes_frontend.go` |
| 已有 W1-01 文档 | 无同名文件，本次新建 |

因为没有 OpenAPI 契约，本期**没有**运行 `openapi-typescript`，也没有伪造契约文件。
DTO 以 Go 结构体的 JSON tag 为事实来源，集中写在 `web/src/api/types.ts`，
并在文件头逐项标注来源。

---

## 2. 后端接口事实（已核对源码）

### 2.1 统一响应包装

`internal/middleware/response.go` 对所有业务接口输出：

```json
{ "code": 0, "message": "OK", "data": {} }
```

失败时 HTTP 状态与 `code` 同时变化，例如商品不存在：

```json
{ "code": 4001, "message": "商品不存在", "data": null }
```

`code` 是数字业务码。W0 的 `apiClient` 只把字符串 `code` 写入 `ApiError`，
本期在 `readErrorPayload` 中同时接受数字并转字符串，避免业务码丢失。

### 2.2 路由前缀与静态资源

- 前台路由注册在 `internal/cmd/routes_frontend.go`，路径**不带** `/api` 前缀：
  `/products`、`/products/:id`、`/products/:id/reviews`、`/categories`、`/banners`、
  `/recommendations/:code`。
- `nginx/nginx.conf` 的 `location /` 原样透传，没有 `/api` 前缀剥离规则。
- 前端页面路由 `/products`、`/products/:id` 与后端 API 同名，因此 API 必须保留独立前缀，
  否则 SPA 页面请求会被代理拦截。

结论：前端继续使用 `VITE_API_BASE_URL=/api`，由转发层剥离前缀：

- 本地开发：`vite.config.ts` 的 `rewrite: (path) => path.replace(/^\/api/, '')`；
- 生产：需要在反向代理/网关配置等价规则（当前 `nginx/nginx.conf` 未配置，属部署缺口）。

### 2.3 商品与 SKU

来源：`api/product/v1/product.go`、`api/sku/v1/sku.go`、`internal/logic/product/product.go`。

`GET /products` 查询参数：

| 参数 | 说明 |
| --- | --- |
| `page` | 默认 1 |
| `size` | 默认 20，最大 100 |
| `category_id` | 精确匹配，不做子树聚合 |
| `keyword` | trim 后 LIKE 匹配 `name`/`brand`，后端转义 `% _ \` |
| `sort` | 白名单 `id` / `price` / `created_at` / `updated_at`，默认 `created_at` |
| `order` | `asc` / `desc`，默认 `desc` |

`GET /products/:id`：

- 仅 `status=on_shelf` 可见，否则 404 `4001`；
- 每次访问会原子自增 `view_count`；
- 响应是 SPU 字段 + `skus`（仅 `enabled`）。

`GET /products/:id/reviews`：

- 公开接口，仅 `published`，按 `id DESC`；
- 返回 `items / page / size / total / avg_rating / count`；
- 不返回买家昵称，只返回 `user_id`。

SKU 只有 `id / product_id / name / price / status`，
**没有**颜色、尺寸等规格维度，也**没有**库存字段。

### 2.4 分类、轮播图、推荐位

- `GET /categories`：树形、仅启用项；叶子节点没有 `children` 字段（`omitempty`）。
- `GET /banners`：公开、仅启用项，字段 `id/title/image_url/link_url/sort`。
- `GET /recommendations/:code`：公开、仅返回启用推荐位中的 `on_shelf` 商品；
  推荐位不存在或禁用时 `code/name` 为空、`items` 为空数组。

关键业务约束：商品只能绑定「存在且启用的叶子分类」（`docs/design/product.md` INV-005），
且列表的 `category_id` 是精确匹配。因此前台只能把**叶子分类**作为筛选项，
顶级分类只能作为分组标题，否则会得到空列表。

### 2.5 库存缺口

`api/inventory/v1/inventory.go` 只提供 `/admin/inventories/:sku_id` 等管理端接口，
需要管理员认证；前台没有公开库存查询接口。SKU DTO 也没有库存字段。
因此详情页的「库存状态」只能明确说明「当前版本未提供公开库存查询，以下单校验为准」，
不能伪造「现货/缺货」。

---

## 3. 实现总览

### 3.1 路由

| 路径 | 页面 | 加载方式 |
| --- | --- | --- |
| `/` | `HomePage` | 同步 |
| `/products` | `ProductsPage` | `React.lazy` 独立 chunk |
| `/products/:id` | `ProductDetailPage` | `React.lazy` 独立 chunk |
| `*` | `NotFoundPage` | 同步 |

分类没有独立路由：分类是列表页的筛选条件，使用 `/products?category_id=<leafId>`，
可分享、可刷新、可被浏览器前进后退恢复。

### 3.2 目录

```text
web/src/
├── api/
│   ├── client.ts            传输层（超时/取消/Token/错误）
│   ├── envelope.ts          统一响应解包 getData
│   ├── query-keys.ts        统一 Query Key
│   ├── types.ts             业务 DTO（来源：Go 源码）
│   ├── products.ts          /products、注意分页与 keyword
│   ├── categories.ts        /categories
│   ├── banners.ts           /banners
│   ├── reviews.ts           /products/:id/reviews
│   └── recommendations.ts   /recommendations/:code
├── components/product/
│   ├── product-card.tsx     统一卡片（1:1 图、两行标题、价格）
│   ├── product-grid.tsx     栅格 + 骨架屏
│   ├── product-image.tsx    空图/破图占位
│   ├── product-gallery.tsx  主图 + 缩略图
│   ├── sku-selector.tsx     单层 SKU 选择
│   ├── rating-stars.tsx     星级展示
│   ├── review-section.tsx   评价汇总 + 分页
│   └── category-nav.tsx     桌面侧栏 / 移动端胶囊
├── components/common/
│   ├── app-error-boundary.tsx  应用级渲染错误兜底
│   ├── route-error-page.tsx    路由 errorElement
│   └── route-loading.tsx       懒加载占位
├── config/env.ts            首页推荐位 code
├── pages/
│   ├── home-page.tsx        商城首页
│   ├── products-page.tsx    列表/搜索/分类/排序/分页
│   └── product-detail-page.tsx
├── router/
│   ├── index.tsx            路由表 + errorElement
│   └── lazy-pages.tsx       路由级懒加载
└── utils/
    ├── catalog-search.ts    URL 查询状态与排序选项
    └── category-tree.ts     分类树工具与叶子收集
```

### 3.3 页面能力

- 首页：品牌首屏、轮播图（来自 `/banners`）、分类入口（叶子分类可点击）、
  最新上架（`sort=created_at&order=desc`）、可选推荐位。
- 列表：URL 驱动的关键词/分类/排序/页码；总数；骨架屏、空状态、错误状态；
  `keepPreviousData` 避免翻页闪烁；切换筛选自动回到第 1 页。
- 详情：面包屑、图册、名称/品牌/浏览量、价格（随 SKU 变化）、SKU 选择、
  库存说明、禁用购物车入口、商品介绍（纯文本）、评价汇总与翻页、404 状态。
- Header：搜索框提交跳转 `/products?q=...`，导航新增「全部商品」。
- 移动端：分类胶囊横向滚动、移动导航抽屉可跳转商品列表。

---

## 4. API 调用链

以商品列表为例：

```text
ProductsPage (URL ?q=&category_id=&page=&sort=&order=)
  └─ useQuery({ queryKey: queryKeys.products.list({...}), queryFn })
       └─ fetchProductList({...}, signal)
            └─ getData('/products', { query: { page,size,category_id,keyword,sort,order }, signal })
                 ├─ apiClient.get('/api/products?...')
                 │    └─ fetch → Vite/Nginx 转发层剥离 /api
                 └─ unwrapEnvelope({code,message,data}) → data
```

其他链路同构：

- 首页分类：`HomePage → fetchCategoryTree → GET /categories`
- 首页轮播：`HomePage → fetchBanners → GET /banners`
- 首页最新：`HomePage → fetchProductList → GET /products?sort=created_at&order=desc`
- 首页推荐：`HomePage → fetchRecommendation(code) → GET /recommendations/:code`
- 详情：`ProductDetailPage → fetchProductDetail(id) → GET /products/:id`
- 评价：`ReviewSection → fetchProductReviews(id,{page,size}) → GET /products/:id/reviews`

组件层不出现 `fetch`/URL 拼接；所有请求参数在 API 模块内转换并集中处理空关键词。

---

## 5. React Query 数据管理

### 5.1 统一 Query Key

`src/api/query-keys.ts`：

```ts
queryKeys.categories.tree()
queryKeys.banners.list()
queryKeys.products.list({ page, size, categoryId, keyword, sort, order })
queryKeys.products.detail(productId)
queryKeys.reviews.productList(productId, page, size)
queryKeys.recommendations.byCode(code)
```

列表 key 携带完整查询参数，参数变化自然产生新缓存；详情 id 统一 `String(id)`，
避免路由参数与数字 id 命中不同缓存。组件内禁止手写 key 数组。

### 5.2 默认缓存策略

沿用 `createQueryClient()`：`staleTime 30s`、`gcTime 5min`、失败重试 1 次、
窗口聚焦不自动刷新。商城浏览场景下 30s 足够避免重复请求，也不会长时间看到过期价格。

### 5.3 翻页与筛选

- 列表与评价都使用 `placeholderData: keepPreviousData`：翻页时保留上一页数据，
  用 `isFetching` 降低不透明度，避免整块骨架屏闪烁。
- 筛选/搜索/排序变化时把 `page` 重置为 1；分页用 `replace` 写入历史，
  避免每次翻页都新增一条浏览器历史。
- URL 是筛选条件唯一事实来源，`parseCatalogSearch` 对非法值（负页码、非法排序字段）
  统一回落默认值。

### 5.4 错误与重试

- `ApiError` 保留 `kind/status/code/details`；列表/详情页按错误状态展示 `ErrorState` 并提供重试。
- 详情 404 不重试（`retry` 中对 404 返回 `false`），直接展示「商品不存在或已下架」。
- 评价加载失败只影响评价区块，不影响商品主信息。

### 5.5 view_count 副作用

`GET /products/:id` 每次成功访问都会自增浏览量，因此：

- 详情查询依靠 React Query 去重与 `staleTime`，避免无意义重复请求；
- 不要在渲染过程中额外触发详情请求；
- 未来若需要「浏览历史」等埋点，必须避免与详情查询重复计数。

---

## 6. SKU 数据模型与交互

```ts
interface Sku {
  id: number
  product_id: number
  name: string
  price: number   // 整数分
  status: 'enabled' | 'disabled'
  created_at: string | null
  updated_at: string | null
}
```

- 详情接口只返回 `enabled` SKU；前端不自行过滤 `disabled`。
- SKU 没有规格维度，因此 UI 是单层规格按钮，不虚构「颜色 × 尺寸」矩阵。
- 默认选中第一个 SKU；未配置 SKU 时展示「该商品暂未配置可售规格」，价格回落到 SPU 价格。
- 选中 SKU 的价格成为详情主价格；切换时同步「已选规格」文案。
- 价格为整数分，统一经 `PriceDisplay`/`formatPriceParts` 展示，禁止浮点元计算。
- 加入购物车入口保持 `disabled`，并用可读文案说明 W3 才开放；未提前实现任何购物车业务。

---

## 7. 关键设计决策

1. **不生成 OpenAPI 类型**：仓库没有 `contracts/openapi.yaml`，强行手写契约会制造假事实。
   DTO 只按 Go 源码定义，并在 `types.ts` 标注来源；契约到位后再切 `openapi-typescript`。
2. **保留 `/api` 前缀而非同源根路径**：SPA 的 `/products` 与 API `/products` 冲突；
   独立前缀 + 转发层剥离是唯一不破坏刷新/直达链接的方案。
3. **分类只筛选叶子节点**：与后端「商品绑定叶子分类 + category_id 精确匹配」保持一致，
   避免顶级分类点进去永远为空。
4. **推荐位用环境变量开关**：后端没有公开的推荐位枚举接口，前端不能猜 code。
   `VITE_HOME_RECOMMEND_CODE` 为空时不请求、不渲染；配置后按真实接口展示。
5. **商品详情按纯文本渲染**：`detail` 可能包含任意 HTML，直接 `dangerouslySetInnerHTML`
   会引入 XSS；当前用 `whitespace-pre-line` 展示文本，若后续要富文本需先做服务端清洗。
6. **统一错误边界两层**：`AppErrorBoundary` 兜 Router 之外的渲染异常，
   路由 `errorElement` 兜页面级异常并保留 Header/Footer。
7. **路由级拆分**：列表与详情各自独立 chunk（构建产物已验证），首页保持同步首屏。
8. **图片统一占位**：商品图/轮播图加载失败或为空时展示占位，不渲染破图、不依赖外部占位服务。
9. **不改后端/网关**：库存、推荐位 code、`/api` 生产剥离规则均作为缺口记录，不越权修改。

---

## 8. 遇到的问题与解决方式

### 8.1 统一响应包装

W0 的 `apiClient` 直接把响应 JSON 当业务数据。本期新增 `getData` 统一解包，
并把结构不符归为 `parse` 错误，避免把 HTML 错误页当商品数据渲染。

### 8.2 jsdom 相对 URL

浏览器允许 `fetch('/api/products')`，Node/jsdom 的 fetch 要求绝对 URL。
处理：

- `apiClient` 改为**调用时**读取 `globalThis.fetch`（而非模块加载时 bind），
  这样测试可以先启动 MSW 再替换 fetch；
- `src/test/setup.ts` 在 `server.listen()` 之后包一层 origin 解析，
  仅测试环境生效，生产代码保持相对路径。

### 8.3 MSW 与 Playwright 并行

并行 worker + Vite 首次依赖优化时，个别页面在 MSW 注册完成前出现空白页。
处理：Playwright 统一 `workers: 1`；轮播图请求增加 MSW `/storage/banners/:file` 处理器，
既保持真实 URL 形态，也避免 E2E 依赖后端静态目录。

### 8.4 后端无商品种子

后端 migration 不插入商品/分类/轮播数据。真实联调时临时插入最小数据（分类、2 个上架商品、
1 个下架商品、SKU、评价、轮播图、推荐位），验证完成后清理容器与数据卷。
仓库不包含这些数据，也不应把 Mock 数据当成后端种子。

### 8.5 后端启动依赖七牛检查

`serve` 启动会真实检查七牛 bucket。本地验证使用仓库已有的
`test/serveproc`（`-tags serve_lifecycle`）+ `test/container/qiniu_mock`，
只改变七牛 UC endpoint，校验逻辑不变；生产二进制不读取该变量。
这是**本地联调手段**，不是产品功能。

### 8.6 生产 `/api` 前缀缺口

`nginx/nginx.conf` 没有 `/api` → 后端的重写规则；本地由 Vite 代理解決。
部署前必须由 Owner 在网关/反向代理补齐，否则生产请求会 404。
本期按约束未修改 Nginx 配置。

---

## 9. 测试验证

### 9.1 门禁命令

```bash
cd web
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
```

实际结果见交付报告。测试覆盖：

- API 模块：解包、分页、关键词 trim、分类过滤、404 业务码、评价聚合。
- URL 状态：解析/生成、非法值回落、排序转换。
- 分类树：叶子收集、查找、层级展开。
- 列表页：首屏、Header 搜索、翻页、分类筛选、空状态、错误状态、价格排序。
- 详情页：价格、SKU 切换、评价翻页、404、非法 id、购物车禁用。
- 首页：分类入口、最新商品、轮播图、推荐位默认关闭。
- E2E：首页、列表、搜索、分页、分类、排序、空/错状态、详情、SKU、评价、404、移动导航。

### 9.2 真实后端联调

在本地临时启动 MySQL/Redis + Go 后端（migrations + 手工最小数据），
Vite 不启用 MSW，通过 `/api` 代理访问真实接口。验证通过：

- 首页分类、轮播、最新商品、推荐位；
- 列表总数、关键词搜索；
- 详情价格、SKU 切换价格、评价、404；
- 加入购物车仍为禁用。

注意：这是一次性人工联调，不属于 `npm run test:e2e`；
仓库 E2E 全部基于 MSW，不能表述为「后端联调通过」。

---

## 10. 后续维护注意事项

1. **接口字段变更**：先改后端源码/契约，再更新 `src/api/types.ts` 与对应 API 模块；
   不要在前端单独加字段。
2. **新增查询**：必须在 `queryKeys` 中登记；列表参数变化时确认 key 覆盖完整。
3. **分类筛选**：继续保持叶子节点；如后端未来支持子树聚合，再开放顶级分类点击。
4. **库存能力**：后端提供公开库存接口后，更新详情页库存区块与 SKU 模型；
   在此之前不要写死「有货/缺货」。
5. **推荐位**：后端若提供公开推荐位列表接口，可移除 `VITE_HOME_RECOMMEND_CODE` 的配置依赖。
6. **OpenAPI**：契约落地后运行 `npm run typegen`，逐步替换手写 DTO；
   过渡期保持 API 模块签名不变，减少组件改动。
7. **view_count**：任何新增的详情预取/轮询都会放大浏览量，需评估业务影响。
8. **E2E Mock 钩子**：`keyword=__error__` 只存在于 `src/mocks/handlers.ts`，
   不要在后端或生产代码中实现同名逻辑。
9. **部署**：上线前必须补齐 `/api` 前缀剥离；否则所有业务请求会命中 SPA 或被后端 404。
10. **购物车衔接**：W3 接入时把禁用的「加入购物车」替换为真实 mutation，
    复用当前选中的 `sku.id` 与数量，不要复制价格计算逻辑。
