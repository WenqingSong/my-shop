# Technical Contract

## Decision Status

APPROVED

（2026-10-01 Owner 确认 CONTRACT_REVISION 落地细节：新增 `4007 PRODUCT_CATEGORY_DISABLED`、FK 1451→409 映射、keyword 转义 + 显式 ESCAPE + 参数化绑定；两个 Open Risks 暂不扩展 Scope。本 Contract 冻结，待 Task Builder 同步 `task.md` 后进入 Coder。）

## Problem

交付「商品 SPU」基础能力（回答"这是什么商品"）：商品创建、更新、上下架、列表、详情、分类筛选、分页、排序、关键词搜索、图片与详情，RBAC 权限保护，前后台商品可见性隔离。SPU 只承载基础展示价，不负责 SKU、库存、销量、订单。

需要 Analyst 固化并交 Owner 确认的设计问题：建表机制（已由 db-migration 前置任务落地）、`status` 存储/出参映射、上下架 API 路径、`price` 上限、分页默认值与上限、商品错误码编号，以及分类"叶子判定"和"删除保护"两个跨域校验的落点。

2026-10-01 追加（CONTRACT_REVISION）：`products.category_id` 增加 FK（`ON DELETE RESTRICT`）做并发删除保护；`keyword` 按普通文本转义 LIKE 特殊字符；商品创建/修改 `category_id`/上架时分类须同时满足「存在 + 叶子 + enabled」。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3 / Go 1.23.0，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `controller` → `service`（接口 + Register）→ `logic`（`init()` 注册）；数据访问用 `g.DB().Model()`，无 dao/model 层。商品模块沿用此结构。
- VERIFIED：RBAC 已完整实现。`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（`IsSuper` 直接放行，DB 授权查询失败 fail-closed 500）；`internal/boot/seed.go` 幂等 seed 16 个权限；分类写接口已在 `internal/cmd/routes_admin.go` 用 `require("category:xxx")` 挂载。`agents.md` 第 11 节「RBAC 未实现」已过时。
- VERIFIED：错误码体系在 `internal/codes/codes.go` 集中定义并映射 HTTP 状态。已用：通用 1000-1005、IAM 2001-2010、分类 3001-3004。`CodeCategoryHasProducts` 与商品域 4000-4999 均不存在。
- VERIFIED：分类模块 `internal/logic/categories/categories.go`：树形 CRUD；`Delete` 仅检查「有子分类」（`CodeCategoryHasChildren` 3003），无商品关联检查；无独立"叶子判定"函数（只有 `maxLevel=3` 层级校验）。叶子判定需新增。
- VERIFIED（2026-10-01 更新）：db-migration 前置任务已完成并合入（HEAD `05c2072`，`feat/db-migration` 并入 `feat/spu`）。建表机制已由 `boot.ensureTables` 迁移为 golang-migrate v4.19.0：迁移文件在 `internal/migrations/sql/`，命名 `{version}_{title}.up.sql`，version 为 14 位时间戳（`YYYYMMDDHHMMSS`），仅提供 `.up.sql`；baseline = `20261001000001_baseline.up.sql`（7 张既有表，多语句单文件，`CREATE TABLE` 已去掉 `IF NOT EXISTS`）。`internal/boot/boot.go` 已删除 `ensureTables` 与 7 个建表常量，`Bootstrap` 改为 `migrations.Status` 只读就绪检查（dirty 或 current < latest 时 fail-fast，不执行任何 DDL）。CLI 拆为 `my-shop migrate up/force/version` + `my-shop serve`。SPU 的 `products`/`product_images` 经该机制新增迁移。
- VERIFIED：统一响应 `{code,message,data}`（`internal/middleware/response.go`），客户端靠 `code` 判型；HTTP 状态由 `codes.HTTPStatus` 映射。
- VERIFIED：路由分离 `internal/cmd/routes_frontend.go`（公开）与 `routes_admin.go`（`AdminAuth` + `require(permission)`），在 `cmd.go` 的 `s.Group("/")` 下注册，外层挂 `middleware.Response`。
- VERIFIED：当前无任何商品后端代码（api/controller/service/logic 均只有 admin/categories/health/iam）；`product` 关键词仅出现在前端 Vue/JS（`frotend_web|manage/src/views/productManager/*`），本次明确 Out of Scope。
- VERIFIED（2026-10-01 更新）：当前 HEAD `05c2072`，工作区干净。相比原 Task base `2d9bb93`，分支已合入 db-migration 前置任务的实现（`internal/migrations/*`、`internal/cmd` 子命令重构、`internal/boot/boot.go` 删除建表）与任务文档，属前置任务产物、不属 SPU 改动；SPU 实现时不得改动 db-migration 已合入的迁移与机制。
- UNKNOWN：无（阻塞项与各参数均有代码/文档证据支持，见下）。

## Recommendation

RECOMMENDATION（待 Owner 确认，逐项对应 Analyst Questions）：

1. **建表机制（#1，已落地）**：Owner 已选方案 B，db-migration 前置任务已完成并合入（golang-migrate v4，迁移文件 `internal/migrations/sql/{version}_{title}.up.sql`，version 为 14 位时间戳）。SPU 经该机制新增迁移：**建议单文件 `internal/migrations/sql/20261001000002_products.up.sql`**（紧随 baseline `20261001000001`，多语句单文件含 `products` + `product_images` 两表），不回退到 `boot.go`。迁移 DDL 与 baseline 一致去掉 `IF NOT EXISTS`。

2. **`status` 映射（#2）**：DB 存 `TINYINT`（`0=draft`、`1=on_shelf`、`2=off_shelf`）；API 出参用字符串枚举 `"draft"`/`"on_shelf"`/`"off_shelf"`。取字符串枚举是因为本状态机是具名三态（非布尔开关），与 AC/任务通篇用词一致、前端自解释；DB 用 TINYINT 与 categories `status TINYINT` 风格一致。备选：API 出参也用 int（更贴 categories，但语义自解释性差）。

3. **上下架路径（#3）**：`POST /admin/products/:id/on-shelf` 与 `POST /admin/products/:id/off-shelf`（kebab，与项目既有风格一致；权限 code 保持 snake_case `product:on_shelf`/`product:off_shelf`）。上下架无请求体，仅 `:id`；响应返回更新后的商品详情。

4. **`price` 上限（#4）**：`price` 为 `INT UNSIGNED`，业务上限 `0 ≤ price ≤ 99,999,999` 分（= ¥999,999.99）。负数与超上限由 logic 校验拒绝；"非整数"由 JSON→`int64` 类型绑定自然失败（GoFrame 返回 400）。

5. **分页（#5）**：默认 `page=1`、`size=20`，最大 `size=100`；`page ≥ 1`、`size ≥ 1`，超限 `size` 被钳制为 100。

6. **错误码（#6）**：分类域新增 `CodeCategoryHasProducts = 3005`（409）；商品域新增 `4001`-`4006`（见 Error Semantics）。`ProductInvalidStatus`（4006）仅在"普通 update 提交未知 `status` 值"时触发——合法 `status` 值被忽略（满足 AC-005），非法值拒绝。

关键取舍：**前后台可见性与状态机一致性**是本次最大风险点——前台列表/详情强制 `WHERE status=on_shelf`，后台查看全部状态；状态迁移用「条件 UPDATE + `RowsAffected`」把并发重复操作收敛为"最多一次成功"，其余得到 409。这比"先查后写"多一层原子性保障，代价是上下架各多一次确定性 UPDATE 判定，换取 AC-010 的并发正确性。

## Selected Design

Owner 已确认（2026-09-30）六项 Analyst Questions；#1（建表机制）选方案 B，前置 db-migration 已于 2026-10-01 完成并合入，阻塞解除。本文件据此固化迁移机制下的落地细节，并已于 2026-10-01 获 Owner 重新确认（单文件 `20261001000002_products.up.sql` 两表、DDL 不用 `IF NOT EXISTS`）：

1. **建表机制（#1，已落地）**：方案 B。db-migration 前置任务已完成（golang-migrate v4，迁移文件 `internal/migrations/sql/{version}_{title}.up.sql`，version 为 14 位时间戳，baseline `20261001000001`）。SPU 经该机制新增 `products`/`product_images` 迁移：**单文件 `20261001000002_products.up.sql`**（多语句单文件两表，DDL 不用 `IF NOT EXISTS`），**不回退到 `boot.go`**。
2. **`status` 映射（#2）**：DB 存 `TINYINT`（`0=draft`、`1=on_shelf`、`2=off_shelf`）；API 出参字符串枚举 `"draft"`/`"on_shelf"`/`"off_shelf"`。
3. **上下架路径（#3）**：独立接口；路径命名遵循现有项目路由风格，无既定规范时用 `on-shelf`/`off-shelf`（kebab），即 `POST /admin/products/:id/on-shelf`、`POST /admin/products/:id/off-shelf`；权限 code 保持 snake_case `product:on_shelf`/`product:off_shelf`。
4. **`price` 上限（#4）**：`0 ≤ price ≤ 99,999,999` 分。
5. **分页（#5）**：默认 `page=1`、`size=20`，最大 `size=100`。
6. **错误码（#6）**：`CodeCategoryHasProducts=3005`；商品域 `4001-4007`（含 2026-10-01 修订新增 4007 `PRODUCT_CATEGORY_DISABLED`）。

## Interfaces and Data

### 路由

| 方法/路径 | 保护 | 权限 code | 说明 |
| --- | --- | --- | --- |
| `GET /products` | 公开 | - | 仅 `on_shelf`，分页/筛选/搜索/排序 |
| `GET /products/:id` | 公开 | - | 仅 `on_shelf` |
| `GET /admin/products` | `AdminAuth` | - | 全部状态 |
| `GET /admin/products/:id` | `AdminAuth` | - | 全部状态 |
| `POST /admin/products` | `AdminAuth` | `product:create` | 创建，强制 `draft` |
| `PUT /admin/products/:id` | `AdminAuth` | `product:update` | 更新，不改状态 |
| `POST /admin/products/:id/on-shelf` | `AdminAuth` | `product:on_shelf` | 上架 |
| `POST /admin/products/:id/off-shelf` | `AdminAuth` | `product:off_shelf` | 下架 |

- 后台查询接口仅 `AdminAuth`（无 `product:list/read` 权限，本次不新增读权限）。
- 列表查询参数：`page`（默认 1）、`size`（默认 20，max 100）、`category_id`（可选，精确匹配）、`keyword`（可选，转义后 `name LIKE ? OR brand LIKE ?`，`%`/`_`/转义字符先转义，`ESCAPE` 显式声明）、`sort`（白名单 `id`/`price`/`created_at`/`updated_at`，默认 `created_at`）、`order`（`asc`/`desc`，默认 `desc`）。排序字段走白名单映射、关键词参数化绑定 + 转义，禁止拼接前端字段进 SQL。

### 权限 seed 新增（4 个，追加到 `seedPermissionList`）

`product:create`、`product:update`、`product:on_shelf`、`product:off_shelf`。

### 数据表（经 golang-migrate 机制新增迁移）

迁移文件：`internal/migrations/sql/20261001000002_products.up.sql`（单文件，含 `products` + `product_images` 两表，多语句；version 紧随 baseline `20261001000001`，落地时若 `20261001000002` 已被占用则取下一个更大的 14 位时间戳）。DDL 与 baseline 一致，**不使用 `IF NOT EXISTS`**（迁移只执行一次，由 `schema_migrations` 追踪）。

```sql
CREATE TABLE products (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(128)    NOT NULL,
  brand       VARCHAR(64)     NOT NULL DEFAULT '',
  category_id BIGINT UNSIGNED NOT NULL,
  price       INT UNSIGNED    NOT NULL,
  main_image  VARCHAR(512)    NOT NULL DEFAULT '',
  detail      TEXT            NULL,
  status      TINYINT         NOT NULL DEFAULT 0,
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_category_id (category_id),
  KEY idx_status (status),
  CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE product_images (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  url        VARCHAR(512)    NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `products.category_id` 增加 FK → `categories.id`（`ON DELETE RESTRICT`）：并发下删除分类被 DB 拒绝，杜绝孤儿商品；`product_images.product_id` 仍无 FK（引用有效性由应用层 + 事务保证）。
- `status` 存储映射：`0=draft`、`1=on_shelf`、`2=off_shelf`。
- `name`（trim 后非空，≤128 字符）、`brand`（可选，≤64 字符）、`main_image`（可选，≤512 字符）、`images[].url`（非空）。

### 代码接口

- 新增 `api/product/v1`（Create/Update/OnShelf/OffShelf/List/Detail 的 Req/Res）、`internal/controller/product`、`internal/logic/product`（Register 模式）、`internal/service` 的 `IProduct` 接口；`internal/logic/logic.go` 注册 product 包。
- 新增 `IProduct` 接口（含 `CountByCategory(ctx, categoryID) (int64, error)` 供分类删除保护调用）。
- 扩展 `ICategory` 接口：新增 `Exists(ctx, id int64) (bool, error)`、`HasChildren(ctx, id int64) (bool, error)` 与 `IsEnabled(ctx, id int64) (bool, error)`。
- 分类删除保护：`categories.Delete` 在「有子分类」检查后，调用 `service.Product().CountByCategory(ctx, id)`，`>0` 返回 `CodeCategoryHasProducts`（409）；并发窗口下 DELETE 命中 FK 1451（`ON DELETE RESTRICT`）时同样映射为 `CodeCategoryHasProducts`（409），不泄漏 500。
- 商品分类校验（创建 / 修改 `category_id` / 上架）：product logic 依次校验 `Exists`（不存在→`CodeProductInvalidCategory` 4003）、叶子（`!HasChildren`，非叶子→`CodeProductCategoryNotLeaf` 4004）、`IsEnabled`（禁用→`CodeProductCategoryDisabled` 4007）。任一不满足即拒绝且无写入。
- `internal/codes` 新增 3005 与 4001-4007；`internal/boot/seed.go` 追加 4 个权限；`internal/cmd` 挂载路由。

## Business Invariants

- INV-001（状态机合法迁移）：商品状态仅允许 `draft→on_shelf`、`on_shelf→off_shelf`、`off_shelf→on_shelf`；其余迁移被拒（409）且状态不变。对应 AC-006/007/008/009。
- INV-002（并发迁移最多一次成功）：同一商品并发重复上架/下架，条件 UPDATE 保证最多一次 `RowsAffected=1`，最终状态正确。对应 AC-010。
- INV-003（前后台可见性隔离）：前台列表/详情仅返回 `status=on_shelf`；`draft`/`off_shelf` 前台不可见（404 或等价）。对应 AC-011/012/013。
- INV-004（创建强制 draft，普通 update 不改状态）：创建接口不能直接产生 `on_shelf`；普通 update 提交 `status` 不改变状态。对应 AC-001/005。
- INV-005（SPU 仅绑定存在且启用的叶子分类）：创建、修改 `category_id`、上架时，`category_id` 必须同时满足「存在 + 叶子 + enabled」，任一不满足被拒（4003/4004/4007）且不产生写入。对应 AC-004/014（含 Owner 追加的 enabled 与上架校验）。
- INV-006（分类删除保护）：分类下存在商品时禁止删除，返回 409 `CodeCategoryHasProducts`；并发窗口由 FK `ON DELETE RESTRICT` 兜底，分类与商品均不受影响。对应 AC-022/023。
- INV-007（商品与图片原子一致）：创建/更新时 `products` 与 `product_images` 在同一事务内写入，失败整体回滚，无半成品。对应 AC-018。
- INV-008（价格整数分）：`price` 全程整数分存储与出参；负数/超上限拒绝且无写入。对应 AC-002/003。
- INV-009（keyword 按普通文本搜索）：`keyword` 对 LIKE 的 `%`/`_`/转义字符做转义后匹配，不暴露通配符语义。对应 AC-016。

## Failure and Consistency Semantics

- 事实来源：MySQL `products`（商品主数据 + 状态）、`product_images`（图片，从属 `products`）、`categories`（叶子/存在性/启用判定）。`products.category_id` 的引用完整性由 FK `ON DELETE RESTRICT` 保证。无 Redis、无 MQ、无异步、无跨系统事务。
- 创建/更新（同步）：单事务写入 `products`（+ `product_images` 全量替换）——更新时 `images` 提供则删除旧行重插，未提供则保留；任一步失败整体回滚。
- 状态迁移（同步、原子）：上架 `UPDATE products SET status=1 WHERE id=? AND status IN (0,2)`；下架 `UPDATE products SET status=2 WHERE id=? AND status=1`。先 `SELECT` 判定存在（不存在→404），再条件 UPDATE，`RowsAffected=0` → 409 `CodeProductInvalidStatusTransition`（覆盖非法迁移与并发重复，二者都归为"当前状态不允许该迁移"）。无删除接口，故无删除竞态。
- 前台可见性（每请求）：列表/详情 SQL 强制 `status=on_shelf`；不存在或非 `on_shelf` 在前台返回 404。
- 分类删除保护：`存在检查 → 有子分类(3003) → 有商品(3005) → DELETE`，核对 `RowsAffected`；并发窗口下 DELETE 因 FK `ON DELETE RESTRICT` 返回 1451，同样映射为 409 `CodeCategoryHasProducts`（应用层检查保留、FK 兜底最终一致性）。
- 失败语义：认证失败 401、授权失败 403（无写入）；参数/业务校验失败 400/404/409，均无写入；DB 技术错误统一 `CodeInternalError` 500，不泄漏底层细节。

## Error Semantics

| code | 语义 | HTTP |
| --- | --- | --- |
| 3005 | CATEGORY_HAS_PRODUCTS（分类下有商品） | 409 |
| 4001 | PRODUCT_NOT_FOUND | 404 |
| 4002 | PRODUCT_INVALID_PRICE（负数/超上限） | 400 |
| 4003 | PRODUCT_INVALID_CATEGORY（分类不存在/非法） | 400 |
| 4004 | PRODUCT_CATEGORY_NOT_LEAF（非叶子分类） | 400 |
| 4005 | PRODUCT_INVALID_STATUS_TRANSITION（非法/并发冲突迁移） | 409 |
| 4006 | PRODUCT_INVALID_STATUS（未知 status 值，仅 update 提交非法值时触发） | 400 |
| 4007 | PRODUCT_CATEGORY_DISABLED（分类已禁用） | 400 |

复用 `1001`（参数错误，兜底）、`1002`（401）、`1003`（403）、`1004`（404，兜底）。

## Allowed / Forbidden Changes

允许：
- 新增迁移文件 `internal/migrations/sql/20261001000002_products.up.sql`（`products` + `product_images` 两表，多语句单文件，DDL 不用 `IF NOT EXISTS`，不回退到 `boot.go`）；`products.category_id` 增加 FK → `categories.id` `ON DELETE RESTRICT`；`seedPermissionList` 追加 4 个商品权限。
- 新增 `api/product/v1`、`internal/controller/product`、`internal/service`（IProduct）、`internal/logic/product`、错误码 3005/4001-4007、路由挂载。
- 扩展 `ICategory`（`Exists`/`HasChildren`/`IsEnabled`）并修改 `categories.Delete` 增加商品关联保护（含 1451→409 映射）。
- 商品分类校验覆盖创建、修改 `category_id`、上架三处（存在 + 叶子 + enabled）；`keyword` 搜索转义 LIKE 特殊字符。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 不改动已合入的 baseline 迁移（`20261001000001_baseline.up.sql`）与 db-migration 机制本身（`internal/migrations/*`、`internal/cmd` 子命令、`boot.go` 的 readiness 检查）；仅通过既有机制新增 products/product_images 迁移。不引入 SKU/库存/销量/订单/删除/促销价/上传存储等 Out of Scope 能力。
- 不改变 `/health`、前台 `/register`/`/login`/`/me`/`/logout`、分类公开/写接口的既有公开行为（分类删除新增商品保护是唯一改动）。
- 不新增商品删除接口；不新增 `product:list/read` 读权限（后台查询仅 `AdminAuth`）。
- 不在 SQL 中拼接前端传入的排序字段或关键词（排序白名单映射、关键词参数化绑定）。
- 不 fail-open 放行未认证/未授权请求；不把 `status` 迁移交给普通 update。

## Verification Requirements

- INV-001/002 → 状态机集成测试：3 条合法迁移成功、≥3 条非法迁移被拒且状态不变；多 goroutine 并发上/下架同一商品，断言最多一次成功、最终状态正确（建议 `-race` 下跑，以业务断言为准）。需 MySQL。
- INV-003 → 构造 `draft`/`on_shelf`/`off_shelf` 商品，访问前台/后台列表与详情，断言可见性隔离。
- INV-004 → 创建断言 `status=draft`；普通 update 提交 `status` 断言状态不变。
- INV-005 → 不存在分类、非叶子分类、禁用分类在创建/修改 `category_id`/上架时均被拒（4003/4004/4007）且无写入；存在且启用的叶子分类成功。
- INV-006 → 分类下有商品时删除断言 409 `3005` 且分类与商品不变；无商品分类删除成功；并发创建商品+删分类由 FK `ON DELETE RESTRICT` 兜底（删除失败、不产生孤儿商品）。
- INV-009 → `keyword` 含 `%`/`_`/`\` 时按普通文本匹配（不产生通配符效果），断言结果等价于字面匹配。
- INV-007 → 创建/更新后查 `product_images` 核对行与 `sort`；模拟图片写入失败断言回滚无半成品。
- INV-008 → 合法价格整数分存储/出参；负数/超上限断言 400 且查库无新行。
- RBAC（AC-019/020/021）→ 无 token 401、有 token 无权限 403（查库无写入）、有权限成功、超级管理员放行。
- 列表（AC-014/015/016/017）→ `category_id` 精确匹配、分页结构 `{items,total,page,size}`、`size` 上限钳制、`keyword` 转义后 LIKE 匹配、排序白名单与非法字段回落默认、无注入。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL/Redis 集成验证需说明容器就绪（`docker compose up -d`）。

## Open Risks

- 已绑定商品后分类被禁用：已有商品（含已上架）不受影响，前台列表/详情仍按商品自身 `status` 过滤、不按分类 `status` 过滤（沿用现有 Category 语义）；再次上架会因分类禁用被拒。该「禁用后处理已有商品」语义暂不在 SPU V1 扩展，如现有语义不足另立任务。
- 叶子分类被后续添加子分类后会变为非叶子：已绑定商品不受影响，但再次上架会被拒（非叶子）。
- FK `ON DELETE RESTRICT` 使「删除分类」在存在商品时必然失败、不会误删；但应用层 409 与 FK 1451 两条路径需在 `categories.Delete` 统一映射为 `CodeCategoryHasProducts`，避免并发窗口泄漏 500。

## Owner Decision Record

Owner 于 2026-09-30 确认 6 项决定：

1. **建表机制**：选 B——先暂停 SPU，实现独立的 db-migration 前置任务；SPU 不回退到 `boot.go` 建表。
2. **status**：DB 存 0/1/2，API 输出 `draft`/`on_shelf`/`off_shelf` 字符串枚举。
3. **上下架接口**：独立接口；路径优先遵循现有项目路由风格，无既定规范用 `on-shelf`/`off-shelf`。
4. **price**：上限 `99,999,999` 分。
5. **分页**：`page=1`、`size=20`、max `size=100`。
6. **错误码**：`CodeCategoryHasProducts=3005`，商品域 `4001-4006`。

适用范围：`product-spu-v1` 本任务。

后续进展：
- 2026-10-01：db-migration 前置任务完成并合入 `feat/spu`（HEAD `05c2072`，golang-migrate v4.19.0，baseline `20261001000001`），SPU 阻塞解除。Analyst 据此固化「数据表」为经 `internal/migrations/sql/` 迁移新增（单文件 `20261001000002_products.up.sql` 两表，DDL 不用 `IF NOT EXISTS`），并更新 Allowed/Forbidden Changes。决定 #2-#6 不变。
- 2026-10-01（APPROVED）：Owner 确认 #1 落地细节——采用单文件 `20261001000002_products.up.sql` 创建 `products` 与 `product_images`，DDL 不使用 `IF NOT EXISTS`；并批准本 Contract 转为 `APPROVED`。六项决定全部落实，无遗留待决问题。
- 2026-10-01（CONTRACT_REVISION，APPROVED）：Owner 确认 3 项落地细节——① 新增错误码 `4007 PRODUCT_CATEGORY_DISABLED`；② FK `ON DELETE RESTRICT` 触发的 MySQL 1451 映射为 409 + `CodeCategoryHasProducts`，不返回 500；③ `keyword` 对 `\`/`%`/`_` 做 LIKE 转义并显式声明 escape，继续参数化绑定。两个 Open Risks（分类禁用后已有商品不自动处理、叶子变非叶子后已有商品不自动处理）本次接受、暂不扩展 Scope，上架时继续执行最新分类有效性校验。原 6 项决定与 APPROVED 记录保留；本 Contract 转 `APPROVED`。

待办（Coder 启动前必须完成）：
- [x] Task Builder 新建 `db-migration` 前置任务并完成 migration 机制（已完成，2026-10-01 合入）。
- [ ] Task Builder 同步更新 `product-spu-v1/task.md`：移除「SPU 暂停」与旧 `boot.go` 建表描述（改为 golang-migrate 迁移文件 `internal/migrations/sql/20261001000002_products.up.sql`）；并新增 3 项 Owner 决定对应的 Scope/AC 变更（FK 保护、keyword 转义、分类「存在+叶子+enabled」校验及上架校验），更新 Analyst Question 结论与 Review Baseline。完成后进入 Coder 拆分与实现。
