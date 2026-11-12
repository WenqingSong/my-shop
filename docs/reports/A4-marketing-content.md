# A4 营销与内容管理 — 交付报告

- 阶段：A4（优惠券、秒杀活动、轮播图、商品推荐位、文章）
- 分支：`feat/phase2-admin-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 开发知识文档：`.agent/tasks/dev/A4-marketing-content.md`
- 后端改动：无（未修改 `internal/**`、`api/**`、`migrations/**`、`contracts/**`）

---

## 1. 任务目标与实际范围

在现有 Vue 3 管理后台内实现五个营销/内容模块，只接入 Go 后端已有能力；
后端缺失的列表/详情/写接口一律记录，不用 MSW 假数据包装成真实功能。

未进入 A5：没有用户、管理员、角色权限管理，也没有支付/结算/消息等新阶段。

---

## 2. 五个模块完成情况

状态：**IMPLEMENTED** / **VERIFIED**（MSW+Vitest）/ **PARTIALLY_VERIFIED** /
**NOT_VERIFIED** / **NOT_IMPLEMENTED**。

### 2.1 优惠券

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 列表 | IMPLEMENTED / VERIFIED | 全部模板，id 倒序 |
| 名称搜索 / 状态筛选 / 分页 | IMPLEMENTED / VERIFIED | 后端返回全量，前端完整数据集过滤与分页 |
| 详情 | IMPLEMENTED / VERIFIED | 抽屉展示列表中的完整模板字段（无详情接口） |
| 创建 | IMPLEMENTED / VERIFIED | 满减券，金额元→分，时间必填 |
| 停止领取 | IMPLEMENTED / VERIFIED | `coupon:stop`，1→0，幂等 |
| 编辑 / 删除 / 重新启用 | NOT_IMPLEMENTED | 后端无接口 |
| 类型筛选 | NOT_IMPLEMENTED | 后端只有满减一种规则，无 type 字段 |
| 领取/核销/订单优惠计算 | NOT_IMPLEMENTED | 属用户端与后端，管理端不做 |

### 2.2 秒杀活动

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 创建活动 | IMPLEMENTED / VERIFIED | 名称、时间、SKU 绑定、秒杀价、库存 |
| 按 ID 更新 | IMPLEMENTED / VERIFIED | 部分更新；name/status/时间/SKU 价与库存 |
| 商品与 SKU 选择 | IMPLEMENTED / VERIFIED | 复用 A2 商品搜索与详情 |
| 活动状态 | IMPLEMENTED / VERIFIED | enabled/disabled（经 update） |
| 活动列表 / 详情 / 删除 | NOT_IMPLEMENTED | 后端无接口 |
| 活动统计 / 秒杀订单 | NOT_IMPLEMENTED | 后端无管理端查询接口 |
| 请求修复 / 审计 | NOT_IMPLEMENTED | 无请求列表可用于定位 |
| Redis/Kafka/Outbox 操作 | NOT_IMPLEMENTED | 明确不做，管理端只写活动配置 |

### 2.3 轮播图

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 列表 / 图片预览 | IMPLEMENTED / VERIFIED | 全部状态，sort 升序 |
| 新增 / 编辑 | IMPLEMENTED / VERIFIED | 标题、图片、跳转、排序、状态 |
| 图片上传 | IMPLEMENTED / PARTIALLY_VERIFIED | 复用 A2 七牛凭证+直传组件；真实七牛 NOT_VERIFIED |
| 启用 / 禁用 | IMPLEMENTED / VERIFIED | 通过 update status |
| 删除 | IMPLEMENTED / VERIFIED | 二次确认 |
| 分页 | IMPLEMENTED / VERIFIED | 前端分页（后端无参数） |
| 展示位置 / 有效期 | NOT_IMPLEMENTED | 后端无字段 |

### 2.4 商品推荐位

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 推荐位列表 / 详情 | IMPLEMENTED / VERIFIED | 含推荐商品关系 |
| 创建 / 编辑 / 删除 | IMPLEMENTED / VERIFIED | code 不可变；删除级联关系 |
| 启用 / 禁用 | IMPLEMENTED / VERIFIED | 经 update |
| 推荐商品列表 | IMPLEMENTED / VERIFIED | 复用 A2 商品详情补全名称/图片/价格 |
| 添加 / 移除商品 | IMPLEMENTED / VERIFIED | 重复添加 15005，移除 15004 |
| 调整排序 | IMPLEMENTED / VERIFIED | 上移/下移，全量提交 product_ids |
| 个性化推荐算法 / 统计 | NOT_IMPLEMENTED | 后端无能力 |

### 2.5 文章

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 列表 / 分页 | IMPLEMENTED / VERIFIED | 公开 `GET /articles` |
| 详情 / 正文 | IMPLEMENTED / VERIFIED | 纯文本展示，不渲染 HTML |
| 标题搜索 / 状态筛选 | NOT_IMPLEMENTED | 后端无参数、无 status 字段 |
| 创建 / 编辑 / 删除 | NOT_IMPLEMENTED | 只有用户侧作者接口，管理员 token 不适用 |
| 发布 / 下架 | NOT_IMPLEMENTED | 后端无状态机 |
| 封面 / 分类 | NOT_IMPLEMENTED | 后端无字段 |

---

## 3. API 接入情况

### 3.1 真实接入

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| GET | `/admin/coupons` | `coupon:list` |
| POST | `/admin/coupons` | `coupon:create` |
| POST | `/admin/coupons/:id/stop` | `coupon:stop` |
| POST | `/admin/flash-sales` | `flash_sale:create` |
| PUT | `/admin/flash-sales/:id` | `flash_sale:update` |
| GET | `/admin/banners` | AdminAuth |
| GET | `/admin/banners/:id` | AdminAuth |
| POST | `/admin/banners` | `banner:create` |
| PUT | `/admin/banners/:id` | `banner:update` |
| DELETE | `/admin/banners/:id` | `banner:delete` |
| GET | `/admin/recommend-positions` | AdminAuth |
| GET | `/admin/recommend-positions/:id` | AdminAuth |
| POST | `/admin/recommend-positions` | `recommend:create` |
| PUT | `/admin/recommend-positions/:id` | `recommend:update` |
| DELETE | `/admin/recommend-positions/:id` | `recommend:delete` |
| POST | `/admin/recommend-positions/:id/items` | `recommend:item` |
| DELETE | `/admin/recommend-positions/:id/items/:product_id` | `recommend:item` |
| PUT | `/admin/recommend-positions/:id/items/sort` | `recommend:item` |
| GET | `/articles`、`/articles/:id` | 公开只读 |

另复用 A2：`GET /admin/products`、`GET /admin/products/:id`、
`GET /admin/qiniu/upload/token` 与七牛直传。

### 3.2 Provisional API

无。五个模块的 DTO、路径、校验规则均来自 Go 源码核对，没有暂定字段，
未创建 OpenAPI 文件。

### 3.3 后端不支持的能力

- 优惠券：详情、编辑、删除、重新启用；折扣率/范围/限领。
- 秒杀：活动列表、详情、删除、独立启停、活动统计、请求列表。
- 轮播图：展示位置、有效期、分页/搜索参数。
- 推荐位：商品快照（需前端补全）、推荐算法与统计。
- 文章：管理端增删改、发布/下架、封面、分类、搜索。

### 3.4 前端组合能力

| 能力 | 实现 | 说明 |
| --- | --- | --- |
| 优惠券搜索/状态/分页 | 后端全量列表 + 前端过滤分页 | 不是只过滤当前页 |
| 轮播图分页 | 后端全量列表 + 前端分页 | 后端无分页参数 |
| 推荐商品信息 | 关系 + A2 商品详情（并发 4） | 失败显示 `商品 #id` |
| 秒杀 SKU 选择 | A2 商品搜索 + 商品详情 | 不重复实现商品管理 |
| 轮播图上传 | A2 七牛凭证 + 直传组件 | 未新建上传机制 |

---

## 4. 关键业务约束

- **优惠券**：满减固定金额；`threshold_amount` 0 表示无门槛；金额整数分；
  领取/使用时间必填且 start < end；创建即 `status=1`；停止领取只影响新领取。
- **秒杀**：创建即启用；SKU 绑定只能在创建时新增；更新只改已绑定 SKU；
  `total_stock >= sold`；活动时间 start < end；管理端不触碰 Redis/Kafka/Outbox/
  库存扣减/请求状态。
- **轮播图**：`status` 通过创建/更新写入，无独立启停接口；图片 URL 经七牛上传。
- **推荐位**：`code` 创建后不可变；关系只存 product_id + sort；
  排序必须全量覆盖，否则 15006。
- **文章**：正文为纯文本 TEXT；无状态/封面/分类；管理端只读。

---

## 5. 主要修改文件

新增（`admin/**`）：

- `src/api/types/{coupon,flash-sale,banner,recommendation,article}.ts`
- `src/api/modules/{coupon,flash-sale,banner,recommendation,article}.ts`
- `src/api/adapters/{coupon,flash-sale,banner,recommendation,article}-adapter.ts`
- `src/views/marketing/coupons/**`、`src/views/marketing/flash-sales/**`
- `src/views/content/banners/**`、`src/views/content/recommendations/**`、
  `src/views/content/articles/**`
- `src/router/modules/{marketing,content}.ts`
- `src/mocks/handlers/{coupon,flash-sale,banner,recommendation,article}.ts`
- `tests/unit/{marketing-coupon,marketing-flash-sale,content-banner,content-recommendation,content-article}.test.ts`

修改：

- `src/mocks/handlers/mock-db.ts` — 五个模块夹具与计数器。
- `src/mocks/handlers/index.ts` — 注册 A4 handlers。
- `src/utils/permission.ts` — 12 个真实权限码。
- `src/utils/money.ts` — `parseYuanToCents`。
- `src/utils/datetime.ts` — `toServerDateTime`。
- `src/router/enums.ts`、`src/components/ReIcon/src/offlineIcon.ts`、两份 locales。

---

## 6. 测试结果

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 19 文件 / 279 用例通过（A4 新增 62） |
| `pnpm build` | 通过（3.29 MB） |
| `git diff --check` | 通过 |

A4 新增：优惠券 15、秒杀 14、轮播图 11、推荐位 15、文章 7。
A0-A3 回归测试全部继续通过。

### 联调状态

- 真实 Go 后端：**NOT_VERIFIED**（未启动后端，全部 MSW）。
- 真实七牛上传：**NOT_VERIFIED**。
- 1366 / 1920 浏览器视觉验收：**NOT_VERIFIED**（无图形浏览器环境）。

---

## 7. 已知限制

1. 秒杀没有活动列表/详情，页面只能创建/按 ID 更新并展示本次响应；刷新后不可回读。
2. 文章只读；管理员无法创建/编辑/发布/下架/删除。
3. 优惠券只能创建与停止领取，不能编辑/删除/重新启用。
4. 优惠券与轮播图的分页/筛选为前端实现（后端返回全量数据）。
5. 推荐商品名/图片/价格依赖逐条商品详情；商品删除后只显示 ID。
6. 轮播图无展示位置/有效期，文章无封面/分类/状态。
7. 非超管角色的按钮可见性仍受 A1 本地角色映射限制。

---

## 8. Git

- 功能提交 SHA：`61f6ced62eb14a78bb54d1e49b548d977e39f21a`
  （`feat(admin): implement marketing and content management`，47 files changed）
- 文档回填提交：本条提交（`docs(admin): 记录 A4 提交与推送结果`）
- 目标分支：`feat/phase2-admin-frontend`
- 远端：`origin/feat/phase2-admin-frontend`
- 提交前远端状态：`origin/feat/phase2-admin-frontend...HEAD = 0 0`
- 推送方式：普通 `git push`（未 force push，未改写历史）

---

## 9. A5 启动条件

1. A4 提交已推送，工作区干净，A0-A4 回归测试通过。
2. 后端补齐 A4 缺失能力（秒杀活动列表/详情、文章管理端接口、优惠券编辑等）后，
   只需替换 API 模块与 adapter，页面结构不变。
3. 真实后端联调完成，至少确认五个模块的读写接口与权限码在真实环境可用。
4. A5 涉及的用户/管理员/角色权限范围与 `permissions[]` 下发方式明确。
5. Owner 明确下发 A5 任务书后再启动，不自动进入下一阶段。
