-- 用户基础资料 V1：users 增加 nickname / avatar_url / phone 与手机号唯一约束。
-- 说明：纯 additive 变更，不改既有认证列（username / password_hash / status / auth_epoch）及其语义，
--   不新增资料表、不改密码算法，不回填任何历史数据。
-- nickname / avatar_url：展示用，空串表示未设置，前端回退 username / 默认头像。
-- phone：允许 NULL（未设置）；已设置时必须唯一，由 uk_phone 在 DB 层强约束，并发下亦成立。
--   MySQL 唯一索引允许多个 NULL，因此「未设置手机号」的用户不受限制；
--   注意不得写入空串 ''（第二个空串会撞 uk_phone），未设置一律写 NULL。
--   手机号格式校验（大陆 11 位）由应用层负责，本迁移不引入 CHECK 约束，避免阻塞未来国际号码扩展。

ALTER TABLE users
  ADD COLUMN nickname   VARCHAR(32)  NOT NULL DEFAULT '' AFTER password_hash,
  ADD COLUMN avatar_url VARCHAR(512) NOT NULL DEFAULT '' AFTER nickname,
  ADD COLUMN phone      VARCHAR(20)  NULL AFTER avatar_url,
  ADD UNIQUE KEY uk_phone (phone);
