# T2-06.1.2 验证报告：Migration 记录完整性、失败恢复与 Pod 生命周期

- 基线：`4ba0d12dde661024c04dceaa8a9a0087e8c6be69`（分支 `feat/k8s-t2-06-1-1-deploy-lock-lifecycle`）
- 原始基线：`a333e26c1bb24f22517f344c62c04f9af5b3e9d5`
- 任务分支：`feat/k8s-t2-06-1-2-migration-recovery-safety`
- 结论：`T2-06.1.2_IMPLEMENTATION_PASS_PENDING_OWNER_DECISION`

  - AUD-D2（Lease 接管清空记录）、AUD-D3（Failed Job 但 Pod 仍在跑）、
    AUD-D4（PENDING + uid 缺失 + Job NotFound 死路）已修复，并有对应行为测试；
  - **AUD-02 = NOT_CLOSED**，**AUD-05 = NOT_CLOSED**：已发出的请求无法撤销，
    `read-after-write` 只提高可观测性，不构成 fencing（§4）。等待 Owner 决策；
  - 真实集群相关项为 **NOT_VERIFIED**（§7）。

本报告记录「改了什么、怎么验证的、什么还没验证」。设计论证见
`docs/design/deploy-lock.md`，操作手册见 `docs/development/kubernetes.md`（§10.8 已重写）。

## 1. Git 基线与分支

| 项 | 值 |
|---|---|
| 基线 HEAD | `4ba0d12dde661024c04dceaa8a9a0087e8c6be69` |
| 基线父提交 | `b4ceb234af2a68b23dced6ca671d7d67271f91d3` |
| 基线分支 | `feat/k8s-t2-06-1-1-deploy-lock-lifecycle` |
| 远端同名分支 SHA | `4ba0d12dde661024c04dceaa8a9a0087e8c6be69`（与基线一致） |
| 新分支 | `feat/k8s-t2-06-1-2-migration-recovery-safety` |
| 新分支创建点 | `4ba0d12dde661024c04dceaa8a9a0087e8c6be69`（未重写历史） |
| 工作区（开始） | 干净 |
| 合并 `develop` / `main` | 未合并 |
| 旧分支历史 | 未修改 |

## 2. 修改文件清单

| 文件 | 变化 |
|---|---|
| `scripts/deploy-k8s.sh` | 记录快照与写入路径重构（AUD-D2）；Pod 生命周期门禁（AUD-D3）；数据库证据恢复（AUD-D4）；Application apply 的 read-after-write 与 `APPLICATION_OUTCOME_UNKNOWN`（AUD-01/AUD-05 边界）；续租预算纳入重试链（D7）；释放不改 `leaseDurationSeconds`（D6）；新增 `migration-updated-at` / `migration-terminal-at` / `migration-recovery-*` / `migration-previous-*` |
| `test/deployk8s/fakestate/state.go` | Pod `ownerReferences`（`OwnerJobUID` / `Orphan`）、`JobPods`（按 job-name 索引）、`MigrationPods`（全命名空间扫描）、`LeaseRecordConflicts`、迁移 Pod 读取故障注入 |
| `test/deployk8s/fakekubectl/main.go` | 渲染 `ownerReferences`；按 job-name / component 分别返回 Pod；`lease.write` 事件（逐次记录写入携带的迁移记录）；记录写入专用 409 |
| `test/deployk8s/harness_test.go` | 新注解常量、`leaseAnnotations` / `requireAnnotationsPreserved` / `seedExpiredLease` / `stealLease`；**外部延迟包装**（application apply / secret 检查） |
| `test/deployk8s/record_test.go`（新，10） | TTL 4 例、接管保留记录、版本检查失败仍保留、续租/释放保留、接管后崩溃保留、归档只发生一次 |
| `test/deployk8s/podgate_test.go`（新，3） | Failed Job × Pod 状态矩阵（7 子用例）、Pod 终态后允许重试、本轮 Failed + Pod 仍在跑 |
| `test/deployk8s/recovery_test.go`（新，8） | 证据恢复矩阵（完成 / dirty / 落后 / version=0 / 格式非法 / 未来版本 / 仍有 Pod / 终态缺 uid 死路修复） |
| `test/deployk8s/application_inflight_test.go`（新，4） | apply 前失锁、apply 在途失锁、服务端已接受但客户端超时、SIGSTOP 后恢复执行 |
| `test/deployk8s/{lease,renew,lifecycle,inflight}_test.go` | 适配新租期预算与新的接管判据/文案 |
| `test/manifest/manifest_test.go` | D8：余量 60 → 180（与脚本建议一致）；租期预算契约纳入 attempts/backoff |
| `scripts/test-deploy-k8s.sh` | 覆盖范围说明与 AUD-02/AUD-05 边界表述 |
| `docs/design/deploy-lock.md` | 数据契约（13 键）、写入路径、Pod 门禁、证据恢复、续租预算、AUD-02/AUD-05 NOT_CLOSED |
| `docs/development/kubernetes.md` | §9.1/§9.1.1/§9.2/§9.4/§9.7/§10.1/§10.2/§10.4/§10.8/§11/§14 同步 |
| `docs/design/deployment.md`、`docs/development/migrations.md` | 发布流程与恢复语义同步 |

未修改：业务代码（订单/库存/Kafka/Outbox/迁移 SQL）、Kubernetes 清单、RBAC 示例。

## 3. 修复方案

### 3.1 AUD-D2：Lease 接管不得清空 Migration 记录

根因：`lease_takeover` / `lease_renew` / `lease_release` 都用内存里的 `MIG_*`
（此时是 `NO_MIGRATION`）整体回写注解。PUT 覆盖整个对象，于是上一轮的
`COMPLETE/FAILED + job + uid + detail` 被清空。

修复：把「记录快照」变成显式的三态：

| 路径 | 数据来源 | 行为 |
|---|---|---|
| `lease_create` | 显式清空 | 新建锁没有上一轮 |
| `lease_takeover` / `lease_renew` / `lease_release` | `lease_record_from_lease`（服务器读到的值） | **原样保留**，只改 holder/renewTime/duration |
| `migration_record_write` | 本次参数 + 服务器 `previous-*` | 唯一允许有意识更新记录的入口 |

数据契约（13 个固定键，数量有界）：

```text
当前记录：migration-state / -job / -job-uid / -detail / -updated-at /
          -terminal-at / -recovery-method / -recovery-evidence
上一轮：  migration-previous-state / -job / -job-uid / -detail / -terminal-at
```

归档规则：`archive=1` 的写入把服务器上的当前记录复制到 `previous-*`，再写新记录，
**同一次 CAS 完成**；重试时若服务器记录已等于目标值则**不重复归档**（避免把自己归档掉）。

验证的关键时序：

```text
COMPLETE → 释放 → 新 Runner 接管 → 版本降级检查失败 → 释放
   → 原 COMPLETE（job/uid/detail/terminal-at）完整保留        ✅ TestTakeoverKeepsPreviousRecordWhenVersionGuardRejects
FAILED → 释放 → 接管 → 尚未开始新迁移 → SIGKILL
   → 原 FAILED 记录仍然存在                                   ✅ TestCrashAfterTakeoverKeepsPreviousRecord
```

### 3.2 AUD-D3：Failed Job 不等于 Pod 已停止

判据从「Job 条件」改成「Job 条件 + 关联 Pod 全部终态」：

| Pod 状态 | 允许开始新一轮迁移 |
|---|---|
| `Running` / `Pending` / `Unknown` / 空 phase | 否 |
| 带 `deletionTimestamp`（终止中） | 否 |
| `Succeeded` / `Failed` | 是 |
| Pod 查询失败 | 否（fail-closed） |
| `ownerReferences` 里的 Job uid 不匹配 / 没有 owner | 否（fail-closed） |

归属校验用 `ownerReferences[].uid == Job.metadata.uid`，不接受仅凭 `job-name` 标签。
门禁在四处生效：接管核验（终态/非终态记录）、等待循环内 Job `Failed`、
超时二次判定、`begin_migration` 之前的全局 Pod 扫描。

本轮自身 Job `Failed` 且 Pod 仍在跑时：记录 `MIGRATION_FAILED` 但
`migration-terminal-at` 为空（不声称终结），不创建新 Job、不 apply 应用、不删 Job、
不 force，退出码 **7**，并打印旧 Job / uid / Pod 名称 / phase。

### 3.3 AUD-D4：PENDING + uid 缺失 + Job NotFound 的可执行恢复

旧 runbook 的死路：把 state 改成 `COMPLETE` 仍因 uid 为空被拒；复位 `NO_MIGRATION`
又可能丢掉证据。新增**可机器校验的数据库证据**：

```text
migration-recovery-method   = database-evidence
migration-recovery-evidence = version=<14位时间戳> dirty=<true|false> latest=<14位时间戳>
```

证据来自只读的 `make migrate-version`（`my-shop migrate version` 输出
`version: … (dirty=…)` 与 `latest available: …`）。脚本自行判定：

| 证据 | 动作 |
|---|---|
| `dirty=true` | 拒绝（退出码 7），要求人工核对 schema |
| `version > latest` | 拒绝（方向不一致） |
| `version == latest` | 当前记录收敛为 `MIGRATION_COMPLETE`（uid 保持为空） |
| `version < latest` 且 `> 0` | 归档并复位 `NO_MIGRATION` |
| `version == 0` | 拒绝（无法区分空库与未纳管库） |
| 格式不匹配 / 仍有 Pod 未终止 | 拒绝 |

硬约束：不自动 `migrate force`、不伪造 Job uid、不只改 state 就假装验证、
证据来源写入 detail 与 `previous-detail`；两种结论之后都仍会执行一次 `migrate up`
（不跳过迁移）。`docs/development/kubernetes.md` §10.8 已按上述契约重写，
每条命令都可以直接执行。

### 3.4 AUD-02 / AUD-05（Fencing 残余风险，NOT_CLOSED）

- 明确区分「请求**尚未发出**」（可以停止，资源未变更）与「请求**已经发出**」
  （无法撤销，只能如实报告）；
- Application apply 返回非零时做 read-after-write：
  - 若 Deployment 已是目标镜像/版本 → 打印「很可能已被 API Server 接受并生效」；
  - 否则打印「无法判定是否生效」；
  - 两种情况都输出 `APPLICATION_OUTCOME_UNKNOWN` 并退出码 1（不打印成功标记）；
- 请求在途期间失锁（含 SIGSTOP 后恢复）→ 输出 `APPLICATION_OUTCOME_UNKNOWN`，退出码 3，
  不重试、不释放他人 Lease；
- 保留变更前的服务端 holder/uid 复核，并新增请求发出后的归属观察
  （`observe_lock_after_request`），仅用于如实报告；
- 文档与脚本注释统一标注 `AUD-02 = NOT_CLOSED`、`AUD-05 = NOT_CLOSED`，
  并说明真正的 fencing 需要 Admission Webhook / 专用发布控制器。

### 3.5 D6 / D7 / D8

| 项 | 处理 |
|---|---|
| D6 | 释放时 `leaseDurationSeconds` 保持配置值（不再写 1）；语义是「持有者租期」，不是「释放后失效时间」。人工 runbook 同步去掉该字段 |
| D7 | 租期预算纳入重试链：`interval + attempts(3)×timeout + backoff(1) + margin(5) < duration`，默认 `10+9+1+5=25<30`；默认 `--lease-renew-timeout` 5 → 3；`test/manifest` 同步 |
| D8 | `test/manifest` 的迁移余量门禁 60s → 180s，与脚本 `MIGRATION_TIMEOUT_MARGIN` 一致；文档措辞同步 |

## 4. AUD-02 / AUD-05 处理结论

| 审计项 | 状态 | 说明 |
|---|---|---|
| AUD-02（缺少 fencing token） | **NOT_CLOSED** | Lease CAS 只保护 Lease 对象，不保护 Deployment 写入；被挂起/分区的旧持有者仍可能成功写入一次 |
| AUD-05（已发出的请求无法撤销） | **NOT_CLOSED** | `read-after-write` 只提高可观测性；`APPLICATION_OUTCOME_UNKNOWN` 是「如实说不知道」，不是修复 |

本任务**不声称**已实现强 fencing，也**不代替 Owner 接受风险**。测试明确断言：

- 失锁后不再发起新的写请求（`TestLockLostBeforeApplicationApplyIssuesNothing`、
  `TestInflightLockLossStopsNewOperations`）；
- 在途请求完成后不再发起新操作（`TestLockLostDuringApplicationApplyReportsUnknown`）；
- 已经发出的请求留在集群里、不会被撤销（fake 中 `Applied == true`，
  `TestApplicationApplyAcceptedButClientTimesOut`、`TestPausedRunnerResumesAfterLockLoss`）；
- 失锁后不修改其他持有者的 Lease（`TestInflightAbortDoesNotLeaseWrites`）。

## 5. 测试数量与实际覆盖

`test/deployk8s` 用例总数 **100**（基线 75，新增 25），全部真实执行
`scripts/deploy-k8s.sh`，通过 fake kubectl 观察调用、事件、注解与退出码。

| 文件 | 用例 | 覆盖 |
|---|---|---|
| `record_test.go`（新，10） | TTL 4 例（COMPLETE/FAILED/PENDING/RUNNING + Job NotFound）、接管保留 COMPLETE/FAILED、版本检查失败仍保留、续租保留、释放保留（含 D6）、失败证据保留、接管后 SIGKILL 保留、归档只发生一次（CAS 重试） | AUD-D2 / D5-A / D5-B |
| `podgate_test.go`（新，3） | Failed Job × Pod 状态矩阵（Running/Pending/Terminating/Succeeded/Failed/查询失败/归属不一致）、Pod 终态后允许重试、本轮 Failed + Pod 仍在跑 | AUD-D3 / D5-D |
| `recovery_test.go`（新，8） | 证据=latest（收敛 COMPLETE、uid 为空）、dirty、落后（复位 NO_MIGRATION）、version=0、格式非法、未来版本、仍有残留 Pod、终态缺 uid 的死路修复 | AUD-D4 / D5-E |
| `application_inflight_test.go`（新，4） | apply 前失锁（零 apply）、apply 在途失锁（`APPLICATION_OUTCOME_UNKNOWN`）、服务端已接受但客户端超时、SIGSTOP 恢复 | AUD-02/AUD-05 边界 / D5-C |
| `lifecycle_test.go`（11） | 超时后记录保留、第二个 Runner 被拒、终态后接管、Failed 接管、查询失败、Job NotFound、崩溃识别、apply 报错但已创建、Application apply 失败、无记录扫描、未知状态 | AUD-01 |
| `migration_test.go`（14） | 完成/失败/超时分离、Job 消失、二次判定、deadline 关系 | AUD-04/06 |
| `renew_test.go`（8） | 非法/临界参数（含重试链预算）、续租专用超时、500/409/UID 冲突、后台续租与 cleanup 并发 | AUD-03 / D7 |
| `lease_test.go`（10） | 首次创建、并发竞争、串行化、占用、过期接管、续租、续租失败、不释放他人锁、权限、释放 CAS | T2-06.1 回归 |
| `version_test.go`（10）、`verify_test.go`（7） | 降级保护、发布成功判定 | T2-06.1 回归 |
| `read_test.go`（7）、`fault_test.go`（6） | 读取健壮性、故障路径（kubectl 缺失、集群不可达、SIGTERM、临时文件） | AUD-08/09/11 |

每个新用例都断言：退出码、Lease 最终状态（holder + 记录 13 键）、是否创建新 Job
（`apply.migration` 事件）、是否执行 Application apply、以及 Deployment 是否被修改
（fake 的 `Applied`）。

fake kubectl 本轮新增能力：Pod `ownerReferences`（含「属于另一个 Job」「没有 owner」）、
按 job-name 与 component 分别返回 Pod、`lease.write` 事件、记录写入专用 409、
以及 fake 之外的**外部延迟包装**（application apply / secret 检查）。

### 5.1 门禁真实结果（本机环境，全部为真实退出码）

| 门禁 | 命令 | 退出码 |
|---|---|---|
| 格式 | `gofmt -l .`（无输出） | 0 |
| 构建 | `go build ./...` | 0 |
| 静态检查 | `go vet ./...` | 0 |
| 单元测试 | `bash scripts/test-unit.sh` | 0 |
| 发布脚本行为 | `bash scripts/test-deploy-k8s.sh`（100 用例，PASS 100 + 13 子用例） | 0 |
| Kustomize 清单 | `bash scripts/test-k8s-manifest.sh`（kustomize v5 + kubeconform 9/9 Valid） | 0 |
| 竞态 | `bash scripts/test-race.sh`（含 Outbox 11 项、B3 2 项） | 0 |
| 完整入口 | `bash scripts/test.sh` | 0 |
| 迁移门禁 | `bash scripts/test-migration.sh` | 0 |
| 容器运行时 | `bash scripts/test-container-runtime.sh`（16 项断言） | 0 |

说明：`test-race.sh` / `test-migration.sh` / `test.sh` / `test-container-runtime.sh`
需要本地 MySQL/Redis（`scripts/deps-start.sh`）与本地镜像；本次在 `make deps-start` 之后
执行，数据库密码通过 `DATABASE_DEFAULT_PASS` 注入（脚本默认 `root`，本机 `.env` 使用
一次性本地口令，未提交）。

## 6. 原审计问题回归

| 审计项 | 复现要点 | 回归结果 |
|---|---|---|
| AUD-D2 | 上一轮 COMPLETE/FAILED → 锁过期 → 新 Runner 接管 → 记录被写成 NO_MIGRATION | 已复现并修复：`lease.write` 事件逐次断言接管/续租/释放携带原记录；`TestTakeoverKeepsPreviousRecordWhenVersionGuardRejects`、`TestCrashAfterTakeoverKeepsPreviousRecord`、`TestRenewPreservesRecordAnnotations`、`TestReleasePreservesRecordAnnotations` PASS |
| AUD-D3 | Job `Failed=True` 但 Pod 仍 `Running` → 接管 → 创建 Job B → DDL 重叠 | 已复现并修复：7 子用例矩阵；Running/Pending/Terminating/查询失败/归属不一致均退出码 7，零 `apply.migration`、零 `apply.application`、持有者与记录不变 |
| AUD-D4 | `PENDING` + `uid=""` + Job `NotFound` → 无路可走 | 已复现并修复：`version==latest` 收敛 COMPLETE（uid 仍为空）；`version<latest` 复位并归档；dirty/0/未来版本/格式非法/残留 Pod 全部拒绝并打印可执行步骤 |
| AUD-D1（AUD-02/AUD-05） | 失锁后已发出的 `kubectl apply` 仍可能在服务端完成 | **NOT_CLOSED**，如实披露；新增在途/超时/恢复执行测试，断言「请求可能已生效」「不重试」「不释放他人锁」 |
| AUD-D5 | 覆盖缺口 | 已补齐：TTL 4 例、记录保留 6 例、在途 5 例、Pod 矩阵 8 例、恢复矩阵 9 例 |

## 7. NOT_VERIFIED

1. **Lease 在真实 API Server 上的并发语义**：fake 用文件锁串行化，真实环境是 etcd 事务 +
   多 apiserver；两者证明的不是同一件事。
2. **RBAC 最小权限的实际效果**（含新增的 `pods get/list`）：未在真实集群 apply。
3. **真实集群中「Job Failed 但 Pod 仍 Running」的时序**：只在 fake 中构造，
   未用真实 MySQL 长 DDL + kubelet 终止时序验证。
4. **Job 被 kubelet 以 `DeadlineExceeded` 终止后的条件传播时延**：180s 余量是保守估计。
5. **fencing 窗口实测**：未做「暂停 Runner + 网络分区」的真集群实验。
6. **时钟偏移导致的误接管**：未在可控时钟环境验证。
7. **server-side dry-run 与真实部署**：无集群，未执行。
8. **`APPLICATION_OUTCOME_UNKNOWN` 之后的人工核对流程**：无真实集群，未演练。

## 8. 剩余风险

1. **没有 fencing token（AUD-02 = NOT_CLOSED）**：旧持有者的写入仍可能成功一次。
2. **已发出的请求无法撤销（AUD-05 = NOT_CLOSED）**：`read-after-write` 只提高可观测性。
3. **证据是人工抄录的事实**：脚本只能校验格式与内部一致性，不能独立验证真实性；
   拥有 `update leases` 权限的人可以写入错误证据（与「直接改 state」同一类信任边界）。
4. **恢复不覆盖「部分 DDL 已生效」**：`dirty=true` 一律要求人工核对。
5. **过期判定依赖 Runner 本地时钟**：偏移过大会误接管（偏快）或让死锁更久（偏慢）。
6. **锁只保护脚本路径**：直接 `kubectl apply` 或其他 GitOps 工具不受保护。
7. **`create leases` 无法用 `resourceNames` 限制**：RBAC 固有限制。
8. **迁移记录依赖 API 可读**：`list jobs` / `list pods` 缺失或 API 抖动时表现为
   「发布被拒绝」（fail-closed 的代价）。
9. **浅克隆**：版本保护 fail-closed，需 `git fetch --unshallow`。

## 9. Commit SHA

| 提交 | SHA | 内容 |
|---|---|---|
| 主实现 | `b5b6a946c6529a2c14b9242c2dd9bceed05131d9` | `scripts/deploy-k8s.sh`、`test/deployk8s/`、`test/manifest/`、设计/操作文档 |
| 报告 | 见 `git log -1 --format=%H`（包含本文件的提交） | 本验证报告 |

## 10. 工作区状态

提交并推送后 `git status --porcelain` 为空（`.env` 为本地一次性文件，已在 `.gitignore` 中）。

## 11. 远端同步

| 项 | 值 |
|---|---|
| 推送分支 | `refs/heads/feat/k8s-t2-06-1-2-migration-recovery-safety` |
| 本地 HEAD | `<LOCAL_HEAD>` |
| 远端 SHA（`git ls-remote`） | `<REMOTE_HEAD>` |
| 一致性 | `<SYNC_RESULT>` |
