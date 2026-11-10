-- B1-04-01 回滚。
-- 破坏性说明：DROP users.birthday 会永久丢失已填写的生日；DROP addresses.region_id 会永久丢失
--   地址的结构化地区引用（province/city/district 文本列不受影响，地址本身仍可读写）。
--   uk_user_default 生成列与唯一约束、历史地址数据均不受影响；回滚后依赖 region_id 的接口必须同步下线。

ALTER TABLE addresses
  DROP INDEX idx_region_id,
  DROP COLUMN region_id;

ALTER TABLE users
  DROP COLUMN birthday;
