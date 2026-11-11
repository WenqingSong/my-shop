-- 推荐关系图全局变更锁（B1-04-FIX-01）。
--
-- 为什么需要一张专用锁表：
--   一级推荐关系是一张有向图（边方向：child → referrer），业务不变量要求「整张图无环」。
--   只锁定「本人 + 推荐人」两行无法阻止并发成环：A→B 与 B→A 两个事务各自只看到自己涉及的
--   两行，写后环检测使用的是一致性读（快照），可能都看不到对方尚未提交的插入，最终双双提交
--   形成 A→B→A。因此必须让「关系图变更」在同一时刻只有一个事务在进行。
--
-- 为什么复用 MySQL 而非进程内锁 / Redis 普通锁：
--   进程内 mutex、全局变量锁在多实例部署下完全失效；Redis 普通 SETNX 锁在锁过期、主从切换、
--   网络分区下无法与 MySQL 事务提交原子绑定。本表用 InnoDB 单行记录的排他行锁（SELECT ... FOR UPDATE）
--   实现：锁的生命周期与事务一致（提交/回滚才释放），跨连接、跨 goroutine、跨 Go 实例天然生效，
--   且与关系写入落在同一个数据库、同一个事务里，不存在「锁已释放但事务未提交」的窗口。
--
-- 锁协议（应用层，见 internal/logic/distribution 与 docs/design/distribution.md）：
--   1. 绑定事务的第一条语句：SELECT id FROM referral_graph_guard WHERE id = 1 FOR UPDATE；
--   2. 锁记录缺失 → fail-closed（拒绝写入，不降级为无锁）；
--   3. 锁内使用当前读（FOR UPDATE）重新读取本人关系与整条推荐链，完成成环检查；
--   4. 锁内完成 INSERT；事务提交或回滚后锁自动释放。
--
-- 单行约束：id 只允许 1（应用侧以固定常量 1 访问），迁移内初始化该唯一记录。
-- 该表不承载业务数据，不参与外键，不写入用户数据；仅为并发控制存在。

CREATE TABLE referral_graph_guard (
  id         TINYINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 初始化唯一锁记录：这是并发控制的一部分，缺少该行时应用侧 fail-closed。
INSERT INTO referral_graph_guard (id) VALUES (1);
