# B1-03-01 数据库模型现状审查与迁移方案分析

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-03-01（只读审查） |
| 阶段 | Phase 2.0 |
| 所属分支 | `feat/p2-backend` |
| 审查基线 | `3491a9abecfc02bb897d64e38a987038cd6d28f3` |
| 审查时间 | 2026-11-10 |
| 代码/契约/Migration 改动 | **无**（本任务只读，仅产出本报告） |

> 事实来源优先级：① 当前 Go 代码与 Migration DDL；② `openapi/surgecart.openapi.yaml`；
> ③ `docs/design/*.md`（长期设计）；④ `docs/phase2/B1-01/B1-02`（契约事实与决策）。
>
> **两项输入不可用，已如实标注，未做任何推断性补写**：`tmp/` 下的 SpringBoot 参考项目不存在
> （全盘无 `pom.xml`/`build.gradle`），参考 SQL（`storage/private/legacy/shopping.sql`）也不在磁盘上
> （`.gitignore` 排除且未归档）。因此 §4、§5 标记为 **NOT AVAILABLE**，相关判断改用可验证的替代证据
> （`internal/logic/legacymigrate/` 的遗留 schema 读取代码、设计文档、契约）。

---

## 1. Git 基线事实

```text
仓库路径        /root/projects/my-shop
分支            feat/p2-backend
HEAD            3491a9abecfc02bb897d64e38a987038cd6d28f3
远端            origin/feat/p2-backend = 3491a9abecfc02bb897d64e38a987038cd6d28f3
ahead/behind    0 / 0
工作区          clean（git status --porcelain 空）
stash           空
```

与任务书预期基线完全一致，无推进、无差异。本次未创建/切换分支，未提交、未推送、未修改任何文件
（除本报告）。

---

## 2. 当前数据库结构

### 2.1 迁移机制

- 基于 **golang-migrate v4**，迁移文件通过 `//go:embed sql/*.sql` 内嵌
  （`internal/migrations/migrations.go:1-41`），运行时用 `iofs` 挂载，不依赖工作目录。
- 版本号 14 位时间戳（`YYYYMMDDHHMMSS`），当前序列 `20261001000001` ～ `20261001000024`，
  **24 个 up 文件 + 1 个 down 文件**（仅 outbox 有 down）。
- 追踪表 `schema_migrations`、并发串行化（`GET_LOCK`）由 golang-migrate 提供；
  启动时 `internal/boot/boot.go` 只做**只读** schema 就绪校验（版本号匹配且非 dirty），
  **不在启动时建表**；`checkSchemaReady` 版本落后即拒绝启动。
- dirty 状态禁止自动继续、禁止自动 force（`migrations.go` 的 dirty 分支），`Force` 仅用于 baseline 接管。
- Seed 只有两类，均在启动时幂等执行：`seedSuperAdmin`（超管）与 `seedPermissions`（权限码清单，
  `internal/boot/seed.go`）。

### 2.2 表清单（37 张）与业务职责

| 域 | 表 | 业务职责 | 关键约束/索引 |
|---|---|---|---|
| 身份 | `users` | 前台用户账号（用户名+密码哈希）、`status` 启停、`auth_epoch` 撤销代际 | `uk_username` |
| 身份 | `admins` | 后台管理员账号 | `uk_admin_username` |
| 身份 | `roles` | 角色定义 | `uk_role_name` |
| 身份 | `permissions` | 权限码字典（`resource:action`） | `uk_permission_code` |
| 身份 | `admin_roles` | 管理员↔角色关联 | PK(admin_id, role_id) |
| 身份 | `role_permissions` | 角色↔权限关联 | PK(role_id, permission_id) |
| 身份 | `refresh_tokens` | refresh token 哈希、Family 血缘、`auth_epoch` | `uk_token_hash`、`idx_family/idx_user/idx_sid` |
| 身份 | `user_status_audits` | 后台启停用户的审计流水（append-only） | `idx_target_user/idx_operator` |
| 商品 | `categories` | 分类树（`parent_id` 自关联，无 FK）、排序、启停 | `uk_parent_name` |
| 商品 | `products` | 商品 SPU：名称、品牌、分类、**展示价**、主图、详情、三态、`view_count` | `idx_category_id`、`idx_status`、FK→categories RESTRICT |
| 商品 | `product_images` | 商品图册（URL+排序） | `idx_product_id` |
| 商品 | `skus` | SKU：`product_id`、**仅 name+price+status**，无规格字段、无库存字段 | `uk_product_name(product_id,name)`、FK→products RESTRICT |
| 库存 | `inventories` | 普通库存：1:1 `sku_id`，`quantity` 无符号 | `uk_sku_id`、FK→skus RESTRICT |
| 库存 | `inventory_logs` | 库存变更流水（increase/deduct，前后值、操作者软引用） | `idx_sku_id`、FK→skus RESTRICT |
| 购物车 | `cart_items` | 用户+SKU 粒度条目，价格快照、勾选、数量 | `uk_user_sku` |
| 地址 | `addresses` | 收货地址：省/市/区 **自由文本**、详细地址、默认地址 | `idx_user_id`、`uk_user_default(default_key)`、FK→users CASCADE |
| 订单 | `orders` | 订单主数据：金额三快照、地址快照、状态(10..70)、幂等键、各阶段时间 | `uk_order_no`、`uk_user_idempotency`、`idx_user_id`、`idx_status_expire` |
| 订单 | `order_items` | 订单项快照（SKU/商品名、主图、成交价、数量） | `idx_order_id`、FK→orders CASCADE |
| 评价 | `reviews` | 已购评价：一单一品一条（`uk_order_item`），评分/内容/图片 JSON，软删 status=3 | `uk_order_item`、`idx_product_status` |
| 互动 | `favorites` / `product_likes` | 商品收藏 / 点赞 | 各自 `uk_user_product` |
| 内容 | `articles` / `article_likes` / `article_favorites` | 文章及点赞收藏 | `uk_user_article` 等 |
| 运营 | `banners` | 轮播图（图、跳转、排序、启停） | `idx_status_sort` |
| 运营 | `recommend_positions` / `recommend_items` | 推荐位与位内商品 | `uk_code`、`uk_position_product`、FK→positions CASCADE |
| 秒杀 | `flash_sale_activities` | 活动：名称、状态、时间窗 | `idx_status`、`idx_time` |
| 秒杀 | `flash_sale_activity_skus` | 活动↔SKU：秒杀价、**总库存/已售** | `uk_activity_sku`、FK→activities CASCADE |
| 秒杀 | `flash_sale_orders` | 秒杀订单（**独立订单模型**，自带快照与订单号） | `uk_flash_order_no`、`uk_flash_idempotency`、`uk_flash_one_per_user` |
| 秒杀 | `flash_sale_order_requests` | 异步落单请求队列（Outbox）：状态、重试、发布租约字段 | `uk_request_idempotency`、`idx_dequeue`、`idx_outbox_claim` |
| 秒杀 | `flash_sale_request_audits` | 秒杀请求修复审计 | `idx_request_id/idx_operator` |
| 退款 | `refund_requests` | 退款申请：状态、金额、审核/执行信息 | `idx_order_id/idx_user_id`、`uk_active(active_key)`（生成列，保证一单一条活跃退款） |
| 退款 | `refund_audits` | 退款审计（append-only） | `idx_refund_id` |
| 优惠券 | `coupon_templates` | 券模板：满减（固定金额，**仅 threshold+amount**）、总量/已发、领取/使用时间窗、启停 | 无额外索引（量小） |
| 优惠券 | `user_coupons` | 用户券实例：available/locked/used、绑定订单、各时间戳 | `uk_user_template`、`idx_user_id`、`idx_order_id` |
| 遗留 | `legacy_id_map` | 遗留数据迁移的旧 ID→新 ID 映射 | `uk_source_entity_old` |

**外键共 8 个**：products→categories、skus→products、inventories→skus、inventory_logs→skus
（均 RESTRICT）；addresses→users（CASCADE）；order_items→orders（CASCADE）；
flash_sale_activity_skus→activities（CASCADE）；recommend_items→positions（CASCADE）。
其余（orders.user_id/address_id、order_items.sku_id/product_id、cart_items.*、user_coupons.*、
refund_requests.*、flash_sale_orders.*）均为**软引用**，目的是让快照自足、避免历史数据被外键删除阻断。

### 2.3 金额、状态与时间约定

- **金额一律整数分**（`INT UNSIGNED`），上限 99,999,999；无浮点、无 decimal。
- 状态一律 `TINYINT`，API 层映射为字符串枚举（订单/退款/评价/优惠券）或 int（分类/轮播/用户状态）。
- 时间 `DATETIME`（无时区），`updated_at ON UPDATE CURRENT_TIMESTAMP`；订单支付截止用 MySQL `NOW()` 计算，
  避免 Go 与 MySQL 时区漂移。
- 关键生成列：`addresses.default_key`（VIRTUAL，保证每人最多一个默认地址）、
  `refund_requests.active_key`（STORED，保证一单最多一条活跃退款）。

### 2.4 现有事务边界（36 处 `g.DB().Transaction`）

| 事务 | 位置 | 原子范围 |
|---|---|---|
| 下单 | `internal/logic/order/order.go:305`（`insertOrder`） | 写 orders + order_items + 扣库存 + 预占券 + 清购物车条目 |
| 取消/超时取消 | `order.go:716`（`cancelInTx`） | 条件改状态 + 恢复库存 + 释放券 |
| 支付 | `order.go:529` | 状态推进 + 券核销 |
| 发货/收货 | `order.go:561`/`609` | 状态推进（发货前校验是否有活跃退款） |
| 退款执行 | `internal/logic/refund/refund.go`（`Execute`） | 订单置退款 + 恢复库存 + 退款状态 + 审计 |
| 领券 | `internal/logic/coupon/coupon.go:197` | `issued_quantity` 条件自增 + 插入 user_coupons |
| 库存调整 | `internal/logic/inventory/inventory.go:80` | 条件 upsert + 写 inventory_logs |
| 秒杀消费 | `internal/logic/flashsale/consume.go:191/264` | 出队(SKIP LOCKED) + 保存点内建单+扣 sold + 状态推进 |
| 地址默认 | `internal/logic/address/address.go:91/222` | 取消旧默认 + 设新默认 |

---

## 3. 当前 Go 业务模型

业务域→表映射（由 `Model("...")` 实际引用统计）：

```text
address addresses | admin admin_roles,admins,permissions,role_permissions,roles
article articles,article_likes,article_favorites | banner banners | cart cart_items
categories categories | coupon coupon_templates,user_coupons | dashboard orders
favorite favorites | flashsale admins,categories,flash_sale_*,products,skus
iam admins,refresh_tokens,user_status_audits,users | inventory inventories,inventory_logs
like product_likes | order cart_items,order_items,orders
product product_images,products | recommendation recommend_items,recommend_positions
refund admins,orders,refund_audits,refund_requests,users | review reviews | sku skus
health/upload/legacymigrate 无业务表引用（legacymigrate 读 staging 库）
```

模型承载方式：**没有 `internal/model` 包**，每个 logic 包内定义行结构体（`orm` 标签）+ 读写方法，
SQL 以 `g.DB().Model(...)` 链式构造为主，复杂查询用原生 SQL（联表、`FOR UPDATE`、`SKIP LOCKED`）。

各域当前承担的业务职责（要点）：

- **User/IAM**：注册/登录/刷新/登出/会话撤销 + 后台启停（`users.status` + `auth_epoch` 级联吊销）。
  用户资料只有 `id`/`username`（`api/iam/v1/iam.go:53-56` 的 `MeRes` 仅两字段）。
- **Product/SKU**：SPU 三态（draft/on_shelf/off_shelf）、图册、浏览量；SKU 二态（enabled/disabled），
  `skus.price` 与 `products.price` 独立、无派生（`docs/design/sku.md:2.3`）。
  设计文档明确：SKU 只提供 `skus.id` 作稳定引用键，**不含库存、不含规格**。
- **Inventory**：`inventories.quantity` 条件扣减（`UPDATE ... WHERE quantity >= ?` +
  `RowsAffected`，`inventory.go:133-146`），流水记录前后值；无幂等键（设计已知留白）。
- **Cart**：用户+SKU 唯一，`price_snapshot` 加购快照不锁价，实时联查 SKU/商品/库存给出可购性与差额。
- **Order**：服务端重读 `skus.price` 定价，`original_amount/discount_amount/total_amount` 三快照，
  地址按列快照（不依赖 addresses 是否存在）；状态机 10→20→30→40→50，10→60，20→70；
  幂等由 `uk_user_idempotency` + `request_hash` 双闸门；超时取消由后台 scanner + 入口懒取消。
- **Refund**：一单一条活跃退款（`active_key` 生成列唯一），仅 `paid → refunded`，恢复库存、**不返券**。
- **Coupon**：模板（满减固定金额 + 领取/使用时间窗 + 总量）+ 用户券（available→locked→used），
  下单事务内 `LockInTx`，支付时 `MarkUsedInTx`，取消时 `ReleaseInTx`。
  折扣只按订单 `original_amount` 与模板门槛比较（`coupon.go:278-315`），**无适用范围、无百分比**。
- **Flash Sale**：Redis Lua 预扣（`flashsale:stock:*`，`internal/logic/flashsale/redis.go:18-24`）作为准入闸门，
  请求落 `flash_sale_order_requests`（Outbox，`SKIP LOCKED` 出队），消费事务内写 `flash_sale_orders`
  并条件扣 `flash_sale_activity_skus.sold`（`flashsale.go:527-535`），随后发布 Kafka 事件。
  **秒杀订单不写 `orders`、不扣 `inventories`**，两套订单模型互不共享行。

---

## 4. Java 参考项目可确认的业务功能

**NOT AVAILABLE（无法审查）**。

证据：

```text
find / -maxdepth 6 -name "pom.xml" -o -name "build.gradle"   → 无结果
/root/projects/ 下仅 my-shop
仓库 .gitignore 含 /tmp/
/root/projects/my-shop/tmp/                                 → 不存在
```

因此本报告**不声称**任何来自 SpringBoot 项目的 Controller、Service 或 SQL 结论。
任务背景提到"此前已发现 Java 项目缺少四个核心模块"，本次无权复核该判断，不再复述为事实。

可替代的、可验证的遗留业务证据只有一处：`internal/logic/legacymigrate/`（CRMEB 导出库迁移工具）。
它证明遗留系统具备**多规格商品**能力，并给出了映射规则：

- 读 `store_product`、`store_product_attr`（规格维度数）、`store_product_attr_value`（SKU 明细行）；
- 有明细行 → 每行生成一个 SKU（`name = attr_value.sku` 规格名）+ 一条库存；
- 无明细行且维度 ≤1 → 单规格，用商品级 price/stock 生成 1 个默认 SKU；
- 维度 >1 但缺明细 → `ErrMultiSpecMissingDetail` 跳过（不臆造规格）。

（`internal/logic/legacymigrate/mapping.go:240-296`）

---

## 5. SQL 模型分析

**NOT AVAILABLE（无法审查）**。

证据：`docs/legacy-data.md:7` 声明参考 SQL 位于 `storage/private/legacy/shopping.sql`，
但该路径在当前磁盘不存在（`storage/` 下只有 `banners/`），且 `.gitignore`/`.dockerignore` 已排除，
Git 历史中无跟踪记录。

因此无法核对：数据库版本、字段类型、索引、主外键、金额表示、状态字段、SKU 规格模型、
优惠券模型、订单模型。**不复制、不猜测任何原始数据**（含 PII/AK/SK/Token/密码）。

若后续提供该文件，应只在隔离环境做**结构提取**（`SHOW CREATE TABLE` 或 `CREATE TABLE` 段落），
禁止整文件入库、禁止执行导入。

---

## 6. 当前模型与目标业务的差距

判定口径：以"完整电商核心业务"为基准，对照 §2 的表结构、§3 的职责与 `openapi/surgecart.openapi.yaml`
的 142 条常驻接口（B1-01 §14 已登记 10 项前台缺失 + 11 项后台缺失）。

| # | 差距 | 证据 | 影响 |
|---|---|---|---|
| G1 | **无商品规格模型**：规格组/规格值/SKU↔规格值关联全缺，SKU 只有 `name` | `skus` DDL 无规格列；`api/sku/v1/sku.go:18-26` 仅 id/product_id/name/price/status | 前端无法做"选颜色/尺码→定位 SKU"；只能让运营把规格拼进 SKU 名 |
| G2 | **SKU 无图片** | `skus` 无 image 列；`product_images` 只有商品级 | 规格切换后主图不变，体验与主流商城不一致 |
| G3 | **无支付流水表**：只有 `orders.paid_at`，Mock 支付 | `orders` DDL；`order.Pay` 仅改状态 | 无法记录渠道/流水号/金额；对账、退款溯源缺依据 |
| G4 | **无物流/发货信息表**：只有 `orders.shipped_at` | `orders` DDL；B1-01 §14.3-B8 已登记缺失 | 后台无法录入承运商/运单号，用户看不到物流 |
| G5 | **无运费字段**：`total_amount = original - discount` | `docs/design/order.md:2.3` 明确公式 | 无法表达运费，实付与应付口径不完整 |
| G6 | **用户资料过薄**：仅 username | `users` DDL；`MeRes` 两字段 | 无手机号/昵称/头像；收货联系人只能手填 |
| G7 | **优惠券无适用范围/折扣类型/每人限领** | `coupon_templates` 仅 threshold+discount_amount；`uk_user_template` 限制每人每模板 1 张 | 无法做品类券/商品券/折扣券；运营手段受限 |
| G8 | **无地区码表**：省市区自由文本 | `addresses` DDL；`docs/design/address.md:78` 已声明留白 | 运费/配送范围/地址标准化无法做 |
| G9 | **普通订单与秒杀订单双模型**，无统一视图 | `flash_sale_orders` 独立表；Order 域不引用 | 用户"我的订单"看不到秒杀单；后台两套列表 |
| G10 | **无订单状态流水**（仅当前状态 + 各阶段时间） | `orders` DDL；`refund_audits`/`user_status_audits` 有审计，订单无 | 争议处理缺少可追溯轨迹 |
| G11 | **商品无软删除**：物理删除被 FK RESTRICT 阻断（无删除接口） | `docs/design/sku.md:INV-002`；B1-01 §14.3-B6 | 商品/SKU 生命周期不完整 |
| G12 | **后台缺用户/管理员/评价/秒杀活动列表**（纯 API 缺口，不涉及新表） | B1-01 §14.3 B1-B5 | B1-03 只需索引支撑，不需要新模型 |

**已确认存在、不要重复建设的能力**：用户地址簿（含默认地址唯一约束）、购物车（含价格快照与可购性）、
普通库存与流水、退款申请与审计、优惠券领取/预占/核销/释放、秒杀 Redis+Lua+Outbox+Kafka 全链路、
评论（一单一品一条）、收藏/点赞、推荐位、文章、轮播图、RBAC。

---

## 7. 各领域推荐数据模型

### 7.1 User（用户资料）

现状：`users(id, username, password_hash, status, auth_epoch, created_at, updated_at)`。

推荐（**MUST**，仅在 `users` 上加列，不建新表）：

| 列 | 类型 | 说明 |
|---|---|---|
| `nickname` | `VARCHAR(32) NOT NULL DEFAULT ''` | 展示昵称，空则前端回退 username |
| `avatar_url` | `VARCHAR(512) NOT NULL DEFAULT ''` | 头像 |
| `phone` | `VARCHAR(20) NULL` | 手机号，`UNIQUE KEY uk_phone(phone)`（多 NULL 允许） |

约定：手机号不做短信验证（无短信通道），仅作为资料字段；验证与绑定流程属于后续任务（**OPTIONAL**）。
`username` 仍是登录唯一凭证，不改语义。

### 7.2 Product / SKU（规格模型）

**选型**：商品级规格（每个商品自定义规格项），不建全局规格字典。理由：
① 现有后台是单商品维护，全局字典需要额外的字典管理页；② 规格名（颜色/尺码）跨品类复用价值低；
③ 后续若需筛选，可由 `product_specs.name` 建索引扩展，不阻塞。

**MUST**：

| 表 | 列 | 约束 |
|---|---|---|
| `product_specs` | `id`, `product_id`, `name VARCHAR(32)`, `sort INT`, `created_at` | `UNIQUE uk_product_spec_name(product_id,name)`、FK→products CASCADE |
| `product_spec_values` | `id`, `spec_id`, `value VARCHAR(32)`, `image_url VARCHAR(512) NULL`, `sort INT` | `UNIQUE uk_spec_value(spec_id,value)`、FK→product_specs CASCADE |
| `sku_spec_values` | `sku_id`, `spec_id`, `spec_value_id` | `PRIMARY KEY(sku_id, spec_id)`、`UNIQUE uk_sku_value(sku_id, spec_value_id)`、FK→skus CASCADE |

`skus` 加列：

| 列 | 类型 | 说明 |
|---|---|---|
| `image_url` | `VARCHAR(512) NOT NULL DEFAULT ''` | 规格图，空则回退商品主图 |
| `spec_signature` | `CHAR(32) NULL` | 规格组合指纹（`spec_value_id` 升序拼接后 MD5 或等价），**唯一索引 `uk_product_signature(product_id, spec_signature)`**；历史/无规格 SKU 为 NULL（唯一索引允许多 NULL） |

约束与兼容：

- `skus.name` **保留**为展示名（历史数据不变）；新 SKU 由规格值拼接生成（如 `红/S`），仍走 `uk_product_name`。
- 不新增 `stock` 列（库存仍在 `inventories`）。
- 无规格商品（现有全部 SKU）不写 `sku_spec_values`，前端按"单规格/无规格"降级。

**SHOULD**：`skus.sku_code VARCHAR(64) NULL` + `uk_sku_code(sku_code)`（商家编码；现有 `skus.id` 已是稳定键，
故非 MUST）；`skus.sort INT`（后台排序）。

### 7.3 Order（金额、支付、物流、快照）

现状：`orders` 金额三快照 + 地址快照 + 状态机 + 各阶段时间；`order_items` 名称/图/价快照。

**MUST**：

1. `orders.shipping_fee INT UNSIGNED NOT NULL DEFAULT 0`（`AFTER discount_amount`）。
   历史订单保持 0，`total_amount` **不重算**（线上公式暂不变，语义扩展在后续实现任务中做）。
2. `payments` 表：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | BIGINT UNSIGNED PK | |
| `order_id` | BIGINT UNSIGNED | 软引用（与 orders 现有风格一致） |
| `channel` | `VARCHAR(16)` | `mock` / 未来 `alipay`、`wechat` |
| `amount` | `INT UNSIGNED` | 实付金额（分） |
| `status` | `TINYINT` | 10=待支付、20=成功、30=失败、40=已退款 |
| `transaction_no` | `VARCHAR(64) NULL` | 渠道流水号 |
| `paid_at` | `DATETIME NULL` | |
| `raw_note` | `VARCHAR(255) NULL` | 诊断信息（不存全量报文） |
| `created_at/updated_at` | DATETIME | |

索引：`uk_order_channel(order_id, channel)`（一单每渠道最多一条）、`idx_status_paid(status, paid_at)`。

3. `order_shipments` 表（发货/物流）：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | BIGINT UNSIGNED PK | |
| `order_id` | BIGINT UNSIGNED | FK→orders CASCADE（订单不物理删除） |
| `carrier` | `VARCHAR(32)` | 承运商 |
| `tracking_no` | `VARCHAR(64)` | 运单号 |
| `shipped_by` | `BIGINT UNSIGNED NULL` | 操作管理员（软引用） |
| `shipped_at` | `DATETIME NOT NULL` | |
| `created_at` | DATETIME | |

索引：`idx_order_id(order_id)`、`uk_carrier_tracking(carrier, tracking_no)`。

**SHOULD**：`orders.buyer_remark VARCHAR(255)`、`orders.admin_remark VARCHAR(255)`；
`order_items.spec_text VARCHAR(128)`（下单时规格文本快照，便于 SKU 删除后仍能展示规格）；
`order_status_logs`（订单状态流转审计）。

**OPTIONAL（本轮不做）**：`orders.order_type` 与秒杀订单合并、发票、退货物流、订单拆包。

### 7.4 Coupon（批次、范围、限制）

现状：模板（满减固定金额、总量、时间窗、状态）+ 用户券（available/locked/used、绑定订单）。

**MUST（本轮仅索引级）**：为"订单可用券查询"（B1-01 §14.1-M3）补索引
`user_coupons(user_id, status)` 与 `coupon_templates(status, claim_start_time, claim_end_time)`
（后者数据量小，可延后）。**不需要新表**。

**SHOULD**：

| 变更 | 说明 |
|---|---|
| `coupon_templates.discount_type TINYINT`（1=满减、2=折扣） + `discount_value` | 百分比券；需计价逻辑配合 |
| `coupon_template_scopes`（`template_id`, `scope_type` 1=商品/2=分类/3=品牌, `scope_id`） | 适用范围；默认无行=全场 |
| `coupon_templates.per_user_limit INT`（默认 1） | 每人限领；需移除 `uk_user_template` 或改为计数校验 |
| `user_coupons.expires_at DATETIME` | 领取时快照到期时间，避免模板改动影响已领券 |

**OPTIONAL**：多券叠加、券分摊到订单项、转赠、自动发券、积分抵扣。

### 7.5 Address / Logistics（地区、运费）

现状：`addresses` 省市区自由文本 + 默认地址唯一；`orders` 存地址快照。

**SHOULD**：

- `regions(id, code VARCHAR(12), parent_code, name VARCHAR(32), level TINYINT, sort)`，
  `uk_code(code)`、`idx_parent(parent_code)`；数据来源需外部权威数据集（**需 Owner 决策**）。
- `freight_templates`（`id`, `name`, `free_threshold INT NULL`, `status`）+
  `freight_template_rules`（`template_id`, `region_code`, `first_qty`, `first_fee`, `extra_fee`）；
  `products.freight_template_id`。
- 依赖链：`regions` → `freight_templates` → `orders.shipping_fee` 的计算，本轮只落 `shipping_fee` 列。

**OPTIONAL**：地址标签/分组、邮编、海外地址、多仓路由。

### 7.6 Flash Sale（不新增模型）

**结论：秒杀侧零表结构变更**。规格模型为旁路表（`product_specs` 等），秒杀只读 `skus`/`products`，
`flash_sale_activity_skus.sku_id` 语义不变；`flash_sale_orders` 自带快照，不依赖 `order_items`。
唯一可选增强：`flash_sale_orders.spec_text`（若要展示规格）——**OPTIONAL**。

---

## 8. 需要新增的表

| 优先级 | 表 | 归属 | 依赖 | 说明 |
|---|---|---|---|---|
| MUST | `product_specs` | 商品 | products | 规格项 |
| MUST | `product_spec_values` | 商品 | product_specs | 规格值（可带规格图） |
| MUST | `sku_spec_values` | 商品 | skus、product_specs、product_spec_values | SKU↔规格值 |
| MUST | `payments` | 订单 | orders（软引用） | 支付流水（Mock 渠道起步） |
| MUST | `order_shipments` | 订单 | orders | 承运商/运单号 |
| SHOULD | `order_status_logs` | 订单 | orders | 状态审计 |
| SHOULD | `coupon_template_scopes` | 优惠券 | coupon_templates | 适用范围 |
| SHOULD | `regions` | 地址 | — | 行政区划码表 |
| SHOULD | `freight_templates` / `freight_template_rules` | 地址/商品 | regions | 运费模板 |
| OPTIONAL | `inventory_check_orders` / `inventory_check_items` | 库存 | — | 盘点单 |
| OPTIONAL | `payment_callbacks` | 订单 | payments | 回调幂等台账 |
| OPTIONAL | `notifications` | 平台 | — | 消息通知 |

---

## 9. 需要修改的表

| 表 | 变更 | 优先级 | 兼容性 |
|---|---|---|---|
| `skus` | `ADD image_url VARCHAR(512) NOT NULL DEFAULT ''`、`ADD spec_signature CHAR(32) NULL`、`ADD UNIQUE uk_product_signature(product_id, spec_signature)` | MUST | 纯加列；历史行 NULL，唯一索引允许多 NULL |
| `orders` | `ADD shipping_fee INT UNSIGNED NOT NULL DEFAULT 0` | MUST | 默认 0，历史 `total_amount` 不重算 |
| `users` | `ADD nickname VARCHAR(32) NOT NULL DEFAULT ''`、`ADD avatar_url VARCHAR(512) NOT NULL DEFAULT ''`、`ADD phone VARCHAR(20) NULL`、`ADD UNIQUE uk_phone(phone)` | MUST | 纯加列；phone 全 NULL 时可加唯一索引 |
| `skus` | `ADD sku_code VARCHAR(64) NULL`、`ADD UNIQUE uk_sku_code(sku_code)`、`ADD sort INT NOT NULL DEFAULT 0` | SHOULD | 纯加列 |
| `products` | `ADD deleted_at DATETIME NULL`、`ADD freight_template_id BIGINT UNSIGNED NULL` | SHOULD | 软删除需改查询条件，风险中等 |
| `coupon_templates` | `ADD discount_type TINYINT NOT NULL DEFAULT 1`、`ADD discount_value INT UNSIGNED NOT NULL DEFAULT 0`、`ADD per_user_limit INT UNSIGNED NOT NULL DEFAULT 1` | SHOULD | 默认值与现语义一致 |
| `user_coupons` | `ADD expires_at DATETIME NULL`、`ADD KEY idx_user_status(user_id,status)` | SHOULD | 历史行 NULL 时回退模板窗口 |
| `order_items` | `ADD spec_text VARCHAR(128) NOT NULL DEFAULT ''` | SHOULD | 历史行空串 |
| `orders` | `ADD buyer_remark VARCHAR(255) NOT NULL DEFAULT ''`、`ADD admin_remark VARCHAR(255) NOT NULL DEFAULT ''` | SHOULD | 纯加列 |

**明确不改**：`inventories`、`inventory_logs`、`flash_sale_*`、`refresh_tokens`、`cart_items`、
`refund_requests`、`refund_audits`、`addresses`（地区码表为新增，不动 addresses 自由文本列）。

---

## 10. 不需要迁移的表及原因

| 表 | 原因 |
|---|---|
| `inventories` / `inventory_logs` | 条件扣减 + 流水模型已满足核心；规格模型不改变库存粒度（仍是 SKU 级） |
| `flash_sale_activities` / `flash_sale_activity_skus` / `flash_sale_orders` / `flash_sale_order_requests` / `flash_sale_request_audits` | 秒杀链路已含 Redis+Lua+Outbox+Kafka；本轮不改库存与订单模型，避免破坏不变量与压测基线 |
| `cart_items` | 已按 SKU 维度存快照；规格选择在加购前完成，不需要改表 |
| `orders` 既有列（金额/地址/状态/幂等/时间） | 语义仍成立；只做加列，不改类型与含义 |
| `addresses` | 自由文本可用；地区码表以新增表实现，存量地址无需改写 |
| `refund_requests` / `refund_audits` | 一单一条活跃退款 + 审计已满足；部分退款属 OPTIONAL |
| `reviews` / `favorites` / `product_likes` / `articles` / `banners` / `recommend_*` | 与本次核心差距无关 |
| `users` 既有列、`admins` / `roles` / `permissions` / `admin_roles` / `role_permissions` / `refresh_tokens` / `user_status_audits` | RBAC 与会话模型稳定；用户资料只加列 |
| `legacy_id_map` | 遗留迁移映射表，保持只追加 |
| `coupon_templates` / `user_coupons`（本轮 MUST 范围内） | 现有满减模型可用；范围/折扣类型列为 SHOULD |

---

## 11. Migration 执行顺序

版本号策略（**需 Owner 决策**）：现有序列固定为 `202610010000NN`（日期非真实）。建议新迁移改用
真实时间戳（如 `20261110000001`），数字仍严格递增；若要求视觉连续，可继续 `20261001000025` 起。
两种都能被 golang-migrate 正确处理。

推荐顺序（每个文件独立、正向可执行、无跨文件依赖倒置）：

| 顺序 | 文件（建议名） | 内容 | 可独立回滚 |
|---|---|---|---|
| 1 | `..._product_spec.up.sql` | 3 张规格表 + `skus.image_url/spec_signature` + 唯一索引 | 是（drop 表/列） |
| 2 | `..._user_profile.up.sql` | `users` 加 3 列 + `uk_phone` | 是 |
| 3 | `..._order_shipping.up.sql` | `orders.shipping_fee` + `order_shipments` 表 | 是 |
| 4 | `..._payments.up.sql` | `payments` 表 | 是 |
| 5（SHOULD） | `..._coupon_scope.up.sql` | 模板折扣类型/限领 + scope 表 + 索引 | 是 |
| 6（SHOULD） | `..._regions_freight.up.sql` | `regions` + 运费模板 | 是（无数据依赖） |
| 7（SHOULD） | `..._order_audit.up.sql` | `order_status_logs` + 订单备注 | 是 |

执行纪律：

1. 先 `migrate up` 到 0 号（当前 24）确认 baseline；再逐个应用，不用一次性大文件。
2. 每个文件只做 **additive** 变更（`ADD COLUMN` 带默认值、`CREATE TABLE`、`ADD INDEX`），
   不做 `MODIFY COLUMN`、不改列名、不重命名表。
3. 新增唯一索引前先确认无重复（当前数据量小，但脚本要幂等可重跑）。
4. 不在 Migration 里做业务回填（除下一节的显式回填），回填走独立命令，失败可重试。
5. 每个迁移都要能在 `migrate down`（或手工反向 SQL）恢复到上一版；**不做破坏性 down**。

---

## 12. 历史数据兼容和回填策略

| 变更 | 历史数据 | 回填策略 |
|---|---|---|
| 规格三表 | 现有 SKU 无规格行 | **不回填**：`product_specs`/`product_spec_values`/`sku_spec_values` 保持空；前端按"无规格"降级；运营需要时再补录 |
| `skus.image_url` | 默认 `''` | **不回填**（空则回退 `products.main_image`，展示逻辑在应用层） |
| `skus.spec_signature` | NULL | **不回填**（NULL 表示无规格）；唯一索引允许多 NULL |
| `users.nickname` | 默认 `''` | 可选一次性 `UPDATE users SET nickname = username WHERE nickname = ''`（应用层回退亦可，二选一） |
| `users.phone` | NULL | **不回填**（无可靠来源；禁止从遗留库盲目导入 PII） |
| `orders.shipping_fee` | 默认 0 | **不回填**；`total_amount` 保持原值，禁止重算（否则与历史支付金额不一致） |
| `payments` | 表为空 | 可选回填：`INSERT ... SELECT` 已支付订单生成 `channel='mock'` 记录；必须按 `uk_order_channel` 幂等 |
| `order_shipments` | 表为空 | **不回填**（只有 `shipped_at`，无承运商/运单号，造数据会污染物流事实） |
| `order_items.spec_text` | 默认 `''` | **不回填**（历史 `sku_name` 已含规格文本） |
| 地区/运费/券范围 | 新表为空 | **不回填**；默认语义为"全场/不启用运费"，与现状等价 |

回填通则：**只回填能从现有数据无歧义推导的列**；任何需要外部来源（手机号、运单号、地区码）的字段
一律留空，由运营补录。

---

## 13. 秒杀与库存风险

**现有不变量（不得破坏）**：

1. 秒杀库存事实 = `flash_sale_activity_skus.total_stock/sold`；普通库存事实 = `inventories.quantity`。
   两者**互不换算、互不同步**（Redis 只是准入闸门）。
2. 秒杀订单不写 `orders`、不写 `order_items`、不扣 `inventories`；退款也不恢复秒杀库存
   （`docs/design/refund.md:124-129` 的"Deferred / 已知留白"）。
3. 秒杀落单顺序：出队（`FOR UPDATE SKIP LOCKED`）→ 保存点 → 写 `flash_sale_orders` →
   条件扣 `sold`（`sold < total_stock`）→ 状态推进 → Outbox/Kafka。
4. 普通下单顺序：写 orders → 写 order_items → `UPDATE inventories SET quantity=quantity-? WHERE quantity>=?`
   → 预占券 → 清购物车。
5. 幂等三重闸门：`uk_flash_idempotency`、`uk_flash_one_per_user`、`request.status` 状态机；
   普通订单：`uk_user_idempotency` + `request_hash`。

**本次变更的风险评估**：

| 变更 | 对秒杀影响 | 对普通库存影响 | 结论 |
|---|---|---|---|
| 新增 3 张规格表 | 无（不参与秒杀读写路径） | 无 | 安全 |
| `skus.image_url` / `spec_signature` | 无（`resolveSku` 只读固定列，新增列不改变结果） | 无 | 安全 |
| `orders.shipping_fee` | 无（秒杀不写 orders） | 若未来改 `total_amount` 公式需同步订单测试 | 本轮只加列，安全 |
| `payments` / `order_shipments` | 无 | 依赖 orders，不改现有金额/状态 | 安全 |
| 商品软删除 `products.deleted_at` | **有风险**：秒杀 `resolveSku` 需同时判 `deleted_at IS NULL` | 有风险：所有商品查询需带条件 | 本轮不做，列为 SHOULD 并单独评估 |
| 统一订单视图（秒杀并入 orders） | **高风险**：需重写消费事务与对账 | 高风险 | 明确 OPTIONAL，本轮不做 |

**禁止事项**：重建 `inventories` 表；给 `skus` 加 `stock` 列；把 `sold` 改为 `inventories` 派生；
在 Migration 中回填/重算历史订单金额；修改 `flash_sale_*` 的列类型或唯一键。

---

## 14. 前后端接口影响

> **前端资料不可用**：当前仓库只有后端分支（`git branch -r` 无前端分支），无 React/Vue 代码与
> 接口需求文档。以下影响基于 `openapi/surgecart.openapi.yaml` 契约推导，**未与前端实现核对**。

| 变更 | 影响的现有接口 | 需要的新增/增强 |
|---|---|---|
| 规格模型 | `GET /products/:id`、`GET /admin/products/:id`（需返回 `spec_groups`、每个 SKU 的 `spec_value_ids`/`image_url`）、`POST /admin/skus`（需支持规格值绑定） | 规格维护子接口（新增/删除规格与值）；SKU 矩阵批量创建 |
| 用户资料 | `GET /me`、`POST /register`（可选 `phone/nickname`） | `PUT /me`（资料更新） |
| 运费/物流 | `GET /orders/:id`、`GET /admin/orders/:id`（需 `shipping_fee`、`shipments`） | 后台发货接口需接收 `carrier`/`tracking_no`；`POST /admin/orders/:id/ship` 语义扩展 |
| 支付 | `POST /orders/:id/pay`（Mock） | 后台支付流水查询（可选）；支付回调接口（未来） |
| 优惠券 | `GET /coupons/mine` | `GET /coupons/available`（M3）、模板适用范围字段 |
| 秒杀 | 无变化 | 无（若做统一订单视图才需要，本轮不做） |

契约影响：本次只读审查**不修改 OpenAPI**；B1-03 落库后需单独任务更新契约（新增字段/端点，
`x-known-issue` 与 `x-error-codes` 同步），不能在 Migration 任务里顺带改契约。

---

## 15. 需要 Owner 决策的问题

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| D-1 | **参考资料缺失**：`tmp/` Java 项目与 `shopping.sql` 均不在环境内 | ① 补充材料后重跑 §4/§5；② 接受以现有代码+契约为准推进 | 建议 ①（否则"参考功能覆盖度"无法闭环；若选 ②，B1-03 范围以本报告 §6/§7 为准） |
| D-2 | 规格模型选型 | ① 商品级规格；② 全局规格字典 | ①（后台成本低，后续可按需上提字典） |
| D-3 | 支付范围 | ① 只建 `payments` 表，Mock 写入；② 本轮接真实支付 | ①（真实支付涉及渠道、回调、对账，超出 DB 任务） |
| D-4 | 运费与地区 | ① 本轮只加 `orders.shipping_fee`；② 同步做 `regions`+运费模板 | ①（②需要权威地区数据源） |
| D-5 | 优惠券增强 | ① 只补可用券查询索引；② 做适用范围/百分比/限领 | ①，②列为 SHOULD 下一轮 |
| D-6 | 商品软删除 | ① 本轮不加；② 加 `deleted_at` 并改全部查询 | ①（改动面大，且影响秒杀读路径） |
| D-7 | Migration 版本号 | ① 继续 `202610010000NN`；② 改用真实时间戳 | ②（日期与内容一致；仅影响观感，不影响执行） |
| D-8 | 秒杀订单是否统一视图 | ① 保持双模型；② 合并到 orders | ①（合并需重写消费与退款语义，风险高） |

---

## 16. B1-03-02 的具体实施建议

**范围建议（最小可交付）**：只落 §8 的 MUST 表与 §9 的 MUST 列，共 3 个 Migration，全部 additive，
不改任何既有列语义、不碰秒杀/库存表。

### 16.1 交付物

| # | 文件 | 内容 |
|---|---|---|
| 1 | `internal/migrations/sql/<ts>_product_spec.up.sql` / `.down.sql` | `product_specs`、`product_spec_values`、`sku_spec_values`、`skus.image_url`、`skus.spec_signature` + `uk_product_signature` |
| 2 | `<ts>_user_profile.up.sql` / `.down.sql` | `users.nickname`、`users.avatar_url`、`users.phone`、`uk_phone` |
| 3 | `<ts>_order_shipping_payment.up.sql` / `.down.sql` | `orders.shipping_fee`、`order_shipments`、`payments`（可拆成 2 个文件，便于独立回滚） |

> 现有仓库只有 `20261001000024` 一个 down 文件；B1-03 建议 **每个迁移都配 down**，
> 反向 SQL 必须可在空数据/有数据两种情况下执行（drop 表/列不依赖数据）。

### 16.2 实施顺序与验收

1. **Migration + 结构测试**：新增 `internal/migrations` 用例，断言版本数、目标表/列/索引存在、
   `migrate up → down → up` 可重入（测试用临时 schema，不接生产库）。
2. **应用层只读接入（可选同任务）**：不做业务改造，仅在 `product` 详情查询里 `LEFT JOIN` 规格表，
   保证空数据时行为不变（可作为 B1-03-03 的前置）。
3. **契约与 DTO 变更另开任务**：新增字段进入 DTO/OpenAPI 时，必须同步更新 `x-error-codes`、
   `x-pagination-mode` 与契约门禁（`scripts/contract-check.sh` 必须保持全绿）。
4. **不做数据迁移**；`legacy_id_map` 不参与本任务。

### 16.3 测试清单

| 项 | 期望 |
|---|---|
| `migrate up` 从 24 → 27 | 成功，`schema_migrations` 版本正确、dirty=0 |
| `migrate down` 回 24 | 成功，目标表/列消失，既有表数据不变 |
| 重复 up | 幂等（无变更） |
| 规格唯一性 | 同商品同 `spec_signature` 插入失败；NULL 允许重复 |
| 历史 SKU 兼容 | 无 `sku_spec_values` 的 SKU 仍可加购/下单/秒杀读取 |
| 订单金额不变 | 回填 `shipping_fee=0` 后 `total_amount` 与迁移前逐行一致 |
| 秒杀回归 | `internal/logic/flashsale` 单测与 Kafka/Outbox 用例全绿（本次不改表，应无差异） |

### 16.4 明确不做

- 不合并秒杀与普通订单模型；不改 `inventories`、`flash_sale_*`、`cart_items`；
- 不做商品/SKU 数据迁移与规格回填；不导入任何遗留 SQL；
- 不在本任务改 API/DTO/OpenAPI；不引入微服务化字段（如租户、分库键）；
- 不实现拼团、砍价、分销、提现、微信生态、页面装修、积分/会员。

---

## 附：审查方法与可复现证据

```bash
git rev-parse HEAD                      # 3491a9a...
ls internal/migrations/sql              # 24 up + 1 down
grep -v '^--' internal/migrations/sql/*.up.sql   # DDL 事实来源
grep -rhoE 'Model\("[a-z_]+"\)' internal/logic/*/ | sort -u   # 域→表映射
grep -rn "g.DB().Transaction" internal/logic | wc -l          # 36 处事务
grep -rn "sold < total_stock" internal/logic/flashsale        # 秒杀扣减闸门
```

未连接实际 MySQL（`127.0.0.1:3306` 不可达），因此"当前 MySQL 表结构"以 **Migration DDL 为准**，
未与在线库做 schema diff → 该项标记 **NOT VERIFIED against live DB**。
