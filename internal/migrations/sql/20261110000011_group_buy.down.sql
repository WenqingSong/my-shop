-- 回滚拼团基础模型 V1。
-- 破坏性说明（数据丢失）：group_buy_participants / group_buy_teams / group_buy_activities 三张本次新增表
-- 整体删除，拼团活动、团实例与参与记录全部丢失；不触碰 orders、inventories、flash_sale_* 与任何既有表。
-- 删除顺序必须先子后父：participants（FK→teams）→ teams（FK→activities）→ activities（FK→products/skus）。

DROP TABLE group_buy_participants;
DROP TABLE group_buy_teams;
DROP TABLE group_buy_activities;
