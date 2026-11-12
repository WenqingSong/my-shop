# 历史数据迁移设计（Legacy Data Migration）

本文面向项目接手者，说明历史电商数据（`tmp/shopping.sql`，导入隔离 staging 库）迁移到当前 Go 单体后端
目标库的架构、映射规则、状态重建、幂等与失败语义。事实来源为实际 Migration、Go 代码与真实 MySQL 验证，
不以设计文档或历史分析报告中的估算数量为准。

## 1. 职责与边界

- 独立 Data Migration CLI：`my-shop legacy-migrate <dry-run|import>`，复用 `internal/cmd` 子命令。
- **Schema Migration 与 Data Migration 严格区分**：本任务不新增/修改任何 Migration，不改目标表结构；
  只向 `legacy_id_map` 写入旧 ID → 新 ID 映射。目标结构以 `internal/migrations/sql/` 的实际 Migration 为准。
- 源库为隔离 staging schema（默认 `my_shop_staging`，配置键 `legacy.staging.name`）；目标库为
  `database.default.name`。二者必须不同名（`OpenConnections` 强制校验）。
- 不迁移管理端框架、第三方凭据、Secret、页面装修 JSON、充值/提现流水、无业务数据的秒杀/拼团/砍价参与记录。
- 不实现 B1-04 业务 API，不改秒杀业务，不复制完整旧数据库。

## 2. 数据模型

### 2.1 `legacy_id_map`（目标库，migration `20261001000023`）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `source` | VARCHAR(64) | 固定 `shopping.sql` |
| `entity_type` | VARCHAR(16) | 见下表 |
| `old_id` | BIGINT UNSIGNED | 历史旧表主键 |
| `new_id` | BIGINT UNSIGNED | 目标新主键 |
| `created_at`/`updated_at` | DATETIME | 审计 |

唯一约束 `uk_source_entity_old (source, entity_type, old_id)` 是幂等事实源；去重不依赖名称等自然键。
B1-03-04 扩展的 `entity_type` 取值（只扩展数据，不改表结构）：

| entity_type | old_id 来源 | new_id 目标 |
| --- | --- | --- |
| `category` | `store_category.id` | `categories.id` |
| `product` | `store_product.id` | `products.id` |
| `sku` | `store_product_attr_value.id` | `skus.id` |
| `user` | `eshop_user.uid` | `users.id` |
| `address` | `user_address.id` | `addresses.id` |
| `order` | `store_order.id` | `orders.id` |
| `order_item` | `store_order_cart_info.id` | `order_items.id` |
| `coupon_template` | `store_coupon.id` | `coupon_templates.id` |
| `coupon_batch` | `store_coupon_issue.id` | `coupon_batches.id` |
| `user_coupon` | `store_coupon_user.id` | `user_coupons.id` |
| `review` | `store_product_reply.id` | `reviews.id` |
| `favorite` | `store_product_relation.id` | `favorites.id` |

不写入映射的对象：`regions`（直接复用 `city_id`）、派生默认 SKU（无独立历史主键）、
`wallet_accounts`/`user_points_accounts`（以 `uk_user_id` 幂等）、`wallet_transactions`/`user_points_logs`
（以唯一幂等键幂等）。

## 3. 执行顺序与事务边界

固定顺序（代码见 `domainRunners`）：

```text
regions → categories → products(+specs+skus+inventories) → users → addresses
→ coupons(templates+batches+scopes+user_coupons) → orders(+items+status_logs)
→ wallets → points → reviews → favorites
```

依赖原因：

- 商品依赖分类映射；订单/评价/收藏/券范围依赖用户与商品映射；
- 用户券先于订单写入（`orders.user_coupon_id` 需要 `user_coupon` 映射）；
  订单写入后在同一事务回填 `user_coupons.order_id`；
- 评价依赖订单明细映射；收藏依赖用户与商品映射。

事务边界：

| 域 | 事务粒度 |
| --- | --- |
| 分类 | 全部分类一个事务（父先子后） |
| 商品 | 单商品一个事务（商品+图册+规格+SKU+规格关联+库存+映射） |
| 用户/地址 | 单行一个事务 |
| 优惠券模板/批次/用户券 | 单行一个事务 |
| 订单 | 单订单一个事务（订单+明细+状态流水+映射+券回填） |
| 评价/收藏 | 单行一个事务 |
| 钱包/积分 | 单用户一个事务（账户+期初流水） |
| 地区 | 全部有效地区一个事务（字典，整体成功/失败） |

Dry Run 只解析、映射、校验与统计，不写目标库；依赖域使用内存占位映射（`plannedID=-1`）继续校验。

## 4. 表映射总览

| 历史表 | 目标表 | 处理 |
| --- | --- | --- |
| `store_category` | `categories` | 全量（含软删除→禁用） |
| `store_product` | `products` + `product_images` | 有效商品 + 被历史业务引用的软删除商品 |
| `store_product_attr` / `store_product_attr_result` | `product_specs` + `product_spec_values` | `attr_result` 为主，`attr` 为回退 |
| `store_product_attr_value` | `skus` + `sku_spec_values` + `inventories` | 每行一个 SKU；SKU 级库存 |
| `eshop_user` | `users` | 资料迁移；密码强制重置 |
| `user_address` | `addresses` | 仅有效且用户已迁移的地址 |
| `system_city` | `regions` | 全量 3938，ID 复用；同一份数据亦以 `internal/regiondata/regions_v1.tsv` 内嵌分发，可用 `my-shop region-seed import` 独立初始化（B1-04-FIX-02，见 `docs/dev/region-data-initialization.md`） |
| `store_order` | `orders` | 状态由流水重建；金额元→分 |
| `store_order_cart_info` | `order_items` | 解析 `cart_info` JSON 快照 |
| `store_order_status` | `order_status_logs` | 全量 132 条，保留时间线 |
| `store_coupon` | `coupon_templates` | 2 条 |
| `store_coupon_issue` | `coupon_batches` | 12 条（含 `is_del=1`，历史用户券仍引用） |
| `store_coupon.product_id` | `coupon_template_scopes` | 仅 `type=1` 且能准确映射商品 |
| `store_coupon_issue_user` | 批次定位依据 | 不单独建表；用于用户券 `batch_id` |
| `store_coupon_user` | `user_coupons` | 8 条，含权益快照与 `claim_no` |
| `store_product_reply` | `reviews` | 13 条 |
| `store_product_relation` | `favorites` | 仅 `type='collect' AND is_del=0` |
| `eshop_user.now_money` | `wallet_accounts` + `wallet_transactions` | 只迁期初余额快照 |
| `eshop_user.integral` | `user_points_accounts` + `user_points_logs` | 只迁期初积分快照 |
| `user_bill` / `user_recharge` | 不迁移 | 归档证据，禁止重复入账 |

明确不迁移：`store_cart`、`user_bill`、`user_recharge`、`user_extract`、`store_seckill`、`store_pink`、
`store_bargain_user*`、管理端框架表、支付/微信/七牛/邮箱凭据、页面装修 JSON。

## 5. 商品、分类、规格、SKU 与库存

### 5.1 分类

`pid → parent_id`、`cate_name → name`、`sort → sort`；`is_del=1 → status=0`、`is_del=0 → status=1`。
父缺失/父被跳过/父链成环时降级 `parent_id=0` 并记警告；同父级重名逐条跳过。
源库 `sort` 可为 NULL，按 0 处理。

### 5.2 商品范围

- `is_del=0` 的 54 个有效商品；
- 以及被 `store_order_cart_info` / `store_product_reply` / 有效收藏 / `store_coupon.product_id`
  引用的软删除商品（本次为 `store_product.id=226` 1 个）。

软删除但仍被引用的商品以 `off_shelf` 保留，其 SKU `status=disabled`；其余 171 个软删除商品不迁移。
商品金额 `decimal(8,2) 元` 全程定点转整数分，不使用 float64。`image → main_image`、
`slider_image` 逗号拆分 → `product_images`，`description → detail`。

### 5.3 规格

权威来源为 `store_product_attr_result.result`：

```json
{"attr":[{"value":"颜色","detail":["黑色","白色"]},{"value":"容量","detail":["64G","128G"]}],
 "value":[{"detail":{"颜色":"黑色","容量":"64G"}},{"detail":{"颜色":"白色","容量":"128G"}}]}
```

- `attr[].value → product_specs.name`，`attr[].detail[] → product_spec_values.value`（保持顺序写 `sort`）；
- `value[].detail → SKU 的规格名→值组合`；
- `store_product_attr` 仅在 `attr_result` 缺失/不可解析时回退（`attr_name` + 逗号分隔 `attr_values`）；
- `store_product.spec_type=0` 的商品不写规格关联，保持历史无规格兼容。

SKU 与规格值的匹配**不使用 `sku` 字符串位置**：源数据同一商品内 SKU 顺序不一致（例如同一商品同时存在
`128G,16G` 与 `16G,500G`）。实现按 `attr_result.value[].detail` 的「值多重集合」与 `sku` 逗号分量匹配；
无法唯一匹配时不写规格关联并记警告，绝不猜测。

### 5.4 SKU 与指纹

- `store_product_attr_value` 每行一个 SKU：`sku → name`、`price → price`（分）、`image → image_url`
  （空串保持空串，读取时回退商品主图）；
- `spec_signature` 按 `sku.md §2.4`：`spec_id` 升序、`"spec_id:spec_value_id"` 逗号连接、小写 MD5；
  无规格关联写 NULL；
- 单规格/无明细商品使用 `默认` SKU（商品价格/库存）；多规格缺明细时不补造 SKU，商品降级 `off_shelf`；
- SKU 状态 `enabled` 当且仅当商品 `on_shelf`。

### 5.5 库存

每个 SKU 一条 `inventories`，`quantity` 取该 SKU 行 `stock`；商品级 `store_product.stock`、
`seckill_stock`、`pink_stock` 一律不进入库存，绝不叠加。

## 6. 用户、地址与地区

### 6.1 用户

- `uid → legacy_id_map.user.old_id`；`username → users.username`（为空时 `legacy_user_<uid>`）；
  `nickname/avatar/phone` 映射到 B1-03-01 新增列；`status` 1/0 保持；保留 `create_time`。
- **密码安全**：旧密码为无盐 MD5（32 位小写 hex），禁止迁移为可登录密码。统一写入
  `!legacy-md5-forced-reset` 占位值；该值不是 bcrypt 哈希，IAM `bcrypt.CompareHashAndPassword` 必然失败，
  旧密码无法通过正常登录。迁移过程不读取/输出原始密码或哈希，用户须走后续重置密码流程。
- `username`/`phone` 与目标库冲突时逐条跳过并记录，不覆盖既有用户。

### 6.2 地址

- `user_address.uid → users.id`；`real_name/phone/province/city/district/detail` 原样映射；
- 仅 `is_del=0` 且用户已迁移的地址；每个用户最多一条 `is_default=1`（重复默认降级为 0 并记警告）；
- `city_id` 保留在源库证据中，当前 `addresses` 无 `region_id`，不新增字段、不建外键。

### 6.3 地区字典

- `system_city.city_id → regions.id`（显式写入，不重新编号）；`parent_id` 直接复用
  （源库父子关系确实以 `city_id` 引用，非自增 `id`）；`name → name`；
- `level` 0/1/2（省/市/区）→ 目标 1/2/3；
- 导入前校验：省级 `parent_id=0`、父存在、父层级 = 当前层级 - 1、名称非空且 ≤64 字符；
  不满足逐条跳过并记录。`regions` 不建自引用外键，层级一致性由导入方保证。

## 7. 订单、明细与状态流水

### 7.1 状态重建（不直接信任 `store_order.status`）

源库 `store_order.status` 与 `paid`/流水不一致（例如 `paid=1` 但 `status=0`），最终状态以
`store_order_status.change_type` 时间线重建：

| change_type | 语义 | 目标状态 |
| --- | --- | --- |
| `yshop_create_order` | 下单 | 10 待支付 |
| `pay_success` | 支付成功 | 20 已支付 |
| `delivery_goods` | 发货 | 30 已发货 |
| `user_take_delivery` | 收货 | 40 已收货 |
| `order_eval` | 评价 | 50 已完成 |
| `remove_order` | **用户删除订单列表记录** | 不改变状态；写 `change_type='delete'` 审计流水 |
| 未知类型 | — | 不改变状态，保留原类型并记警告 |
| `cancel_order` / `refund_success` | 若出现 | 60 / 70 |

规则：

- `create` 固定为起点 10，其余事件按 `(change_time, id)` 升序；状态只前进，不因重复/回退事件降级；
- 源库存在 `pay` 流水时间早于 `create` 流水时间的现象（提交前写日志），仍先处理 create；
- `remove_order` 不是取消：本次数据 5 条 `remove_order` 对应的订单均已支付/履约/完成，
  若按历史分析报告映射为 60 会把已完成订单错误改成已取消，因此只保留删除审计；
- 全部 132 条源流水逐条写入 `order_status_logs`，`from_status/to_status` 按上述时间线生成，
  `operator_type=1`（用户），`operator_id` 为新用户 ID，`remark=change_message`。

### 7.2 金额

- 全部金额定点 `元 → 整数分`，不使用 float64；
- `total_price → original_amount`、`pay_price → total_amount`、
  `coupon_price + deduction_price → discount_amount`、`total_postage → shipping_fee`；
- 不按当前商品价格重算历史订单；本次验证 `original - discount = total` 与
  `Σ(order_items.price × quantity) = original_amount` 全部成立；
- 历史订单全部 `pay_type='yue'`（余额）→ 目标 `pay_type='balance'`；最终仍待支付的订单 `pay_type=''`；
  `transaction_no` 保持空串，不虚构微信/支付宝交易；`paid_at` 取 `pay_time` 或支付流水时间。

### 7.3 明细

- 解析 `store_order_cart_info.cart_info`（`json.Decoder.UseNumber()`，避免 float 金额）；
  `productInfo.attrInfo.unique` 优先匹配 SKU，失败时回退 `(product_id, attrInfo.sku)`；
- `cartNum → quantity`、`truePrice`（缺失回退 `attrInfo.price`）→ `price`、`storeName → product_name`、
  `image → product_main_image`、`attrInfo.sku → sku_name` 且作为 `spec_text` 快照；
- 任一明细无法映射时整单跳过，不写残缺订单；本次 56 条明细全部映射
  （其中 2 条源 SKU `unique` 已变更，通过商品 + 规格名回退命中）。

### 7.4 收货与履约

- `real_name/user_phone` → 收货人快照；`user_address` 按空白拆四段
  （省/市/区/详细），不足四段时整体作为 `detail`，不编造；
- `delivery_type/delivery_name/delivery_sn` 原样映射（本批源数据为空）；
- `expire_at = create_time + order.pay_timeout`（默认 900 秒）；`address_id` 为 NULL（源订单无地址 ID）。

## 8. 优惠券

- `store_coupon → coupon_templates`：`title/name`、`coupon_price/discount_amount`、
  `use_min_price/threshold_amount`；`total_quantity = Σ 批次 total_count`（为 0 时回退已发放数）；
  `issued_quantity = 用户券数`；`use_start_time = create_time`，
  `use_end_time = create_time + coupon_time 天`；领取窗口取批次最早开始/最晚结束；
  `per_user_limit=0`（源库允许同一用户同模板多次领取，实例：uid=1 持 3 张）；
- `store_coupon_issue → coupon_batches`：全量 12 条（含 `is_del=1`，历史用户券仍引用）；
  `total_quantity=total_count`、`issued_quantity=total_count-remain_count`（负数归零）；
  `is_del=1` 或 `status<>1 → status=0`；`cname` 在目标无对应列，不迁移；
- 适用范围：仅 `type=1` 且 `product_id` 可解析到已迁移商品时写 `coupon_template_scopes`；
  本次 1 条（模板 7 → 商品 2）；
- `store_coupon_user → user_coupons`：
  - `batch_id` 由 `store_coupon_issue_user(uid, issue.cid, create_time)` 精确匹配，失败回退最近不晚于领取时间的批次；
  - `claim_no` 按 `(user_id, template_id, received_at, id)` 顺序从 1 递增，满足 `uk_user_template_claim`；
  - 权益快照 `coupon_title/coupon_price/use_min_price/end_time` 原样保留，不随模板漂移；
  - `status`：0→10 可用、1→30 已使用、2→10（由 `end_time` 判定过期）；
  - `order_id` 由订单的 `coupon_id`（源库指向 `store_coupon_user.id`）回填；
- 本批 6 张已使用券中仅 2 张能在源订单找到 `coupon_id`，其余 4 张保持 `order_id=NULL`（不猜测关联）。

## 9. 钱包与积分期初

- 只迁移 `eshop_user.now_money`、`eshop_user.integral` 当前快照，**不迁移** `user_bill` / `user_recharge` 历史流水；
- 钱包：`now_money > 0` 的用户创建 `wallet_accounts(balance)`，同事务写一条
  `wallet_transactions(tx_type='legacy_opening', direction=1, amount=balance, balance_before=0,
  balance_after=balance, biz_type='legacy_opening', biz_id=旧 uid, idempotency_key='legacy:wallet_opening:<新 user_id>')`；
- 积分：`integral > 0` 的用户创建 `user_points_accounts(points)`，同事务写一条
  `user_points_logs(change_type='legacy_opening', change_amount=points, before=0, after=points,
  idempotency_key='legacy:points_opening:<新 user_id>')`；
- 历史账单仅作归档证据，不进入新账本，避免余额/积分重复累计；
- 积分按整数处理，`decimal(8,2)` 若含非零小数则跳过并记录（本次全部为 `.00`）。

## 10. 评价与收藏

- `store_product_reply → reviews`：`uid → user_id`、`oid+product_id → order_items.id`（走 `legacy_id_map.order_item`）、
  `product_score → rating`、`comment → content`、`pics` 逗号拆分 → `image_urls` JSON；
  仅 `is_del=0`；无法定位订单明细时跳过，不伪造；
- `store_product_relation → favorites`：仅 `type='collect' AND is_del=0`；`foot` 是浏览足迹、`is_del=1`
  是取消收藏，均不迁移；按 `(user_id, product_id)` 去重。

## 11. 幂等与重复执行

- 每个实体先查 `legacy_id_map`（进程内一次加载），已迁移则跳过，不重复插入；
- 业务行与映射行同事务提交；中途失败只回滚当前批次，已提交批次保留，可安全重跑；
- 钱包/积分没有 legacy 主键，以 `uk_user_id`（账户）与唯一幂等键（流水）判断；
- 地区以 `regions.id` 判断已存在；优惠券范围以唯一键冲突判定；
- 不用「删除目标数据」实现重新迁移；源库只读，目标库不执行 `DROP/TRUNCATE`。

## 12. 失败处理与回滚边界

- 分类/地区整批事务：任一行非预期失败则整批回滚并返回错误，重跑从零开始；
- 商品/订单/优惠券/用户/地址/评价/收藏/钱包/积分：单行事务，失败记入 `Failures` 与 `DomainStat.Failed`，
  继续处理后续行；
- 唯一键冲突（同父级分类重名、username/phone 冲突、评价重复、收藏重复）按业务跳过并记录原因；
- 外键/引用缺失（分类/用户/商品/SKU/订单明细）跳过并记录，不关闭目标库外键校验（INV-004）；
- 不做跨域分布式事务；依赖域先执行，映射是跨域事实源。

## 13. 安全边界

- 原始 SQL 只进入隔离 staging 库；`.gitignore`/`.dockerignore` 阻止进入 Git/镜像；
- 不连接生产库；验证使用独立 `my_shop_b10304_verify` 库；
- 不输出原始密码、MD5 哈希、手机号、Token、第三方凭据；报告只含数量与对象 ID；
- 迁移工具不新增 HTTP 接口、不新增错误码域。

## 14. 验证与测试入口

- 纯映射单元测试：`go test ./internal/logic/legacymigrate -run 'TestSpecSignature|TestYuanToCents|TestSplitLegacyAddress'`；
- 真实 MySQL 集成测试：`DATABASE_DEFAULT_PASS=... DATABASE_DEFAULT_NAME=<测试库> go test ./internal/logic/legacymigrate`；
  测试夹具会克隆目标库结构到进程号隔离的测试库，不清理配置库数据；
- 端到端：`my-shop migrate up` → `my-shop legacy-migrate dry-run` → `my-shop legacy-migrate import`（执行两次验证幂等）；
- 验收数量与完整性证据见 `docs/reports/B1-03-04-legacy-data.md`。

## 15. 已知数据问题与限制

- `store_order.status` 与 `paid`/流水不一致，本文只以流水重建，`is_del=1` 的 13 单中仅 5 单有
  `remove_order` 流水，其余 8 单缺少删除事件（保留订单，不伪造流水）；
- 6 张已使用用户券中 4 张在源订单无 `coupon_id`，保持 `order_id=NULL`；
- 8 条源地址引用不存在的用户（uid 42/50/53/54/59/61/71），跳过；5 条地址已软删除；
- 172 个软删除商品中仅 1 个仍被历史订单引用；2 个 SKU 属于未被引用的软删除商品，不迁移；
- `store_product_attr_result.value[].sku` 存在空值/缺失，因此 SKU 名以 `store_product_attr_value.sku` 为准；
- 旧 Java 源码快照缺少订单/优惠券 Service 实现，`remove_order` 的最终语义由接口（`/order/del` 删除订单）
  与真实数据（删除后订单仍为已完成）共同确认；未在源码层确证的部分已在交付报告标注。
