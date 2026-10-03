# 商品分类设计（Category）

本文面向项目接手者，说明商品分类的架构、数据模型与边界。Category 历史任务为 NORMAL（无独立 APPROVED Contract），本文事实来源为可验证的 `categories-v1/task.md`、最终实现、测试，以及 `product-spu-v1` APPROVED Contract 中明确引用的 Category 事实（叶子/enabled 校验、删除保护、`Exists`/`HasChildren`/`IsEnabled` 服务边界）。

## 1. 职责与边界

商品分类用 `parent_id` 邻接表实现**最多 3 级**的树形分类，提供公开的树形/详情查询，以及受保护（`AdminAuth` + `RequirePermission`）的创建、更新、删除。分类名在同级内唯一；删除为物理删除且禁止删除有子分类或有商品的分类；用 `status` 禁用作为软下线手段。

事实来源为单一 MySQL（`categories` + `products`）。无缓存、无 MQ、无异步。

## 2. 数据模型

### 2.1 `categories`

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `parent_id` | BIGINT UNSIGNED | 非空默认 0（0 表示顶级） |
| `name` | VARCHAR(64) | 非空，trim 后校验 |
| `sort` | INT | 非空默认 0，同级排序 |
| `status` | TINYINT | 非空默认 1（`1=启用`、`0=禁用`） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

唯一约束：`uk_parent_name (parent_id, name)`（同级重名由 DB 兜底）；无 FK（顶级 `parent_id=0` 非引用）。

### 2.2 层级与树

- `maxLevel = 3`（顶级为 1）。
- 树构建：一次查全表，内存按 `parent_id` 组装嵌套树；查询固定 `ORDER BY parent_id, sort, id`；同级按 `sort` 升序、`sort` 相同按 `id` 升序。
- 循环引用防护：更新 `parent_id` 禁止指向自身或自身后代。
- 层级校验：创建或改父后（含整棵被移动子树的**最深后代**）超过 3 级时拒绝（400）。
- 软下线：`status=0` 不物理删除，树接口不再展示该分类及其子树（禁用父隐藏其启用子）；详情接口仍可访问。

## 3. 业务不变量

- INV-001（层级上限）：任何创建/改父后最深后代 ≤ 3 级，否则 400 `3004` 且数据不变、不产生第 4 级。
- INV-002（同级名唯一）：同一 `parent_id` 下 `name` 唯一，由 `uk_parent_name` 兜底，撞名 409 `3002`；并发同名仅一个成功。
- INV-003（无循环引用）：`parent_id` 不得指向自身或自身后代，否则 400 `3004` 且父子关系不变。
- INV-004（删除保护）：有子分类禁止删除（409 `3003`）；有商品关联禁止删除（409 `3005`，应用层检查 + FK `ON DELETE RESTRICT` 兜底）；两者均不影响分类/商品数据。
- INV-005（软下线语义）：`status=0` 后记录仍在、树不再展示该分类及其子树、详情仍可访问。

## 4. 一致性模型与失败语义

- 事实来源：MySQL `categories`（分类树与状态）、`products`（商品关联判定）。
- 创建/更新/删除为同步单表操作；唯一性由 DB 复合唯一键兜底（MySQL 1062 → 3002）。
- 删除顺序：`存在检查 → 有子分类(3003) → 有商品(3005) → DELETE`，核对 `RowsAffected`；并发窗口下 DELETE 命中 FK 1451（商品已建立引用）→ 409 `3005`，不泄漏 500。
- 失败语义：未认证 401、无权限 403、校验 400/404/409 均无写入；DB 技术错误统一 `1000` 500。

## 5. 安全与权限边界

- 前台公开：`GET /categories`（树形，仅启用项）、`GET /categories/:id`（详情）。
- 后台写操作：`AdminAuth` + `RequirePermission`：
  - `POST /categories` → `category:create`
  - `PUT /categories/:id` → `category:update`
  - `DELETE /categories/:id` → `category:delete`
- 授权判定见 `rbac.md`。

## 6. 错误码域

| code | 语义 | HTTP |
| --- | --- | --- |
| 3001 | CATEGORY_NOT_FOUND | 404 |
| 3002 | CATEGORY_NAME_EXISTS（同级重名） | 409 |
| 3003 | CATEGORY_HAS_CHILDREN（有子分类不能删除） | 409 |
| 3004 | CATEGORY_INVALID_PARENT（父不存在/指向自身或后代/超 3 级） | 400 |
| 3005 | CATEGORY_HAS_PRODUCTS（分类下有商品不能删除） | 409 |

复用：`1001`（400）、`1002`（401）、`1003`（403）。`3005` 由 `product-spu-v1` 引入。

## 7. 跨模块关系

- `products.category_id` → `categories.id`（FK `ON DELETE RESTRICT`）：分类删除保护的商品关联兜底，见 `product.md`。
- Category 向 Product 暴露 `Exists`/`HasChildren`/`IsEnabled` 服务边界，供商品创建/修改/上架时校验「存在 + 叶子 + enabled」，见 `product.md`。
- 建表经 golang-migrate 迁移（baseline `20261001000001` 含 `categories`），见 `migration.md`。

## 8. Deferred / 已知留白

- 分类写接口原「需登录」（`middleware.Auth`），已迁移为 `AdminAuth` + `category:*` 权限（见 `rbac.md`）；本文件描述的是当前最终状态。
- 批量操作、排序拖拽、导入导出、前端展示、分类与商品绑定/解绑等不在当前范围。
