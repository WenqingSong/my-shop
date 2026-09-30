# Technical Contract

## Decision Status

BLOCKED（阻塞于 db-migration 前置任务；SPU 暂停，待前置任务完成后恢复，暂不标记为可实现/APPROVED）

## Problem

交付「商品 SPU」基础能力（回答"这是什么商品"）：商品创建、更新、上下架、列表、详情、分类筛选、分页、排序、关键词搜索、图片与详情，RBAC 权限保护，前后台商品可见性隔离。SPU 只承载基础展示价，不负责 SKU、库存、销量、订单。

需要 Analyst 固化并交 Owner 确认的设计问题：建表机制（阻塞项）、`status` 存储/出参映射、上下架 API 路径、`price` 上限、分页默认值与上限、商品错误码编号，以及分类"叶子判定"和"删除保护"两个跨域校验的落点。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3 / Go 1.23.0，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta`）→ `controller` → `service`（接口 + Register）→ `logic`（`init()` 注册）；数据访问用 `g.DB().Model()`，无 dao/model 层。商品模块沿用此结构。
- VERIFIED：RBAC 已完整实现。`internal/middleware/auth.go` 提供 `AdminAuth` 与 `RequirePermission(code)`（`IsSuper` 直接放行，DB 授权查询失败 fail-closed 500）；`internal/boot/seed.go` 幂等 seed 16 个权限；分类写接口已在 `internal/cmd/routes_admin.go` 用 `require("category:xxx")` 挂载。`agents.md` 第 11 节「RBAC 未实现」已过时。
- VERIFIED：错误码体系在 `internal/codes/codes.go` 集中定义并映射 HTTP 状态。已用：通用 1000-1005、IAM 2001-2010、分类 3001-3004。`CodeCategoryHasProducts` 与商品域 4000-4999 均不存在。
- VERIFIED：分类模块 `internal/logic/categories/categories.go`：树形 CRUD；`Delete` 仅检查「有子分类」（`CodeCategoryHasChildren` 3003），无商品关联检查；无独立"叶子判定"函数（只有 `maxLevel=3` 层级校验）。叶子判定需新增。
- VERIFIED：建表机制为 `internal/boot/boot.go` 的 `ensureTables` + `CREATE TABLE IF NOT EXISTS` 幂等建表（users/categories/admins/roles/permissions/admin_roles/role_permissions）。全仓库（含 git 历史）无 migration 机制或文件；`.agent/tasks/` 下无 db-migration 任务；`categories-v1`、`admin-identity-rbac` 均明确沿用 `boot.Bootstrap`。环境每日重置、数据不持久，表必须可重复创建。
- VERIFIED：统一响应 `{code,message,data}`（`internal/middleware/response.go`），客户端靠 `code` 判型；HTTP 状态由 `codes.HTTPStatus` 映射。
- VERIFIED：路由分离 `internal/cmd/routes_frontend.go`（公开）与 `routes_admin.go`（`AdminAuth` + `require(permission)`），在 `cmd.go` 的 `s.Group("/")` 下注册，外层挂 `middleware.Response`。
- VERIFIED：当前无任何商品后端代码（api/controller/service/logic 均只有 admin/categories/health/iam）；`product` 关键词仅出现在前端 Vue/JS（`frotend_web|manage/src/views/productManager/*`），本次明确 Out of Scope。
- VERIFIED：基线 HEAD `2a8b209`（任务文档提交），工作区干净；与 Task 声明的 base `2d9bb93` 仅差一个任务文档 commit，无需要区分的既有修改。
- UNKNOWN：无（阻塞项与各参数均有代码/文档证据支持，见下）。

## Recommendation

RECOMMENDATION（待 Owner 确认，逐项对应 Analyst Questions）：

1. **建表机制（#1，阻塞）**：方案 A——沿用现有 `boot.Bootstrap` 幂等 `CREATE TABLE IF NOT EXISTS` 建表，新增 `products`/`product_images` 挂 `boot.ensureTables`。不采纳方案 B（先补带时间戳 Migration 机制）。理由：代码现实无任何 migration 基础，方案 B 会显著扩大 Scope、牵涉历史表迁移，违反"不为未来假设提前引入基础设施"且与本任务无关；本任务 Out of Scope 已明确"不负责改造既有建表/迁移机制"。

2. **`status` 映射（#2）**：DB 存 `TINYINT`（`0=draft`、`1=on_shelf`、`2=off_shelf`）；API 出参用字符串枚举 `"draft"`/`"on_shelf"`/`"off_shelf"`。取字符串枚举是因为本状态机是具名三态（非布尔开关），与 AC/任务通篇用词一致、前端自解释；DB 用 TINYINT 与 categories `status TINYINT` 风格一致。备选：API 出参也用 int（更贴 categories，但语义自解释性差）。

3. **上下架路径（#3）**：`POST /admin/products/:id/on_shelf` 与 `POST /admin/products/:id/off_shelf`（与状态枚举同名，减少映射错误）。上下架无请求体，仅 `:id`；响应返回更新后的商品详情。

4. **`price` 上限（#4）**：`price` 为 `INT UNSIGNED`，业务上限 `0 ≤ price ≤ 99,999,999` 分（= ¥999,999.99）。负数与超上限由 logic 校验拒绝；"非整数"由 JSON→`int64` 类型绑定自然失败（GoFrame 返回 400）。

5. **分页（#5）**：默认 `page=1`、`size=20`，最大 `size=100`；`page ≥ 1`、`size ≥ 1`，超限 `size` 被钳制为 100。

6. **错误码（#6）**：分类域新增 `CodeCategoryHasProducts = 3005`（409）；商品域新增 `4001`-`4006`（见 Error Semantics）。`ProductInvalidStatus`（4006）仅在"普通 update 提交未知 `status` 值"时触发——合法 `status` 值被忽略（满足 AC-005），非法值拒绝。

关键取舍：**前后台可见性与状态机一致性**是本次最大风险点——前台列表/详情强制 `WHERE status=on_shelf`，后台查看全部状态；状态迁移用「条件 UPDATE + `RowsAffected`」把并发重复操作收敛为"最多一次成功"，其余得到 409。这比"先查后写"多一层原子性保障，代价是上下架各多一次确定性 UPDATE 判定，换取 AC-010 的并发正确性。

## Selected Design

Owner 已确认（2026-09-30），六项 Analyst Questions 全部有结论；其中 #1 改变本任务实现基础，SPU 暂停、待 db-migration 前置任务完成后恢复：

1. **建表机制（#1）**：方案 B。当前仓库尚无 db-migration 机制；先暂停 SPU，由 Task Builder 新建独立的 db-migration 前置任务（建立带时间戳 Migration 机制、迁移既有表），完成后 SPU 经该机制新增 `products`/`product_images` 迁移，**不回退到 `boot.go` 新增建表逻辑**。
2. **`status` 映射（#2）**：DB 存 `TINYINT`（`0=draft`、`1=on_shelf`、`2=off_shelf`）；API 出参字符串枚举 `"draft"`/`"on_shelf"`/`"off_shelf"`。
3. **上下架路径（#3）**：独立接口；路径命名遵循现有项目路由风格，无既定规范时用 `on-shelf`/`off-shelf`（kebab），即 `POST /admin/products/:id/on-shelf`、`POST /admin/products/:id/off-shelf`；权限 code 保持 snake_case `product:on_shelf`/`product:off_shelf`。
4. **`price` 上限（#4）**：`0 ≤ price ≤ 99,999,999` 分。
5. **分页（#5）**：默认 `page=1`、`size=20`，最大 `size=100`。
6. **错误码（#6）**：`CodeCategoryHasProducts=3005`；商品域 `4001-4006`。

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
- 列表查询参数：`page`（默认 1）、`size`（默认 20，max 100）、`category_id`（可选，精确匹配）、`keyword`（可选，`name LIKE '%kw%' OR brand LIKE '%kw%'`）、`sort`（白名单 `id`/`price`/`created_at`/`updated_at`，默认 `created_at`）、`order`（`asc`/`desc`，默认 `desc`）。排序字段走白名单映射，禁止拼接前端字段进 SQL。

### 权限 seed 新增（4 个，追加到 `seedPermissionList`）

`product:create`、`product:update`、`product:on_shelf`、`product:off_shelf`。

### 数据表（经 db-migration 机制新增迁移；最终以 db-migration 前置任务确立的迁移文件格式为准，本文件仅固化表结构）

```sql
CREATE TABLE IF NOT EXISTS products (
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
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS product_images (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  url        VARCHAR(512)    NOT NULL,
  sort       INT             NOT NULL DEFAULT 0,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- 不引入外键（沿用现有风格）；`product_images.product_id` 引用有效性由应用层保证。
- `status` 存储映射：`0=draft`、`1=on_shelf`、`2=off_shelf`。
- `name`（trim 后非空，≤128 字符）、`brand`（可选，≤64 字符）、`main_image`（可选，≤512 字符）、`images[].url`（非空）。

### 代码接口

- 新增 `api/product/v1`（Create/Update/OnShelf/OffShelf/List/Detail 的 Req/Res）、`internal/controller/product`、`internal/logic/product`（Register 模式）、`internal/service` 的 `IProduct` 接口；`internal/logic/logic.go` 注册 product 包。
- 新增 `IProduct` 接口（含 `CountByCategory(ctx, categoryID) (int64, error)` 供分类删除保护调用）。
- 扩展 `ICategory` 接口：新增 `Exists(ctx, id int64) (bool, error)` 与 `HasChildren(ctx, id int64) (bool, error)`（叶子判定：`Exists && !HasChildren`）。
- 分类删除保护：`categories.Delete` 在「有子分类」检查后，调用 `service.Product().CountByCategory(ctx, id)`，`>0` 返回 `CodeCategoryHasProducts`。
- 商品叶子校验：product logic 调用 `service.Category().Exists/HasChildren`，映射为商品域错误（不存在→`CodeProductInvalidCategory`，非叶子→`CodeProductCategoryNotLeaf`）。
- `internal/codes` 新增 3005 与 4001-4006；`internal/boot/seed.go` 追加 4 个权限；`internal/cmd` 挂载路由。

## Business Invariants

- INV-001（状态机合法迁移）：商品状态仅允许 `draft→on_shelf`、`on_shelf→off_shelf`、`off_shelf→on_shelf`；其余迁移被拒（409）且状态不变。对应 AC-006/007/008/009。
- INV-002（并发迁移最多一次成功）：同一商品并发重复上架/下架，条件 UPDATE 保证最多一次 `RowsAffected=1`，最终状态正确。对应 AC-010。
- INV-003（前后台可见性隔离）：前台列表/详情仅返回 `status=on_shelf`；`draft`/`off_shelf` 前台不可见（404 或等价）。对应 AC-011/012/013。
- INV-004（创建强制 draft，普通 update 不改状态）：创建接口不能直接产生 `on_shelf`；普通 update 提交 `status` 不改变状态。对应 AC-001/005。
- INV-005（SPU 仅绑定存在的叶子分类）：`category_id` 不存在或非叶子时创建/更新被拒，且不产生写入。对应 AC-004/014。
- INV-006（分类删除保护）：分类下存在商品时禁止删除，返回 409 `CodeCategoryHasProducts`，分类与商品均不受影响。对应 AC-022/023。
- INV-007（商品与图片原子一致）：创建/更新时 `products` 与 `product_images` 在同一事务内写入，失败整体回滚，无半成品。对应 AC-018。
- INV-008（价格整数分）：`price` 全程整数分存储与出参；负数/超上限拒绝且无写入。对应 AC-002/003。

## Failure and Consistency Semantics

- 事实来源：MySQL `products`（商品主数据 + 状态）、`product_images`（图片，从属 `products`）、`categories`（叶子/存在性判定）。无 Redis、无 MQ、无异步、无跨系统事务。
- 创建/更新（同步）：单事务写入 `products`（+ `product_images` 全量替换）——更新时 `images` 提供则删除旧行重插，未提供则保留；任一步失败整体回滚。
- 状态迁移（同步、原子）：上架 `UPDATE products SET status=1 WHERE id=? AND status IN (0,2)`；下架 `UPDATE products SET status=2 WHERE id=? AND status=1`。先 `SELECT` 判定存在（不存在→404），再条件 UPDATE，`RowsAffected=0` → 409 `CodeProductInvalidStatusTransition`（覆盖非法迁移与并发重复，二者都归为"当前状态不允许该迁移"）。无删除接口，故无删除竞态。
- 前台可见性（每请求）：列表/详情 SQL 强制 `status=on_shelf`；不存在或非 `on_shelf` 在前台返回 404。
- 分类删除保护：`存在检查 → 有子分类(3003) → 有商品(3005) → DELETE`，核对 `RowsAffected`。已知 TOCTOU 竞态见 Open Risks。
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

复用 `1001`（参数错误，兜底）、`1002`（401）、`1003`（403）、`1004`（404，兜底）。

## Allowed / Forbidden Changes

允许：
- 经 db-migration 机制新增 `products`/`product_images` 迁移（不回退到 `boot.ensureTables`）；`seedPermissionList` 追加 4 个商品权限。
- 新增 `api/product/v1`、`internal/controller/product`、`internal/service`（IProduct）、`internal/logic/product`、错误码 3005/4001-4006、路由挂载。
- 扩展 `ICategory`（`Exists`/`HasChildren`）并修改 `categories.Delete` 增加商品关联保护。
- 新增对应测试（见 Verification Requirements）。

禁止：
- 本 SPU 任务不自行实现 migration 机制（由 db-migration 前置任务负责），仅通过已建立的 migration 机制新增 `products`/`product_images` 表；不引入 SKU/库存/销量/订单/删除/促销价/上传存储等 Out of Scope 能力。
- 不改变 `/health`、前台 `/register`/`/login`/`/me`/`/logout`、分类公开/写接口的既有公开行为（分类删除新增商品保护是唯一改动）。
- 不新增商品删除接口；不新增 `product:list/read` 读权限（后台查询仅 `AdminAuth`）。
- 不在 SQL 中拼接前端传入的排序字段或关键词（排序白名单映射、关键词参数化绑定）。
- 不 fail-open 放行未认证/未授权请求；不把 `status` 迁移交给普通 update。

## Verification Requirements

- INV-001/002 → 状态机集成测试：3 条合法迁移成功、≥3 条非法迁移被拒且状态不变；多 goroutine 并发上/下架同一商品，断言最多一次成功、最终状态正确（建议 `-race` 下跑，以业务断言为准）。需 MySQL。
- INV-003 → 构造 `draft`/`on_shelf`/`off_shelf` 商品，访问前台/后台列表与详情，断言可见性隔离。
- INV-004 → 创建断言 `status=draft`；普通 update 提交 `status` 断言状态不变。
- INV-005 → 不存在分类、非叶子分类创建/更新被拒且无写入；叶子分类成功。
- INV-006 → 分类下有商品时删除断言 409 `3005` 且分类与商品不变；无商品分类删除成功。
- INV-007 → 创建/更新后查 `product_images` 核对行与 `sort`；模拟图片写入失败断言回滚无半成品。
- INV-008 → 合法价格整数分存储/出参；负数/超上限断言 400 且查库无新行。
- RBAC（AC-019/020/021）→ 无 token 401、有 token 无权限 403（查库无写入）、有权限成功、超级管理员放行。
- 列表（AC-014/015/016/017）→ `category_id` 精确匹配、分页结构 `{items,total,page,size}`、`size` 上限钳制、`keyword` LIKE 匹配、排序白名单与非法字段回落默认、无注入。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；MySQL/Redis 集成验证需说明容器就绪（`docker compose up -d`）。

## Open Risks

- 分类删除保护是"先查后删"、无外键：并发「创建商品」与「删除分类」可能留下指向已删除分类的孤儿商品（引用完整性由应用层保证，V1 可接受，不建议为此引入 FK 或锁）。分类删除为低频运维操作，风险有限。
- `keyword` 中的 `%`/`_` 会被当作 SQL LIKE 通配符（非注入，参数化绑定已消除注入面）；V1 不做转义，`keyword="%"` 会匹配全部商品，属可接受边界。
- 叶子判定只检查"存在且无子分类"，不校验分类 `status`（禁用分类仍可被绑定）；如 Owner 需要，可追加"分类必须启用"规则（当前不在 AC 内）。

## Owner Decision Record

Owner 于 2026-09-30 确认 6 项决定：

1. **建表机制**：选 B——先暂停 SPU，实现独立的 db-migration 前置任务；SPU 不回退到 `boot.go` 建表。
2. **status**：DB 存 0/1/2，API 输出 `draft`/`on_shelf`/`off_shelf` 字符串枚举。
3. **上下架接口**：独立接口；路径优先遵循现有项目路由风格，无既定规范用 `on-shelf`/`off-shelf`。
4. **price**：上限 `99,999,999` 分。
5. **分页**：`page=1`、`size=20`、max `size=100`。
6. **错误码**：`CodeCategoryHasProducts=3005`，商品域 `4001-4006`。

适用范围：`product-spu-v1` 本任务。六项问题虽已全部有结论，但决定 #1 改变本任务的实现基础（建表机制由 `boot.go` 迁移到 migration），且前置 db-migration 任务尚未建立，故本 Contract 保持 `BLOCKED`，**不标记为 APPROVED/可实现**。

待办（阻塞解除条件）：
- Task Builder 新建 `db-migration` 前置任务并完成 migration 机制（含既有表迁移）。
- Task Builder 更新 `product-spu-v1/task.md`：Relevant Context 的建表现状、产物清单（`internal/boot 建表` → migration 文件）、Analyst Question #1 结论；Out of Scope「不负责改造既有建表/迁移机制」保留。
- db-migration 完成后恢复 SPU 分析流程：确认 migration 文件命名/机制后，更新本 Contract 的「数据表」与「Allowed/Forbidden Changes」，重新交 Owner 确认后再置 `APPROVED`。
