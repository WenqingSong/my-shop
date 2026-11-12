-- 回滚砍价基础模型 V1。
-- 破坏性说明（数据丢失）：bargain_help_records / bargain_instances / bargain_activities 三张本次新增表
-- 整体删除，砍价活动、参与实例与助力记录全部丢失；不触碰 orders、inventories、flash_sale_* 与任何既有表。
-- 删除顺序必须先子后父：help_records（FK→instances）→ instances（FK→activities）→ activities（FK→products/skus）。

DROP TABLE bargain_help_records;
DROP TABLE bargain_instances;
DROP TABLE bargain_activities;
