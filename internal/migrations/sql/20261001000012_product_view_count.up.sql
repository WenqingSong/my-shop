-- 商品浏览量计数 V1：products 新增 view_count 列（累计浏览量）。
-- 说明：经 golang-migrate 机制新增（version 20261001000012，紧随 flash_sale 20261001000011）。
-- 仅加列、不新增表；DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 计数语义：V1 总浏览量（每次前台公开详情访问 +1，不去重），BIGINT UNSIGNED 上限 ~1.8e19 不会溢出。
-- 计数写入：前台详情单条条件 UPDATE SET view_count = view_count + 1 原子自增（InnoDB 行锁串行化并发），
-- 并显式 updated_at = updated_at 规避 ON UPDATE CURRENT_TIMESTAMP 把「浏览」误记为「最近更新」。
-- 不建索引（V1 无按浏览量排序/排行需求）。

ALTER TABLE products ADD COLUMN view_count BIGINT UNSIGNED NOT NULL DEFAULT 0;
