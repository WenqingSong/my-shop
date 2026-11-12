-- 回滚运费模板 V1 批次。
--
-- 破坏性说明：本 down 会删除 freight_templates / freight_template_rules 两张表以及
-- products.freight_template_id 列，**模板、区域规则与商品绑定关系数据全部丢失且不可恢复**。
-- 仅用于测试库验证与人工显式回滚；生产发布流程不执行 down（见 docs/design/migration.md）。
--
-- 顺序：先解除 products 对模板的引用列，再删规则表，最后删模板表（避免外键依赖顺序问题）。

ALTER TABLE products
  DROP KEY idx_freight_template_id,
  DROP COLUMN freight_template_id;

DROP TABLE IF EXISTS freight_template_rules;

DROP TABLE IF EXISTS freight_templates;
