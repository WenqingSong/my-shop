-- 地区字典 V1：省 / 市 / 区三级行政区划结构。
-- 说明：本迁移只建结构，不导入任何地区数据（数据导入属于独立的历史数据迁移任务）。
-- 与遗留 system_city 的兼容约定：
--   - regions.id 为 BIGINT UNSIGNED AUTO_INCREMENT，导入时允许显式指定原 system_city.city_id，
--     保证已有地址/历史数据里的地区 id 不需要二次映射；
--   - parent_id 对应 system_city.parent_id，省级为 0（沿用 categories 的「0 表示无父级」约定）；
--   - level 对应 system_city.level（1=省、2=市、3=区/县）；
--   - name 对应 system_city.name。
-- 层级与父子一致性由导入/写入方保证（不建外键、不建唯一约束）：
--   自引用外键要求父行先存在，无法表达「省级 parent_id=0」；唯一名称约束会阻碍历史数据中的重名/占位
--   节点导入。校验放在导入工具与后续应用层，避免在结构层制造导入阻塞。
-- 本迁移不修改 addresses：既有省/市/区自由文本列保持不变，结构化地区仅作为只读字典供后续任务接入。

CREATE TABLE regions (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  parent_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  level      TINYINT         NOT NULL,
  name       VARCHAR(64)     NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_parent (parent_id),
  KEY idx_level (level)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
