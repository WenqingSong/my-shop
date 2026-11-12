# B1-03-01-FINAL：真实电商项目全面复审与迁移规划

> 审查类型：只读复审（无任何代码、Migration、OpenAPI、原始资料变更）
> 审查日期：2026-11-10
> 审查基线：`feat/p2-backend` / `3491a9abecfc02bb897d64e38a987038cd6d28f3`
> 参考资料：`tmp/` 下实际恢复的 PC 端、APP 端、SpringBoot 后端、MySQL dump
> 说明：本报告不沿用任何历史报告的结论。凡历史报告与本报告冲突处，以本报告列出的源码/SQL/日志证据为准。

---

## 0. 基线核验与证据等级

### 0.1 Git 事实

| 项 | 实测值 | 结论 |
|---|---|---|
| 仓库路径 | `/root/projects/my-shop` | 一致 |
| 当前分支 | `feat/p2-backend` | 与预期一致 |
| 当前 HEAD | `3491a9abecfc02bb897d64e38a987038cd6d28f3` | 与预期基线完全一致，未前移 |
| 上游 | `origin/feat/p2-backend` | 已建立跟踪 |
| 本地 vs 远端 | `## feat/p2-backend...origin/feat/p2-backend`，无 ahead/behind | 完全同步 |
| 工作区 | 仅 2 个未跟踪文件：`docs/phase2/B1-03_REFERENCE_REAUDIT.md`、`docs/phase2/B1-03_SCHEMA_DISCOVERY.md` | 无已跟踪文件改动 |
| 本次操作 | 只读 | 未改代码/迁移/契约/原始资料，未提交、未推送 |

### 0.2 证据等级定义

| 等级 | 含义 |
|---|---|
| **FACT-CODE** | 直接读到源码并逐行确认 |
| **FACT-SQL** | 由 dump 的 DDL / INSERT 结构直接统计得出 |
| **FACT-LOG** | 由运行期日志/异常栈确认类名、方法、行为 |
| **FACT-FE** | 由前端源码的 API 封装与页面字段确认契约 |
| **INFER** | 由上述事实推断，未读到实现 |
| **UNVERIFIABLE** | 源码缺失，无法验证 |

本报告所有"Java 业务实现程度"结论，除 4 个文件外（`AuthService`、`OrderSupplyService`、`CreatShareProductService`、`ProductRelationServiceImpl`），均为 **INFER/UNVERIFIABLE**，原因是 **Service/Mapper/Domain/Enum 四个层次整体缺失**（见 §2.3）。

---

## 1. 参考资料清单（第一部分）

递归扫描 `tmp/` 结果：

| 资料 | 实际路径 | 技术栈 | 规模 | 完整性 | 缺失依赖 |
|---|---|---|---|---|---|
| PC 用户商城前端 | `tmp/eshop-pc-vue/` | Vue 2.5 + vue-router 3 + vuex 3 + webpack 4 + Element-UI 2.12 + swiper 4 | 242 文件；23 个 `.vue`；3 个 api 模块；17 条路由 | **结构完整** | `node_modules` 缺失（无法直接构建）；`package.json` 依赖齐全 |
| APP 用户商城前端 | `tmp/eshop-app-vue/` | Vue 2.6 + @vue/cli 4.5 + Vant 2.12 + vuex + axios + lib-flexible | 71 文件；20 个 `.vue`；13 个 api 模块；19 条路由 | **结构完整** | `node_modules` 缺失；`node-sass` 与 Node 版本强相关 |
| SpringBoot 后端 | `tmp/eshop-backend-java/` | Spring Boot 2.4.3 + MyBatis-Plus 3.4.2 + Druid + Redis(Jedis) + Swagger + Quartz + JWT | 147 `.java`（142 在 `eshop-app`，5 在 `eshop-business`）；150 `.class` | **不完整（仅 2 / ≥6 模块）** | `eshop-common`、`eshop-weixin`、`eshop-message`、`eshop-mall`、`eshop-tools`、`eshop-logging`、`eshop-api` 全部缺失（§2.3） |
| MySQL 数据库 SQL | `tmp/shopping.sql` | Navicat dump，MySQL **5.7.17**，`utf8mb4`，`SET FOREIGN_KEY_CHECKS=0`，DDL 无任何 `FOREIGN KEY` | 5.5 MB / 18,835 行；**83 表 / 992 字段 / 94 索引 / 16,692 条 INSERT** | **完整** | 无 |
| 压缩包 | `tmp/eshop-pc-vue.rar`、`tmp/eshop-app-vue.rar`、`tmp/eshop-backend-java.rar` | — | 6.1 MB / 94 KB / 17.4 MB | 与目录内容对应 | 未解包校验（避免二次落盘） |
| Java 运行日志 | `tmp/eshop-backend-java/logs/`（`yshop-app`、`yshop-admin`） | logback + log4jdbc 配置 | 736 KB | 可作为运行期证据 | — |

**关键入口**

- PC：`tmp/eshop-pc-vue/src/main.js`（推断）→ `src/router/index.js`（17 路由）→ `src/api/index.js`（**全部后端 URL 集中在此文件**）
- APP：`src/main.js` → `src/router/router.config.js`（19 路由）→ `src/api/*.js`（13 个域模块）
- Java：`eshop-app/src/main/java/com/eshop/ApiRun.java`（`:8008`，context-path `/api`），拦截器 `common/interceptor/PermissionInterceptor.java`
- SQL：`tmp/shopping.sql`（源库名与源主机内网地址已脱敏，不在本报告出现）

**NOT AVAILABLE（明确缺失，不据历史报告补齐）**

| 项 | 状态 |
|---|---|
| Java 管理端模块（`yshop-admin` 源码） | **NOT AVAILABLE**（仅日志目录存在） |
| Java `eshop-common` / `eshop-weixin` / `eshop-message` / `eshop-mall` | **NOT AVAILABLE** |
| Java Service 实现 / Mapper XML / Domain 实体 / Enum 定义 | **NOT AVAILABLE** |
| 小程序端（uni-app）源码 | **NOT AVAILABLE**（代码中出现 `/pages/shop/GoodsCon/index` 路径引用） |
| 管理后台前端源码 | **NOT AVAILABLE** |
| Maven 本地仓库 / 可运行 jar | **NOT AVAILABLE**（`~/.m2` 无 `com/zhonghui`，环境无 `mvn`） |

---

## 2. Java 后端结构与完整性（第二部分·1）

### 2.1 父 POM 与子模块

`tmp/eshop-backend-java/pom.xml`：

- `groupId=com.zhonghui`，`artifactId=eshop`，`version=3.3.0`，`packaging=pom`
- `parent = spring-boot-starter-parent 2.4.3`，`java.version=1.8`
- `<modules>` **仅 2 个**：`eshop-app`、`eshop-business`
- 关键依赖：`spring-boot-starter-web/aop/cache/data-redis`、`mybatis-plus`、`druid`、`fastjson`、`hutool`、`mapstruct`、`easy-captcha`、`UserAgentUtils`、`gson`
- 私服地址为内网/第三方仓库（存在不可复现风险）

### 2.2 源码分布（FACT-CODE）

| 模块 | Java 文件 | 内容 | 说明 |
|---|---|---|---|
| `eshop-app` | 142 | 19 Controller、68 Param、7 DTO、2 VO、3 Service、common（config/ util/ aspect/ interceptor/ handler） | **前端 API 聚合层** |
| `eshop-business` | 5 | `StoreProductRelation` Domain、`ProductRelationService(+Impl)`、`ProductRelationMapper`、`StoreProductRelationQueryVo` | 仅"商品收藏/浏览关系"一个业务 |

`eshop-app` 内 **不存在** `*/domain/`、`*/service/mapper/`、`*/service/impl/`（唯一例外是 `eshop-business` 的收藏关系）。因此在 `eshop-app` 中检索到的所有 `xxxService` 均来自外部 jar。

### 2.3 缺失依赖的量化证据（FACT-CODE）

`eshop-app/pom.xml` 声明了 3 个内部 artifact，全部不在源码树内：

```xml
<dependency><groupId>com.zhonghui</groupId><artifactId>eshop-weixin</artifactId><version>3.3.0</version></dependency>
<dependency><groupId>com.zhonghui</groupId><artifactId>eshop-business</artifactId><version>3.3.0</version></dependency>
<dependency><groupId>com.zhonghui</groupId><artifactId>eshop-message</artifactId><version>3.3.0</version>
```

`eshop-business/pom.xml` 又依赖 `eshop-common:3.3.0`。对 `eshop-app` 142 个源文件的 `import com.eshop.*` 做「目标类是否在源码树内」校验，得到缺失分布：

| 缺失包 | import 次数 | 缺失内容 |
|---|---|---|
| `com.eshop.modules.activity` | 45 | 秒杀/拼团/砍价/优惠券的 Service、Domain、VO |
| `com.eshop.modules.user` | 39 | 用户/账单/充值/等级/任务 Service、Domain |
| `com.eshop.common.web` | 37 | `ApiResult` 体系、分页 VO |
| `com.eshop.modules.order` | 25 | 订单 Domain、Service、VO |
| `com.eshop.modules.shop` | 23 | 系统配置/分组数据 Service、Domain |
| `com.eshop.modules.product` | 19 | 商品/分类 Service、Domain |
| `com.eshop.modules.mp` | 8 | 微信模板消息 Service |
| `com.eshop.enums.*`（`OrderInfoEnum`/`OrderLogEnum`/`PayTypeEnum`/`BillDetailEnum`/`ProductEnum`/`AppFromEnum`/`ShopCommonEnum`） | 18 | **状态机与账单类型枚举全部缺失** |
| `com.eshop.utils.*`（`JwtToken`/`RedisUtils`/`OrderUtil`/`StringUtils`/`EncryptUtils`） | 16 | 基础设施 |
| `com.eshop.logging.*` | 16 | 操作日志 AOP |
| `com.eshop.tools.express` | 3 | 快递查询 SDK 封装 |

进一步缺口：Controller 引用的 `com.eshop.modules.order.param.OrderParam`、`...param.ComputeOrderParam` 之外的多个 Param 也不在树内（例如 `OrderParam` 缺失，仅 `ComputeOrderParam`/`ConfirmOrderParam` 存在）。

### 2.4 运行期证据（FACT-LOG）

- 启动日志确认 MyBatis-Plus Mapper 来自缺失模块：
  - `com.eshop.modules.product.service.mapper.StoreProductMapper.selectPage`
  - `com.eshop.modules.activity.service.mapper.StoreCouponIssueMapper.selectOne`
  - `com.eshop.modules.system.service.mapper.MenuMapper`
- 错误日志确认 **登录接口在缺失模块**：
  - `com.eshop.modules.auth.rest.AuthController.login(AuthController.java:64)`
  - `com.eshop.modules.user.controller.ApiController.login(ApiController.java:50)`
  - 异常文案："账号或者密码不正确"
- 错误日志确认 **存在 License 用户数限制**：
  - `com.eshop.exception.BadRequestException: 当前服务器用户端人数超过数量限制`
  - 监听器 `com.eshop.logging.tools.listener.LicenseCheckListener.onApplicationEvent`
  - 配置项 `licensepath`（`.git` 最新提交 "增加licensepath配置"）

### 2.5 结论

> **Java 参考后端只提供了「Controller 层 + Param/DTO 层」的完整实现（业务意图可信），没有提供任何「业务规则实现层」。**
>
> 因此本报告对 Java 的判定规则是：
> - Controller 内的编排、校验、状态机分支 → **FACT-CODE（可信）**
> - Controller 调用的 Service 内部规则 → **INFER**（可用 SQL 表结构、字段语义、前端字段、日志交叉验证）
> - 涉及具体金额公式、并发控制、事务边界 → **UNVERIFIABLE**

这直接改变迁移策略：**不能"翻译 Java 代码"，只能"按 Java 暴露的业务契约 + SQL 数据事实，在 Go 侧按现有架构重新设计"**，这与任务要求一致。

---

## 3. Java 业务模块清单（第二部分·2）

**统计口径（FACT-CODE）**：19 个 Controller，**113 个 HTTP 接口**（63 GET + 50 POST，无 PUT/DELETE）。

| # | Controller（相对 `com/eshop/`） | 接口数 | 业务域 |
|---|---|---|---|
| 1 | `modules/auth/rest/AuthController` | 4 | 认证：短信验证码、注册、登出、忘记密码 |
| 2 | `modules/product/rest/StoreProductController` | 7 | 商品列表/详情/推荐/海报/评价列表+评分配置 |
| 3 | `modules/product/rest/StoreCategoryController` | 1 | 分类树 |
| 4 | `modules/cart/rest/StoreCartController` | 5 | 加购/列表/改数量/删除/计数 |
| 5 | `modules/order/rest/StoreOrderController` | 17 | 确认/算价/下单/支付/列表/详情/收货/删除/评价/退款申请/取消/物流/核销/上传 |
| 6 | `modules/manage/rest/ShoperController` | 9 | 商家端：统计/订单列表/详情/改价/发货/退款/物流公司 |
| 7 | `modules/coupon/rest/CouponController` | 4 | 可领取券/领券/我的券/下单可用券 |
| 8 | `modules/activity/rest/StoreSeckillController` | 3 | 秒杀列表（按时段）/详情/时段配置 |
| 9 | `modules/activity/rest/StoreCombinationController` | 5 | 拼团列表/详情/团详情/海报/取消开团 |
| 10 | `modules/activity/rest/StoreBargainController` | 12 | 砍价列表/详情/发起/助力/助力金额/助力列表/统计/取消/海报 |
| 11 | `modules/activity/rest/StoreIntegralController` | 1 | 积分商品列表 |
| 12 | `modules/user/rest/UserController` | 11 | 用户资料/菜单/订单统计/余额/签到 4 接口/改资料/改密码 |
| 13 | `modules/user/rest/UserAddressController` | 7 | 省市区/地址增删改查/默认地址 |
| 14 | `modules/user/rest/UserBillController` | 6 | 推广数据/积分明细/分销海报/推广人/佣金明细/推广订单 |
| 15 | `modules/user/rest/UserLevelController` | 3 | 会员等级/等级任务/升级检测 |
| 16 | `modules/user/rest/UserRechargeController` | 3 | 充值方案/微信充值/**模拟充值** |
| 17 | `modules/user/rest/UserExtractController` | 2 | 提现参数/申请提现 |
| 18 | `modules/shop/rest/IndexController` | 9 | 首页聚合/轮播/精品/猜你喜欢/拼团位/菜单/热搜/城市 json/画布 |
| 19 | `controller/ProductCollectController` | 4 | 收藏/取消/批量取消/收藏列表 |

### 3.1 关键业务调用链（可确证部分）

**下单主链（FACT-CODE，`StoreOrderController:96-182`）**

```
POST /order/confirm      → storeOrderService.confirmOrder(user, cartId)
POST /order/computed/{k} → orderSupplyService.check(uid,k,param) → storeOrderService.computedOrder(uid,k,couponId,useIntegral,shippingType,addressId)
POST /order/create/{k}   → orderSupplyService.check(...) → storeOrderService.createOrder(user,k,param)
                          → if payPrice<=0 && payType!=INTEGRAL: storeOrderService.yuePay(orderId,uid)
                          → else orderSupplyService.goPay(map,orderId,uid,payType,from,dto)
POST /order/pay          → getOrderInfo(uni,uid) → 置状态 → yuePay / goPay
```
- `key` 是算价缓存键，`orderSupplyService.check` 用 `(uid,key)` 做**幂等/重复下单拦截**：已存在订单则返回 `EXTEND_ORDER`（`OrderSupplyService.java:99-156`），拼团场景额外返回 `PINK_ORDER_FAIL_1/2`。
- `@NoRepeatSubmit` 是另一层防重（AOP，实现缺失）。

**支付分派（FACT-CODE，`OrderSupplyService.java:168-245`）**

```java
switch (PayTypeEnum.toType(payType)) {
  case WEIXIN:    // H5 / 小程序 / APP / 公众号 四种下单，调 weixinPayService.unifyPay
  case YUE:       storeOrderService.yuePay(orderId, uid);        // 余额支付
  case INTEGRAL:  storeOrderService.integralPay(orderId, uid);   // 积分支付
}
```
→ Java 侧共 **3 种支付方式**：微信（真实外部）、余额（内部真实状态）、积分（内部真实状态）。这三种在 SQL 中均有对应字段（`pay_type`、`now_money`、`integral`、`pay_integral`）。

**砍价助力（FACT-CODE，`StoreBargainController:144-209`）**

```
POST /bargain/start   → storeBargainUserService.setBargain(bargainId, uid)     // 生成用户砍价实例
POST /bargain/help    → isBargainUserHelp(..)==false ? 直接返回 SUCCESSFUL(已帮过)
                        : storeBargainService.doHelp(bargainId, bargainUserUid, uid)
POST /bargain/help/price → 查 store_bargain_user → 查该 uid 的 store_bargain_user_help.price
```
→ 具备 **重复助力拦截 + 每条助力金额落库**（`store_bargain_user_help.price`），不是纯前端动画。

**拼团（FACT-CODE，`StoreCombinationController:69-150` + `OrderSupplyService:132-152`）**

```
GET  /combination/list|detail/{id}|pink/{id}
POST /combination/remove  → storePinkService.removePink(uid,cid,pinkId)   // 取消开团
下单时 pinkId>0 → getOrderPink(pinkId,uid) 判重 + getIsPinkUid(pinkId,uid) 判"是否已在团内"
```
→ 团实例（`store_pink`）+ 订单关联（`store_order.combination_id / pink_id`）构成真实业务闭环。

**优惠券（FACT-CODE，`CouponController:55-113`）**

```
GET  /coupons            → couponIssueService.getCouponList(page,limit,uid,productId,type)
POST /coupon/receive     → couponIssueService.issueUserCoupon(couponId,uid)
GET  /coupons/user/{t}   → storeCouponUserService.getUserCoupon(uid)
GET  /coupons/order/{ids}→ storeCouponUserService.beUsableCouponList(uid, cartIds)   // 按购物车算可用
```
→ 三层模型：`store_coupon`（券模板）→ `store_coupon_issue`（发行批次，含总量/余量/有效期）→ `store_coupon_user`（用户券，含**权益快照** `coupon_title/coupon_price/use_min_price/end_time`）。

**秒杀（FACT-CODE，`StoreSeckillController:64-150`）**

```
GET /seckill/list/{time} → getList(page,limit,Integer(time))
GET /seckill/detail/{id} → getDetail(id)
GET /seckill/index       → 读取 system_group_data(group_name='yshop_seckill_time')，
                           按 {"time":起始小时,"continued":持续小时} 计算 抢购中/即将开始/已结束
```
→ 秒杀是**时段表 + 表内库存扣减**，无 Redis 准入、无 MQ、无请求表。

---

## 4. Mock 与真实业务区分（第二部分·3）

> 判定原则：**"有外部依赖" ≠ "无迁移价值"**。若具备完整表结构 + 状态变化 + 业务规则，则视为可迁移的 MOCK 业务。

| 业务域 | 判定 | 证据 | 说明 |
|---|---|---|---|
| 用户注册/登录 | **REAL（但登录端点缺失）** | `AuthController:52-146` 短信码走 Redis `code_{account}`，`SecureUtil.md5` 存密码；登录在缺失模块 `ApiController.login` / `AuthController.login`（FACT-LOG） | 密码 = **无盐 MD5**，与 Go 现有 `password_hash` 不兼容 |
| 商品/分类/SKU | **REAL** | 商品详情含规格、SKU 映射（前端 `productDetails.vue:185-218`） | 服务层缺失，规则由数据结构确证 |
| 购物车 | **REAL** | `StoreCartController` 5 接口 + `store_cart` 78 行 | — |
| 订单 | **REAL** | 17 接口 + `store_order`(55 字段)/`store_order_cart_info`/`store_order_status` | 状态机可完全由 `change_type` 还原（§9.4） |
| 支付 | **MOCK（余额/积分）+ REAL（微信，不可复现）** | `OrderSupplyService.goPay`；SQL 中 51/51 订单 `pay_type='yue'` | **参考库真实成交路径是余额支付，不是微信** |
| 退款 | **PARTIAL** | `POST /order/refund/verify`（用户申请）+ `POST /admin/order/refund`（商家执行）；`refund_status` 单字段 + 4 个 `refund_reason_*` 字段；SQL 51 单 `refund_status=0`（无退款数据） | 有申请→执行的二段式，但无审核明细表、无审计流水 |
| 库存 | **UNVERIFIABLE** | 无库存 Service；库存分散在 `store_product.stock`、`store_product_attr_value.stock`、`.seckill_stock`、`.pink_stock` + 活动表 `stock` | 只能确证"库存是分散字段"，无法确证扣减口径 |
| 优惠券 | **REAL** | 三层表 + 4 接口 + 权益快照字段 | — |
| 秒杀 | **MOCK/简化实现** | `store_seckill`(0 行) + 时段配置 6 条 | 与 Go 的 Lua+Outbox 方案是**两代不同技术**，不构成迁移压力 |
| 拼团 | **MOCK（无第三方，但状态与记录真实）** | `store_pink` 19 字段含 `people/status/stop_time/k_id/is_refund`；订单 `combination_id/pink_id`；5 接口 | 需要真实成团判定与退款联动 → 表结构证明规则存在 |
| 砍价 | **MOCK（同上）** | `store_bargain`(1 行,32 字段) + `store_bargain_user`(状态/已砍金额) + `store_bargain_user_help`(每条助力金额) + 12 接口 | 有重复助力拦截，规则真实 |
| 积分 | **REAL（账户+流水+抵扣）** | `eshop_user.integral`、`user_bill.category=2`（积分）、`store_order.use_integral/gain_integral/pay_integral/back_integral`、`PayTypeEnum.INTEGRAL`、`integralPay()`；`system_config.integral_ratio` | 账户、流水、抵扣、返还四个环节齐备 |
| 会员 | **PARTIAL** | `system_user_level`(6 行，含 `money/grade/discount/valid_date/is_pay`) + `user_level`(0 行) + `setLevelComplete(uid)` | 等级配置与升级判定存在；**折扣是否参与算价 = UNVERIFIABLE**（`discount` 字段在 `user_level` 与 `system_user_level` 皆有） |
| 分销 | **REAL（关系+佣金流水）** | `eshop_user.spread_uid/spread_time/brokerage_price/is_promoter/spread_count`、`user_bill`(category=佣金)、`system_config.store_brokerage_ratio/store_brokerage_two`、6 接口 + `getSpreadCount()` 返回 first/second | 两级关系与两级佣金比例均为配置项 |
| 提现 | **PARTIAL / 外部依赖** | `user_extract`(0 行) + `/extract/cash` | 依赖真实打款渠道，数据为空 |
| 充值 | **MOCK（完整状态变化）** | `/recharge/test` 在 Controller 内直接：`now_money += price + give_price` → `userService.updateById` → 插 `user_bill(pm=1,title="账户充值")`；见 `UserRechargeController:158-210` | **这是本项目最有价值的 MOCK**：具备余额、账单、充值单三处一致写入 |
| 物流 | **API_ONLY** | `POST /order/express` → `ExpressAutoConfiguration.expressService().getExpressInfo(...)`（第三方聚合查询） | 外部服务，不可复现 |
| 门店核销 | **PARTIAL** | `POST /order/order_verific` → `verifyOrder(code,isConfirm,uid)`；`store_order.verify_code/store_id`；`shipping_type` 1=快递 2=自提；`system_store`(3)/`system_store_staff`(5) | 有核销码与门店配置，无独立门店域服务 |
| 微信 | **REAL（外部）** | `AuthService.wxappLogin/wechatLogin`（present，FACT-CODE）、订阅消息模板、`wechat_*` 6 表 | 需 AppSecret，不可复现 |
| 内容/装修 | **REAL（前端驱动）** | `IndexController` 9 接口读 `system_group_data`（banner/menus/roll/hot_search）+ `store_canvas` + `article` | 装修 JSON 与 PC/APP 强耦合 |

### 4.1 七个重点结论（任务点名项）

| 问题 | 结论 | 证据 |
|---|---|---|
| Mock 支付是否真正更新订单状态 | **是**。`create` 在 `payPrice<=0` 时直接 `yuePay`；`pay` 接口先校验 `paid` 再分派；`goPay` 的 `YUE`/`INTEGRAL` 分支调用 `storeOrderService.yuePay/integralPay` | `StoreOrderController:172-181`、`OrderSupplyService:232-239` |
| 拼团是否真正维护团状态与参与记录 | **是（结构层面）**。`store_pink` 有 `k_id`(开团人)、`people`、`stop_time`、`status`、`is_refund`；订单表 `pink_id` 反查参与记录 | DDL + `StoreCombinationController` |
| 砍价是否真正记录参与与金额变化 | **是**。`store_bargain_user.bargain_price`（已砍金额）+ `store_bargain_user_help.price`（每次助力金额），并有重复助力校验 | `StoreBargainController:146-209` |
| 积分是否真正维护账户与流水 | **是**。`eshop_user.integral` 账户 + `user_bill(category=2)` 流水 + 订单 `use/gain/pay/back_integral` 四字段 | DDL + `PayTypeEnum.INTEGRAL` |
| 会员是否真正实现等级与权益判断 | **部分**。等级配置与升级检测存在；折扣参与算价 **UNVERIFIABLE** | `UserLevelController:44-79` |
| 分销是否真正维护推荐关系与佣金 | **是（结构层面）**。`spread_uid` 自关联树 + `brokerage_price` 累计 + `user_bill` 佣金流水 + 两级比例配置 | DDL + `UserBillController` |
| 充值是否真正维护余额与充值记录 | **是（Mock 但完整）**。三处一致写入：`user_recharge` 单 → `eshop_user.now_money` → `user_bill` | `UserRechargeController:158-210` |

---

## 5. PC 用户端业务清单（第三部分·1）

**技术栈仅作参考，不作为 SurgeCart 新前端要求。**

17 条路由 / 23 个视图组件 / 3 个 API 模块（`src/api/index.js` 集中声明全部 URL）。

| 页面（路由） | 组件 | 业务模块 | 调用的后端接口（FACT-FE） | 状态 |
|---|---|---|---|---|
| `/index` 首页 | `views/home/index.vue` | 首页/推荐 | `/index/banner`、`/index/bastList`、`/index/like` | 完整 |
| `/category` 分类 | `views/category/index.vue` | 分类 | `/category` | 完整 |
| `/search` 搜索 | `views/search/index.vue` | 搜索 | `/products`、`/app/getSearchProducts` | **接口不一致**（`/app/getSearchProducts` 在 Java 中不存在） |
| `/productDetail` 商品详情 | `views/product/productDetail.vue` + `detailData/evaluation/likeList` | 商品/SKU/评价 | `/product/detail/{id}`、`/reply/config/{id}`、`/reply/list/{id}` | 完整 |
| `/shopping-trolley` 购物车 | `views/shopping-trolley/index.vue` + `deleteGoods` | 购物车 | `/cart/add`、`/cart/list`、`/cart/num`、`/cart/del` | 完整 |
| `/placeOrder` 确认订单 | `views/placeOrder/index.vue` | 订单确认/优惠券/地址/积分 | `/order/confirm`、`/order/computed/{key}`、`/order/create/{key}`、`/coupons/order/{ids}`、`/address/list`、`/city_list` | 完整；`pinkId` 硬编码 0 |
| `/orderDetail` 订单详情 | `views/orderDetail/index.vue` | 订单/支付 | `/order/detail/{k}`、`/order/pay`、`/order/cancel`、`/order/del`、`/order/take` | 完整 |
| `/myOrder` 我的订单 | `views/userCenter/myOrder/myOrder.vue` | 订单列表 | `/order/list` | 完整 |
| `/evaluate` 评价 | `views/evaluate/index.vue` | 评价 | `/order/comment`、`/uploads` | 完整 |
| `/recharge` 充值 | `views/userCenter/recharge/recharge.vue` | 余额/充值 | `/recharge/index`、**`/recharge/test`** | 完整（**走 Mock 充值**） |
| `/signingAddress` 地址 | `.../signingAddress.vue` | 地址 | `/address/list`、`/address/edit`、`/address/del`、`/city_list`、**`/receive/getSelect`**、**`/receive/getById`** | **2 个接口不一致**（Java 无 `/receive/*`，仅 `/address/detail/{id}`） |
| `/userInfo` 资料 | `views/userCenter/userInfo/userInfo.vue` | 用户资料 | `/userinfo`、`/user/edit`、`/user/password` | 完整 |
| `/favorites` 收藏 | `.../favorites/favorites.vue` | 收藏 | `/collect/user`、`/collect/del`、`/collect/dels/{ids}` | 完整 |
| `/browseRecords` 足迹 | `.../browseRecords.vue` | 足迹 | `/collect/user`（type 区分） | 完整 |
| `/userCenter` 个人中心 | `views/userCenter/userCenter.vue` | 聚合 | `/userinfo`、`/order/data` | 完整 |
| `/login` 登录 | `views/login/login.vue` | 认证 | `/register/verify`、`/register`、`/login`、`/sign/login` | `/sign/login` 为无效残留 |
| `/custom`、`/demo` | `views/custom`、`views/demo` | 装修/样例 | `/getCanvas` | 展示型 |

**PC 端能力边界**：**没有**秒杀页、拼团页、砍价页、积分页、会员页、分销页、签到页。仅 `components/canvasShow/basics/spikeList.vue` 含秒杀展示组件（装修块），不构成业务闭环。

---

## 6. APP 用户端业务清单（第三部分·2）

19 条路由 / 20 个视图 / **13 个 API 域模块**。

| 页面（路由） | 业务模块 | 接口 | 状态 |
|---|---|---|---|
| `/home` | 首页 | `/index`、`/index/banner`、`/index/bastList`、`/index/combinationList`、`/index/like`、`/index/menus`、`/sign/list`、`/sign/integral` | 完整（首页含拼团位 + 签到） |
| `/login`、`/register` | 认证 | `/login`、`/register`、`/register/verify`、`/auth/logout`、`/user/password` | 完整；`/login` 端点 **Java 源码缺失** |
| `/productList`、`/productClassify` | 商品/分类 | `/products`、`/category` | 完整 |
| `/productDetails` | 商品/SKU | `/product/detail/{id}`、`/reply/list/{id}`、`/collect/add`、`/cart/add` | 完整（SKU 选择器 `van-sku`） |
| `/cart` | 购物车 | `/cart/list`、`/cart/num`、`/cart/del` | 完整；渲染 `combinationId!=0` 的"团购价" |
| `/creationOrder` | 确认订单/优惠券/积分 | `/order/confirm`、`/order/computed/{key}`、`/order/create/{key}`、`/coupons/order/{ids}`、`/address/list`、`/address/detail/{id}` | 完整；`pinkId: 0`，`use_integral` 存在 |
| `/orderType`、`/order`、`/paySuccess` | 订单/支付 | `/order/list`、`/order/detail/{k}`、`/order/pay`、`/order/take`、`/order/del`、`/order/cancel` | 完整；按钮 `to="/groupBookOk?groupId="+pinkId` → **路由不存在（死链）** |
| `/collect` | 收藏/足迹 | `/collect/user?type=`、`/collect/del`、`/collect/dels/{ids}` | 完整 |
| `/wallet` | 余额/充值 | `/user/balance`、`/recharge/index`、**`/recharge/test`** | 完整（**Mock 充值**） |
| `/contactList`、`/newContact` | 地址 | `/address/list`、`/address/edit`、`/address/del`、`/citys` | 完整 |
| `/rpwd` | 改密 | `/user/password` | 完整 |
| `/search` | 搜索 | `/search/keyword`、`/products` | 完整 |
| `/nullPage` | 404 | — | — |

**APP 端关键发现**

1. `src/api/groupbook.js` 封装了 `/combination/detail/{id}`、`/combination/pink/{id}`、`/combination/remove` —— **没有任何页面 import 它**（`grep "from.*groupbook"` 无结果）。→ **拼团 = API_ONLY + 死代码**。
2. `groupBookOk` 路由不存在 → 拼团闭环在 APP 端 **未完成**。
3. `src/api/wallet.js` 的 `prepaid()` 指向 `/recharge/test` → **Mock 充值是前端实际使用路径**。
4. APP **无**秒杀页、砍价页、积分商城页、会员页、分销页。

### 6.1 PC 与 APP 差异

| 维度 | PC（`eshop-pc-vue`） | APP（`eshop-app-vue`） |
|---|---|---|
| UI 框架 | Element-UI（桌面栈） | Vant（移动栈） |
| 商品 SKU 交互 | 自定义规格选择 | `van-sku` 组件（`tree` + `list`） |
| 优惠券 | 下单页选择 | 下单页选择（独立 `discount.js`） |
| 充值 | 有（`/recharge/test`） | 有（`/wallet`） |
| 签到 | 无 | 有（`/sign/list`、`/sign/integral`） |
| 拼团 | 无 | 仅 API 封装 + 展示位 + 死链 |
| 搜索 | `/app/getSearchProducts`（不存在） | `/products`（正确） |
| 地址 | `/receive/getSelect`、`/receive/getById`（不存在） | `/address/list`、`/address/detail/{id}`（正确） |
| 足迹/收藏 | 同一接口用 type 区分 | 同 |

---

## 7. SQL 全部表清单（第四部分·1）

**数据库事实（FACT-SQL）**

| 项 | 值 |
|---|---|
| 表数量 | **83** |
| 字段总数 | **992** |
| 索引条目（PRIMARY/UNIQUE/KEY） | **83**（另有 11 条 `CONSTRAINT ... FOREIGN KEY` 行，合计 94 条键定义） |
| 外键约束 | **11 条**，**全部位于管理端框架表**（`dict_detail→dict`、`job→dept`、`user(dept_id/job_id/avatar_id)`、`users_roles`、`roles_menus`）。**业务表（`store_*`、`eshop_user`、`user_bill` 等）外键为 0**，纯应用层关联；dump 头部有 `SET FOREIGN_KEY_CHECKS=0` |
| 引擎 | **83/83 = InnoDB** |
| 字符集 | **78 表 `utf8`(utf8mb3)/`utf8_general_ci`**；仅 5 表 `utf8mb4`（`material`、`material_group` 为 `utf8mb4_bin`；`store_customer`、`wechat_live`、`wechat_live_goods` 为 `utf8mb4_general_ci`）。**所有业务表均为 utf8mb3，不支持 4 字节 emoji** —— 与 Java 侧 `EmojiParser.removeAllEmojis(...)` 的处理一致（`StoreOrderController:373`） |
| dump 头部 | `SET NAMES utf8mb4; SET FOREIGN_KEY_CHECKS = 0;`（仅客户端会话设置，不代表表字符集） |
| 软删除 | 大量表使用 `is_del TINYINT(1)`；`store_order` 另有 `is_system_del` |
| 金额类型 | **`decimal(P,2)` 元**（主键多为 `bigint(20)`） |
| 时间类型 | `datetime`（无时区） |
| MySQL 版本 | 5.7.17（不支持 `CHECK`、窗口函数） |
| INSERT 总数 | **16,692** |

### 7.1 商品域（6）

| 表 | 行数 | 用途 |
|---|---|---|
| `store_product` | 226 | 商品主表（41 字段，含 `price/vip_price/ot_price/postage/cost`、`spec_type`、`is_seckill/is_bargain/is_integral`、`mer_id`） |
| `store_product_attr` | 72 | 规格组（`attr_name` + `attr_values` 逗号串） |
| `store_product_attr_value` | 111 | SKU 行（`sku`=规格名组合、`unique`=SKU 唯一码、`stock/sales/price/cost/ot_price/brokerage/pink_*/seckill_*/integral`） |
| `store_product_attr_result` | 226 | **规格指纹 JSON**（`{"attr":[{value,detail[]}],"value":[{detail,value1,value2,sku,price,stock,...}]}`，226/226 可解析） |
| `store_product_rule` | 11 | 营销规则 JSON（`rule_name/rule_value`） |
| `store_category` | 57 | 分类树（`pid`，`mediumint` 主键） |

### 7.2 交易域（4）

| 表 | 行数 | 用途 |
|---|---|---|
| `store_cart` | 78 | 购物车（`type='product'`、`product_attr_unique`、`is_new/is_pay/is_del`、`combination_id/seckill_id/bargain_id`） |
| `store_order` | 51 | 订单主表（**55 字段**：金额 5 项、`paid/pay_time/pay_type`、`status/refund_status`、`coupon_id/coupon_price`、4 个积分字段、`delivery_*`、`combination_id/pink_id/seckill_id/bargain_id`、`verify_code/store_id/shipping_type`、`unique`） |
| `store_order_cart_info` | 56 | 订单行快照（`cart_info` 为 **整行 JSON**，含 `productInfo.attrInfo`） |
| `store_order_status` | 132 | 订单状态流水（`change_type/change_message/change_time`） |

### 7.3 营销域（10）

| 表 | 行数 | 用途 |
|---|---|---|
| `store_coupon` | 2 | 券模板（`type` 0 通用/1 商品/2 内部，`coupon_price/use_min_price/coupon_time/integral`） |
| `store_coupon_issue` | 12 | 发行批次（`total_count/remain_count/is_permanent/start_time/end_time/ctype`） |
| `store_coupon_issue_user` | 8 | 领取记录（`uid/issue_coupon_id`） |
| `store_coupon_user` | 8 | 用户券（**权益快照** + `status/is_fail/use_time/end_time`） |
| `store_seckill` | **0** | 秒杀活动（29 字段，`time_id` 关联时段，`stock/sales/num`） |
| `store_combination` | 4 | 拼团活动（`people/price/effective_time/is_host/start_time/stop_time/stock/sales`） |
| `store_pink` | **0** | 团实例（`k_id` 团长、`people`、`stop_time`、`status`、`is_refund`、`order_id_key`） |
| `store_bargain` | **1** | 砍价活动（32 字段，`bargain_max_price/min_price/bargain_num/num/stock`） |
| `store_bargain_user` | **0** | 用户砍价实例（`bargain_price` 已砍、`price` 当前价、`status`） |
| `store_bargain_user_help` | **0** | 每次助力（`price` 砍掉金额） |

### 7.4 用户与资金域（14）

| 表 | 行数 | 用途 |
|---|---|---|
| `eshop_user` | 20 | C 端用户（33 字段：`username/password(md5)/phone/nickname/avatar/now_money/brokerage_price/integral/sign_num/level/spread_uid/spread_count/is_promoter/pay_count/user_type/wx_profile(json)/is_del`） |
| `user` | 5 | 管理端账号（`dept_id/job_id/enabled/is_del`） |
| `user_address` | 36 | 收货地址（`city_id` + 省市区文本 + 经纬度） |
| `user_bill` | **429** | 资金/积分流水（`pm` 收支、`category` 1 金额/2 积分、`type` 业务类型、`number/balance/link_id`） |
| `user_recharge` | **325** | 充值单（`price/give_price/recharge_type/paid/pay_time/refund_price`） |
| `user_extract` | 0 | 提现申请 |
| `user_sign` | 15 | 签到记录（`number/balance`）。注意：主键列名为 `int`（历史缺陷） |
| `user_level` | 0 | 用户等级实例（`level_id/grade/valid_time/is_forever/discount`） |
| `system_user_level` | 6 | 等级配置（`money/valid_date/is_forever/is_pay/grade/discount`） |
| `user_task_finish` | 0 | 等级任务完成记录 |
| `system_user_task` | 18 | 等级任务配置（`task_type/number/level_id/is_must`） |
| `user_avatar` | 1 | 头像素材 |
| `user_group` | 0 | 用户分组 |
| `user_enter` | 0 | 商户入驻申请 |

### 7.5 互动域（3）

| 表 | 行数 | 用途 |
|---|---|---|
| `store_product_reply` | 13 | 评价（`product_score/service_score/comment/pics/merchant_reply_*`） |
| `store_product_relation` | 104 | 收藏/足迹（`type` + `category`） |
| `store_visit` | 0 | 浏览统计 |

### 7.6 内容域（11）

| 表 | 行数 | 用途 |
|---|---|---|
| `article` | 3 | 文章（`cid/title/content/is_hot/is_banner/product_id`） |
| `store_canvas` | 2 | 页面装修 JSON（`terminal` 端类型） |
| `material` / `material_group` | 242 / 4 | 素材库 |
| `qiniu_content` | 27 | 七牛文件索引 |
| `wechat_live` / `wechat_live_goods` / `wechat_media` | 0 / 3 / 0 | 微信直播/素材 |
| `wechat_menu` / `wechat_reply` / `wechat_template` | 1 / 1 / 4 | 公众号菜单/自动回复/模板消息 |

### 7.7 物流与门店（6）

| 表 | 行数 | 用途 |
|---|---|---|
| `express` | 4 | 快递公司字典（`code/name`） |
| `shipping_templates` | 5 | 运费模板（`type`、`region_info` JSON、`appoint`） |
| `shipping_templates_region` | 23 | 区域运费（`first/first_price/continues/continue_price`） |
| `shipping_templates_free` | 26 | 包邮规则（`number/price` 满件/满额） |
| `system_store` | 3 | 门店（`day_time/valid_time/lat/lng`） |
| `system_store_staff` | 5 | 门店管理员（`verify_status`） |

### 7.8 地区字典（1）

| 表 | 行数 | 用途 |
|---|---|---|
| `system_city` | **3,938** | 省市区（`city_id/level/parent_id/area_code/name/merger_name/lng/lat`） |

### 7.9 管理端框架与运维（28）

| 表 | 行数 | 用途 |
|---|---|---|
| `menu` / `roles_menus` | 206 / 45 | 后台菜单与角色菜单 |
| `role` / `users_roles` / `roles_depts` / `dept` / `job` | 4 / 5 / 1 / 8 / 4 | RBAC 与组织 |
| `dict` / `dict_detail` | 6 / 12 | 数据字典 |
| `system_config` | 52 | 单键配置（`integral_ratio`、`store_brokerage_ratio`、`store_brokerage_two`、`store_postage`、`store_free_postage`、`user_extract_min_price`、`site_url` …） |
| `system_group_data` | 46 | 分组 JSON（`yshop_home_banner` 7、`yshop_my_menus` 11、`yshop_sign_day_num` 7、`yshop_seckill_time` 6、`yshop_recharge_price_ways` 2 …） |
| `system_attachment` | 7 | 附件 |
| `quartz_job` / `quartz_log` | 3 / 21 | 定时任务（`bean_name/method_name/cron_expression`） |
| `gen_config` / `column_config` / `gen_test` | 15 / 291 / 0 | 代码生成器配置 |
| `local_storage` | 17 | 本地上传记录 |
| `monitor_server` | 1 | 服务器监控 |
| `app_version` / `picture` | 0 / 0 | 版本/图库 |
| `alipay_config` / `email_config` / `qiniu_config` | 1 / 1 / 1 | **第三方凭据（含私钥/AK/SK，禁止迁移）** |
| `verification_code` / `store_customer` | 0 / 0 | 核销码/公众号客户 |
| `visits` | 6 | 站点 PV/UV 统计 |
| `log` | **9,621** | 操作日志（最大表，纯噪声） |

---

## 8. SQL 数据量统计（第四部分·2）

### 8.1 总量分布

| 分组 | 表数 | 行数 | 占比 |
|---|---|---|---|
| 框架与运维 | 28 | 10,374 | 62.1% |
| 地区字典 | 1 | 3,938 | 23.6% |
| **真实业务数据** | **54** | **2,380** | **14.3%** |
| 合计 | 83 | 16,692 | 100% |

真实业务 2,380 行拆分：

| 业务域 | 行数 |
|---|---|
| 用户与资金 | 855 |
| 商品 | 703 |
| 交易 | 317 |
| 内容 | 287 |
| 互动 | 117 |
| 物流/门店 | 66 |
| 营销 | 35 |

### 8.2 有结构有数据 / 有结构无数据

**有表结构且有业务数据（30 张，其中 22 张值得迁移）**

`eshop_user`(20)、`user_address`(36)、`user_bill`(429)、`user_recharge`(325)、`user_sign`(15)、`system_user_level`(6)、`store_product`(226/有效 54)、`store_product_attr`(72)、`store_product_attr_value`(111)、`store_product_attr_result`(226)、`store_category`(57)、`store_cart`(78)、`store_order`(51)、`store_order_cart_info`(56)、`store_order_status`(132)、`store_coupon`(2)、`store_coupon_issue`(12)、`store_coupon_issue_user`(8)、`store_coupon_user`(8)、`store_product_reply`(13)、`store_product_relation`(104)、`article`(3)、`store_combination`(4)、`store_bargain`(1)、`shipping_templates`(5)+region(23)+free(26)、`express`(4)、`system_store`(3)、`system_store_staff`(5)

**只有表结构、零业务数据（16 张）**

`store_seckill`、`store_pink`、`store_bargain_user`、`store_bargain_user_help`、`user_level`、`user_task_finish`、`user_extract`、`user_enter`、`user_group`、`store_visit`、`verification_code`、`wechat_live`、`wechat_media`、`picture`、`app_version`、`gen_test`

**只有初始化字典/配置（7 张）**

`system_city`(3938)、`express`(4)、`system_user_level`(6)、`dict`/`dict_detail`(6/12)、`system_config`(52)、`system_group_data`(46)

**只有管理端框架数据（约 12 张，合计 10,374 行含 `log`）**

`menu`、`roles_menus`、`role`、`users_roles`、`roles_depts`、`dept`、`job`、`quartz_*`、`gen_config`、`column_config`、`local_storage`、`log`、`visits`

### 8.3 数据事实中值得注意的"反直觉"结论

| 观察 | 数据 | 迁移含义 |
|---|---|---|
| **订单支付方式单一** | 51/51 `pay_type='yue'`（余额） | 历史订单只能映射为"余额支付"，Go 必须补余额账户；微信/支付宝无历史数据 |
| 订单状态不可单列解读 | `status` 0:35 / 1:3 / 2:5 / 3:8，而 `paid=1` 有 37 条 | 必须用 `store_order_status.change_type` 序列还原状态（§9.4） |
| 无退款数据 | `refund_status=0` 全员 | 退款表只需迁移结构，不迁数据 |
| 营销活动几乎空跑 | 秒杀 0、团 0、砍价参与 0、积分商品 0 | 营销域**没有历史数据包袱**，可自由设计模型 |
| 商品大量软删 | 226 中 `is_del=1` 达 172（76%） | 只迁 54 个有效商品及其 SKU |
| C 端用户全部同质 | 20 人全部 `user_type='h5'`、`level=0`、`is_promoter=0`、`spread_uid` 空 | 分销/会员**无历史数据**，也印证原库未真实运营这些功能 |
| 充值记录多于订单 | `user_recharge` 325 > `store_order` 51 | 充值是原系统最活跃的资金动作；`now_money>0` 仅 14 人 |
| 积分有小额沉淀 | 7 人 `integral>0` | 积分期初余额需迁移 |
| 唯一强结构化快照 | `store_product_attr_result` 226/226 可 JSON 解析 | SKU 规格可 100% 自动重建 |

---

## 9. 数据迁移映射矩阵（第四部分·3）

分类含义：`MIGRATE_SCHEMA_AND_DATA` / `MIGRATE_SCHEMA_ONLY` / `SEED_DATA` / `TRANSFORM_AND_MIGRATE` / `ARCHIVE` / `EXCLUDE`

**覆盖校验**：§9.1–§9.7 合计覆盖 **83 张表**，每表一条处理建议，无遗漏、无重复（`local_storage` 统一归入 §9.7）。

### 9.1 商品域

| 原表 | 用途 | 行数 | 分类 | 对应 Go 表 | 字段映射与转换 | 必要性 | 风险 |
|---|---|---|---|---|---|---|---|
| `store_category` | 分类树 | 57 | MIGRATE_SCHEMA_AND_DATA | `categories` | `id→legacy_id_map.old_id`；`pid→parent_id`（0→0）；`cate_name→name`；`sort→sort`；`is_show→status(1→1,0→0)`；忽略 `pic/create_time/update_time`（用迁移时间） | 高 | Go 有 `uk_parent_name`，**同名同父会冲突**，需按 `parent_id+name` 去重后映射 |
| `store_product` | 商品 | 226 | TRANSFORM_AND_MIGRATE | `products`（+`product_images`） | `store_name→name`；`keyword`/`store_info`→拼接入 `detail` 前言或丢弃；`cate_id`（逗号串）→ 取首个有效分类→`category_id`；`price` **decimal 元 → int 分**（`round(x*100)`）；`image→main_image`；`slider_image`（逗号串）→`product_images`；`is_show→status`；`sales→view_count`（近似）；`stock` **不写入商品表**（改用 `inventories`）；`is_del=1` 的 172 条 **不迁**（ARCHIVE） | 高 | 一商品多分类被压成单分类；`price` 为 0 或异常值需校验；`vip_price/ot_price/postage/cost` 无对应列 |
| `store_product_attr` | 规格组 | 72 | TRANSFORM_AND_MIGRATE | 新增 `spec_groups` / `spec_values` | `attr_name→spec_groups.name`；`attr_values`（逗号串）→ 拆分为 `spec_values`（保持原顺序） | 高 | 值内含逗号会拆错，需以 `store_product_attr_result.attr[].detail` 数组为**主数据源**校正 |
| `store_product_attr_value` | SKU | 111 | MIGRATE_SCHEMA_AND_DATA | `skus` + `inventories` | `unique→skus.sku_code`（唯一）；`sku`（"红,S"）→`skus.spec_key` + `spec_values` JSON；`price`→分；`stock→inventories.quantity`（**仅迁一次**）；`sales` 丢弃或入统计；`pink_*/seckill_*` **不入 inventories** | 高 | **库存不得重复计入**（商品表 `stock` 与 SKU `stock` 并存，只能取 SKU 级） |
| `store_product_attr_result` | 规格指纹 | 226 | TRANSFORM_AND_MIGRATE | `skus.spec_key` / 商品 `spec_schema` | 解析 `{"attr":[{value,detail[]}],"value":[{value1,value2,sku,price,stock,pic,...}]}`；用它生成 `spec_groups/spec_values` 与 `skus.spec_key` 的**权威来源** | 高 | 214 条为 2 维规格、12 条带 `attrHidden`；需兼容 1 维/无规格（`spec_type=0` 的 183 个商品） |
| `store_product_rule` | 营销规则 JSON | 11 | EXCLUDE | — | 规则名/值为后台可编辑 JSON，当前无可执行语义 | 低 | 若后续实现"满减/费率"需重新建模 |
| `store_product_reply` | 评价 | 13 | TRANSFORM_AND_MIGRATE | `reviews` | `uid→user_id`；`oid`+`unique`→定位 `order_items.id`（`store_order_cart_info.unique` 关联）；`product_score→rating`；`comment→content`；`pics`（逗号串）→`image_urls` JSON；`merchant_reply_*` 无对应列→丢弃或新增字段 | 中 | 订单行定位失败则降级为"无订单评价"，需允许 `order_item_id` 空（当前 Go 该列 NOT NULL 视迁移实现而定） |

### 9.2 交易域

| 原表 | 用途 | 行数 | 分类 | 对应 Go 表 | 字段映射与转换 | 必要性 | 风险 |
|---|---|---|---|---|---|---|---|
| `store_cart` | 购物车 | 78 | EXCLUDE | `cart_items` | 购物车是瞬时态；含 `is_pay/is_del` 且 `combination_id/seckill_id/bargain_id` 语义不同 | 低 | 迁入会产生脏钩子（指向已结束活动） |
| `store_order` | 订单 | 51 | TRANSFORM_AND_MIGRATE | `orders` | `order_id→order_no`；`uid→user_id`；`real_name/user_phone/user_address`→地址四段（拆分失败则整串入 `detail`）；`total_price→original_amount`；`pay_price→total_amount`；`coupon_price→discount_amount`；`freight_price/postage` → 新增 `freight_amount`；`pay_type('yue')→pay_channel=balance`；`paid/pay_time`；`status` **不可直读**（见 §9.4）；`create_time→created_at`；`unique→幂等键占位` | 高 | **状态必须由状态流水推导**；`is_del=1` 的 5 单对应 `remove_order`；`pay_integral/use_integral` 有值但 `store_product.is_integral=0`，需按 0 处理 |
| `store_order_cart_info` | 订单行快照 | 56 | TRANSFORM_AND_MIGRATE | `order_items` | `cart_info` JSON → 拆出 `product_id/sku(unique)/cart_num/price` 写入行快照；`oid→order_id` | 高 | JSON 结构为 `productInfo.attrInfo`，与本报告 §9.3 SKU 模型一致 |
| `store_order_status` | 状态流水 | 132 | TRANSFORM_AND_MIGRATE | 新增 `order_status_logs` | `oid→order_id`；`change_type`→`from_status/to_status`（按 §9.4 映射表）；`change_time→created_at`；`change_message` 脱敏后入 `remark` | 高（**同时是状态推导依据**） | 132 条含 `yshop_create_order` 51 → 每条订单必有创建流水，可用于完整性校验 |
| `store_coupon` | 券模板 | 2 | TRANSFORM_AND_MIGRATE | `coupon_templates` | `title→name`；`coupon_price→discount_amount`；`use_min_price→threshold_amount`；`coupon_time`（天）→ 用批次 `start/end_time` 覆盖 `use_*_time`；`type` → 新增 `scope`(0 通用/1 商品/2 内部)；`product_id` → 新增 `scope_product_ids` | 中 | `coupon_time` 与批次有效期语义重叠，需以批次为准 |
| `store_coupon_issue` | 发行批次 | 12 | MIGRATE_SCHEMA_AND_DATA | 新增 `coupon_batches` | `cname→name`；`cid→template_id`；`total_count/remain_count`；`start_time/end_time`；`is_permanent`；`status` | 中 | Go 现模型把 `total_quantity/issued_quantity` 放在模板上，**多批次会丢结构**，必须新增批次表 |
| `store_coupon_issue_user` | 领取记录 | 8 | TRANSFORM_AND_MIGRATE | `user_coupons` + `coupon_batches` | `uid→user_id`；`issue_coupon_id→batch_id`；`create_time→received_at`；`status` 需结合 `store_coupon_user` 修正 | 中 | 领取但未生成用户券的记录会"丢券"，需以 `store_coupon_user` 为准并补领 |
| `store_coupon_user` | 用户券 | 8 | TRANSFORM_AND_MIGRATE | `user_coupons` | `cid→template_id`；**快照三字段**（`coupon_title/coupon_price/use_min_price`）→ 新增快照列或复用模板；`status`；`use_time→used_at`；`end_time`；`is_fail` | 中 | 快照与模板可能不一致，**必须保留快照**防止历史券面额漂移 |

### 9.3 营销域

| 原表 | 用途 | 行数 | 分类 | 对应 Go 表 | 字段映射与转换 | 必要性 | 风险 |
|---|---|---|---|---|---|---|---|
| `store_seckill` | 秒杀活动 | 0 | **EXCLUDE** | `flash_sale_activities`（不迁） | 模型是"时段表 + 表内 stock"，与 Go 的 Lua 准入 + 请求表 + Outbox 完全不同代 | 无（0 行） | 若强行导入会与 `flash_sale_activity_skus.sold` 语义冲突 |
| `store_combination` | 拼团活动 | 4 | TRANSFORM_AND_MIGRATE | 新增 `group_buy_activities` | `title→name`；`people→required_size`；`price→group_price`；`product_price→original_price`；`stock→total_stock`；`sales→sold`；`start_time/stop_time`；`effective_time`（小时）→`group_expire_hours`；`is_show→status`；`product_id/attr`(SKU unique) | 中 | 4 条活动对应商品可能已软删，需先过滤 |
| `store_pink` | 团实例 | 0 | MIGRATE_SCHEMA_ONLY | 新增 `group_buy_instances` / `group_buy_members` | 结构参考：`k_id`(团长)、`people`(已参人数)、`stop_time`、`status`、`is_refund`、`order_id_key` | 中 | 无数据，可自由设计（建议 member 独立表而非计数） |
| `store_bargain` | 砍价活动 | 1 | TRANSFORM_AND_MIGRATE | 新增 `bargain_activities` | `title`；`price→start_price`；`min_price→floor_price`；`bargain_min_price/bargain_max_price`→每次助力区间；`bargain_num`→最多助力人数；`num`→参与名额；`stock→total_stock` | 中 | 1 条活动是否保留取决于是否实现砍价 |
| `store_bargain_user` | 砍价实例 | 0 | MIGRATE_SCHEMA_ONLY | 新增 `bargain_instances` | `bargain_price`(已砍)、`price`(当前价)、`status` | 中 | — |
| `store_bargain_user_help` | 助力记录 | 0 | MIGRATE_SCHEMA_ONLY | 新增 `bargain_helps` | `bargain_user_id`→instance；`price`=本次砍掉金额；`uid` | 中 | **必须唯一约束 (instance_id, helper_uid)** 防重复助力 |

### 9.4 订单状态映射（专项）

`store_order.status` 无法单列判定。**权威依据是 `store_order_status.change_type` 流水**（FACT-SQL 分布）：

| `change_type` | 条数 | 语义 | 目标 Go `status` |
|---|---|---|---|
| `yshop_create_order` | 51 | 下单 | 10（待支付） |
| `pay_success` | 37 | 支付成功 | 20（已支付） |
| `delivery_goods` | 14 | 已发货 | 30（已发货） |
| `user_take_delivery` | 12 | 已收货 | 40（已收货） |
| `order_eval` | 13 | 已评价 | 50（已完成） |
| `remove_order` | 5 | 用户删除 | 60（已取消，`cancel_reason` 标记为"迁移·用户删除"） |

**推算规则**：按 `oid` 分组、按 `change_time` 升序取**最后一个** `change_type` → 映射到上表状态；同时把整条流水写入 `order_status_logs`。
校验：`paid=1` 的 37 条必须至少有 `pay_success`，否则进 `data_issues` 报表（不阻塞迁移，标记人工复核）。

**Java → Go 状态机差异**

| 原项目 | Go 现有 |
|---|---|
| `status`(0/1/2/3/4) + `paid` + `refund_status` **三列并存、语义重叠** | `status` 单列 7 态（10/20/30/40/50/60/70） |
| 无独立退款状态机，仅 `refund_status` + `refund_*` 文本字段 | `refund_requests.status` 独立状态机 + `refund_audits` 审计 |
| 无取消原因枚举 | `cancel_reason` 1 用户 / 2 超时 |
| 无 `expire_at` | `orders.expire_at` + 超时扫描 `CancelExpired` |

→ 迁移时必须做**状态归一**，不能保留原 `status` 数字。

### 9.5 用户与资金域

| 原表 | 用途 | 行数 | 分类 | 对应 Go 表 | 字段映射与转换 | 必要性 | 风险 |
|---|---|---|---|---|---|---|---|
| `eshop_user` | C 端用户 | 20 | TRANSFORM_AND_MIGRATE | `users` + 新增 `user_profiles` | `uid→legacy_id_map`；`phone→phone`（唯一）；`nickname/avatar→user_profiles`；`username`（多为手机号/openid）→ `users.username`；**`password`(无盐 MD5 32 位) 不可转 bcrypt** → 置为 `!legacy` 并强制重置；`now_money`→ 新增 `wallets.balance`（**期初余额**）；`integral`→`points_accounts.balance`（期初）；`brokerage_price`→`wallets.commission_balance`；`spread_uid→distribution_relations`；`is_promoter/spread_count → 冗余统计`；`status→users.status`；`is_del=1` 不迁 | **高** | ① 密码不可迁移（安全问题，必须强制重置）；② `now_money` 与 `user_bill` 双写会导致余额翻倍 → **只迁余额快照，账单仅作历史**；③ `now_money` 为 `decimal(8,2)`，上限 999,999.99 元 |
| `user` | 管理端账号 | 5 | EXCLUDE | `admins` | 原密码算法未知且含 `dept/job` 体系 | 低 | 直接迁移等于引入未知凭据，安全风险 |
| `user_address` | 地址 | 36 | MIGRATE_SCHEMA_AND_DATA | `addresses` | `uid→user_id`；`real_name→recipient_name`；`phone`；`province/city/district/detail`；`is_default→is_default`（Go 用 `default_key` 唯一约束，需重算）；`city_id` 需重映射到新 `regions.id` | 高 | 一个用户多条 `is_default=1` 会撞 Go 唯一约束 |
| `user_bill` | 资金/积分流水 | 429 | TRANSFORM_AND_MIGRATE | 新增 `wallet_ledger` / `points_ledger` | `category=1` → `wallet_ledger`；`category=2` → `points_ledger`；`pm`(1 收/2 支)→`direction`；`type`→`biz_type`；`number→amount`（分）；`balance→balance_after`；`link_id→ref_id` | 中 | 与 `now_money` 快照二选一，**不可同时入账**；`type` 取值需枚举对齐 |
| `user_recharge` | 充值单 | 325 | TRANSFORM_AND_MIGRATE（归档式） | 新增 `recharge_orders` | `order_id→order_no`；`price/give_price→amount/bonus`；`paid/pay_time`；`recharge_type`；`refund_price`；**仅 `paid=1` 且余额快照未覆盖时**才可入账 | 中 | 325 条若全部入账，余额会远超 `now_money` → 必须按"快照优先、流水归档"处理 |
| `user_sign` | 签到 | 15 | TRANSFORM_AND_MIGRATE | 新增 `points_ledger`(biz_type=sign) | `number→amount`；`balance→balance_after`；`create_time`；注意主键列名为 `int` | 低 | 主键列名异常，读取需别名 |
| `user_level` | 等级实例 | 0 | MIGRATE_SCHEMA_ONLY | 新增 `user_member_levels` | `level_id/grade/valid_time/is_forever/discount` | 中 | — |
| `system_user_level` | 等级配置 | 6 | SEED_DATA | 新增 `member_levels` | `name/money/grade/discount/valid_date/is_forever/is_pay/is_show` | 中 | `money` 为升级门槛，语义需 Owner 确认（累计消费 or 单笔） |
| `system_user_task` | 等级任务 | 18 | EXCLUDE（建议） | — | `task_type/number/level_id/is_must` 任务体系复杂且 `user_task_finish` 为 0 | 低 | 若要完整会员任务体系则成本翻倍 |
| `user_task_finish` | 任务完成 | 0 | MIGRATE_SCHEMA_ONLY | — | — | 低 | — |
| `user_extract` | 提现 | 0 | ARCHIVE | — | 依赖真实打款，不实现提现 | 无 | — |
| `user_avatar` | 头像素材 | 1 | EXCLUDE | — | 上传产物 | 无 | — |
| `user_group` | 用户分组 | 0 | EXCLUDE | — | 空表 | 无 | — |
| `user_enter` | 商户入驻 | 0 | EXCLUDE | — | 多商户体系不迁（Go 无 `mer_id` 概念） | 无 | — |

> **字符集注意**：`eshop_user.nickname` 等业务列在源库为 **utf8mb3**，4 字节 emoji 已被 Java `EmojiParser.removeAllEmojis` 剥离；迁移到 Go 的 `utf8mb4` 为"超集升级"，无截断风险，但也**不要期望还原 emoji**。

### 9.6 互动 / 内容 / 物流门店 / 地区

| 原表 | 行数 | 分类 | 对应 Go 表 | 转换要点 | 必要性 | 风险 |
|---|---|---|---|---|---|---|
| `store_product_relation` | 104 | TRANSFORM_AND_MIGRATE | `favorites` | `type='collect'` → `favorites`（唯一 `(user_id,product_id)`，需去重）；`type` 为足迹 → 需新增 `browse_history` 或丢弃 | 中 | 104 条中若同一用户重复收藏需去重 |
| `store_visit` | 0 | MIGRATE_SCHEMA_ONLY | 新增 `browse_history` | `product_id/uid/count/add_time` | 低 | — |
| `article` | 3 | TRANSFORM_AND_MIGRATE | `articles` | `title/synopsis→summary/content`；`image_input→cover`；`status`；`cid` 分类丢弃 | 低 | 正文含外链图片，需保留原 URL |
| `store_canvas` | 2 | EXCLUDE | — | 装修 JSON 与新前端强耦合 | 无 | — |
| `material` / `material_group` / `qiniu_content` | 242/4/27 | EXCLUDE | — | 素材库与对象存储索引（`local_storage` 归入 §9.7） | 无 | — |
| `wechat_live` / `wechat_live_goods` / `wechat_media` / `wechat_menu` / `wechat_reply` / `wechat_template` | 0/3/0/1/1/4 | EXCLUDE | — | 全部依赖微信 openid/AppSecret | 无 | — |
| `express` | 4 | SEED_DATA | 新增 `express_companies` | `code/name` | 低 | — |
| `shipping_templates` | 5 | TRANSFORM_AND_MIGRATE | 新增 `freight_templates` | `name/type`；`region_info/appoint_info` JSON → 规则行 | 中 | JSON 内含地区 id，需随 `regions` 重映射 |
| `shipping_templates_region` | 23 | TRANSFORM_AND_MIGRATE | 新增 `freight_template_rules` | `first/first_price/continues/continue_price`（元→分）；`province_id/city_id` 重映射 | 中 | 首件/续件为 `decimal`，运费存在小数分风险 |
| `shipping_templates_free` | 26 | TRANSFORM_AND_MIGRATE | 新增 `freight_template_rules`(type=free) | `number`(满件)/`price`(满额) | 低 | 与 region 规则叠加顺序需定义 |
| `system_store` | 3 | MIGRATE_SCHEMA_ONLY（若不做门店核销则 EXCLUDE） | 新增 `stores` | `name/phone/address/day_time/valid_time/lat/lng` | 低 | 订单 `store_id/shipping_type` 需兼容 NULL |
| `system_store_staff` | 5 | EXCLUDE | — | 门店员工权限体系 | 无 | — |
| `system_city` | 3938 | **SEED_DATA** | 新增 `regions` | `city_id→id`（**保持原 id 以兼容 `user_address.city_id`**）；`parent_id`；`level`(1省/2市/3区)；`name`；`merger_name`；`area_code`；`lng/lat` | **高** | 若重编号则所有地址需二次映射；建议**直接复用原 id** |

### 9.7 管理端框架与运维（28 张，全部 EXCLUDE，附例外）

| 表 | 行数 | 分类 | 理由 |
|---|---|---|---|
| `menu`、`roles_menus`、`role`、`users_roles`、`roles_depts`、`dept`、`job` | 206/45/4/5/1/8/4 | EXCLUDE | Go 已有独立 `admins/roles/permissions/admin_roles/role_permissions` RBAC，模型不同 |
| `dict`、`dict_detail` | 6/12 | EXCLUDE | 框架字典 |
| `system_config` | 52 | **SEED_DATA（白名单键）** | 仅迁非敏感且已实现的配置：`integral_ratio`、`store_postage`、`store_free_postage`、`store_brokerage_ratio`、`store_brokerage_two`、`user_extract_min_price`。**含密钥/支付/社交凭据的键一律禁止** |
| `system_group_data` | 46 | **SEED_DATA（白名单）** | 仅 `yshop_recharge_price_ways`(2)、`yshop_sign_day_num`(7)、`yshop_seckill_time`(6) 有业务语义；首页 banner/menus/roll/热搜属装修，EXCLUDE |
| `system_attachment` | 7 | EXCLUDE | 附件记录 |
| `quartz_job`、`quartz_log` | 3/21 | EXCLUDE | Go 用 worker + 扫描器，不迁调度器 |
| `gen_config`、`column_config`、`gen_test` | 15/291/0 | EXCLUDE | 代码生成器元数据 |
| `local_storage` | 17 | EXCLUDE | 本地文件索引 |
| `monitor_server`、`visits`、`app_version`、`picture` | 1/6/0/0 | EXCLUDE | 运维/统计 |
| `alipay_config`、`email_config`、`qiniu_config` | 1/1/1 | **EXCLUDE（强制）** | **含第三方私钥/AK/SK/邮箱口令，禁止读取、禁止迁移、禁止入库** |
| `verification_code`、`store_customer` | 0/0 | EXCLUDE | 空表 |
| `log` | 9621 | EXCLUDE | 操作日志噪声 |

### 9.8 迁移实施的四类工作分离

| 工作类型 | 内容 | 载体 |
|---|---|---|
| **① Schema Migration** | 新增/修改表结构（§12） | `internal/migrations/sql/*.up.sql`（新编号区间） |
| **② 历史业务数据 Migration** | 用户/商品/SKU/库存/订单/优惠券/评价/收藏（§9.1-9.6） | `internal/logic/legacymigrate` 扩展 |
| **③ 字典初始化** | `regions`、`express_companies`、`member_levels`、充值方案、签到档位、秒杀时段 | 独立 seed migration（幂等 `INSERT ... ON DUPLICATE KEY`） |
| **④ 新业务功能开发** | 钱包/积分/拼团/砍价/会员/分销 | B1-04（不属于迁移） |

---

## 10. Go 当前能力对照（第五部分）

**Go 事实基线**：37 张表 / 25 个迁移文件（24 up + 1 down）/ OpenAPI 112 路径 142 operations / 21 个 `api/*/v1` 包。

| 业务域 | Java 实现情况 | 原 SQL 表 | 前端需求 | Go 已有能力 | Go 缺失能力 | 推荐迁移 |
|---|---|---|---|---|---|---|
| 认证 | REAL（`AuthController` 4 接口；登录在缺失模块） | `eshop_user` | PC/APP 均需手机号登录 | ✅ `iam`：注册/登录/refresh/me/logout/会话管理/用户禁用（`api/iam/v1`） | ❌ 手机号+短信验证码注册；❌ 微信登录 | 复用现有；补 phone 登录 |
| 用户资料 | REAL（`/userinfo`、`/user/edit`） | `eshop_user` | 昵称/头像/手机号/生日 | ⚠️ 仅 `username/password_hash/status` | ❌ `user_profiles`（nickname/avatar/phone/birthday/gender） | **P0 补齐** |
| 商品/分类 | REAL | `store_product`/`store_category` | 列表/详情/分类树 | ✅ `product`（含 `product_images`、`view_count`；`/products` 已支持 `keyword`+`category_id`+`sort`）、`categories` | ❌ 一个商品多分类（原 `cate_id` 为逗号串）；❌ `subtitle/original_price/unit/sales_count`；⚠️ 排序字段不含销量 | 复用；轻量扩展 |
| SKU 规格 | REAL（规格组+SKU 组合） | `store_product_attr`/`_attr_value`/`_attr_result` | `van-sku` 需 `attr[]`+`productValue{}` | ⚠️ `skus` 仅 `name/price/status`，无规格 | ❌ 规格组/规格值/组合指纹/`sku_code` | **P0 补齐（最大缺口）** |
| 购物车 | REAL | `store_cart` | 加购/改数/删除/勾选 | ✅ `cart_items`（含 `selected`、`price_snapshot`） | ❌ 活动维度（拼团/秒杀/砍价）标记 | 复用；按需加 `biz_type/biz_id` |
| 订单 | REAL | `store_order`/`order_cart_info`/`status` | 确认/算价/创建/取消/收货/删除 | ✅ `order`：创建/列表/详情/Pay/Cancel/Receive/Ship/AdminList/超时取消；状态机 7 态；幂等键+`request_hash`；金额分 | ❌ 运费；❌ 优惠明细口径（仅有 `discount_amount`）；❌ 积分抵扣；❌ 支付渠道；❌ 状态流水表；❌ 门店自提 | 复用为主，补齐金额明细与流水 |
| 支付 | MOCK（余额/积分）+ 微信 | `store_order.pay_type` | Mock 支付 | ✅ `order.Pay`（幂等、仅 10→20） | ❌ 余额支付；❌ 支付渠道字段 | **P0 钱包 + 余额支付** |
| 退款 | PARTIAL（申请→执行） | `store_order.refund_status/refund_*` | 申请退款 | ✅ `refund`：Apply/Revoke/Detail/List/AdminList/Review/Execute/Close + `refund_audits` + `active_key` 唯一 | — | 复用（已优于原项目） |
| 库存 | UNVERIFIABLE | 5 处分散字段 | 下单校验 | ✅ `inventories` + `inventory_logs`（`DeductInTx`/`IncreaseInTx`） | ❌ 多仓/门店库存 | 复用；**迁移时只取 SKU 级** |
| 优惠券 | REAL（3 层） | `store_coupon`/`_issue`/`_issue_user`/`_user` | 领券/我的券/下单可用券 | ✅ `coupon_templates` + `user_coupons`（Claim 原子闸门/LockInTx/MarkUsedInTx/ReleaseInTx/GetDiscount；`uk_user_template` 已保证同模板仅 1 张） | ❌ 发行批次表；❌ 券类型（通用/商品/内部）；❌ 权益快照列；⚠️ 限领固定为 1（`uk_user_template`），不可配置 | 复用 + 增批次/范围 |
| 秒杀 | MOCK（时段+表内扣减） | `store_seckill`(0) | PC 装修块/APP 无 | ✅ `flash_sale_*` 5 表 + Redis Lua 闸门 + 请求表 + Outbox + 消费幂等 + 对账 | ❌ 活动时段"档位"概念（可选） | **不迁原模型**，仅对齐展示契约 |
| 拼团 | MOCK（团状态真实） | `store_combination`/`store_pink` | 首页拼团位；APP 死链 | ❌ 无 | ❌ 活动/团实例/成员/成团判定/失败退款 | **P1 新建** |
| 砍价 | MOCK（助力记录真实） | `store_bargain`/`_user`/`_user_help` | 无页面 | ❌ 无 | ❌ 活动/实例/助力/封顶价 | **P2 新建** |
| 积分 | REAL | `eshop_user.integral`、`user_bill` | APP 签到、下单抵扣 | ❌ 无 | ❌ 账户/流水/抵扣/返还/签到 | **P1 新建** |
| 会员 | PARTIAL | `system_user_level`/`user_level` | 无页面 | ❌ 无 | ❌ 等级/折扣/升级 | **P2 新建** |
| 分销 | REAL | `spread_uid`/`brokerage_price`/`user_bill` | 无页面 | ❌ 无 | ❌ 关系/佣金/回冲 | **P2 新建（不含提现）** |
| 充值/余额 | MOCK（完整状态） | `user_recharge`(325)、`now_money` | PC+APP 充值页 | ❌ 无 | ❌ 钱包/充值单/充值方案/余额支付 | **P1 新建（P0 优先余额）** |
| 物流 | API_ONLY 第三方 | `express`、`delivery_*` | 查看物流 | ⚠️ `Ship` 仅状态推进 | ❌ 运单号/快递公司/运费模板 | 部分补齐（运费 P1，轨迹 DROP） |
| 门店核销 | PARTIAL | `system_store`/`store_staff`、`verify_code` | 无 | ❌ 无 | ❌ 门店/核销码/自提 | **DROP（本轮）** |
| 微信 | REAL（外部） | `wechat_*` 6 表 | 无（H5 栈） | ❌ 无 | ❌ 全量 | **DROP** |
| 内容/装修 | REAL | `article`、`store_canvas`、`system_group_data` | PC 首页/装修块 | ✅ `article`(+like/favorite)、`banner`、`recommend_*` | ❌ 装修 JSON 渲染 | 复用 |
| 评价/收藏/点赞 | REAL | `store_product_reply`/`store_product_relation` | 有 | ✅ `review`、`favorite`、`like`（商品+文章） | — | 复用 |
| 管理端 | 独立 admin 模块（缺失） | 28 张框架表 | 无 | ✅ `admin` 15 路径、`dashboard` 5 路径 | ❌ 商家端多租户 | 复用；不做多商户 |

**复用红线（不得重复实现）**：`order`（状态机+幂等+超时）、`inventory`（事务内扣减+流水）、`coupon`（锁定/核销/释放事务语义）、`flashsale`（Redis Lua 准入 + 请求表 + Outbox + 消费幂等 + 对账 + 熔断限流）、`refund`（审批审计 + 库存/金额回滚）。新增业务**必须调用**这些既有能力，不得旁路。

---

## 11. 业务功能迁移建议与成本评估（第六部分）

评分口径：复杂度基于**现有 Go 架构下的增量工作量**，不是"业务名词难度"。

| 功能 | 原项目实现程度 | 外部依赖 | 需新增表 | Go 复杂度 | 涉及 Go 模块 | 事务/并发风险 | 展示价值 | 优先级 |
|---|---|---|---|---|---|---|---|---|
| 用户资料（手机/昵称/头像/生日） | REAL | 无 | `user_profiles`（或 ALTER users） | **低** | `iam`、`api/iam` | 无（单行更新） | 中（基础能力） | **P0** |
| SKU 规格（规格组/值/组合） | REAL | 无 | `spec_groups`、`spec_values`，`skus` 增列 | **中** | `product`、`sku`、`cart`、`order` | 中（下单需按 `sku_code` 定价，防伪造） | **高**（电商核心） | **P0** |
| 订单金额明细（运费/优惠/积分） | REAL | 无 | ALTER `orders`（`freight_amount/points_discount/member_discount`） | **中** | `order` | **高**（算价必须服务端唯一真源） | 高 | **P0** |
| 钱包余额 + 余额支付 | MOCK（真实状态） | 无 | `wallets`、`wallet_ledger` | **中** | 新 `wallet` + `order.Pay` 扩展 | **高**（余额扣减与订单状态必须同事务/幂等） | **高** | **P0** |
| 充值（方案 + 模拟充值） | MOCK（完整） | 无 | `recharge_plans`、`recharge_orders` | 低 | `wallet` | 中（幂等、记账一次） | 中 | P1 |
| 积分（账户/流水/抵扣/签到） | REAL | 无 | `points_accounts`、`points_ledger`、`sign_in_records` | 中 | 新 `points` + `order` | 中（抵扣需锁定+返还） | 中高 | P1 |
| 运费模板 | REAL | 无 | `freight_templates`、`freight_template_rules` | 中 | `order` | 低（纯计算） | 中 | P1 |
| 拼团 | MOCK（团状态真实） | 无 | `group_buy_activities/instances/members` | **中高** | 新 `groupbuy` + `order` + `refund` | **高**（成团/失败退款/库存回滚） | **高**（差异化） | P1 |
| 砍价 | MOCK（助力真实） | 无 | `bargain_activities/instances/helps` | 中 | 新 `bargain` + `order` | 中（助力并发、金额封顶） | 高 | P2 |
| 会员等级与折扣 | PARTIAL | 无 | `member_levels`、`user_member_levels` | 中 | 新 `member` + `order` | 中（折扣入算价并快照） | 中高 | P2 |
| 分销佣金 | REAL（结构）/ 数据为空 | 无（提现除外） | `distribution_relations` + 复用 `wallet_ledger` | **中高** | 新 `distribution` + `order` 支付/退款钩子 | **高**（佣金必须随退款回冲） | 高 | P2 |
| 门店核销/自提 | PARTIAL | 无 | `stores`、`verify_codes` | 中 | `order` | 中 | 低（与线上商城重复） | **DROP（本轮）** |
| 提现 | PARTIAL / 0 数据 | **真实打款** | `withdrawals` | 高 | `wallet` | 高 | 低 | **DROP** |
| 微信登录/支付/订阅消息 | REAL | **AppSecret/商户号** | 多表 | 高 | 全链路 | 高 | 低（H5 无载体） | **DROP** |
| 第三方物流轨迹 | API_ONLY | **快递聚合 API** | — | 低（接 SDK） | `order` | 低 | 低 | **DROP** |
| 支付宝 | 配置表（凭据） | **私钥** | — | 高 | 支付 | 高 | 低 | **DROP（且禁止读取凭据）** |
| 多商户 `mer_id` | 结构存在、无真实多租户 | 无 | 大量 | 高 | 全域 | 高 | 低 | **DROP** |
| 页面装修 `store_canvas` | REAL | 无 | — | 中高 | 前端为主 | 无 | 低 | **DROP** |
| 管理端框架（菜单/定时/代码生成/素材/日志） | REAL | 无 | — | 高 | — | 无 | 无 | **DROP** |

**结论**：原项目 **拼团/砍价/积分/会员/分销/充值 全部为"无第三方依赖的 Mock 或内部状态业务"**，技术上均可在 Go 侧用合理 Mock 实现；差异只在成本与风险，不在可行性。因此没有任何一项因"MOCK"而被排除。

---

## 12. 数据模型改造建议（第七部分）

### 12.1 最终 Go 业务模块（模块化单体，不做微服务）

```
internal/logic/
  iam            （既有：用户账号/会话/禁用）
  profile        （新：昵称/头像/手机号/生日，1:1 users）
  product        （既有：商品/图片/浏览数）
  category       （既有）
  sku            （既有 → 扩展规格）
  inventory      （既有：唯一库存真源）
  cart           （既有）
  order          （既有：状态机/幂等/算价）
  wallet         （新：余额 + 余额支付 + 充值）
  points         （新：积分账户/流水/抵扣/签到）
  coupon         （既有 → 扩展批次/范围/快照）
  flashsale      （既有：不改）
  groupbuy       （新：拼团）
  bargain        （新：砍价）
  member         （新：等级/折扣）
  distribution   （新：推荐关系/佣金）
  refund         （既有：不改）
  review/favorite/like/recommend/article/banner/upload （既有）
  region         （新：省市区，只读字典）
  freight        （新：运费模板，被 order 调用）
```

**边界原则**

| 关系 | 规则 |
|---|---|
| 订单 ↔ 库存 | 库存唯一真源 `inventories`；订单只能经 `InventoryService.DeductInTx/IncreaseInTx` 变更；秒杀库存只在 `flash_sale_activity_skus`（`total_stock/sold`），**两者不共享列** |
| 订单 ↔ 营销 | 营销只提供"资格与价格"，不直接写订单；订单在**同一事务**内校验并落快照（`biz_type` + `biz_id` + `活动价`） |
| 订单 ↔ 钱包/积分 | 支付与抵扣必须在订单事务内完成余额/积分变更，且以 `wallet_ledger/points_ledger` 的**唯一业务键**（`biz_type+biz_id+direction`）保证幂等 |
| 支付边界 | 仅两条通道：`mock`（默认）+ `balance`。`channel` 字段预留 `wechat/alipay` 但**不实现**，返回明确未启用错误 |
| 外部服务边界 | 短信/微信/快递/OSS/支付网关全部 `MockProvider` 接口化，配置 `mock.enabled=true`，实现里只打日志 + 返回确定结果，**不引入 SDK** |
| 历史数据边界 | 历史数据只进 `legacymigrate` 通道；生产代码不得依赖 `legacy_id_map` 之外的原库字段；迁移数据一律标记 `data_source='legacy'` |

### 12.2 需新增的表（24 张）

| # | 表 | 关键列（金额=int 分） | 服务于 |
|---|---|---|---|
| 1 | `user_profiles` | `user_id`(UK)、`phone`(UK)、`nickname`、`avatar`、`gender`、`birthday`、`data_source` | 用户资料 |
| 2 | `regions` | `id`（**沿用原 city_id**）、`parent_id`、`level`、`name`、`merger_name`、`area_code`、`lng`、`lat` | 地区字典 |
| 3 | `spec_groups` | `product_id`、`name`、`sort` | SKU 规格 |
| 4 | `spec_values` | `group_id`、`value`、`sort` | SKU 规格 |
| 5 | `order_status_logs` | `order_id`、`from_status`、`to_status`、`action`、`operator_type`、`remark` | 订单审计 |
| 6 | `wallets` | `user_id`(UK)、`balance`、`frozen`、`commission_balance`、`version`(乐观锁) | 钱包 |
| 7 | `wallet_ledger` | `user_id`、`direction`、`amount`、`balance_after`、`biz_type`、`biz_id`、`idempotency_key`(UK) | 钱包流水 |
| 8 | `recharge_plans` | `name`、`amount`、`bonus`、`status`、`sort` | 充值 |
| 9 | `recharge_orders` | `order_no`(UK)、`user_id`、`plan_id`、`amount`、`bonus`、`status`、`paid_at` | 充值 |
| 10 | `points_accounts` | `user_id`(UK)、`balance`、`total_earned` | 积分 |
| 11 | `points_ledger` | `user_id`、`direction`、`amount`、`balance_after`、`biz_type`、`biz_id`、`idempotency_key`(UK) | 积分 |
| 12 | `sign_in_records` | `user_id`、`sign_date`（UK `user_id+sign_date`）、`points`、`streak` | 签到 |
| 13 | `freight_templates` | `name`、`type`、`status` | 运费 |
| 14 | `freight_template_rules` | `template_id`、`region_id`、`first_unit`、`first_fee`、`extra_unit`、`extra_fee`、`free_threshold` | 运费 |
| 15 | `coupon_batches` | `template_id`、`name`、`total`、`remaining`、`claim_start`、`claim_end`、`status` | 优惠券 |
| 16 | `group_buy_activities` | `product_id`、`sku_id`、`name`、`required_size`、`group_price`、`start_at`、`end_at`、`expire_hours`、`total_stock`、`sold`、`status` | 拼团 |
| 17 | `group_buy_instances` | `activity_id`、`leader_user_id`、`required_size`、`joined`、`status`、`expire_at`、`success_at` | 拼团 |
| 18 | `group_buy_members` | `instance_id`、`user_id`、`order_id`、`role`、`status`（UK `instance_id+user_id`） | 拼团 |
| 19 | `bargain_activities` | `product_id`、`sku_id`、`start_price`、`floor_price`、`help_min`、`help_max`、`max_helpers`、`quota`、`start_at`、`end_at`、`status` | 砍价 |
| 20 | `bargain_instances` | `activity_id`、`user_id`、`current_price`、`cut_amount`、`helper_count`、`status`、`expire_at` | 砍价 |
| 21 | `bargain_helps` | `instance_id`、`helper_user_id`、`cut_amount`（UK `instance_id+helper_user_id`） | 砍价 |
| 22 | `member_levels` | `name`、`grade`、`threshold_amount`、`discount_rate`、`status` | 会员 |
| 23 | `user_member_levels` | `user_id`(UK)、`level_id`、`expire_at` | 会员 |
| 24 | `distribution_relations` | `user_id`(UK)、`inviter_id`、`level`、`bound_at` | 分销 |

> 计数说明：会员与分销拆为 3 张独立表（`member_levels`、`user_member_levels`、`distribution_relations`），不可合并计数。

### 12.3 需修改的表（7 张）

| 表 | 变更 | 理由 |
|---|---|---|
| `users` | 不加列（资料入 `user_profiles`）；`username` 允许手机号 | 保持 IAM 稳定 |
| `skus` | 增 `sku_code`(UK)、`spec_key`、`spec_values` JSON、`original_price`、`weight` | 规格与 SKU 组合（对应 `unique`/`sku`） |
| `products` | 增 `subtitle`、`original_price`、`unit`、`sales_count`、`freight_template_id`、`data_source` | 对齐原商品字段 |
| `orders` | 增 `freight_amount`、`points_used`、`points_discount`、`member_discount`、`pay_channel`、`balance_paid`、`biz_type`、`biz_id`、`remark`、`data_source` | 金额明细 + 活动归属 + 历史标记 |
| `order_items` | 增 `sku_code`、`spec_text`、`original_price` | 快照可读性 |
| `user_coupons` | 增 `batch_id`、`snapshot_title`、`snapshot_amount`、`snapshot_threshold`、`scope`、`scope_product_ids` | 权益快照 + 批次 |
| `coupon_templates` | 增 `scope`、`scope_product_ids`、`per_user_limit` | 券类型与限领 |

### 12.4 不建议做的改造

- ⚠️ 外键沿用既有 Go 风格（现存 8 条：`products→categories`、`skus→products`、`inventories/inventory_logs→skus`、`addresses→users`、`order_items→orders`、`flash_sale_activity_skus→activities`、`recommend_items→positions`）。**强归属子表加 FK（`ON DELETE CASCADE/RESTRICT`）**；**流水/审计表（`wallet_ledger`/`points_ledger`/`order_status_logs`/`refund_audits`）不加 FK**，只建索引，避免删除路径被阻断与迁移期锁表
- ❌ 不引入 `decimal`（金额统一 `INT` 分）
- ❌ 不引入软删除统一基类（既有表用 `status`，`orders` 用状态机）
- ❌ 不为未来微服务预留 `service_id`/分库键
- ❌ 不迁移 `mer_id` 多商户维度

---

## 13. 秒杀兼容性分析（第七部分·专项）

| 维度 | 原 Java / SQL | Go 现有 | 兼容结论 |
|---|---|---|---|
| 模型 | `store_seckill`（29 字段，`time_id` 关联 `system_group_data.yshop_seckill_time` 的 6 个时段，`start_time/stop_time` 为 `date`） | `flash_sale_activities` + `flash_sale_activity_skus`（`total_stock/sold`） + `flash_sale_orders` + `flash_sale_order_requests` | **不同代模型**，不做字段级迁移 |
| 库存 | 活动表 `stock/sales/num` + SKU `seckill_stock/seckill_price` + 商品 `stock` | 活动 SKU `total_stock/sold`，与 `inventories` **完全分离** | ✅ 与 Go 设计一致（秒杀库存独立）。**迁移时严禁把 `seckill_stock` 写入 `inventories`** |
| 准入 | 无（直接下单，表内扣减） | Redis Lua 闸门（`EVAL flashSaleGateScript`，6 key：空值/活动/库存/已购/幂等/售罄）+ 用户级/活动级限流 + 排队上限 + 熔断；`runGate` 失败 → `recordFailure` + `CodeServiceUnavailable`（`flashsale.go:303-313`），即 **fail-closed 503** | Go 方案更强，**不得因迁移降级**。注：`redis.go:206` 的注释仍写"降级走纯 MySQL"，与实现不一致，属文档陈旧 |
| 异步 | 无 | `flash_sale_order_requests` 请求表 + Outbox（`published_at/publish_*`）+ Kafka 消费幂等 + `flash_sale_request_audits` | 保留 |
| 幂等 | 无（靠 `@NoRepeatSubmit`） | `idempotency_key` + `request_hash` + 唯一约束 | 保留 |
| 对账 | 无 | `ReconcileCache`/`convergeStock`/`convergeEndedActivity` | 保留 |
| 数据 | **0 行活动、0 行订单** | 已有运行时数据（不干预） | **无需迁移任何秒杀数据** |
| 前端契约 | `/seckill/list/{time}`、`/seckill/detail/{id}`、`/seckill/index`（返回 `seckillTime[]`，含 `status` 1 抢购中/2 即将开始/0 已结束） | `/flash-sale/*` 系列（Go 契约） | 若需对齐旧前端，仅在**契约层**加时段视图；不改内核 |

**结论**：秒杀是本项目**唯一存在"代际差"**的模块。正确做法是：**不迁移原表、不导入原数据、不改造既有链路**，只在需要时把"时段档位"作为活动的一个展示字段（`session_start_hour`），由 `flash_sale_activities.start_time/end_time` 派生。

---

## 14. 风险与待决事项（第八部分）

### 14.1 风险清单

| # | 风险 | 等级 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | Java 业务实现层整体缺失 | **高** | 无法确认原算价/扣减/事务细节 | 只按契约+SQL 事实重建；所有"规则假设"写入设计文档并加测试 |
| R2 | 密码为无盐 MD5，不可迁移 | **高（安全）** | 20 个历史账号无法登录 | 强制重置：`password_hash='!legacy'`，首次登录走短信/重置流程 |
| R3 | 余额双写导致金额翻倍 | **高（资金）** | 迁移后余额错误 | 只迁 `now_money` 期初快照；`user_bill`/`user_recharge` 仅归档，不入账 |
| R4 | 订单状态误读 | **高（资金/售后）** | 状态错乱导致重复退款 | 必须用 `change_type` 流水推导；迁移后跑一致性断言 |
| R5 | 库存三处来源冲突 | **高** | 超卖/少卖 | 只取 `store_product_attr_value.stock`；商品表 `stock` 与 `seckill_stock/pink_stock` 全部忽略 |
| R6 | 原库存在 License 用户数限制 | 中 | 迁移后行为差异 | 不迁移 License；Go 侧不实现 |
| R7 | 原库含第三方凭据（支付/OSS/邮箱/七牛） | **高（安全）** | 泄露 | 明确 EXCLUDE；报告与本任务全程未读取凭据值 |
| R8 | 原库含个人信息（手机号/姓名/地址/微信 openid） | **高（合规）** | 隐私风险 | 仅统计行数；报告不出现任何原值；迁移脚本需脱敏开关 |
| R9 | `decimal(8,2)` → `INT` 分 | 中 | 精度/溢出 | 统一 `round(x*100)`；上限校验 99,999,999 分 |
| R10 | 分类 `uk_parent_name` 冲突 | 中 | 迁移中断 | 迁移前预检重名并合并 |
| R11 | 一人多默认地址撞唯一约束 | 中 | 迁移中断 | 按 `update_time` 取最新一条置默认 |
| R12 | 拼团/砍价/积分等新增业务与既有订单/退款耦合 | **高** | 回归破坏核心链路 | 强制"营销只读订单、订单只调服务"；新增链路必须有回归测试 |
| R13 | SKU 规格改造影响既有 `order/cart` 契约 | **高** | 破坏已完成能力 | `skus.name` 保持兼容，新增列全部可空；契约向后兼容 |
| R14 | 前端契约碎片化（PC 3 个无效端点、APP 拼团死链） | 低 | 误导实现 | 以 Java Controller 为准，前端缺漏记录不实现 |
| R15 | 历史报告与本报告的旧结论 | 低 | 决策混淆 | 以本报告为最终依据；旧报告保留不删除 |

### 14.2 待 Owner 决策（5 项）

| # | 问题 | 选项 | 建议 |
|---|---|---|---|
| D1 | 历史用户密码策略 | A 强制重置（安全） / B 保留 MD5 双算法兼容 | **A**（B 会长期留弱哈希） |
| D2 | 余额与账单的迁移口径 | A 只迁余额快照（账单归档） / B 重放全部流水 | **A**（B 无法保证与快照一致） |
| D3 | 会员门槛语义 | A 累计消费 / B 单笔消费 / C 积分 | **A**（`system_user_level.money` 更像累计） |
| D4 | 分销层级 | A 仅一级 / B 两级（原库有 `store_brokerage_two`） | **A**（两级佣金+退款回冲风险高，展示价值增量有限） |
| D5 | 拼团成团失败处理 | A 自动退款+退库存 / B 仅置失败待人工 | **A**（复用既有 refund 闭环，展示价值高） |

---

## 15. B1-03 固定实施任务：数据库模型与数据迁移（6 项）

> 约束：只做 Schema + 历史数据 + 字典 + 兼容，不做业务逻辑；不修改既有 24 个迁移文件（新增编号）。

| 编号 | 任务 | 交付内容 | 验收标准 |
|---|---|---|---|
| **B1-03-01** | 迁移基线与规范 | ① 新增迁移编号区间与命名规范；② `legacy_id_map.entity_type` 取值表；③ 迁移 dry-run 报告结构（`Result` 扩展）；④ `data_source` 约定 | ① 新迁移可 up/down 且 `migrate` 全绿；② 既有 25 个迁移文件零改动（`git diff` 为空）；③ dry-run 输出分类计数 |
| **B1-03-02** | 交易与用户 Schema | `user_profiles`；`regions`；`ALTER orders`（`freight_amount/points_used/points_discount/member_discount/pay_channel/balance_paid/biz_type/biz_id/remark/data_source`）；`ALTER order_items`（`sku_code/spec_text/original_price`）；`order_status_logs`；`freight_templates` + `freight_template_rules` | ① 迁移可重复执行；② 既有订单读取不受影响（`SELECT` 兼容测试）；③ 金额列全为 `INT UNSIGNED` |
| **B1-03-03** | 商品与 SKU Schema | `spec_groups`；`spec_values`；`ALTER skus`（`sku_code` UK、`spec_key`、`spec_values` JSON、`original_price`）；`ALTER products`（`subtitle/original_price/unit/sales_count/freight_template_id/data_source`） | ① 旧 `skus.name` 仍可写入（兼容）；② `sku_code` 唯一约束生效；③ 无规格商品（`spec_type=0`）可正常创建 |
| **B1-03-04** | 资金与积分 Schema | `wallets`；`wallet_ledger`；`recharge_plans`；`recharge_orders`；`points_accounts`；`points_ledger`；`sign_in_records` | ① 所有流水表具备幂等唯一键；② `wallets.balance` 与 `wallet_ledger.balance_after` 可对账；③ 无 `decimal` 列 |
| **B1-03-05** | 营销与关系 Schema | `coupon_batches`；`ALTER coupon_templates/user_coupons`（范围+快照）；`group_buy_activities/instances/members`；`bargain_activities/instances/helps`；`member_levels/user_member_levels`；`distribution_relations` | ① 唯一约束齐备（团成员、助力、限领）；② 拼团/砍价实例与订单通过 `biz_type/biz_id` 可追溯；③ down 脚本可完整回滚 |
| **B1-03-06** | 历史数据迁移与字典初始化 | ① `legacymigrate` 扩展：category/product/sku/inventory（已有）+ user/profile/address/order/order_item/order_status_log/review/favorite/coupon（新增）；② seed 迁移：`regions`(3938)、`express_companies`(4)、`member_levels`(6)、`recharge_plans`(2)、签到档位(7)、秒杀时段(6)；③ `data_issues` 报表（状态不一致/金额异常/关联缺失） | ① 迁移后行数校验：有效商品 54、SKU 111、订单 51、订单行 56、评价 13、收藏 104、用户 20、地址 36、券模板 2、批次 12、用户券 8；② `legacy_id_map` 覆盖全部迁移对象；③ 状态推导后各状态计数与 §9.4 一致；④ 重复执行幂等（二跑 0 变更） |

---

## 16. B1-04 固定实施任务：Go 后端业务功能迁移（8 项）

> 约束：优先复用既有 `order/inventory/coupon/flashsale/refund`；每个任务含单测 + 集成测试；不实现微信/支付宝/快递/提现/门店核销。

| 编号 | 任务 | 交付内容 | 验收标准 |
|---|---|---|---|
| **B1-04-01** | 用户资料与地区地址 | `profile` 模块（查询/更新昵称头像手机生日）；`region` 只读模块（省市区树/按父级）；地址接口增加 `region_id` | ① 资料读写与手机号唯一校验；② 省市区三级树返回 3938 节点；③ 地址默认唯一（`default_key`）并发安全 |
| **B1-04-02** | SKU 规格与商品详情 | `spec` 写入/读取（规格组、规格值）；SKU 组合生成 + `spec_key` 唯一；商品详情返回 `attr[] + productValue{}`；下单按 `sku_code` 定价 | ① 与前端 `van-sku` 契约一致（`attrName/attrValueArr/unique/sku`）；② 伪造 SKU 价格被拒（服务端定价）；③ 无规格商品回归通过 |
| **B1-04-03** | 订单金额明细与运费 | `freight` 模块（模板 + 区域规则 + 满额免邮）；`orders` 写入 `freight_amount/points_discount/member_discount/discount_amount` 明细；算价接口返回明细 | ① 运费按模板计算（首件/续件/免邮）；② 算价结果与订单落库金额逐项一致；③ 金额守恒：`original + freight - discount == total` |
| **B1-04-04** | 钱包、充值与余额支付 | `wallet` 模块：余额查询/流水；`recharge_plans` 查询；模拟充值（充值单→余额→流水三写）；`order.Pay` 增 `balance` 通道 | ① 充值幂等（同单只入账一次）；② 余额不足拒绝并回滚；③ 支付→取消→退款链路余额一致；④ 并发扣减不超支（乐观锁/行锁） |
| **B1-04-05** | 积分账户与抵扣 | `points` 模块：余额/流水/签到（连续签到）；下单积分抵扣与取消返还；支付成功后按 `integral_ratio` 发放 | ① 抵扣上限与余额校验；② 取消/退款精确返还（不重复）；③ 积分流水与账户可对账 |
| **B1-04-06** | 拼团 | `groupbuy`：活动列表/详情、开团、参团、团详情、取消开团；成团判定（`joined>=required`）；到期未成团失败；订单 `biz_type=groupbuy` 集成 | ① 同一用户同团不可重复参团（唯一约束）；② 并发参团不超员；③ 失败团自动退款+退库存（复用 `refund`）；④ 成团后订单正常流转 |
| **B1-04-07** | 砍价 | `bargain`：活动列表/详情、发起、助力（区间随机、封顶 floor）、助力列表、我的砍价、取消；砍后价下单 | ① 每个助手仅一次（唯一约束）；② 累计不破 `floor_price`；③ 下单价格由服务端校验（拒绝篡改）；④ 并发助力金额一致 |
| **B1-04-08** | 会员等级与分销佣金 | `member`：等级列表、按累计消费升级、等级折扣参与算价并**快照**；`distribution`：绑定推荐关系（注册/首单）、支付成功按比例记佣金（L1）、退款回冲 | ① 折扣只影响一次算价并写入 `member_discount`；② 佣金唯一键防重复；③ 退款后佣金同额回冲；④ 无提现入口 |

---

## 17. B1-05 固定实施任务：后端最终验收（4 项）

| 编号 | 任务 | 交付内容 | 验收标准 |
|---|---|---|---|
| **B1-05-01** | 构建与单元测试 | 全量 `go build ./...`、`go vet`、单测覆盖新增模块 | ① 0 编译错误；② 新增逻辑单测通过率 100%；③ 覆盖率不低于既有基线 |
| **B1-05-02** | 集成测试与 Migration 验证 | 端到端：注册→浏览→加购→算价（券+积分+运费）→下单→余额支付→发货→收货→评价；迁移：up→数据迁移→校验→down | ① 主链全绿；② 迁移行数校验与 §15-06 一致；③ 二跑幂等；④ down 后重建成功 |
| **B1-05-03** | 并发、事务与秒杀回归 | 并发：库存扣减、余额扣减、拼团参团、砍价助力、优惠券领取；秒杀：Lua 闸门/售罄/重复购买/Redis 故障 fail-closed/Kafka 幂等 | ① 无超卖、无负余额、无超员；② 秒杀既有测试全绿且**未修改内核**；③ Outbox 无丢失/无重复消费 |
| **B1-05-04** | OpenAPI 核对与交付报告 | 契约与实现逐条核对（新增路径/字段/错误码）；迁移映射矩阵回填实测行数；最终报告 | ① 契约校验门禁通过；② 文档与实现 0 偏差（或显式登记）；③ 交付 `docs/phase2/B1-05_FINAL_REPORT.md` |

**任务总量固定为：B1-03（6）+ B1-04（8）+ B1-05（4）= 18 项，不设 B1-06。**

---

## 18. 给 Owner 的汇报（第八部分）

1. **三个项目是否全部恢复**：是。PC（`tmp/eshop-pc-vue`，242 文件）、APP（`tmp/eshop-app-vue`，71 文件）、Java（`tmp/eshop-backend-java`，147 `.java`）、SQL（`tmp/shopping.sql`，5.5 MB/18,835 行）均在位。**但 Java 只恢复到 2 个 Maven 模块（应 ≥6），Service/Mapper/Domain/Enum 全部缺失**——这是本轮最重要的新事实。

2. **Java 后端规模**：10 个业务域（认证、商品/SKU、购物车、订单、支付、退款、优惠券、营销活动、用户资金、内容），**19 个 Controller、113 个接口**（63 GET + 50 POST）。

3. **真实实现 / Mock / 部分实现**：
   - 真实（无外部依赖且有状态变化）：订单、优惠券、积分、分销、评价/收藏、内容
   - Mock（有完整状态但无第三方）：充值、拼团、砍价、秒杀
   - 部分实现：退款、会员、门店核销
   - 仅接口封装：物流轨迹（第三方）
   - 无法验证：库存扣减、具体算价公式、事务边界（源码缺失）

4. **PC / APP 能力**：两端都只覆盖"首页→分类/搜索→详情→购物车→确认订单→支付→订单→退款申请→评价→地址→收藏/足迹→充值"。**拼团仅 APP 有 API 封装且无页面（死代码 + 死链）**；砍价、积分商城、会员、分销两端均**无页面**；秒杀在 PC 仅一个装修展示组件。

5. **SQL 规模**：**83 表 / 992 字段 / 83 条索引 + 11 条外键（外键全在管理端框架表，业务表 0 外键） / 16,692 条 INSERT**。真实业务数据仅 **2,380 行（14.3%）**，另有地区字典 3,938 行、框架与运维 10,374 行。

6. **值得迁移的 SQL 数据**：分类 57、有效商品 54、SKU 111、库存 111、用户 20、地址 36、订单 51、订单行 56、状态流水 132、优惠券（模板 2/批次 12/用户券 8）、评价 13、收藏 104、积分期初 7 人、余额期初 14 人、地区 3938（字典）。**不迁**：`log`(9621)、框架表、微信表、装修 JSON、凭据表、秒杀/团/砍价参与（0 行）。

7. **值得在 Go 重写的 Java 功能**：钱包+余额支付（原库 51/51 订单都用余额支付）、积分、拼团、砍价、会员折扣、分销 L1、运费模板、SKU 规格。**不建议**：微信/支付宝/快递/提现/门店核销/多商户/装修。

8. **难点真实评估**：拼团（中高，涉及成团+失败退款）、砍价（中，涉及并发助力与价格封顶）、积分（中，涉及抵扣与返还）、会员（中，折扣入算价）、分销（中高，佣金需随退款回冲）、充值（低，三表一致写入）。**没有一项因"是 Mock"而不可做**。

9. **Go 最重要缺口**：① **SKU 规格模型**（`skus` 只有 `name`，无法支撑 `van-sku` 与历史 SKU）；② **用户资料**（无 nickname/phone/avatar）；③ **钱包/余额**（历史订单 100% 余额支付）；④ **订单金额明细**（无运费/积分/会员折扣列）；⑤ 优惠券**批次**与**权益快照**。

10. **最终推荐模型与迁移方案**：新增 24 张表、修改 7 张表；金额统一 `INT` 分；`regions` 沿用原 `city_id` 保证地址兼容；订单状态由 `store_order_status.change_type` 推导而非读 `status`；库存只取 SKU 级；余额/积分只迁期初快照、流水归档；秒杀**不迁移**。

11. **固定计划**：B1-03（6 项）→ B1-04（8 项）→ B1-05（4 项），共 18 项，无 B1-06。

12. **需 Owner 决策**：D1 密码策略（建议强制重置）、D2 余额迁移口径（建议只迁快照）、D3 会员门槛语义（建议累计消费）、D4 分销层级（建议仅一级）、D5 拼团失败处理（建议自动退款）。

---

## 附录 A：本次审查使用的只读命令与事实来源

| 事实 | 来源 |
|---|---|
| Git 基线/同步/工作区 | `git rev-parse HEAD`、`git status -sb`、`git branch -a` |
| Java 模块与依赖 | `tmp/eshop-backend-java/pom.xml`、`eshop-app/pom.xml`、`eshop-business/pom.xml`、`.idea/modules.xml` |
| Java 缺失依赖量化 | 对 `eshop-app/src/main/java` 的 142 个源文件做 `import com.eshop.*` 与源树比对 |
| Java 路由清单 | 19 个 Controller 的 `@GetMapping/@PostMapping` 统计（63+50） |
| Java 业务语义 | `StoreOrderController`、`OrderSupplyService`、`StoreBargainController`、`StoreCombinationController`、`CouponController`、`StoreSeckillController`、`UserRechargeController`、`UserBillController`、`UserLevelController`、`AuthController`、`AuthService` |
| 登录/License 事实 | `tmp/eshop-backend-java/logs/yshop-app/error.log` |
| SQL 结构/行数 | 离线 Python 解析（只统计 DDL 与 INSERT 条数，**不输出任何行内容**） |
| 订单状态语义 | `store_order_status.change_type` 聚合分布 |
| 规格指纹结构 | `store_product_attr_result` JSON 键结构（仅键名，不含值） |
| 前端契约 | `eshop-pc-vue/src/api/index.js`、`eshop-app-vue/src/api/*.js`、`views/productDetails/productDetails.vue` |
| Go 现状 | `internal/migrations/sql/*`、`internal/logic/{order,coupon,refund,flashsale,inventory}/*`、`api/*/v1/*`、`openapi/paths/*` |

## 附录 B：数据安全声明

- 本报告**未包含**任何真实姓名、手机号、收货地址、微信 openid、身份证、邮箱、密码、AK/SK、支付私钥或第三方账号。
- 原始 SQL 中的 `alipay_config`、`qiniu_config`、`email_config` 三张表被明确标记为**禁止读取/禁止迁移**，审查过程中未读取其值。
- 源库主机与库名仅用于确认 dump 来源，本报告已脱敏为"内网地址"。
- 所有统计均为**行数/键名/枚举分布**级别的聚合结果。
