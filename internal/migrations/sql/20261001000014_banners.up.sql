-- 轮播图 V1：banners（轮播图主数据）。
-- 说明：经 golang-migrate 机制新增（version 20261001000014，紧随 product_view_count 20261001000012）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- image_url 为软引用（无 FK）：存可访问相对路径（/storage/banners/<file>），V1 不校验文件存在性。
-- link_url 为自由字符串（无跳转时 NULL），无 DB 级引用完整性。
-- sort 允许重复（无唯一约束），公开列表同值按 id 升序兜底。
-- idx_status_sort(status, sort) 支撑公开列表查询（WHERE status=1 ORDER BY sort）。
-- status：1=启用、0=禁用。

CREATE TABLE banners (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  title      VARCHAR(64)     NOT NULL,
  image_url  VARCHAR(255)    NOT NULL,
  link_url   VARCHAR(512)    NULL,
  sort       INT             NOT NULL DEFAULT 0,
  status     TINYINT         NOT NULL DEFAULT 1,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_status_sort (status, sort)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
