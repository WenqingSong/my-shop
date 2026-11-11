# B1-04-FIX-01 一级分销并发成环修复报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-FIX-01：一级分销并发成环修复 |
| 分支 | `feat/p2-backend` |
| 基线 HEAD | `f6948d7`（已核实；`63cd568` 为其祖先） |
| 修复提交 | `a0618a7` `fix(distribution): prevent concurrent referral cycles` |
| 本报告提交 | 提交后见 `git log -1` |
| 数据库 | MySQL 8.0.46，隔离库 `my_shop_b104_test`（`@@transaction_isolation = REPEATABLE-READ`，`innodb_lock_wait_timeout = 50`） |
| 最终结论 | **PASS** |

## 1. 最终结论

**PASS。** 一级分销推荐关系图的「成环检查 + 写入」已由 MySQL InnoDB 单行排他锁串行化，
跨 goroutine、跨数据库连接、跨 Go 实例（含独立的非 Go 进程）均生效；新增的 10 个真实 MySQL
并发用例（含 100 轮 ×2 压力）与 1 个 HTTP 层用例全部通过，原有幂等/不可改绑/一人一上级语义不变。

同时确认：用**修复前**实现运行同一批新用例会稳定复现缺陷（双向互绑 2 个都成功、三节点 3 条边全落库），
说明用例确实覆盖了原并发窗口，而不是「写完再验证通过」。

除 5 个与本任务无关的既有地区字典用例（B1-04 验收遗留的外部数据源阻塞）外，全量测试无新增失败。

## 2. 根因分析

### 2.1 原实现（`f6948d7`）

`BindReferral` 的绑定事务顺序为：

1. `SELECT ... FROM user_referrals WHERE user_id = ?`（**一致性读**，判定是否已绑定）；
2. `INSERT INTO user_referrals (user_id, referrer_user_id)`；
3. **写后**成环检查：从本人沿 `referrer_user_id` 向上遍历（同样是**一致性读**），回到本人则回滚。

### 2.2 为什么无法阻止并发互绑

InnoDB `REPEATABLE-READ` 下，一致性读使用事务的 read view：

- 事务 A（`A → B`）与事务 B（`B → A`）几乎同时开始；两个事务的第一次一致性读都早于对方提交，
  因此各自的 read view 都**看不到对方尚未提交**的插入；
- 事务 A 写入 `A → B` 后自检：从 B 向上没有记录 → 判定无环；
- 事务 B 写入 `B → A` 后自检：从 A 向上同样只看到自己的快照 → 判定无环；
- `uk_user_id` 只约束「`user_id` 唯一」，两条边的主键分别是 A 与 B，**互不冲突**，两个事务都能提交；
- 最终形成 `A → B → A`。三节点 `A→B / B→C / C→A` 同理：三条边全部落库。

### 2.3 逐项回答调查要求

| 问题 | 结论 |
| --- | --- |
| 为什么成环检查挡不住并发 | 检查用的是**一致性读**（事务快照），看不到并发事务未提交的插入；且检查与写入之间没有任何跨事务互斥 |
| 哪些 SQL 存在并发窗口 | 原实现的两处 `SELECT ... user_referrals`（本人关系、写后链遍历）以及其后的 `INSERT`；窗口覆盖整个「读—判—写」区间 |
| 当前隔离级别 | MySQL 服务端默认 `REPEATABLE-READ`（`@@transaction_isolation` 实测），应用未覆盖 |
| 是否存在其他写入入口 | 无。`user_referrals` 只有 `BindReferral` 一处 INSERT；没有 UPDATE/DELETE 业务路径，`legacymigrate` 也不写该表 |
| 唯一索引能保证什么 | `uk_user_id` 只保证「一个被推荐人至多一个上级」；它对「不同被推荐人之间的有向环」完全无约束，也无法在 `INSERT` 时发现环 |

## 3. 技术方案

### 3.1 全局关系图变更锁（数据库级）

新增表 `referral_graph_guard`（migration `20261111000004`）：

```sql
CREATE TABLE referral_graph_guard (
  id         TINYINT UNSIGNED NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO referral_graph_guard (id) VALUES (1);
```

锁协议（`BindReferral` 事务内，顺序固定）：

```text
BEGIN
  1) SELECT id FROM referral_graph_guard WHERE id = 1 FOR UPDATE   -- 第一条语句，全局排他行锁
  2) SELECT ... FROM user_referrals WHERE user_id = ? FOR UPDATE    -- 当前读本人关系
       已存在：同推荐人幂等返回 / 不同推荐人 20003（结束事务）
  3) 从 referrer 沿 referrer_user_id 逐跳 FOR UPDATE 追溯           -- 当前读整条链
       到达 child / 重复访问 / 超过深度上限 → 20004（回滚）
  4) INSERT INTO user_referrals (user_id, referrer_user_id)
  5) SELECT ... WHERE user_id = ? FOR UPDATE                        -- 回填响应
COMMIT / ROLLBACK                                                   -- 行锁在此释放
```

### 3.2 关键设计点

| 要求 | 实现 |
| --- | --- |
| 跨实例有效 | 锁是 InnoDB 行锁（`referral_graph_guard.id=1`），由 MySQL 统一仲裁；进程内无任何锁、无缓存、无 Redis |
| 锁持有到事务结束 | `FOR UPDATE` 在事务内获取，只在 `COMMIT`/`ROLLBACK` 时释放；不存在「先释放锁再提交」的窗口 |
| 检查读取最新数据 | 锁后所有读取均用 `LockUpdate()`（`SELECT ... FOR UPDATE`，当前读），**不使用一致性读**，不受事务早期快照影响 |
| 锁必须是第一条语句 | 避免事务先建立 read view；代码层面固定为事务回调的第一条语句 |
| 锁记录必须可靠存在 | 迁移内初始化；读取失败/空结果时 `fail-closed`（返回内部错误，拒绝绑定），绝不降级为无锁 |
| 异常/脏数据 | 既有关系图已有环、深度超过 200、链路异常均拒绝写入（fail-closed） |
| 原业务语义 | API、Principal 身份、错误码（20001/20002/20003/20004）、幂等返回、不可改绑、佣金与支付链路均未改动 |

### 3.3 为什么不用其他方案

- Go `sync.Mutex` / 进程内全局锁：多实例部署下完全失效；
- 普通 `SELECT` 后直接 `INSERT`：就是本次缺陷；
- 只锁「本人 + 推荐人」两行：两个事务锁的是不同的两行，仍可同时通过检查；
- Redis 普通锁：锁过期、主从切换、网络分区时与 MySQL 事务提交无原子关联，无法证明安全；
- 仅依赖 `uk_user_id`：只能保证一人一上级，对跨节点的环没有任何约束。

## 4. 修改文件

| 类型 | 文件 | 说明 |
| --- | --- | --- |
| 迁移 | `internal/migrations/sql/20261111000004_referral_graph_guard.up.sql` | 建锁表 + 初始化唯一记录 |
| 迁移 | `internal/migrations/sql/20261111000004_referral_graph_guard.down.sql` | `DROP TABLE IF EXISTS referral_graph_guard` |
| 实现 | `internal/logic/distribution/distribution.go` | 新增 `lockReferralGraphInTx` / `findReferralForUpdateInTx` / `requireAcyclicForUpdateInTx`；`BindReferral` 改为「锁 → 当前读判定 → 成环检查 → 写入」；删除写后检查 `requireAcyclicInTx` |
| 测试 | `internal/logic/distribution/distribution_concurrency_test.go`（新增） | 10 个真实 MySQL 并发用例（见 §6） |
| 测试 | `test/e2e/distribution_test.go` | 新增 `TestDistributionBindReferralConcurrentMutualCycle`（HTTP 层双向互绑） |
| 测试基础设施 | `internal/migrations/migrations_test.go` | `latestMigrationVersion` → `20261111000004`；`businessTables` 增 `referral_graph_guard`（60→61） |
| 测试基础设施 | `internal/migrations/b104_integration_migration_test.go` | up/down/up 断言新版本与锁表/锁记录单例 |
| 测试基础设施 | `internal/boot/boot_migration_test.go` | `migrationRelatedTables` 增 `referral_graph_guard`（否则「模拟空库」后重新 up 会因表已存在失败） |
| 文档 | `docs/design/distribution.md` | §2.2 并发语义/成环校验/锁竞争失败语义重写；新增 §2.3 锁表模型；新增 INV-D15 |
| 文档 | `docs/dev/distribution.md` | `BindReferral` 调用链与锁顺序、锁协议、排障、测试地图、§12 修正 |
| 文档 | `docs/design/migration.md` | 迁移清单 + §2.4 锁表生命周期 + 版本分配说明 |
| Registry | `.agent/registry/migrations.md` | 领取 `20261111000004`（RESERVED） |
| 报告 | `docs/reports/B1-04-FIX-01-referral-concurrency.md` | 本文件 |

未修改：任何历史 migration、分销佣金/支付/退款/结算逻辑、API 契约、其他 B1-04 模块。

## 5. 测试结果

命令均在 `feat/p2-backend`、隔离库 `my_shop_b104_test` 上执行；MySQL 8.0.46。

| # | 命令 | 退出码 | 状态 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `gofmt -l .` | 0 | **PASS** | 无输出 |
| 2 | `go build ./...` | 0 | **PASS** | — |
| 3 | `go vet ./...` | 0 | **PASS** | — |
| 4 | `DATABASE_DEFAULT_NAME=my_shop_b104_test go test -count=1 ./internal/logic/distribution/` | 0 | **PASS** | 含 10 个新增并发用例，8.3s |
| 5 | `DATABASE_DEFAULT_NAME=my_shop_b104_test go test -race -count=1 ./internal/logic/distribution/` | 0 | **PASS** | 13.6s，无数据竞争 |
| 6 | `DATABASE_DEFAULT_NAME=my_shop_b104_test go test -count=1 -run 'TestDistribution' ./test/e2e/` | 0 | **PASS** | 含新增 HTTP 双向互绑用例 |
| 7 | `bash scripts/test-unit.sh` | 0 | **PASS** | — |
| 8 | `bash scripts/check-registry.sh` | 0 | **PASS** | 无版本重复/漂移 |
| 9 | `bash scripts/contract-check.sh` | 0 | **PASS** | 210 常驻 API + 4 planned、490 Schema，无契约变更 |
| 10 | `DATABASE_DEFAULT_NAME=my_shop_b104_test bash scripts/test-migration.sh` | 0 | **PASS** | 14 个用例（空库/幂等/并发/锁等待/被杀恢复等） |
| 11 | `go run . migrate up` + `migrate version`（隔离库） | 0 | **PASS** | `20261111000004 (dirty=false)`，锁表 1 行 `id=1` |
| 12 | `go test -run TestB104IntegrationMigrationsUpDownUp ./internal/migrations/` | 0 | **PASS** | 04 up → down（锁表消失）→ up（锁表恢复 + 单例记录） |
| 13 | `DATABASE_DEFAULT_NAME=my_shop_b104_test go test -p 1 -count=1 ./...` | 1 | **PARTIAL（既有问题）** | 45 个包 `ok`；日志中仅 `internal/controller/address`（2 例）与 `internal/controller/region`（3 例）失败，单独复跑这两个包退出码同为 1；根因为 `regions` 为空，与本次修复无关（B1-04 验收遗留外部数据源阻塞） |
| 14 | **修复前对照**：旧 `distribution.go` + 新用例（`TestBindConcurrentMutualCycleRejected` / `...ThreeNode...`） | 1 | **FAIL（预期）** | 双向 `success=2 cycle=0`；三节点 `success=3 cycle=0` —— 证明新用例能复现原缺陷 |

> 说明：第 13 项中 `internal/boot`、`internal/migrations`、`internal/logic/distribution`、`test/e2e` 均为 `ok`。

## 6. 并发验证结果

全部断言基于**数据库最终关系图**（直接读 `user_referrals` 并做环检测），不依赖接口返回值。

| 场景 | 用例 | 结果 |
| --- | --- | --- |
| 双向并发互绑（`A→B` / `B→A`） | `TestBindConcurrentMutualCycleRejected` | 成功 1 / 成环拒绝 1（20004）/ 关系 1 行 / 无环 |
| 三节点并发（`A→B` / `B→C` / `C→A`） | `TestBindConcurrentThreeNodeCycleRejected` | 成功 2 / 成环拒绝 1 / 关系 2 行 / 无环 |
| 长链（预置 `A→B→C→D`） | `TestBindRejectsCycleOnLongChain` | `D→A` 与 `D→C` 均 20004；既有 3 条关系逐条不变 |
| 同一用户并发绑定不同推荐人 | `TestBindConcurrentSameUserDifferentReferrers` | 成功 1 / 冲突 20003 1 / 关系 1 行 |
| 相同关系并发重复提交 ×3 | `TestBindConcurrentSameRelationIdempotent` | 3 个请求全部幂等成功 / 关系 1 行 |
| 全局锁真实生效 | `TestBindReferralWaitsForGraphLock` | 外部持有 `referral_graph_guard` 行锁期间绑定未完成（600ms 观察窗口）；释放后立即成功 |
| 事务回滚 | `TestBindReferralTxRollbackReleasesLockAndLeavesNoRow` | 回滚后关系 0 行；后续绑定立即成功（无锁泄漏） |
| 锁记录缺失 | `TestBindReferralFailsClosedWhenGuardRowMissing` | 绑定被拒绝（1000/500），关系 0 行（fail-closed）；恢复记录后正常 |
| 双向互绑压力（100 轮） | `TestBindConcurrentMutualCycleStress` | 成功 100 / 成环拒绝 100 / 最终 100 条关系 / 每轮无环 |
| 三节点压力（100 轮） | `TestBindConcurrentThreeNodeCycleStress` | 成功 200 / 成环拒绝 100 / 最终 200 条关系 / 每轮无环 |
| HTTP 层双向互绑 | `TestDistributionBindReferralConcurrentMutualCycle` | 200 一次 + 409/20004 一次；`user_referrals` = 1 行；无 `A↔B` 双节点环 |

**跨实例/跨进程验证（非 Go 进程持锁）**：用 `mysql` CLI（独立 OS 进程、独立会话）执行
`START TRANSACTION; SELECT id FROM referral_graph_guard WHERE id=1 FOR UPDATE; SELECT SLEEP(8); COMMIT;`，
在此期间运行 `go test -run TestBindConcurrentMutualCycleRejected ./internal/logic/distribution/`：

| 场景 | 退出码 | 墙钟耗时 | 结果 |
| --- | --- | --- | --- |
| 无外部持锁（基线） | 0 | 1297ms（测试 0.44s） | PASS |
| 外部进程持锁约 7s | 0 | **7103ms**（测试 6.24s） | PASS（等待锁释放后才写入） |

说明锁由数据库仲裁、与进程无关：Go 侧没有任何进程内互斥，跨实例部署天然一致。

**Race 与数据库并发的区分**：`-race`（第 5 项）只覆盖 Go 内存数据竞争；并发事务的隔离、
行锁与提交顺序由上述真实 MySQL 用例覆盖，二者均已执行，互不替代。

## 7. Git 信息

| 项 | 值 |
| --- | --- |
| 分支 | `feat/p2-backend` |
| 基线 SHA | `f6948d7` |
| 修复提交 | `a0618a7` |
| 本报告提交 | 见 `git log -1`（其后可能仅追加 SHA 回填的元数据提交） |
| 工作区 | 干净（`git status --short` 无输出） |
| Push / Merge | 未 Push、未合并 `main`/`develop` |
| 分支/Worktree | 未删除、未创建 |

## 8. 遗留问题

1. **未验证（与本任务无关）**：全量 `go test -p 1 ./...` 仍因地区字典数据缺失（`regions` 为空）
   失败 5 例（address 2、region 3）。该阻塞在 B1-04 最终验收报告中登记为外部数据源 BLOCKED，
   本次未伪造数据、未跳过用例。
2. **HTTP 层覆盖范围**：HTTP 层新增了双向互绑用例；三节点、100 轮压力、锁阻塞/回滚/缺锁等场景
   在 logic 层（真实 MySQL、多连接）验证，未逐一复制到 e2e 层（避免重复跑同一 DB 行为）。
3. **写入口约束**：`user_referrals` 当前只有 `BindReferral` 一处写入。后续如新增写入入口
   （批量导入、后台改绑等），**必须**先取得同一把 `referral_graph_guard` 行锁，否则会绕过串行化；
   已在 `docs/dev/distribution.md` §6 与设计文档 §2.3 固化该约束。
4. **锁等待语义**：高并发下绑定在锁上排队，超过 `innodb_lock_wait_timeout`（当前 50s）会返回
   内部错误并回滚（可安全重试）。当前未做重试与超时分级，属可接受边界；如需更高吞吐，
   应在 Owner 决策下评估「按关系图分片锁」的方案，而不是削弱串行化。
