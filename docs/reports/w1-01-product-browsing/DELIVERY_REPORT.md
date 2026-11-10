# W1-01 商品浏览主链路：交付报告

- 任务：SurgeCart 用户商城前端 W1-01（商品浏览主链路）
- 分支：`feat/phase2-user-frontend`
- 模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W1-01-product-browsing.md`

---

## 0. 摘要

本期在 W0 工程基线上完成用户商城商品浏览主链路：商城首页、商品分类、商品搜索、
排序与分页、商品详情、SKU 选择、商品评价，并把页面路由接入路由级代码拆分与统一错误边界。
购物车、登录与下单不在本期范围，相关入口保持明确禁用状态。

接口事实来源为仓库内的 Go 后端源码。仓库**不存在** `contracts/openapi.yaml`，
因此没有生成 OpenAPI 类型，也没有新增/修改任何所谓「契约」文件；
前端 DTO 按 Go 结构体 JSON tag 定义并标注来源。

已执行 `lint / typecheck / test / build / test:e2e` 全部门禁；
并额外完成一次真实后端联调（临时 MySQL/Redis + Go 服务 + Vite 无 Mock）。

---

## 1. 开始前核查

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend`（未切分支、未合并、未 push） |
| W0 提交 `631dc032edd35cbf8fc5f73ad4ec57abfa71c300` | 已在当前 HEAD 历史中 |
| 工作区初始状态 | clean，无未提交改动可被覆盖 |
| `contracts/openapi.yaml` | 不存在 |
| 已有同名 W1-01 文档 | 不存在，未覆盖既有成果 |
| 后端商品/分类/SKU/评价/推荐/轮播接口 | 均已存在，见第 3 节 |

后端相关接口源码位置：

- 路由注册：`internal/cmd/routes_frontend.go`
- 统一响应：`internal/middleware/response.go`
- 商品：`api/product/v1/product.go`、`internal/controller/product/product.go`、`internal/logic/product/product.go`
- SKU：`api/sku/v1/sku.go`、`internal/logic/sku/`
- 分类：`api/categories/v1/categories.go`、`internal/logic/categories/categories.go`
- 评价：`api/review/v1/review.go`、`internal/logic/review/review.go`
- 轮播图：`api/banner/v1/banner.go`、`internal/logic/banner/banner.go`
- 推荐位：`api/recommendation/v1/recommendation.go`、`internal/logic/recommendation/recommendation.go`
- 库存（仅管理端）：`api/inventory/v1/inventory.go`

---

## 2. 实际完成的页面与功能

### 2.1 页面路由

| 路由 | 页面 | 状态 |
| --- | --- | --- |
| `/` | 商城首页 | 完成 |
| `/products` | 商品列表 / 分类筛选 / 搜索 / 排序 / 分页 | 完成 |
| `/products/:id` | 商品详情 / SKU / 评价 | 完成 |
| `*` | 404 | 保留并适配新站点 |

分类不单独建路由，统一使用 `/products?category_id=<叶子分类 id>`。

### 2.2 商城首页

- 品牌首屏与「浏览全部商品」入口。
- 轮播图区域：使用 `GET /banners` 真实数据；无轮播时展示分类快捷入口面板。
- 商品分类区域：展示顶级分类与可点击的叶子分类，并给出分类数量。
- 最新上架区域：`GET /products?sort=created_at&order=desc`，商品卡片进入详情。
- 推荐位区域：仅当配置 `VITE_HOME_RECOMMEND_CODE` 且接口返回商品时展示。
- 分类/商品/轮播均有加载、空、错误状态。

### 2.3 商品列表与搜索

- 商品卡片：1:1 主图、两行标题、品牌、`PriceDisplay` 价格，整卡可点击。
- 分类筛选：桌面左侧分组侧栏，移动端横向胶囊；只暴露叶子分类。
- 搜索：Header 搜索框提交跳转 `/products?q=关键词`；列表页展示关键词筛选标签。
- 排序：最新上架（默认）、价格从低到高、价格从高到低。
- 分页：每页 12 件，页码写入 URL，翻页保留上一页数据避免闪烁。
- 状态：骨架屏、空状态（含清除筛选/回到第一页）、错误状态（可重试）。
- URL 是筛选条件的唯一事实来源，可分享、刷新、前进后退恢复。

### 2.4 商品详情

- 图册：主图 + 缩略图切换，同图去重。
- 商品信息：名称、品牌、浏览量、商品编号、分类面包屑。
- 价格：展示当前 SKU 价格，切换 SKU 同步更新。
- SKU：单层规格按钮，默认第一个；无 SKU 时给出明确提示。
- 库存状态：明确说明当前版本没有公开库存查询接口，以下单校验为准（不伪造库存）。
- 加入购物车：按钮保留但禁用，文案说明 W3 才开放。
- 商品介绍：纯文本展示（不使用 `dangerouslySetInnerHTML`）。
- 评价：平均分、星级、总数、列表分页；评价失败不影响商品主信息。
- 404：商品不存在/已下架展示独立状态与返回列表入口。

### 2.5 工程质量

- 路由级代码拆分：列表页、详情页各自独立 chunk（构建产物已确认）。
- 两层错误边界：`AppErrorBoundary`（Router 外）+ 路由 `errorElement`（页面级，保留公共布局）。
- 统一 Query Key、统一响应解包、组件禁止直接 `fetch`。
- Header/Footer/移动导航更新为真实商品入口，不制造死链。

---

## 3. 页面使用的后端 API 明细

> 以下接口均来自实际 Go 源码与真实请求验证。响应统一包裹为
> `{code:number,message:string,data:...}`；失败返回非 2xx + `data:null`。

### 3.1 `GET /categories`（无鉴权）

- 使用页面：首页分类入口、列表页分类筛选、详情页面包屑。
- 请求参数：无。
- 响应 `data`：

```json
{
  "items": [
    {
      "id": 1,
      "parent_id": 0,
      "name": "数码",
      "sort": 1,
      "status": 1,
      "created_at": "2026-11-10 04:11:19",
      "updated_at": "2026-11-10 04:11:19",
      "children": [
        {
          "id": 11,
          "parent_id": 1,
          "name": "手机",
          "sort": 1,
          "status": 1,
          "created_at": "2026-11-10 04:11:19",
          "updated_at": "2026-11-10 04:11:19"
        }
      ]
    }
  ]
}
```

- 说明：仅启用项；叶子节点不返回 `children`；同级按 `sort,id` 升序。
- 源码：`api/categories/v1/categories.go`（`ListReq`/`ListRes`）、`internal/logic/categories/categories.go`、`internal/cmd/routes_frontend.go`。

### 3.2 `GET /products`（无鉴权，仅上架）

- 使用页面：首页「最新上架」、商品列表/搜索/分类/排序/分页。
- 请求参数（query）：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `page` | int | 默认 1 |
| `size` | int | 默认 20，最大 100；前端列表固定 12，首页固定 10 |
| `category_id` | int | 精确匹配；前端只传叶子分类 id |
| `keyword` | string | 匹配 `name`/`brand`；前端 trim 后为空则不发送 |
| `sort` | string | `id`/`price`/`created_at`/`updated_at`，默认 `created_at` |
| `order` | string | `asc`/`desc`，默认 `desc` |

- 响应 `data`：

```json
{
  "items": [
    {
      "id": 2,
      "name": "云听 Air 3 主动降噪耳机",
      "brand": "云听",
      "category_id": 12,
      "price": 89900,
      "main_image": "/storage/banners/banner-2.png",
      "detail": "双麦克风主动降噪。",
      "status": "on_shelf",
      "view_count": 0,
      "images": [],
      "created_at": "2026-11-02 10:00:00",
      "updated_at": "2026-11-02 10:00:00"
    }
  ],
  "total": 2,
  "page": 1,
  "size": 12
}
```

- 说明：`price` 为整数分；`images` 为图册（按 `sort` 升序）；列表不触发浏览量自增。
- 源码：`api/product/v1/product.go`、`internal/logic/product/product.go`（`queryList`/`normalizePage`）。

### 3.3 `GET /products/:id`（无鉴权，仅上架）

- 使用页面：商品详情。
- 请求参数：路径 `id`（int64）。
- 响应 `data`：SPU 字段（同上）+ `skus`：

```json
{
  "id": 1,
  "name": "星海 X1 Pro 5G 手机 12GB+256GB",
  "brand": "星海",
  "category_id": 11,
  "price": 429900,
  "main_image": "/storage/banners/banner-1.png",
  "detail": "6.7 英寸高刷屏。\n支持 66W 快充。",
  "status": "on_shelf",
  "view_count": 1,
  "images": ["/storage/banners/banner-3.png"],
  "created_at": "2026-11-01 10:00:00",
  "updated_at": "2026-11-01 10:00:00",
  "skus": [
    {
      "id": 11,
      "product_id": 1,
      "name": "标准款",
      "price": 429900,
      "status": "enabled",
      "created_at": "2026-11-10 04:11:19",
      "updated_at": "2026-11-10 04:11:19"
    }
  ]
}
```

- 失败：商品不存在或非上架 → HTTP 404，`{"code":4001,"message":"商品不存在","data":null}`。
- 副作用：每次成功访问 `view_count + 1`。
- 源码：`api/product/v1/product.go`（`DetailReq`/`DetailRes`）、`internal/logic/product/product.go`（`Detail`）、`api/sku/v1/sku.go`。

### 3.4 `GET /products/:id/reviews`（无鉴权）

- 使用页面：详情页评价区块。
- 请求参数：路径 `id`；query `page`（默认 1）、`size`（默认 20，最大 100；前端固定 5）。
- 响应 `data`：

```json
{
  "items": [
    {
      "id": 2,
      "product_id": 1,
      "sku_id": 12,
      "rating": 4,
      "content": "真实后端联调评价：手感不错。",
      "user_id": 1002,
      "created_at": "2026-11-06 10:00:00"
    }
  ],
  "page": 1,
  "size": 5,
  "total": 2,
  "avg_rating": 4.5,
  "count": 2
}
```

- 说明：仅 `published`，按 `id DESC`；`avg_rating`、`count` 为实时聚合；
  不返回昵称，前端统一展示「买家」，不展示 `user_id`。
- 源码：`api/review/v1/review.go`（`ListReq`/`ListRes`）、`internal/logic/review/review.go`（`ListProduct`）。

### 3.5 `GET /banners`（无鉴权）

- 使用页面：首页轮播图区域。
- 请求参数：无。
- 响应 `data`：

```json
{
  "items": [
    {
      "id": 1,
      "title": "新品首发",
      "image_url": "/storage/banners/banner-1.png",
      "link_url": "/products?category_id=11",
      "sort": 1
    }
  ]
}
```

- 源码：`api/banner/v1/banner.go`、`internal/logic/banner/banner.go`。

### 3.6 `GET /recommendations/:code`（无鉴权）

- 使用页面：首页推荐位（仅当 `VITE_HOME_RECOMMEND_CODE` 非空）。
- 请求参数：路径 `code`（运营在后台创建的稳定标识）。
- 响应 `data`：

```json
{
  "code": "home",
  "name": "编辑推荐",
  "items": [
    {
      "product_id": 1,
      "name": "星海 X1 Pro 5G 手机 12GB+256GB",
      "main_image": "/storage/banners/banner-1.png",
      "price": 429900,
      "sort": 0
    }
  ]
}
```

- 说明：推荐位不存在/禁用时返回空 `code/name/items`；前端默认不猜 code。
- 源码：`api/recommendation/v1/recommendation.go`、`internal/logic/recommendation/recommendation.go`。

---

## 4. 数据模型摘要

集中定义在 `web/src/api/types.ts`，字段与 Go JSON tag 一致（未做 camelCase 转换）：

- `Product`：`id/name/brand/category_id/price/main_image/detail/status/view_count/images/created_at/updated_at`
- `ProductDetail`：`Product + skus`
- `Sku`：`id/product_id/name/price/status/created_at/updated_at`
- `Category`：`id/parent_id/name/sort/status/created_at/updated_at/children?`
- `ReviewItem`：`id/product_id/sku_id/rating/content/user_id/created_at`
- `BannerItem`：`id/title/image_url/link_url/sort`
- `RecommendationItem`：`product_id/name/main_image/price/sort`

注意：后端的 `price` 全部是整数分；前端只在展示层格式化，不做浮点金额计算。

---

## 5. 真实联调与 Mock 验证边界

### 5.1 已真实联调（前端 → Vite 代理 → Go 服务 → MySQL）

联调环境：

- MySQL 8.0 / Redis 7（临时 `docker compose` 依赖容器）；
- Go 后端从仓库源码构建，执行 `migrate up` 与 `serve`；
  因本地无真实七牛凭据，使用仓库已有 `test/serveproc`（`-tags serve_lifecycle`）
  与 `test/container/qiniu_mock` 仅重定向七牛 UC endpoint，校验逻辑不变；
- Vite dev server 不启用 MSW，`VITE_DEV_PROXY_TARGET=http://127.0.0.1:8000`，
  请求经 `/api` rewrite 进入后端；
- 数据库临时插入 3 个分类、2 个上架商品、1 个下架商品、3 个 SKU、2 条评价、
  1 个轮播图、1 个推荐位。验证结束后已删除容器与数据卷。

真实请求验证结果：

| 项目 | 结果 |
| --- | --- |
| `GET /categories` 结构/树形 | 通过 |
| `GET /products` 列表、总数、`page/size` | 通过 |
| `GET /products?keyword=星海` 搜索 | 通过（命中 1 条） |
| `GET /products/1` 详情 + enabled SKU | 通过 |
| `GET /products/3`（下架）→ 404/4001 | 通过 |
| `GET /products/1/reviews` 聚合与分页字段 | 通过 |
| `GET /banners` | 通过 |
| `GET /recommendations/home` | 通过 |
| 首页分类/轮播/商品/推荐区块渲染 | 通过 |
| 列表总数、关键词搜索 | 通过 |
| 详情价格、SKU 切换价格、评价、禁用购物车 | 通过 |
| 商品 404 状态 | 通过 |

补充说明：一次性验证脚本中「首页商品卡片」断言曾报 strict mode 冲突，
原因是同一商品同时出现在「最新上架」与「推荐位」两个区块（两处同名链接），
人工核对两处均正确渲染；这不是应用缺陷，后续脚本需使用 `.first()` 或限定区块。

### 5.2 仅通过 Mock 验证（仓库内自动化测试）

- `npm run test`（Vitest + MSW）全部用例；
- `npm run test:e2e`（Playwright + 浏览器 MSW）19 条用例；
- 多页分页（真实临时数据仅 2 件商品，只有 1 页）；
- 列表网络错误状态（E2E 通过 Mock 专用 `keyword=__error__` 触发，真实后端无此钩子）；
- 移动端布局与导航抽屉（E2E 视口模拟）；
- Mock 商品图（`web/public/mock/*.svg`）与 E2E 轮播图（MSW `/storage/banners/:file`）。

**不得将上述 Mock 测试表述为真实后端联调通过。**

---

## 6. 后端能力缺口（本期未提供/未开放）

| 缺口 | 影响 | 处理 |
| --- | --- | --- |
| 无 `contracts/openapi.yaml` | 无法生成类型 | DTO 按 Go 源码手写并标注来源 |
| 无前台库存查询接口 | 无法展示真实库存 | 详情页明确说明以下单校验为准，不伪造状态 |
| SKU 无规格维度 | 只能做单层规格选择 | 按 SKU `name` 渲染单层按钮 |
| 无公开推荐位列表接口 | 前端无法自动发现推荐位 code | 用 `VITE_HOME_RECOMMEND_CODE` 显式配置，默认关闭 |
| `category_id` 精确匹配、不聚合子树 | 顶级分类筛选必为空 | 前台只暴露叶子分类 |
| 商品无销量/评价数列表字段 | 卡片无法展示销量/评价数 | 卡片只展示真实字段（名称/品牌/价格/图） |
| 商品详情无富文本清洗契约 | 直接渲染 HTML 有 XSS 风险 | 以纯文本展示 |
| Nginx 无 `/api` 前缀剥离 | 生产环境 API 路径不匹配 | 记录为部署缺口，本期未改 Nginx |

---

## 7. 新增与修改的关键文件

### 7.1 新增

| 文件 | 职责 |
| --- | --- |
| `web/src/api/types.ts` | 业务 DTO，字段来源标注到 Go 文件 |
| `web/src/api/envelope.ts` | 统一响应 `{code,message,data}` 解包与业务错误兜底 |
| `web/src/api/query-keys.ts` | 统一 Query Key 工厂 |
| `web/src/api/products.ts` | 商品列表/详情请求与参数转换 |
| `web/src/api/categories.ts` | 分类树请求 |
| `web/src/api/banners.ts` | 轮播图请求 |
| `web/src/api/reviews.ts` | 商品评价分页请求 |
| `web/src/api/recommendations.ts` | 推荐位请求 |
| `web/src/config/env.ts` | 首页推荐位 code 配置 |
| `web/src/utils/catalog-search.ts` | 列表 URL 状态解析/生成、排序转换 |
| `web/src/utils/category-tree.ts` | 分类树展开、叶子收集、查找、计数 |
| `web/src/components/product/*`（8 个） | 卡片、栅格、图片、图册、SKU、星级、评价、分类筛选 |
| `web/src/components/common/route-loading.tsx` | 路由懒加载占位 |
| `web/src/components/common/route-error-page.tsx` | 路由级错误边界 |
| `web/src/components/common/app-error-boundary.tsx` | 应用级错误边界 |
| `web/src/router/lazy-pages.tsx` | 列表/详情路由级懒加载 |
| `web/src/pages/products-page.tsx` | 商品列表/搜索/分类/排序/分页 |
| `web/src/pages/product-detail-page.tsx` | 商品详情/SKU/评价/404 |
| `web/src/test/render.tsx` | 测试用路由与 QueryClient 渲染工具 |
| `web/src/mocks/data/catalog.ts` | 符合后端结构的 Mock 数据 |
| `web/public/mock/product-*.svg` | 测试用商品图占位 |
| `web/src/api/catalog.test.ts`、`envelope.test.ts` | API 层测试 |
| `web/src/utils/catalog-search.test.ts`、`category-tree.test.ts` | 工具函数测试 |
| `web/src/pages/*.test.tsx`、`home-page.test.tsx` | 页面测试 |
| `web/tests/e2e/products.spec.ts`、`product-detail.spec.ts` | 新增核心 E2E |

### 7.2 修改

| 文件 | 变更 |
| --- | --- |
| `web/src/api/client.ts` | 支持数字业务码；fetch 改为调用时读取（兼容 MSW） |
| `web/src/pages/home-page.tsx` | 由 W0 工程展示页重写为商城首页 |
| `web/src/pages/home/section-primitives.tsx` | 保留并精简区块标题组件 |
| `web/src/router/index.tsx` | 新增商品路由、懒加载、errorElement |
| `web/src/App.tsx` | 包裹应用级错误边界 |
| `web/src/components/layout/site-header.tsx` | 真实商品导航与分类入口 |
| `web/src/components/layout/search-box.tsx` | 接入搜索跳转 |
| `web/src/components/layout/mobile-nav.tsx` | 真实路由与阶段说明 |
| `web/src/components/layout/site-footer.tsx` | 更新站点阶段文案 |
| `web/src/mocks/handlers.ts` | 商品域 Mock、错误钩子、轮播图静态 Mock |
| `web/src/test/setup.ts` | MSW 启动后补齐相对 URL origin |
| `web/src/__tests__/app.test.tsx` | 适配新首页与路由 |
| `web/src/vite-env.d.ts` | 新增推荐位环境变量类型 |
| `web/vite.config.ts` | `/api` 代理剥离前缀；`/storage` 代理 |
| `web/playwright.config.ts` | E2E 启用 MSW/推荐位、串行执行 |
| `web/.env.example`、`web/README.md` | 环境变量与 W1 使用说明 |
| `web/tests/e2e/home.spec.ts` | 重写为商城首页 E2E |

### 7.3 删除

| 文件 | 原因 |
| --- | --- |
| `web/src/pages/home/component-showcase-section.tsx` | W0 工程展示区块，首页已业务化 |
| `web/src/pages/home/design-baseline-section.tsx` | 同上 |

---

## 8. 测试命令与真实执行结果

执行目录：`/root/projects/my-shop/web`

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过，0 error / 0 warning |
| `npm run typecheck` | 通过，无输出错误 |
| `npm run test` | 14 个测试文件、60 条用例全部通过 |
| `npm run build` | 通过；产物含 `products-page` 7.81 kB、`product-detail-page` 12.75 kB 独立 chunk |
| `npm run test:e2e` | 19 条 Playwright 用例全部通过（约 21s） |

E2E 覆盖：

- 首页：首屏、分类入口、最新商品、轮播、推荐位、商品卡跳详情、分类跳列表、404、移动导航；
- 列表：首屏、Header 搜索、分页、分类筛选、价格排序、空状态、错误状态；
- 详情：基础信息、SKU 切换价格、图册缩略图、评价分页、404、面包屑返回。

说明：E2E 使用 MSW Mock 数据；真实后端联调见第 5.1 节，二者不能混为一谈。

---

## 9. 交付文档路径

1. 开发知识文档：`.agent/tasks/dev/W1-01-product-browsing.md`
2. 正式交付报告：`docs/reports/w1-01-product-browsing/DELIVERY_REPORT.md`（本文件）

两份文档职责不同：前者解释实现与维护方式，后者记录范围、接口、验证与缺口。

---

## 10. 提交与工作区

- 本任务代码与两份文档在同一提交中完成，提交信息：
  `feat(web): implement product browsing flow`。
- 提交 SHA 与最终工作区状态以本次交付汇报为准。
- 未修改 main、未修改后端 Go 代码、未修改 Docker/Nginx/K8s/Prometheus 配置、
  未修改其他前端工程、未跨分支合并、未 force push、未 push。

---

## 11. 风险与后续建议

1. **上线前必须补齐 `/api` 前缀剥离**（Nginx/Ingress），否则生产 API 请求不可用。
2. 后端需提供公开库存接口后，详情页才能展示真实库存；当前仅给出说明。
3. 推荐位建议由后端提供公开的「启用推荐位列表」，前端可去掉 code 配置依赖。
4. 正式 OpenAPI 契约落地后，运行 `npm run typegen` 并用生成类型替换手写 DTO。
5. 商品详情 `detail` 若改为富文本，必须先明确服务端清洗策略，避免 XSS。
6. 商品/分类/轮播仍无仓库内种子数据，联调需运营后台配置或数据迁移。
