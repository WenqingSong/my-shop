-- 用户基础资料 V1 回滚。
-- 破坏性说明：DROP 三列会永久丢失已填写的昵称、头像与手机号，uk_phone 唯一约束一并移除；
--   账号、密码哈希、状态与认证版本不受影响，回滚后登录/会话逻辑仍可用。
--   回滚前必须先下线读取这些列的资料接口，否则查询会直接报错。

ALTER TABLE users
  DROP INDEX uk_phone,
  DROP COLUMN phone,
  DROP COLUMN avatar_url,
  DROP COLUMN nickname;
