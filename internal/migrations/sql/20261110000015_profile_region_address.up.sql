-- B1-04-01：用户资料生日字段 + 地址结构化地区引用（region_id）。
-- 事实来源：B1-03 实际交付 schema。users 已有 nickname/avatar_url/phone（20261110000002），
--   本迁移只补齐缺失的 birthday；不创建 user_profiles 表，不改既有认证列。
-- addresses 已有省/市/区自由文本列与 default_key 生成列（20261001000005），本迁移只新增
--   可空 region_id，用于引用 regions.level=3（区/县）节点；不重建、不导入 regions。
-- 兼容性：
--   - birthday 允许 NULL，历史用户保持 NULL（未设置生日）；
--   - region_id 允许 NULL，历史地址保持 NULL，读写均按「无结构化地区」处理；
--   - 不修改任何历史地址数据，不新增/修改唯一约束（uk_user_default 保持原样）。
-- 索引：idx_region_id 支撑按地区反查地址；region_id 为软引用（不建外键），
--   与 regions 无 FK 的既有约定一致（regions.parent_id 无法用自引用外键表达省级 parent_id=0）。

ALTER TABLE users
  ADD COLUMN birthday DATE NULL AFTER phone;

ALTER TABLE addresses
  ADD COLUMN region_id BIGINT UNSIGNED NULL AFTER district,
  ADD KEY idx_region_id (region_id);
