# 全局 Migration Version Registry

本文件是 **migration version 分配**的权威事实源（`.agent/registry/`，位于 `develop` 上）。任何需要新增 migration 的 Task，必须在进入 Coder 前，通过一个只改 Registry 文件的 commit 把 `RESERVED` 条目提交到 `develop`，才能使用对应 version；仅在 Feature Branch 内自行声明不构成有效预留。

机制、生命周期与角色职责见 `.agent/specs/AgentCollaborationSpecification.md`（跨任务全局资源预留）与 `.agent/specs/AgentResponsibilityBoundary.md`。

## 状态与生命周期

- `RESERVED`：Reservation 已通过「只改 Registry 的 commit」落到共享 `develop` 生效，但拥有该资源的 Feature 尚未合并进 `develop`。
- `ACTIVE`：已合并进 `develop`，资源在 `develop` 实际生效（终态）。
- `RELEASED`：Task 取消释放（记录保留）。

转换：`RESERVED → ACTIVE`（feature 合并进 `develop` 时由合并任务同步）；`RESERVED → RELEASED`（Task 取消时由 Analyst/Owner 标记）。

复用规则：migration version 一旦被 Reservation 分配，即使 Task 后续取消也永久保留 tombstone、**不得复用**；`next` 恒为 `max(所有已记录 version, 含 RELEASED) + 1`。

## 分配规则

- 保留现有 `YYYYMMDD + 序号` 的实际格式，`next = max(所有已记录 version, 含 RELEASED) + 1`。
- **禁止 Coder 根据当前 migration 最大值自行 `max+1`**。

## 分配表

| version | title | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- | --- |
| 20261001000001 | baseline | db-migration | ACTIVE | 基线 |
| 20261001000002 | products | db-migration | ACTIVE | 基线 |
| 20261001000003 | skus | db-migration | ACTIVE | 基线 |
| 20261001000004 | inventory | db-migration | ACTIVE | 基线 |
| 20261001000005 | addresses | db-migration | ACTIVE | 基线 |
| 20261001000006 | cart_items | db-migration | ACTIVE | 基线 |
| 20261001000007 | orders | order-v1 | ACTIVE | orders + order_items |
| 20261001000008 | refresh_tokens | iam-v4 | ACTIVE | IAM V4 refresh token 表（前台用户域） |
| 20261001000009 | reviews | product-review-v1 | ACTIVE | reviews 评价表 |
| 20261001000010 | favorites | product-favorite-v1 | ACTIVE | favorites 收藏表 |
| 20261001000011 | flash_sale | flash-sale-v1 | ACTIVE | flash_sale_activities + flash_sale_activity_skus + flash_sale_orders |
| 20261001000012 | product_view_count | product-view-count-v1 | ACTIVE | products.view_count 浏览量计数字段 |
| 20261001000013 | product_likes | product-like-v1 | ACTIVE | product_likes 点赞表 |
| 20261001000014 | banners | banner-v1 | ACTIVE | banners 轮播图表 |
| 20261001000015 | flash_sale_order_requests | flash-sale-v3 | ACTIVE | flash_sale_order_requests 异步下单请求表 |
| 20261001000016 | recommend | recommendation-v1 | ACTIVE | recommend_positions + recommend_items 推荐位两表 |
| 20261001000017 | articles | article-cms-v1 | ACTIVE | articles + article_likes + article_favorites 文章三表 |
| 20261001000018 | flash_sale_request_audits | flash-sale-v4 | ACTIVE | flash_sale_request_audits 秒杀请求人工修复审计表 |
| 20261001000019 | user_status | iam-v5 | ACTIVE | users 增 status/auth_epoch、refresh_tokens 增 auth_epoch、新增 user_status_audits 审计表 |
| 20261001000020 | refund | order-refund-v1 | ACTIVE | refund_requests + refund_audits 退款两表 |
| 20261001000021 | coupon | coupon-v1 | ACTIVE | coupon_templates + user_coupons 优惠券两表 |
| 20261001000022 | order_coupon_amounts | coupon-v1 | ACTIVE | orders 增 original_amount/discount_amount/user_coupon_id 三列 + 回填 |
| 20261001000023 | legacy_id_map | legacy-data-migration | ACTIVE | 目标库新增 legacy_id_map 旧 ID→新 ID 映射表 |
| 20261001000024 | flash_sale_request_outbox | flashsale-kafka-b2 | RESERVED | flash_sale_order_requests 增发布标记/租约/退避列及领取索引 idx_outbox_claim |
| 20261110000001 | product_spec | B1-03-01 | RESERVED | product_specs + product_spec_values + sku_spec_values 规格三表；skus 增 image_url/spec_signature + uk_product_signature（含 down） |
| 20261110000002 | user_profile | B1-03-01 | RESERVED | users 增 nickname/avatar_url/phone + uk_phone（含 down） |
| 20261110000003 | regions | B1-03-01 | RESERVED | regions 省/市/区三级地区字典，仅结构不导数据（含 down） |
| 20261110000004 | order_shipping_payment | B1-03-02 | RESERVED | orders 增 shipping_fee/pay_type/transaction_no/delivery_type/delivery_name/delivery_sn；order_items 增 spec_text（无回填；含 down） |
| 20261110000005 | order_status_logs | B1-03-02 | RESERVED | order_status_logs 订单状态流水表，append-only（含 down） |
| 20261110000006 | wallet | B1-03-02 | RESERVED | wallet_accounts + wallet_transactions 钱包账户与资金流水（含 down） |
| 20261110000007 | recharge_orders | B1-03-02 | RESERVED | recharge_orders Mock 充值订单（含 down） |
| 20261110000008 | points | B1-03-02 | RESERVED | user_points_accounts + user_points_logs 积分账户与流水（含 down） |
| 20261110000009 | coupon_snapshot | B1-03-02 | RESERVED | user_coupons 增 coupon_title/coupon_price/use_min_price/end_time 可空快照列（无回填；含 down） |
| 20261110000010 | coupon_batch | B1-03-03 | RESERVED | coupon_templates 增 discount_type/discount_rate_bp/per_user_limit；新增 coupon_batches + coupon_template_scopes；user_coupons 增 batch_id/claim_no，uk_user_template 替换为 uk_user_template_claim（含 down） |
| 20261110000011 | group_buy | B1-03-03 | RESERVED | group_buy_activities + group_buy_teams + group_buy_participants 拼团活动/团/参与记录（含 down） |
| 20261110000012 | bargain | B1-03-03 | RESERVED | bargain_activities + bargain_instances + bargain_help_records 砍价活动/实例/助力（含 down） |
| 20261110000013 | member | B1-03-03 | RESERVED | member_levels + user_memberships + member_growth_logs 等级/用户会员/成长值流水，不重复创建积分账本（含 down） |
| 20261110000014 | distribution | B1-03-03 | RESERVED | user_referrals + distribution_commissions 一级推荐关系与订单佣金（含 down） |
| 20261110000015 | profile_region_address | B1-04-01 | RESERVED | users 增 birthday DATE NULL；addresses 增 region_id（可空，软引用 regions.level=3）+ idx_region_id（含 down；不重建/不回填 regions）。B1-04 八分支集成核实：本版本号唯一，无其它分支占用 |
| 20261111000001 | sku_code_pricing | B1-04-02 | RESERVED | skus 增 sku_code（唯一）/original_price + uk_sku_code，历史行回填 SKU<id> 与售价；order_items 增 sku_code/original_price（含 down；不回算历史订单金额） |
| 20261111000002 | freight | B1-04-03 | RESERVED | freight_templates + freight_template_rules；products 增可空 freight_template_id（含 down）。B1-04 八分支集成：原拟 `20261110000016` 在已应用 `20261111000001` 的数据库上会被 golang-migrate 跳过（版本号更小），故整体上移至当前最高版本之后；该号未被任何环境应用过，语义不变 |
