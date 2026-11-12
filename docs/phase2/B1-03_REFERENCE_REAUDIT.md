# B1-03-01-R 真实电商参考项目恢复后专项审查

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-03-01-R（只读审查） |
| 所属分支 | `feat/p2-backend` |
| 审查基线 | `3491a9abecfc02bb897d64e38a987038cd6d28f3`（与 B1-03-01 相同，无推进） |
| 工作区 | 仅 `?? docs/phase2/B1-03_SCHEMA_DISCOVERY.md`（上一轮报告未提交，未被覆盖） |
| 已有报告 | `docs/phase2/B1-03_SCHEMA_DISCOVERY.md` 存在（39,921 字节），本次**未修改** |
| 代码/Migration/OpenAPI 改动 | **无** |

**证据等级标记（全文通用）**

| 标记 | 含义 |
|---|---|
| 【事实】 | 有 DDL / 源码 / 命令输出直接支持 |
| 【推断】 | 由列名、表名、行业惯例推导，未执行验证 |
| 【未验证】 | 缺少源码或运行环境，无法确认 |

---

## 1. 恢复资料清单

```text
$ find tmp -type f
tmp/shopping.sql          # 5.5 MB

$ find / -maxdepth 8 -name "*.java"（排除 IDE 目录）
（无结果）
$ find / -maxdepth 6 -name "pom.xml" -o -name "build.gradle"
（无结果）
$ find / -maxdepth 5 -type d -name "uniapp" -o -name "vue*" -o -name "src"（排除系统目录）
（无前端源码目录）
```

| # | 资料 | 状态 | 证据 |
|---|---|---|---|
| 1 | 商城 SQL 备份 `tmp/shopping.sql` | **AVAILABLE** | 5.5 MB，83 张 `CREATE TABLE`，16,692 条 `INSERT` |
| 2 | SpringBoot 后端（`tmp/` 下 Java 工程） | **NOT AVAILABLE** | `tmp/` 只有 `shopping.sql`；全盘无 `.java`/`pom.xml`/`build.gradle`/`*.jar` |
| 3 | PC 用户商城前端 | **NOT AVAILABLE** | 全盘无前端工程（无 `package.json` 业务工程、无 `uniapp`/Vue 源码目录） |
| 4 | APP 用户商城前端 | **NOT AVAILABLE** | 同上 |
| 5 | 其他业务资料 | 无 | `tmp/` 中仅 1 个文件 |

> **结论：3 个项目中只有 SQL 恢复，Java 后端与两个前端仍缺失。**
> 任务书要求的 Controller/Service/Mapper 审查、PC/APP 页面与 API 对应、三者契约一致性
> **无法执行**，本报告对应章节一律标记 NOT AVAILABLE，不做任何猜测性补写。
> 已恢复的 SQL 本身是强证据：它能确证**原系统的数据模型与业务域边界**，但不能证明任何代码实现。

**数据安全处理**：全程只用 `awk` 提取 `CREATE TABLE` 段与 `grep -c` 计数；
未读取、未复制、未输出任何 `INSERT` 行内容；未连接数据库、未执行导入；SQL 仍在 `.gitignore` 覆盖范围内
（`git check-ignore` → `.gitignore:10:/tmp/`），不会进入 Git。

---

## 2. 三个项目的实际技术栈

| 项目 | 技术栈 | 依据 |
|---|---|---|
| 商城 SQL | MySQL **5.7.17**；Navicat Data Transfer 导出（2021-09-27）；字符集混合 `utf8` / `utf8mb4`、`utf8_general_ci` | 【事实】SQL 头部注释 `Source Server Version : 50717`；每张表 `ENGINE=InnoDB` |
| SpringBoot 后端 | **NOT AVAILABLE**（无法确认 Spring Boot 版本、模块划分、ORM、依赖） | 无源码 |
| PC 前端 | **NOT AVAILABLE** | 无源码 |
| APP 前端 | **NOT AVAILABLE** | 无源码 |

对 SQL 本身的判断【推断】：表结构由 **eladmin 风格管理端**（`user`/`role`/`menu`/`dept`/`dict`/
`quartz_*`/`gen_config`/`column_config`/`monitor_server`/`log`）与 **CRMEB 风格商城**
（`store_*`/`user_*`/`eshop_user`/`system_city`/`wechat_*`）拼合而成；管理端 11 个物理外键、
商城表 0 个物理外键，二者风格不同，支持"两套代码合库"的判断。

---

## 3. SpringBoot 模块完整性

**NOT AVAILABLE。**

无法确认：Maven 模块划分、依赖树、是否存在多模块（api/service/dao/common）、
`Controller→Service→Mapper` 是否完整、是否存在空实现（TODO/占位）。

唯一可做的**结构性**判断【事实】：SQL 覆盖的业务域数量远超"四个核心模块"，详见 §9。
任务书提到"此前已发现 Java 项目缺少四个核心模块"，**本次无法复核该结论**；
同时必须澄清：**SQL 中出现了拼团/砍价/积分/会员相关表，不等于 Java 侧有对应实现**
（表可能是遗留数据或未启用模块，`store_seckill`、`store_pink` 的 INSERT 行数为 0，可作为"未被运营使用"的旁证【推断】）。

---

## 4. Controller 与 API 业务域清单

**NOT AVAILABLE（无源码，无法列出任何 Controller 或接口路径）。**

可提供的替代信息：由 83 张表反推的**业务域清单**（不代表接口存在），见 §9 分类；
以及 SQL 中能体现的"业务域边界"。

> 计数口径：下表"有（N 条 INSERT）"来自 `grep -c "^INSERT INTO ..."`，只说明**该表有数据写入**，
> 不等于精确行数（导出文件存在多值插入），也不能说明线上真实数据量。

| 业务域 | SQL 证据（表名） | 是否有真实业务数据 |
|---|---|---|
| 用户与认证 | `eshop_user`（username/password/login_type/wx_profile） | 有（20 条用户） |
| 用户资料与地址 | `eshop_user`(nickname/avatar/phone/real_name)、`user_address` | 有（36 条地址） |
| 商品与分类 | `store_product`、`store_category`、`store_product_relation`、`store_product_reply` | 有（226 商品 / 57 分类条 INSERT） |
| SKU 与规格 | `store_product_attr`、`store_product_attr_value`、`store_product_attr_result`、`store_product_rule` | 有（72 规格项 / 111 SKU 行 / 226 矩阵条 INSERT） |
| 购物车 | `store_cart`（type/combination_id/seckill_id/bargain_id） | 有（78 条 INSERT） |
| 订单 | `store_order`（55 列）、`store_order_cart_info`、`store_order_status` | 有（51 单 / 56 快照 / 132 状态记录条 INSERT） |
| 支付 | 订单内嵌 `paid`/`pay_time`/`pay_type`；`user_recharge` 充值单 | 有（325 条充值） |
| 退款 | 订单内嵌 `refund_status`/`refund_price`/`refund_reason*`（无独立退款表） | 【未验证】数量未统计 |
| 库存 | `store_product.stock`（SPU）、`store_product_attr_value.stock`（SKU）、`store_seckill.stock`、`store_combination.stock`、`store_bargain.stock` | 有（分散在 5 处） |
| 优惠券 | `store_coupon`（模板）、`store_coupon_issue`（领取活动）、`store_coupon_issue_user`、`store_coupon_user`（券实例） | 有（2 模板 / 8 用户券条 INSERT） |
| 秒杀 | `store_seckill`（29 列）+ `store_product.is_seckill` + `store_order.seckill_id` | 表存在，**0 条 INSERT** |
| 拼团 | `store_combination`、`store_pink`、`store_cart.combination_id`、`store_order.combination_id/pink_id` | 活动 4 条、团实例 0 条 INSERT |
| 砍价 | `store_bargain`、`store_bargain_user`、`store_bargain_user_help` | 活动 1 条、参与 0 条 INSERT |
| 积分 | `eshop_user.integral`、`store_order.gain_integral/use_integral/pay_integral`、`user_bill` | 有（429 条账单） |
| 会员 | `system_user_level`、`user_level`、`eshop_user.level`、`store_product.vip_price` | 等级定义 6 条 INSERT、用户等级 0 条（未使用） |
| 分销 | `eshop_user.spread_uid/is_promoter/brokerage_price`、`user_extract`（提现） | 【推断】 |
| 充值与余额 | `user_recharge`、`eshop_user.now_money`、`user_bill` | 有 |
| 物流 | `express`（快递字典）、订单内嵌 `delivery_sn/delivery_name/delivery_type/delivery_id` | 有 |
| 运费 | `shipping_templates`、`_region`、`_free`、`store_product.temp_id/postage` | 有（5 个模板） |
| 地区 | `system_city`（`city_id/level/parent_id/area_code/merger_name/lng/lat`） | 有（3,938 条 INSERT） |
| 门店核销 | `system_store`、`system_store_staff`、`store_order.store_id/verify_code/shipping_type` | 【推断】 |
| 微信生态 | `wechat_live*`、`wechat_menu`、`wechat_reply`、`wechat_media`、`wechat_template` | 【推断】 |
| 页面装修 | `store_canvas`（`terminal`/`json`） | 【推断】 |
| 内容运营 | `article`、`material`、`material_group`、`picture` | 【推断】 |

---

## 5. 业务调用链完整性分析

**全部业务域：UNVERIFIABLE（缺 Java 源码）。**

按任务书的五级标准逐项标注：

| 业务域 | 判定 | 说明 |
|---|---|---|
| 用户与认证 / 资料 / 地址 | UNVERIFIABLE | 表结构完整（`eshop_user` 33 列、`user_address` 16 列），无代码可追踪 |
| 商品 / 分类 / 评价 / 收藏点赞 | UNVERIFIABLE | `store_product` 41 列、`store_product_reply` 16 列 |
| SKU 与规格矩阵 | UNVERIFIABLE | 三表 + JSON 矩阵齐备，见 §11.1 |
| 购物车 | UNVERIFIABLE | 表支持 4 种 `type` |
| 订单 / 状态流水 | UNVERIFIABLE | `store_order` 55 列 + `store_order_status` |
| 支付 / 退款 | UNVERIFIABLE | 支付信息内嵌订单；无独立退款表 |
| 库存 | UNVERIFIABLE | 5 处库存字段分散，无流水表 |
| 优惠券 | UNVERIFIABLE | 模板/领取活动/用户券三层 |
| 秒杀 | UNVERIFIABLE | 表结构完整，数据为 0 |
| 拼团 / 砍价 / 积分 / 会员 / 分销 / 充值 / 提现 | UNVERIFIABLE | 表结构存在，实现未知 |
| 物流 / 运费 / 地区 / 门店 / 微信 / 装修 | UNVERIFIABLE | 表结构存在，实现未知 |

> 因此本报告**不声称任何"COMPLETE/PARTIAL"结论**。若需要该判定，必须先提供 Java 源码。

---

## 6. PC 用户端功能清单

**NOT AVAILABLE。**

无前端源码，无法整理页面清单、路由、请求字段。上一轮 B1-03-01 的判断（"前端仓库不可用"）在本次复盘中
**仍然成立**，且范围扩大到 Java 端。

---

## 7. APP 用户端功能清单

**NOT AVAILABLE。** 同 §6。

---

## 8. PC 与 APP 的功能差异

**NOT AVAILABLE。**

唯一可用的间接线索是 SQL：`store_canvas.terminal`（装修页面按终端区分）、
`app_version`（iOS/Android 版本与更新包）。仅能说明"系统区分终端"，**不能**说明两端的页面与接口差异。

---

## 9. SQL 全部业务表清单（83 张）

**数据库**：MySQL 5.7.17；**引擎**：全部 InnoDB；**表数**：83（`grep -c "^CREATE TABLE"` = 83）；
**视图/触发器/存储过程**：0（`grep -cE "^CREATE (VIEW|TRIGGER|PROCEDURE|FUNCTION)"` = 0）；
**物理外键**：11 个，全部位于管理端表（`dict_detail→dict`、`user→dept/job/avatar`、
`role→dept`、`roles_menus→role/menu`、`roles_depts→role/dept`、`users_roles→user/role`），
**商城表 0 个外键**。

### 9.1 管理端框架与基础设施（29 张）

`user`（管理员，15 列）、`user_avatar`、`role`、`menu`、`dept`、`job`、`roles_menus`、`roles_depts`、
`users_roles`、`dict`、`dict_detail`、`quartz_job`、`quartz_log`、`gen_config`、`gen_test`、
`column_config`、`monitor_server`、`log`、`local_storage`、`system_attachment`、`system_config`、
`system_group_data`、`verification_code`、`app_version`、`visits`、`alipay_config`、`qiniu_config`、
`qiniu_content`、`email_config`

> 与商城无关，属于 RuoYi/eladmin 脚手架；`alipay_config.private_key/public_key` 属敏感配置，
> 本报告不引用其内容。**不建议迁移任何一张**。

### 9.2 商品域（9 张）

| 表 | 列 | 用途（DDL 证据） |
|---|---|---|
| `store_product` | 41 | SPU：`store_name`、`image`、`slider_image`(2000)、`cate_id`(varchar 逗号串)、`price/ot_price/vip_price/postage`、`stock`(SPU 级)、`sales`、`spec_type`、`temp_id`(运费模板)、`is_postage`、`give_integral`、`code_path`、`is_del` |
| `store_product_attr` | 4 | 规格项：`product_id`、`attr_name`、`attr_values`(varchar 256，逗号分隔) |
| `store_product_attr_value` | 20 | **SKU 行**：`product_id`、`sku`(规格名)、`unique`(规格指纹)、`price/ot_price/cost`、`stock/sales`、`image`、`bar_code`、`weight/volume`、`pink_price/pink_stock`、`seckill_price/seckill_stock`、`integral`；`UNIQUE(unique, sku)` |
| `store_product_attr_result` | 4 | 规格组合矩阵：`product_id`、`result`(text)、`change_time` |
| `store_product_rule` | 6 | 规格规则：`rule_name`、`rule_value`(json) |
| `store_category` | 9 | 分类树：`pid`、`cate_name`、`pic`、`is_show` |
| `store_product_relation` | 8 | 收藏/点赞二合一：`uid`+`product_id`+`type`+`category`，`UNIQUE(uid,product_id,type,category)` |
| `store_product_reply` | 16 | 评价：`product_score`/`service_score`/`comment`/`pics`/`merchant_reply_content` |
| `store_visit` | 9 | 商品浏览分析：`product_id`/`cate_id`/`type`/`uid`/`count` |

### 9.3 购物车与订单（4 张）

| 表 | 列 | 用途 |
|---|---|---|
| `store_cart` | 14 | `uid`、`type`(product/seckill/combination/bargain)、`product_id`、`product_attr_unique`、`cart_num`、`is_pay`/`is_del`/`is_new`、`combination_id`/`seckill_id`/`bargain_id`；索引 `uid`、`product_id`、`(uid,is_pay)`、`(uid,is_del)`、`(uid,is_new)`、`type` |
| `store_order` | 55 | 见 §10.3 |
| `store_order_cart_info` | 6 | 订单商品快照：`oid`、`cart_id`、`product_id`、`cart_info`(text)、`unique`；`UNIQUE(oid, unique)` |
| `store_order_status` | 5 | 订单状态流水：`oid`、`change_type`、`change_message`、`change_time` |

### 9.4 营销域（10 张）

| 表 | 列 | 用途 |
|---|---|---|
| `store_coupon` | 13 | 券模板：`title`、`coupon_price`、`use_min_price`、`coupon_time`(有效天数)、`integral`(积分兑换)、`product_id`(适用范围，逗号串)、`type`、`status` |
| `store_coupon_issue` | 13 | **前台领取活动**：`cid`、`start_time/end_time`、`total_count/remain_count`、`is_permanent`、`status` |
| `store_coupon_issue_user` | 6 | 领取记录：`UNIQUE(uid, issue_coupon_id)`（每人每活动一次） |
| `store_coupon_user` | 14 | 用户券实例：`cid`、`uid`、`coupon_title`/`coupon_price`/`use_min_price`/`end_time`（**快照**）、`use_time`、`type`、`status`、`is_fail` |
| `store_seckill` | 29 | 秒杀商品：`product_id`、`price`、`stock`、`sales`、`start_time/stop_time`(date)、`time_id`(场次)、`status`、`is_show`、`spec_type`、`temp_id` |
| `store_combination` | 30 | 拼团活动：`product_id`、`people`、`price`、`stock`、`sales`、`start_time/stop_time`、`effective_time` |
| `store_pink` | 19 | 拼团实例：`uid`、`order_id`、`cid`、`pid`、`people`、`price`、`status`、`stop_time`、`k_id` |
| `store_bargain` | 32 | 砍价活动：`product_id`、`stock`、`min_price`、`bargain_max/min_price`、`bargain_num`、`rule` |
| `store_bargain_user` | 10 | 用户参与砍价 |
| `store_bargain_user_help` | 8 | 好友助力记录 |

### 9.5 用户 / 会员 / 资金（13 张）

| 表 | 列 | 用途 |
|---|---|---|
| `eshop_user` | 33 | 会员主表：`username/password`、`nickname/avatar/phone/real_name/birthday/card_id`、`now_money`(余额)、`brokerage_price`(佣金)、`integral`(积分)、`sign_num`、`status`、`level`、`spread_uid/spread_time/is_promoter`(分销)、`pay_count`、`user_type`、`login_type`、`wx_profile`(json)、`group_id` |
| `user_address` | 16 | 地址：`real_name/phone`、`province/city/city_id/district`、`detail`、`post_code`、`longitude/latitude`、`is_default`；**只有 city_id，无 province_id/district_id** |
| `user_bill` | 14 | 资金流水：`pm`(收支)、`category/type/link_id`、`number`、`balance`（余额快照）、`mark` |
| `user_recharge` | 13 | 充值单：`order_id`(唯一)、`price`、`give_price`、`recharge_type`、`paid`、`pay_time` |
| `user_extract` | 17 | 提现：`extract_type`(bank/alipay/wechat)、`extract_price`、`balance`、`status`、`fail_msg` |
| `user_level` | 14 | 用户等级记录：`uid`、`level_id`、`valid_time`、`discount` |
| `system_user_level` | 16 | 等级定义：`money`(升级门槛)、`discount`、`is_pay`、`grade`、`valid_date` |
| `user_sign` | 8 | 签到记录 |
| `system_user_task` | 13 | 等级任务定义（`task_type`/`number`） |
| `user_task_finish` | 7 | 任务完成记录 |
| `user_group` | 2 | 用户分组 |
| `store_customer` | 8 | 订单通知接收用户（`open_id`） |
| `user_enter` | 18 | 商户入驻申请（**平台化能力**） |

### 9.6 物流 / 运费 / 地区 / 门店（7 张）

| 表 | 列 | 用途 |
|---|---|---|
| `express` | 8 | 快递公司字典：`code`/`name`/`is_show`（4 条 INSERT，含多值插入） |
| `shipping_templates` | 10 | 运费模板：`type`、`region_info`(text)、`appoint`/`appoint_info` |
| `shipping_templates_region` | 10 | 区域计费：`province_id`/`city_id`、`first`/`first_price`、`continues`/`continue_price` |
| `shipping_templates_free` | 8 | 包邮规则：`number`/`price`/`type` |
| `system_city` | 10 | 地区字典：`city_id`/`level`/`parent_id`/`area_code`/`merger_name`/`lng/lat`（3,938 条 INSERT） |
| `system_store` | 19 | 门店自提：`name`/`phone`/`address`/`latitude/longitude`/营业时间 |
| `system_store_staff` | 13 | 门店店员（核销员）：`store_id`、`verify_status` |

### 9.7 内容与装修 / 微信（11 张）

`article`（22 列，含 `is_hot`/`is_banner`/`product_id`）、`material`、`material_group`、`picture`、
`store_canvas`（`terminal`/`json`/`type`）、`wechat_live`、`wechat_live_goods`、`wechat_media`、
`wechat_menu`、`wechat_reply`、`wechat_template`

---

## 10. 核心数据库模型及关联关系

### 10.1 商品 ↔ 规格 ↔ SKU

```text
store_category(1) ──< store_product(N)                      # cate_id 是 varchar 逗号串（多分类），无外键
store_product(1) ──< store_product_attr(N)                  # 规格项：attr_name + attr_values(逗号串)
store_product(1) ──< store_product_attr_value(N)            # SKU 行：sku 名 + unique 指纹 + price/stock
store_product(1) ──  store_product_attr_result(1)           # 规格组合矩阵 JSON（供选择器）
store_product(1) ──< store_product_rule(N)                  # 规格/参数规则（json）
```

关键点【事实】：

1. SKU **不是独立主数据**，而是"商品属性值"表的一行；业务键是 `(unique, sku)` 复合唯一，
   其中 `unique` 是商品级规格组合指纹字符串（购物车用 `product_attr_unique` 指向它）。
2. 规格值没有独立主键：`store_product_attr.attr_values` 是逗号分隔字符串，
   无法单独引用"红色"这个值；规格矩阵另存 JSON（`attr_result.result`，结构【未验证】因未读数据）。
3. SKU 自带 `weight/volume/cost/bar_code/pink_price/seckill_price/integral`，
   即**多种营销价与物流属性都挂在 SKU 行上**。

### 10.2 购物车

```text
store_cart(uid, type, product_id, product_attr_unique, cart_num,
           combination_id, seckill_id, bargain_id)
```

- 无唯一约束（同一用户+SKU 可产生多行【推断：由应用层合并】）。
- `type` 区分普通/秒杀/拼团/砍价，营销 id 列共存于一张表。
- 无价格快照列（下单时重算价格）。

### 10.3 订单

```text
store_order(id, order_id, uid, 收货人快照..., cart_id,
            total_price, total_postage, pay_price, pay_postage,
            freight_price, coupon_id, coupon_price, deduction_price,
            paid, pay_time, pay_type, status,
            refund_status, refund_price, refund_reason*,
            delivery_sn, delivery_name, delivery_type, delivery_id,
            gain_integral, use_integral, pay_integral, back_integral,
            mark, unique, combination_id, pink_id, seckill_id, bargain_id,
            verify_code, store_id, shipping_type)
store_order(1) ──< store_order_cart_info(N)   # UNIQUE(oid, unique)，cart_info 为 JSON 文本快照
store_order(1) ──< store_order_status(N)      # 状态流水
```

- `order_id` 是业务订单号（varchar），`id` 才是自增主键；`UNIQUE(order_id, uid)` + `UNIQUE(unique)`。
- **支付信息内嵌订单**（`paid`/`pay_time`/`pay_type`），无独立支付流水表。
- **退款信息内嵌订单**（`refund_status`/`refund_price`/`refund_reason*`），无独立退款表。
- 营销来源用 `seckill_id`/`combination_id`/`pink_id`/`bargain_id` 四个列表达，
  **秒杀订单与普通订单同表**。
- 无订单状态机枚举表（`status` tinyint，取值语义需代码确认【未验证】）。

### 10.4 优惠券

```text
store_coupon(1) ──< store_coupon_issue(N)          # 领取活动（限量/限时/永久）
store_coupon_issue(1) ──< store_coupon_issue_user(N)  # UNIQUE(uid, issue_coupon_id)
store_coupon(1) ──< store_coupon_user(N)           # 用户券实例（快照 title/price/end_time）
```

### 10.5 库存（5 处分散）

| 位置 | 粒度 | 说明 |
|---|---|---|
| `store_product.stock` | SPU | 单规格商品使用 |
| `store_product_attr_value.stock` | SKU | 多规格商品使用 |
| `store_seckill.stock` | 秒杀商品 | 秒杀独立库存 |
| `store_combination.stock` | 拼团活动 | 拼团独立库存 |
| `store_bargain.stock` | 砍价活动 | 砍价独立库存 |

**无任何库存流水表**（对比 SurgeCart 有 `inventory_logs`）。

### 10.6 地址 ↔ 地区 ↔ 运费 ↔ 物流

```text
user_address.city_id ──> system_city.city_id      # 仅市，无省/区 id；省市区另有文本列
shipping_templates(1) ──< shipping_templates_region(N)   # temp_id + province_id + city_id
shipping_templates(1) ──< shipping_templates_free(N)     # 满件/满额包邮
store_product.temp_id ──> shipping_templates.id          # 商品绑定运费模板
store_order.delivery_id/delivery_name/delivery_sn ──> express.code/name   # 运单快照
```

### 10.7 金额 / 时间 / 状态 / 软删除

| 维度 | 原项目做法【事实】 | 评价 |
|---|---|---|
| 金额 | `decimal(8,2)`，单位**元**（`price`/`pay_price`/`refund_price`/`now_money`/`integral` 都是 decimal） | 需浮点/定点运算，易与"分"混用 |
| 时间 | 以 `datetime` 为主，部分表用 `int` 时间戳（`user_enter.add_time`、`store_visit.add_time`、`user_level.valid_time`、`store_seckill.start_time` 是 **date**） | 类型不统一 |
| 状态 | `tinyint`（`status`/`paid`/`is_show`/`is_del`），无状态机表 | 语义只能靠代码/字典 |
| 软删除 | **51 张表带 `is_del`**（含商品/订单/购物车/优惠券/用户） | 查询需处处过滤，易泄漏已删数据 |
| 唯一键 | 商城表唯一约束很少（如 `store_order.order_id+uid`、`attr_value(unique,sku)`、`coupon_issue_user`） | 幂等主要靠应用层 |
| 幂等 | `store_order.unique` char(32) | 与 SurgeCart 的 `idempotency_key+request_hash` 等价思路 |

---

## 11. 原项目真实业务规则（从 DDL 可确证/可推断）

> 说明：以下规则来自字段与索引，**不代表 Java 实现细节**；标注【推断】的条目需源码确认。

### 11.1 商品规格如何定义【事实（结构）】

1. `store_product.spec_type` 区分单/多规格（0/1，语义【推断】）。
2. 规格项存 `store_product_attr(attr_name, attr_values)`，值以逗号串保存。
3. 运营在后台编辑"规格矩阵"，结果 JSON 落到 `store_product_attr_result.result`；矩阵的
   每个单元格对应 `store_product_attr_value` 的一行。
4. SKU 身份 = `(unique, sku)`：`unique` 是商品内规格组合指纹，`sku` 是拼接后的展示名。

### 11.2 SKU 如何生成与关联【事实（结构）】

- SKU 行包含 `price`/`stock`/`image`/`bar_code`/`weight`/`volume`/`cost`，
  以及营销价 `pink_price`/`seckill_price`/`integral`。
- 购物车通过 `product_attr_unique` 关联 SKU；订单明细通过 `cart_info` JSON 快照保存下单时的
  SKU 信息（不依赖 SKU 行继续存在）。

### 11.3 购物车如何保存 SKU【事实】

- `store_cart(product_id, product_attr_unique, cart_num, type, 营销 id...)`；
  无价格快照，无唯一约束；下单后靠 `is_pay`/`is_del` 标记清理。

### 11.4 订单如何创建【事实（结构）+ 推断】

- 订单主表存**收货人与地址文本快照**（`real_name`/`user_phone`/`user_address`），
  不存 `address_id` → 地址删除不影响订单（与 SurgeCart 的列快照一致）。
- 商品明细存 `store_order_cart_info.cart_info`（JSON 文本）+ `cart_id` 关联原购物车行。
- `unique` 列（char 32）承担下单幂等/防重复提交。

### 11.5 订单金额如何计算【事实（字段语义）+ 推断公式】

字段：`total_price`（商品总额）、`total_postage`（运费）、`pay_price`（实付）、
`pay_postage`（实付运费）、`freight_price`、`coupon_price`（券抵扣）、`deduction_price`（积分抵扣）。

推断公式：`pay_price = total_price + total_postage - coupon_price - deduction_price`，
`pay_postage` 在包邮时为 0。**需源码确认顺序与取整规则**。

### 11.6 优惠券如何领取与使用【事实（结构）】

- 模板（`store_coupon`）定义面额/门槛/有效天数/适用范围/积分兑换；
- 领取活动（`store_coupon_issue`）定义发放量与时间窗，`remain_count` 做库存扣减；
- 领取记录 `store_coupon_issue_user` 用 `UNIQUE(uid, issue_coupon_id)` 做"每人一次"；
- 用户券 `store_coupon_user` **快照** `coupon_title/coupon_price/use_min_price/end_time`，
  模板后续改动不影响已领券（这一点 SurgeCart 当前缺失）。
- 状态：`status`（0 未使用/1 已使用/2 已过期【推断】）+ `is_fail`。

### 11.7 库存如何扣减与恢复【未验证（无代码）】

DDL 只能证明"库存分散在 5 处、无流水表"；扣减/恢复是否用条件更新、是否加锁、
是否与订单同事务，**无法从 SQL 判断**。

### 11.8 退款如何处理【事实（结构）】

- 无独立退款表；订单内嵌 `refund_status`、`refund_price`、`refund_reason_wap`、
  `refund_reason_wap_img`、`refund_reason_time`、`refund_reason`（管理员原因），
  变更轨迹落在 `store_order_status`。

### 11.9 秒杀如何与普通订单区分【事实（结构）】

- 秒杀商品独立表 `store_seckill`（独立价/库存/时间窗），商品侧用 `store_product.is_seckill` 标记，
  购物车用 `type='seckill'` + `seckill_id`，订单用 `store_order.seckill_id` 关联；
- **订单仍写主订单表**，无独立秒杀订单表；秒杀库存与普通库存互不换算。

---

## 12. 原项目与 Go 后端能力对照

> Go 侧证据均为当前提交 `3491a9a` 的代码/DDL/契约，前一轮已核对，本次抽查复核。

| 业务域 | 原项目能力（SQL 证据） | 当前 Go 能力（代码/DDL 证据） | 差距 | 建议 |
|---|---|---|---|---|
| 用户与认证 | `eshop_user`：username/password/手机号/微信登录/状态 | `users(id,username,password_hash,status,auth_epoch)` + JWT + refresh family 血缘（`refresh_tokens`） | Go 认证更强（Token Family/撤销代际）；**资料字段缺失** | MUST 补资料列 |
| 用户资料 | nickname/avatar/phone/real_name/birthday/card_id | 仅 `username`（`api/iam/v1/iam.go:53-56`） | 无昵称/头像/手机号 | MUST（手机号/昵称/头像） |
| 地址 | `user_address`：省市区文本+city_id+邮编+经纬度+默认 | `addresses`：省市区文本+默认唯一（生成列）+FK | Go 默认地址约束更强；**无地区码** | SHOULD 地区码 |
| 商品 | `store_product`：SPU+图集+多分类+销量+浏览+积分+运费模板 | `products`+`product_images`+`view_count` | 无销量、无运费模板、无软删除 | SHOULD |
| SKU/规格 | 规格项+SKU 行+矩阵 JSON+SKU 价格/库存/重量/条码 | `skus(id,product_id,name,price,status)` | **无规格模型**（最大差距） | MUST |
| 购物车 | `store_cart` 支持 4 种营销类型、无唯一约束 | `cart_items`：uk(user,sku)+价格快照+勾选 | Go 唯一约束/快照更好；缺营销类型 | 不迁移营销类型 |
| 订单 | 55 列：运费/券/积分/退款/物流/门店/营销来源全内嵌 | `orders`+`order_items`：三金额快照、地址快照、状态机、幂等 | 缺运费/支付方式/物流/状态流水 | MUST 部分 |
| 支付 | 订单内嵌 `paid/pay_time/pay_type`；`user_recharge` | 仅 `orders.paid_at`（Mock） | 缺支付方式/流水号 | MUST 订单列；payments 表 SHOULD |
| 退款 | 订单内嵌 `refund_*`，无独立表 | `refund_requests`+`refund_audits`+`active_key` 唯一 | **Go 更规范** | 保持 |
| 库存 | 5 处分散库存、无流水 | `inventories`(1:1 sku)+`inventory_logs`+条件扣减 | **Go 更规范** | 保持，不引入 SPU 库存 |
| 优惠券 | 模板+领取活动+用户券快照+适用范围 | `coupon_templates`+`user_coupons`（无快照/无范围） | 缺快照/范围/领取活动层 | 快照 MUST；范围 SHOULD |
| 物流 | `express` 字典 + 订单 `delivery_*` | 仅 `orders.shipped_at` | 缺承运商/运单号 | MUST（订单列+字典） |
| 运费 | 模板+区域计费+满免（3 表） | 无 | 缺运费模型 | `orders.shipping_fee` MUST；模板 SHOULD |
| 地区 | `system_city` 3,938 条 INSERT 的三级码表 | 自由文本 | 缺码表 | SHOULD |
| 秒杀 | `store_seckill` 独立库存，订单并入主表 | Redis+Lua+Outbox+Kafka，独立 `flash_sale_*` 表 | **Go 更完整** | 保持不动 |
| 拼团/砍价 | `store_combination`/`store_pink`/`store_bargain*` | 无 | 缺失 | DEFERRED |
| 积分 | `integral`+积分抵扣+积分兑换券 | 无 | 缺失 | DEFERRED |
| 会员 | 等级折扣 `system_user_level` | 无 | 缺失 | DEFERRED |
| 分销/提现 | `spread_uid`/`brokerage_price`/`user_extract` | 无 | 缺失 | DEFERRED |
| 充值/余额 | `user_recharge`+`user_bill`+`now_money` | 无 | 缺失 | DEFERRED |
| 门店核销 | `system_store(_staff)`+`verify_code` | 无 | 缺失 | DEFERRED |
| 微信生态 | 6 张 `wechat_*` | 无 | 缺失 | DEFERRED |
| 页面装修 | `store_canvas` | 无 | 缺失 | DEFERRED |
| 内容运营 | `article`/`material`/`picture` | `articles`/`banners`/`recommend_positions` | Go 已有等价能力 | 保持 |
| 评价 | `store_product_reply`（双评分+商家回复） | `reviews`（单评分+图片） | 缺商家回复/服务评分 | OPTIONAL |
| 收藏/点赞/足迹 | `store_product_relation`(type)+`store_visit` | `favorites`/`product_likes`/`views` 计数 | 缺浏览明细 | OPTIONAL |

---

## 13. B1-03-01 原有模型方案复核

逐项标注（CONFIRMED / REVISED / REJECTED / NEW / DEFERRED）：

| # | 原 B1-03-01 建议 | 判定 | 依据与调整 |
|---|---|---|---|
| 13.1 | `product_specs` + `product_spec_values` + `sku_spec_values` 三表规范化 | **CONFIRMED** | 原项目同样是"规格项 + SKU 行 + 矩阵"三段式（`store_product_attr`/`attr_value`/`attr_result`）；规范化表比逗号串更可维护 |
| 13.2 | `skus.spec_signature` + `UNIQUE(product_id, spec_signature)` | **CONFIRMED** | 对应原项目 `store_product_attr_value.unique`（商品级规格指纹）；唯一索引是原项目已有思路 |
| 13.3 | `skus.image_url` | **CONFIRMED** | 原项目 SKU 行有 `image` |
| 13.4 | `skus.sku_code`（SHOULD） | **REVISED** | 原项目 SKU 有 `bar_code`(条码) 而无商家编码；建议改为 `skus.bar_code`，并把 `sku_code` 降为 OPTIONAL |
| 13.5 | 新增 `skus.weight/volume/cost` | **NEW** | 原项目 SKU 有 `weight/volume/cost`，是运费与毛利的基础；建议 SHOULD |
| 13.6 | `users.nickname/avatar_url/phone` | **CONFIRMED** | 原项目 `eshop_user` 四个字段齐全（nickname/avatar/phone/real_name）；`phone` 唯一索引是改进（原项目 phone 无唯一） |
| 13.7 | `payments` 支付流水表（MUST） | **REVISED** | 原项目**没有**支付流水表，支付信息内嵌订单（`paid/pay_time/pay_type`）。建议：订单加 `pay_type`/`transaction_no` = **MUST**；独立 `payments` 表降为 **SHOULD** |
| 13.8 | `order_shipments` 表（MUST） | **REVISED** | 原项目用订单内嵌 `delivery_sn/delivery_name/delivery_type/delivery_id` + `express` 字典。建议：订单加 3 列 + `express` 字典 = **MUST**；`order_shipments` 表降为 **SHOULD**（多包裹再上） |
| 13.9 | `orders.shipping_fee`（MUST） | **CONFIRMED** | 原项目 `freight_price`/`total_postage`/`pay_postage` 三列，运费是核心金额项 |
| 13.10 | `regions` 地区码表（SHOULD） | **CONFIRMED** | 原项目 `system_city` 3,938 条 INSERT（`city_id/level/parent_id/area_code`），证明码表必要；但数据量大，仍列 SHOULD（需数据源授权） |
| 13.11 | `freight_templates` + rules（SHOULD） | **REVISED** | 原项目是三层：模板 + 区域计费（首件/续件）+ 满免包邮。建议拆为 `shipping_templates`/`_region`/`_free` 三表，优先级仍 SHOULD（依赖地区数据） |
| 13.12 | 优惠券适用范围 `coupon_template_scopes`（SHOULD） | **CONFIRMED（表结构）/ REJECTED（原项目的逗号串实现）** | 原项目用 `store_coupon.product_id` 逗号串表达适用范围，违反 1NF，**不采纳**；规范化 scope 表正确 |
| 13.13 | `user_coupons.expires_at`（SHOULD） | **REVISED → MUST** | 原项目 `store_coupon_user` 快照 `coupon_title/coupon_price/use_min_price/end_time`；只加 `expires_at` 不够，需 **4 个快照列**，否则模板改动会篡改已领券权益 |
| 13.14 | 优惠券"领取活动"层 | **NEW（SHOULD）** | 原项目 `store_coupon_issue` 独立于模板（限量/限时/永久）；SurgeCart 目前模板自带时间窗，可视为等价，**不新增表**，但需在契约层明确"模板即领取活动" |
| 13.15 | `order_status_logs`（SHOULD） | **REVISED → MUST** | 原项目 `store_order_status` 有 132 条 INSERT（51 单，约 2.6 条/单，按语句计），是订单核心可观测性 |
| 13.16 | `products.deleted_at` 软删除（SHOULD） | **CONFIRMED（行业惯例）/ 保持 SHOULD** | 原项目 51 张表用 `is_del`；但会污染秒杀读路径，仍建议单独评估 |
| 13.17 | 秒杀订单保持独立表（不合并） | **CONFIRMED** | 原项目合并到主订单；但 SurgeCart 的 Outbox/Kafka/幂等不变量更强，合并成本高、收益低 → 明确不合并 |
| 13.18 | 统一订单视图（OPTIONAL） | **DEFERRED** | 原项目本来就是统一订单表；SurgeCart 若合并需重写消费与退款语义，本轮不做 |
| 13.19 | 拼团/砍价/积分/会员/分销/充值/提现/门店/微信/装修 | **DEFERRED** | 原项目均有表，但与本轮"核心交易闭环"无关，且成本高（见 §15 理由） |
| 13.20 | 原报告"无支付流水/无物流表"的差距判断 | **CONFIRMED（差距存在）/ REVISED（优先级）** | 差距成立，但参考项目证明"订单内嵌列"即可支撑核心；独立表属增强而非必需 |
| 13.21 | 原报告"参考项目不可用，无法核对" | **REVISED** | SQL 已恢复，§4/§5/§9/§10/§11 由 NOT AVAILABLE 变为可分析；Java/前端仍 NOT AVAILABLE |

**与原报告冲突、必须明确的点**：

1. 原报告 §8 把 `payments`、`order_shipments` 列为 MUST —— **与参考实现不一致**，本报告下调为 SHOULD，
   改用订单内嵌列（成本更低、与行业实现一致）。
2. 原报告 §9 建议 `user_coupons.expires_at` 单列 —— **不足**，参考实现是 4 列快照。
3. 原报告未提 `order_status_logs` 的必要性强度 —— 参考实现有独立流水表，**升级为 MUST**。

---

## 14. 新增或调整的数据库模型建议（最终版）

### 14.1 MUST（B1-03-02 本轮）

| 变更 | 说明 | 相对原报告 |
|---|---|---|
| `product_specs` / `product_spec_values` / `sku_spec_values` | 规格项/值/SKU 关联，规范化 | 沿用 |
| `skus.image_url`、`skus.spec_signature` + `UNIQUE(product_id, spec_signature)` | 规格图与组合唯一 | 沿用 |
| `users.nickname`、`users.avatar_url`、`users.phone` + `UNIQUE(phone)` | 用户资料 | 沿用 |
| `orders.shipping_fee` | 运费（默认 0，历史不重算） | 沿用 |
| `orders.pay_type VARCHAR(16) NOT NULL DEFAULT ''`、`orders.transaction_no VARCHAR(64) NOT NULL DEFAULT ''` | 支付方式与渠道流水号 | **新增（替代 payments 表）** |
| `orders.delivery_type VARCHAR(32) NOT NULL DEFAULT ''`、`orders.delivery_name VARCHAR(64) NOT NULL DEFAULT ''`、`orders.delivery_sn VARCHAR(64) NOT NULL DEFAULT ''` | 承运商/运单号 | **新增（替代 shipments 表）** |
| `order_status_logs`（`order_id`、`change_type`、`change_message`、`operator_type`、`operator_id`、`created_at`） | 订单状态流水 | 升级为 MUST |
| `user_coupons` 快照列：`coupon_title VARCHAR(64)`、`coupon_price INT UNSIGNED`、`use_min_price INT UNSIGNED`、`end_time DATETIME` | 券权益快照 | **新增** |

### 14.2 SHOULD（下一轮）

| 变更 | 说明 |
|---|---|
| `express` 快递公司字典（`code`/`name`/`is_show`/`sort`） | 与订单 `delivery_name` 配合；也可先用固定枚举 |
| `payments` 支付流水表 | 多渠道/对账场景再上 |
| `order_shipments` 多包裹表 | 拆包发货再上 |
| `coupon_template_scopes` | 适用范围（规范化，不采纳逗号串） |
| `coupon_templates.per_user_limit`、`discount_type`/`discount_value` | 限领与折扣券 |
| `regions` + `shipping_templates(_region/_free)` + `products.freight_template_id` | 依赖地区数据源 |
| `skus.bar_code`、`skus.weight`、`skus.volume`、`skus.cost` | 仓储/运费/毛利 |
| `products.sales_count` / `skus.sales_count` | 列表排序与展示 |
| `products.deleted_at` | 软删除（需评估秒杀读路径） |

### 14.3 OPTIONAL / DEFERRED

- OPTIONAL：`skus.sku_code`、`orders.buyer_remark`/`admin_remark`、评价商家回复、浏览明细表。
- DEFERRED（明确本轮不做）：拼团、砍价、积分、会员等级、分销、充值/余额、提现、门店自提与核销、
  微信生态、页面装修、商户入驻、App 版本管理。

### 14.4 REJECTED（明确不采纳参考实现）

| 参考实现 | 拒绝理由 |
|---|---|
| `store_product.stock`（SPU 级库存） | 与 `inventories` 形成双库存事实，必然不一致 |
| `store_coupon.product_id` 逗号串 | 违反 1NF，无法建索引/外键 |
| `decimal(8,2)` 元金额 | SurgeCart 已统一整数分，回退会引入精度与迁移风险 |
| `store_order_cart_info.cart_info` JSON 快照 | 不如 `order_items` 列快照可查询/可索引 |
| `store_cart` 无唯一约束 | 会产生重复条目，SurgeCart 的 `uk_user_sku` 更稳 |
| 51 张表统一 `is_del` | 查询需处处过滤；SurgeCart 只在商品域按需引入 |

---

## 15. 迁移优先级（含理由）

| 优先级 | 项 | 求职价值 | 实现成本 | 与现有架构契合 | 结论 |
|---|---|---|---|---|---|
| MUST | 规格三表 + SKU 规格字段 | 高（多规格是电商门槛能力，面试可讲"规格矩阵/组合唯一"） | 中（3 表 + 2 列 + 1 唯一索引，无数据迁移） | 高（纯新增，不碰秒杀） | 做 |
| MUST | 用户资料三列 | 中 | 低 | 高 | 做 |
| MUST | 订单运费 + 支付/物流内嵌列 | 高（补齐订单金额与履约闭环） | 低（5 列，默认空） | 高 | 做 |
| MUST | `order_status_logs` | 高（可观测性/审计，易讲） | 低（1 表 + 1 索引） | 高 | 做 |
| MUST | 用户券快照 4 列 | 中高（权益正确性） | 低 | 高 | 做 |
| SHOULD | `express` 字典 | 中 | 低 | 高 | 紧随 MUST |
| SHOULD | `payments` 流水 | 中（对账话题） | 中 | 中（需接支付写路径） | 下一轮 |
| SHOULD | `order_shipments` | 低（单包裹已够） | 中 | 中 | 下一轮 |
| SHOULD | 优惠券范围/折扣类型/限领 | 中 | 中（需计价配合） | 中 | 下一轮 |
| SHOULD | 地区 + 运费模板 | 中 | 高（数据源 + 3 表） | 中 | 下一轮 |
| SHOULD | SKU 条码/重量/体积/成本 | 中 | 低 | 高 | 可并入 MUST 迁移 |
| OPTIONAL | 商品软删除、评价商家回复、浏览明细 | 低 | 中 | 中 | 视时间 |
| DEFERRED | 拼团/砍价/积分/会员/分销/余额/门店/微信/装修 | 低-中 | 很高 | 低 | 不做 |

**判断依据**：本轮是"数据库模型完善"，产出应是可讲清"为什么这样建模"的核心交易模型；
营销玩法类功能（拼团/砍价/积分/会员）会显著扩大表数量，却不能提升核心竞争力，
且与现有秒杀链路的隔离要求冲突最小化的原则相悖。

---

## 16. 需要 Owner 决策的问题

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| D-1 | **Java 后端与 PC/APP 前端仍未恢复** | ① 补齐后重跑 §3-§8；② 接受以 SQL 数据模型 + Go 现状推进 | ①（否则"原项目实现了什么"永远只能停在数据模型层） |
| D-2 | 支付方案 | ① 订单内嵌 `pay_type/transaction_no`（参考实现）；② 独立 `payments` 表 | ① 先做，② 留到多渠道对账 |
| D-3 | 物流方案 | ① 订单内嵌 3 列 + `express` 字典；② 独立 `order_shipments` | ①（与参考一致，成本最低） |
| D-4 | 规格模型 | ① 规范化三表（本报告建议）；② 参考项目"属性逗号串 + JSON 矩阵" | ①（可索引、可校验） |
| D-5 | SKU 业务键 | ① 仅 `skus.id`；② 追加 `bar_code`；③ 追加 `sku_code` | ②（参考有 `bar_code`，仓储需要） |
| D-6 | 优惠券快照与范围 | ① 本轮只加快照列；② 同时做适用范围/折扣类型 | ① |
| D-7 | 地区与运费模板 | ① 本轮不做；② 引入 `system_city` 等价数据（3,938 条 INSERT） | ①（数据授权与体量需单独评估） |
| D-8 | 是否把营销玩法列入路线图 | ① 仅记录不排期；② 排期拼团/砍价/积分/会员 | ① |
| D-9 | Migration 版本号策略 | ① 继续 `202610010000NN`；② 真实时间戳 | ② |

---

## 附：本次审查方法与可复现命令

```bash
# 资料清点（只读）
find tmp -type f
find / -maxdepth 8 -name "*.java" -not -path "/proc/*" -not -path "/sys/*" | head
find / -maxdepth 6 -name "pom.xml" -o -name "build.gradle" | head

# SQL 结构提取（不读 INSERT 内容）
grep -c "^CREATE TABLE" tmp/shopping.sql                 # 83
grep -c "^INSERT INTO" tmp/shopping.sql                  # 16692（仅计数）
awk '/^CREATE TABLE/{f=1} f{print} f&&/^\) ENGINE/{f=0}' tmp/shopping.sql > /tmp/ref_ddl.txt
grep -c "CONSTRAINT" /tmp/ref_ddl.txt                    # 11（全在管理端表）
grep -cE "^CREATE (VIEW|TRIGGER|PROCEDURE|FUNCTION)" tmp/shopping.sql   # 0

# Go 侧不变量（当前提交）
sed -n '525,540p' internal/logic/flashsale/flashsale.go   # sold < total_stock
sed -n '142,150p' internal/logic/inventory/inventory.go   # quantity >= ?
sed -n '196,206p' internal/logic/flashsale/consume.go     # FOR UPDATE SKIP LOCKED
```

**未执行**：SQL 导入、数据库连接（`3306` 不可达）、Java 构建、前端构建、任何代码/Migration 修改。
**未验证**：Java 实现完整度、PC/APP 页面与接口、原系统运行时行为、`store_product_attr_result.result`
的 JSON 结构（未读取数据）、库存扣减与退款的具体实现。
