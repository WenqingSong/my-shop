# B1-03-04 历史 SQL 数据迁移与字典初始化交付报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-03-04：历史 SQL 数据迁移与字典初始化 |
| 分支 | `feat/p2-backend` |
| 基线 SHA | `e123c483d674bb4f66ac09f874506d7fee4abc3f`（B1-03 复审与迁移规划报告） |
| 实现/文档提交 | `86b79e1e8f61907a662ccc6e8d44d57aebf3b087` |
| 本报告提交 | 本次提交（推送后为最终 HEAD） |
| 验证库 | 真实 MySQL `8.0.46`，独立库 `my_shop_b10304_verify`；历史源库 `my_shop_staging`（只读） |
| 当前 Schema 版本 | `20261110000014`，`dirty=0` |
| 执行日期 | 2026-11-10 |

## 1. 任务目标与范围

把 `tmp/shopping.sql` 中有价值的历史业务数据迁移到当前 Go 单体后端的目标库，并初始化地区字典：

- 商品分类、有效商品、被历史业务引用的软删除商品、商品规格、SKU、SKU 级库存；
- 用户资料、有效地址、3938 条省市区字典；
- 普通订单、订单明细、订单状态流水（按流水重建状态）；
- 优惠券模板、发行批次、适用范围、用户券及权益快照；
- 钱包余额期初快照、积分期初快照与对应期初流水；
- 商品评价、用户收藏。

**不做**：B1-04 业务 API、DTO/Controller/路由/OpenAPI、秒杀业务改动、微服务、完整旧库复制、
充值与提现流水入账、第三方凭据/Secret/装修 JSON 迁移、既有 Migration 修改、业务 Schema 新增。

## 2. 开工前核对

| 项 | 结果 |
| --- | --- |
| 当前分支 | `feat/p2-backend`（未合并 main、未 force push） |
| 基线 HEAD | `e123c483d674bb4f66ac09f874506d7fee4abc3f` |
| 工作区 | 仅本次修改/新增文件；`docs/phase2/B1-03_*.md` 三个分析文件为已跟踪状态，本次未修改、未纳入提交 |
| 历史 SQL | `tmp/shopping.sql`（5.6 MB，83 张表），已导入隔离 staging 库，全程只读 |
| 目标库 | 新建独立验证库 `my_shop_b10304_verify`，未连接生产库 |
| 测试数据库 | 集成测试夹具克隆目标库结构到 `my_shop_b10304_test_<pid>`，不清理配置库数据 |

## 3. 源数据库统计（真实 SQL 查询）

| 数据域 | 源表 | 源总量 | 可迁移 | 说明 |
| --- | --- | ---: | ---: | --- |
| 商品分类 | `store_category` | 57 | 57 | 含软删除分类（目标置禁用） |
| 商品 | `store_product` | 226 | 55 | 54 个有效 + 1 个被订单引用的软删除商品（id=226） |
| SKU | `store_product_attr_value` | 111 | 109 行 | 另有 1 个单规格派生默认 SKU；2 行属于未被引用的软删商品 |
| 库存 | `store_product_attr_value.stock` | 111 | 109 行 | 每 SKU 一条，商品级/秒杀/拼团库存不入库 |
| 规格组/值 | `store_product_attr_result`（`store_product_attr` 回退） | 52 / 112 | 52 / 112 | 52 个规格组、112 个规格值 |
| 用户 | `eshop_user` | 20 | 20 | 全部 `is_del=0`；`user` 表 5 行是管理端框架账号，排除 |
| 地址 | `user_address` | 36 | 23 | 5 条软删除 + 8 条引用不存在用户 |
| 订单 | `store_order` | 51 | 51 | 全部 `pay_type='yue'` |
| 订单明细 | `store_order_cart_info` | 56 | 56 | 2 条源 SKU `unique` 已变更，按商品+规格名回退 |
| 状态流水 | `store_order_status` | 132 | 132 | 6 种 `change_type` |
| 券模板 | `store_coupon` | 2 | 2 | — |
| 发行批次 | `store_coupon_issue` | 12 | 12 | 含 9 条 `is_del=1`，历史用户券仍引用 |
| 用户券 | `store_coupon_user` | 8 | 8 | 6 张已使用、2 张未使用 |
| 领取记录 | `store_coupon_issue_user` | 8 | 8 | 用于定位用户券批次 |
| 评价 | `store_product_reply` | 13 | 13 | 全部 `is_del=0` |
| 收藏/足迹 | `store_product_relation` | 104 | 5 | `collect` 24 条（5 有效/19 取消），`foot` 80 条不迁 |
| 钱包 | `eshop_user.now_money > 0` | 20 用户 | 14 | 期初余额快照 |
| 积分 | `eshop_user.integral > 0` | 20 用户 | 7 | 期初积分快照 |
| 地区 | `system_city` | 3938 | 3938 | level 0/1/2 → 1/2/3，ID 复用 `city_id` |

初始任务书中的“用户 20 / 地址 36 / 收藏 104 / 订单 51”等基准经重新统计成立；
但“可迁移”口径需按关联完整性过滤（地址 23、收藏 5、SKU 109+1 派生、商品 55），
未为了匹配数量而制造或丢弃数据。

## 4. 实际导入数量

来自 `my-shop legacy-migrate import` 首次在空验证库执行的真实输出：

| 数据域 | 源总量 | 符合条件 | 成功导入 | 跳过 | 失败 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 地区字典 | 3938 | 3938 | 3938 | 0 | 0 |
| 商品分类 | 57 | 57 | 57 | 0 | 0 |
| 商品 | 226 | 55 | 55 | 171 | 0 |
| SKU | 111 | 109 | 110 | 2 | 0 |
| 库存 | 111 | 109 | 110 | 2 | 0 |
| 商品规格组 | 52 | 52 | 52 | 0 | 0 |
| 商品规格值 | 112 | 112 | 112 | 0 | 0 |
| SKU 规格关联 | 120 | 120 | 120 | 0 | 0 |
| 用户 | 20 | 20 | 20 | 0 | 0 |
| 用户地址 | 36 | 23 | 23 | 13 | 0 |
| 优惠券模板 | 2 | 2 | 2 | 0 | 0 |
| 优惠券批次 | 12 | 12 | 12 | 0 | 0 |
| 优惠券适用范围 | 1 | 1 | 1 | 0 | 0 |
| 用户优惠券 | 8 | 8 | 8 | 0 | 0 |
| 订单 | 51 | 51 | 51 | 0 | 0 |
| 订单明细 | 56 | 56 | 56 | 0 | 0 |
| 订单状态流水 | 132 | 132 | 132 | 0 | 0 |
| 钱包账户 | 20 | 14 | 14 | 0 | 0 |
| 钱包期初流水 | 20 | 14 | 14 | 0 | 0 |
| 积分账户 | 20 | 7 | 7 | 0 | 0 |
| 积分期初流水 | 20 | 7 | 7 | 0 | 0 |
| 商品评价 | 13 | 13 | 13 | 0 | 0 |
| 用户收藏 | 104 | 5 | 5 | 99 | 0 |

说明：

- SKU/库存“成功 110 > 符合条件 109”，因为其中 1 个是单规格商品（`store_product.id=21`）派生的
  `默认` SKU，无历史主键、不写 `legacy_id_map.sku`；
- SKU/库存“跳过 2”是未被任何历史业务引用的软删除商品（id=24）的 2 个 SKU；
- 目标库最终行数：`products=55`、`skus=110`、`inventories=110`、`product_specs=52`、
  `product_spec_values=112`、`sku_spec_values=120`、`users=20`、`addresses=23`、`orders=51`、
  `order_items=56`、`order_status_logs=132`、`coupon_templates=2`、`coupon_batches=12`、
  `coupon_template_scopes=1`、`user_coupons=8`、`wallet_accounts=14`、`wallet_transactions=14`、
  `user_points_accounts=7`、`user_points_logs=7`、`reviews=13`、`favorites=5`。

## 5. 重要数据转换规则

### 5.1 商品规格与指纹

- 规格定义以 `store_product_attr_result.result` 为准：`attr[].value` → `product_specs.name`，
  `attr[].detail[]` → `product_spec_values.value`；`store_product_attr` 仅回退。
- SKU 与规格值按 `attr_result.value[].detail` 的“值多重集合”匹配 `store_product_attr_value.sku`
  的逗号分量，**不按位置**（源库同一商品存在 `128G,16G` 与 `16G,500G` 两种顺序）。
- `spec_signature` 按 `sku.md §2.4`：`spec_id` 升序、`"spec_id:spec_value_id"` 逗号连接、小写 MD5。
  真实库校验：所有非 NULL 指纹与 `sku_spec_values` 重算结果一致（0 条不一致）。
- 无法唯一匹配组合的 SKU 不写规格关联，不猜测；`spec_type=0` 商品保持无规格。

### 5.2 软删除商品

不简单过滤全部 172 个软删除商品：先收集订单明细/评价/有效收藏/券范围引用的商品 ID，
被引用的软删除商品以 `off_shelf` 保留、SKU `disabled`。本批命中 1 个（id=226），
历史订单明细 0 悬空。

### 5.3 订单状态重建

`store_order.status` 不可信（本批 `status` 0/1/2/3 = 35/3/5/8，而 `paid=1` 有 37 单）。
按 `store_order_status` 时间线重建，`create` 固定起点 10，其余事件按 `(change_time,id)` 升序、
状态只前进：

| 事件 | 目标状态 |
| --- | --- |
| `yshop_create_order` | 10 |
| `pay_success` | 20 |
| `delivery_goods` | 30 |
| `user_take_delivery` | 40 |
| `order_eval` | 50 |
| `remove_order` | 不改变状态，写 `change_type='delete'` 审计流水 |

**关键修正**：`remove_order` 是“用户删除订单列表记录”，不是取消。源库 5 条 `remove_order`
对应订单均已支付/履约/完成；若按初始审计报告映射为 60 会把已完成订单错误改成已取消。
最终状态分布：10=14、20=23、30=2、40=3、50=9、60=0、70=0。

### 5.4 金额

- 全部 `decimal(8,2) 元 → 整数分` 定点转换，无 float64；
- `total_price → original_amount`、`pay_price → total_amount`、
  `coupon_price + deduction_price → discount_amount`、`total_postage → shipping_fee`；
- 不按当前商品价格重算历史订单；校验 `original - discount = total`
  与 `Σ(order_items.price × quantity) = original_amount` 均为 0 条异常；
- 金额汇总：`original=13,210,500 分`、`discount=200 分`、`total=13,210,300 分`、`shipping=0`。

### 5.5 支付方式

源库 51/51 `pay_type='yue'` → 目标 `pay_type='balance'`；最终仍待支付的 14 单 `pay_type=''`。
`transaction_no` 保持空串，未虚构微信/支付宝交易记录。

### 5.6 用户密码

旧密码为无盐 MD5。全部 20 个用户写入 `!legacy-md5-forced-reset` 占位值；该值不是 bcrypt 哈希，
IAM `bcrypt.CompareHashAndPassword` 必然失败，旧密码无法通过正常登录。目标库中
`password_hash REGEXP '^[0-9a-f]{32}$'` 计数为 0；迁移过程未读取/输出原始密码或哈希。

### 5.7 钱包与积分

只迁当前余额/积分快照，不迁 `user_bill` / `user_recharge` 历史流水：

- 14 个余额 >0 用户：`wallet_accounts.balance` + 1 条 `wallet_transactions`
  （`direction=1`、`before=0`、`after=balance`、`idempotency_key=legacy:wallet_opening:<user_id>`）；
- 7 个积分 >0 用户：`user_points_accounts.points` + 1 条 `user_points_logs`
  （`change_type=legacy_opening`、`before=0`、`after=points`）；
- 校验：`Σ wallet.balance = 16,693,420 分`、`Σ points = 3,090`，账户余额与期初流水一致，
  二次执行未重复入账。

### 5.8 地区字典

`regions.id = system_city.city_id`（不重新编号），`parent_id` 直接复用源 `parent_id`
（源库父子关系以 `city_id` 引用），`level = 源 level + 1`（0/1/2 → 1/2/3）。
导入前校验省级 parent=0、父存在、父层级=当前层级-1、名称非空且 ≤64 字符；
3938 条全部通过，父子层级异常 0 条。

### 5.9 优惠券

- 模板 2 条；批次 12 条（含 `is_del=1`，历史用户券仍引用）；范围 1 条（模板 type=1 → 商品 2）；
- 用户券 8 条：`batch_id` 由 `store_coupon_issue_user(uid, 模板, 领取时间)` 精确定位；
  `claim_no` 按用户+模板领取顺序生成（uid=1 同模板 3 张 → 1/2/3）；
  权益快照 `coupon_title/coupon_price/use_min_price/end_time` 保留；
  `status` 0→10、1→30、2→10（过期由 end_time 判定）；
- 已知源数据问题：6 张已使用券中仅 2 张能在 `store_order.coupon_id` 找到对应订单，
  其余 4 张保持 `order_id=NULL`，不猜测关联。

### 5.10 评价与收藏

- 评价 13 条：用户/商品/订单明细全部映射，`product_score → rating`、`pics` 逗号拆分 → `image_urls` JSON；
- 收藏仅 `type='collect' AND is_del=0` 5 条；`foot`（足迹）80 条与 `is_del=1`（取消收藏）19 条不迁。

## 6. 迁移工具实现

复用并扩展 `internal/logic/legacymigrate`，未另起系统：

| 文件 | 职责 |
| --- | --- |
| `engine.go` | `Engine.Run`、固定域顺序、`legacy_id_map` 加载、MySQL 错误判定 |
| `domain.go` | `domainRunner`、`domainContext`、ID 映射、跳过/失败/警告、Dry Run 占位 |
| `report.go` | `DomainStat` 统计表、逐条跳过/降级/失败明细、旧字段兼容 |
| `domain_product.go` | 分类、商品、规格、SKU、库存 |
| `domain_user.go` | 地区、用户、地址 |
| `domain_order.go` | 订单、明细、状态流水重建、金额 |
| `domain_coupon.go` | 券模板/批次/范围/用户券、评价、收藏 |
| `domain_wallet.go` | 钱包/积分期初 |
| `db.go` | staging/target DSN；`ParseTime=true` 保留历史时间 |
| `engine_test.go` | 真实 MySQL 测试夹具：克隆目标库结构到进程号隔离库 |
| `b10304_test.go` | 商品规格/软删引用、用户密码/地址/钱包积分、订单状态/券/评价/收藏与幂等测试 |

CLI 复用既有 `my-shop legacy-migrate <dry-run|import>`：Dry Run 只统计不写库；Apply 写库并输出
每域源总量/符合条件/成功/跳过/失败；失败明细含对象 ID 与原因。

## 7. 幂等验证结果

在同一个干净验证库连续执行两次 `legacy-migrate import`：

- 第一次：23 个数据域全部成功，`失败=0`；
- 第二次：所有域 `成功=0`、`失败=0`，`legacy_id_map` 仍为 411 行；
- 关键表行数两次一致：`products=55`、`skus=110`、`orders=51`、`order_items=56`、
  `order_status_logs=132`、`wallet_transactions=14`、`user_points_logs=7`、`reviews=13`、`favorites=5`；
- 未出现重复资金/积分入账，未通过删除目标数据实现“重新迁移”。

Dry Run 在干净库执行后，目标库 `products/users/regions/legacy_id_map` 均为 0 行，证明未写入。

## 8. 数据完整性验证（真实 MySQL 查询）

| 验证对象 | 验证内容 | 结果 |
| --- | --- | --- |
| 商品 | 商品 → 分类外键/关联 | 0 条悬空 |
| SKU | SKU → 商品关联 | 0 条悬空 |
| 库存 | 每 SKU 恰一条库存 | 0 缺失、0 孤儿 |
| 规格 | `spec_signature` 与 `sku_spec_values` 重算一致 | 0 条不一致 |
| 规格 | 规格值归属/孤儿关联 | 0 条 |
| 用户 | 用户密码非 MD5、资料/手机号迁移 | 20 用户，MD5 计数 0 |
| 地址 | 用户关联、默认地址唯一 | 23 条，默认地址每用户 ≤1 |
| 地区 | 父子存在、层级一致 | 3938 条，0 异常 |
| 订单 | 订单/明细关联、金额等式、明细金额合计 | 0 异常 |
| 订单状态 | 流水重建后状态分布 | 10=14、20=23、30=2、40=3、50=9 |
| 优惠券 | 模板/批次/范围/用户券关联、批次与 claim_no | 0 悬空；8 张券批次/序号完整 |
| 钱包 | 余额快照 = 期初流水累计 | 0 不一致，合计 16,693,420 分 |
| 积分 | 积分快照 = 期初记录 | 7 账户，合计 3,090 |
| 收藏 | 用户/商品关联、唯一 | 0 悬空、0 重复 |
| 评价 | 用户/商品/订单明细关联 | 0 悬空 |
| 映射 | `legacy_id_map` 唯一、覆盖迁移对象 | 411 行，0 重复；products 无映射缺口 |

已知例外（源数据问题，不是迁移失败）：4 张已使用用户券无源订单关联；8 个 `is_del=1` 订单无
`remove_order` 流水；1 个批次 `remain_count` 与领取记录数不完全一致（保留源值）。

## 9. 测试命令、退出码与结果

| 命令 | 退出码 | 结果 |
| --- | ---: | --- |
| `go build ./...` | 0 | 全仓库编译通过 |
| `go vet ./internal/logic/legacymigrate/` | 0 | 无告警 |
| `DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop go test ./internal/logic/legacymigrate/... -count=1` | 0 | `ok ... 25.322s`，含 3 个新域集成测试 + 既有引擎测试 + 纯映射单测 |
| `my-shop migrate up`（验证库） | 0 | Schema 到 `20261110000014`，`dirty=0` |
| `my-shop legacy-migrate dry-run` | 0 | 输出统计，目标库 0 写入 |
| `my-shop legacy-migrate import`（第一次） | 0 | 23 域成功，失败 0 |
| `my-shop legacy-migrate import`（第二次） | 0 | 全部 0 新增、失败 0（幂等） |

测试使用真实 MySQL 约束，未用 Mock 替代数据库；集成测试夹具不删除配置库数据。

## 10. NOT_RUN / NOT_VERIFIED

| 项 | 状态 | 原因 |
| --- | --- | --- |
| 连接生产库验证 | NOT_RUN | 任务禁止连接生产库 |
| 全仓库 `go test ./...` | NOT_RUN | 本任务只改 `legacymigrate` 与文档；已执行该包全部测试与全仓库编译 |
| Java Service 层 `remove_order` 实现 | NOT_VERIFIED | `tmp/eshop-backend-java` 源码快照缺少订单/优惠券 Service 实现；由 `/order/del` 接口语义与真实数据判定为“删除订单记录”，非取消 |
| `user_bill` / `user_recharge` 历史流水入账 | NOT_RUN（设计禁止） | 只迁余额/积分快照，流水仅归档证据 |
| 充值、提现、秒杀、拼团、砍价参与记录 | NOT_RUN（明确排除） | 源数据为 0 或不在本次范围 |
| 微信/支付宝/七牛/邮箱凭据 | NOT_RUN（安全禁止） | 不读取、不迁移 |
| 4 张已使用券的订单关联 | NOT_VERIFIED | 源库无对应 `coupon_id`，保持 NULL，不猜测 |
| 8 个 `is_del=1` 订单的删除流水 | NOT_VERIFIED | 源库仅有 5 条 `remove_order`，不伪造缺失流水 |
| `store_coupon_issue.remain_count` 与领取记录一致性 | NOT_VERIFIED | 源库自身不一致，按 `total_count - remain_count` 保留源口径 |

## 11. Git 与文件范围

- 基线：`e123c483d674bb4f66ac09f874506d7fee4abc3f`；
- 实现/文档提交：`86b79e1e8f61907a662ccc6e8d44d57aebf3b087`；
- 报告提交：本次提交；
- 分支：`feat/p2-backend`，未合并 main、未 force push；
- 未修改/未提交 `docs/phase2/B1-03_FINAL_MIGRATION_AUDIT.md`、`B1-03_REFERENCE_REAUDIT.md`、
  `B1-03_SCHEMA_DISCOVERY.md`（均为既有已跟踪文件，本次无改动）；
- 未修改既有 Migration、DTO/Controller/路由/OpenAPI、秒杀业务、`.env`/凭据文件；
- 未提交 `tmp/`、staging 转储、验证库数据（均由 `.gitignore` 排除）。

## 12. 并行子 Agent 实际执行情况

- 实际并行调用 **3 个 `code-explorer` 只读子 Agent**，分别核对：
  1) 订单状态/金额/`remove_order`/`coupon_id` 语义；
  2) 优惠券模板/批次/用户券/收藏类型语义；
  3) 商品规格拼接顺序/`attr_result` 权威性/用户资金与 MD5 密码算法。
- 三个子 Agent 均发现 `tmp/eshop-backend-java` 源码不完整（缺少 Service 实现），相关结论已标注
  `UNVERIFIED`，并由真实 SQL 数据与 REST 接口语义交叉验证，未把推测写成事实。
- **环境限制**：当前 `Task` 工具仅提供只读 `code-explorer` 子 Agent，无法创建可写代码的
  DeepSeek 4.1 编码子 Agent。因此本任务的代码实现、测试、整合与验证由主 Agent 完成；
  未伪造“五个编码 Agent 并行开发”的记录。上述 3 个只读 Agent 为真实调用。

## 13. 剩余问题与后续建议

1. 4 张已使用用户券缺少订单关联、8 个删除订单缺少 `remove_order` 流水：建议 B1-04 以只读报表展示，
   不做自动补写。
2. 历史未支付订单的 `expire_at` 已过期，若 B1-04 启用超时扫描，会将其懒取消（无资金影响）；
   如需保留“历史待支付”展示，应在业务层按 `data_source` 或时间范围区分（本任务未新增列）。
3. `addresses` 仍为自由文本、无 `region_id`；地区字典已就绪，结构化地址接入属后续任务。
4. `skus.image_url` 仅保留源 SKU 图（37 条非空），空串继续回退商品主图；未复制主图填充。
5. 历史订单 `pay_type='balance'` 是语义映射；当前后端尚无余额支付实现，B1-04 应沿用该值或统一常量。
