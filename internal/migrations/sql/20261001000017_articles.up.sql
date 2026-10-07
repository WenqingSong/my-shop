-- 文章 V1：articles（文章主数据）+ article_likes（文章点赞）+ article_favorites（文章收藏）。
-- 说明：经 golang-migrate 机制新增（version 20261001000017，紧随并发预留的 recommendation-v1 16）。
-- DDL 不使用 IF NOT EXISTS（迁移只执行一次，由 schema_migrations 追踪）。
-- 作者身份唯一来源为登录上下文 Principal.UserID；author_id/user_id/article_id 均为软引用（无 FK）。
-- 点赞/收藏复用商品点赞/收藏的机制（幂等 + 唯一约束 + 公开计数 + 独立鉴权查询），但使用独立表，不改动 product_likes/favorites。
-- 文章为硬删除 + 单事务清理点赞/收藏关联数据（见 logic 层 Delete），故无 status/deleted_at 字段。

CREATE TABLE articles (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  author_id  BIGINT UNSIGNED NOT NULL,
  title      VARCHAR(64)     NOT NULL,
  content    TEXT            NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_author_id (author_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 文章点赞：对齐 product_likes 结构（user_id + article_id 二元关系，无 updated_at）。
-- uk_user_article(user_id, article_id) 兜底「同一用户同一文章至多一条」，兼作按用户查询索引。
-- idx_article_id(article_id) 支撑公开点赞数的实时 COUNT 聚合。
CREATE TABLE article_likes (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  article_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_article (user_id, article_id),
  KEY idx_article_id (article_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 文章收藏：对齐 favorites 结构（user_id + article_id 二元关系，无 updated_at）。
-- uk_user_article(user_id, article_id) 兜底「同一用户同一文章至多一条」，前导列 user_id 兼作「我的收藏」查询索引。
-- 无 idx_article_id：删除按 article_id 的扫描为低频可接受，与 favorites 一致。
CREATE TABLE article_favorites (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  article_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_article (user_id, article_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
