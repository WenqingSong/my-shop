# 发布锁、迁移生命周期绑定与版本降级保护（T2-06.1 / T2-06.1.1 / T2-06.1.2 / T2-06.1.3）

本文是 `scripts/deploy-k8s.sh` 发布期并发控制的**设计文档**：为什么用 Kubernetes Lease、
锁如何与 Migration Job 的生命周期绑定、迁移生命周期如何核验、它到底保证了什么、
**没有**保证什么，以及剩余风险。

> T2-06.1.2 变更摘要：记录写入路径拆成「保留」与「有意识更新」两条（AUD-D2）；
> 允许开始新一轮迁移的判据从「Job 条件」改为「Job 条件 + Pod 已终止」（AUD-D3）；
> 新增可校验的数据库证据恢复路径（AUD-D4）；AUD-02 / AUD-05 明确**未关闭**。
>
> T2-06.1.3 变更摘要（F-01…F-07）：
> ① RBAC 示例补齐 core `pods get/list`，并把「脚本声明的权限 == 脚本实际使用的权限 ⊆
> 交付 RBAC」做成自动化契约测试；② 续租预算补上每次 attempt 的 **GET** 请求
> （`attempts × (get + replace)`），默认租期 30s → 45s；③ 续租在租约过期后不再写入
> （不「复活」一把别人可以抢走的锁）；④ 失锁窗口按真实时间戳重新推导（§10.3）；
> ⑤ 退出码语义区分「未发出 / 已发出但未知 / 已确认成功」；⑥ 测试替身的接管语义推进会
> `resourceVersion`（否则 CAS 缺陷不可能被测试发现）；⑦ Pod 查询结果区分「确实存在」
> 与「无法确认」。**AUD-02 / AUD-05 仍然 NOT_CLOSED，本任务不声称实现强 fencing。**
>
> T2-06.1.5 变更摘要（审计 F-01：RBAC resourceNames 预检语义）：
> ① 受 `resourceNames` 限制的 leases get/update 预检改为**对象级**
> （`leases.coordination.k8s.io/surgecart-deploy-lock`），`create` 保持无对象名；
> ② 伪 kubectl 的 `resourceNames` 匹配改为与真实 RBAC 一致（无对象名 → 拒绝，
> 无通配符），并纠正 `auth can-i` 的 `TYPE/NAME` 解析（`pods/log` 的对象名是 `log`，
> 子资源改用 `--subresource`）；③ 新增对象级语义与反向契约测试（§12.3、§13）。
> **不扩大任何授权范围**：交付的 `resourceNames` 与权限集未变。**AUD-02 / AUD-05
> 仍为 NOT_CLOSED。**

操作手册（怎么发布、怎么排障、怎么人工解锁）见 `docs/development/kubernetes.md`；
清单与探针语义见 `docs/development/kubernetes.md` §5–§8。

## 1. 问题

T2-06-FINAL-AUDIT 与 T2-06.1 FINAL-AUDIT 确认的缺口：

1. Migration Job 有数据库级 `GET_LOCK`，**只保护迁移本身**；
2. 应用侧 `kubectl apply` 完全不经过数据库，因此不受该锁保护；
3. 两个发布流水线可以交错执行：A 迁移 → B 迁移 → B 应用 → A 应用；
4. 交错的结果是「旧版本覆盖新版本」，而两个流水线都返回成功；
5. **T2-06.1 的锁只保护到「脚本还活着」为止**：脚本退出时会无条件释放 Lease，
   即使它刚 apply 的 Migration Job 仍在 Running —— 新的 Runner 立刻拿到锁，
   在旧迁移仍在执行 DDL 时启动新一轮迁移（AUD-01）；
6. 续租参数不满足「续租 + 请求超时 + 余量 < 租期」（10 + 30 > 30，AUD-03）；
7. 脚本等待上限与 Job `activeDeadlineSeconds` 同为 900（AUD-04/06）；
8. 当前没有真正的 fencing token，而 T2-06.1 的表述容易让人误以为有（AUD-02）。

目标（本任务的核心不变量）：

> 任何通过 `deploy-k8s.sh` 执行的发布，都在**完整发布生命周期**内受发布级互斥保护；
> **只要上一轮 Migration Job 尚未进入终态，任何 Runner 都不能接管发布锁并开始新一轮发布**；
> 且在没有显式人工授权时，不得把 Deployment 降级到更旧的 commit。

## 2. 为什么是 Lease

| 候选 | 是否采用 | 原因 |
|---|---|---|
| 本地 `flock` | ❌ | 不同 CI Runner 不共享文件系统；本地锁在分布式场景等于没有锁 |
| MySQL `GET_LOCK` | ❌ | 只覆盖迁移；应用 apply 与数据库无交集，且锁连接与迁移进程同生命周期 |
| 对象存储/Redis 锁 | ❌ | 引入新的外部依赖与新的失效模式；Kubernetes 部署已经有 API Server |
| `coordination.k8s.io/v1` Lease | ✅ | 集群内标准协调资源；`create` 由 API Server 保证同名唯一；`update` 带 `resourceVersion` 即乐观并发 |

资源：

```yaml
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: surgecart-deploy-lock
  namespace: surgecart
  annotations:
    # 当前记录（本轮或最近一轮）
    deploy-lock.surgecart.io/migration-state: "MIGRATION_RUNNING"
    deploy-lock.surgecart.io/migration-job: "surgecart-migrate-a1b2c3d4"
    deploy-lock.surgecart.io/migration-job-uid: "0f5c…-uid"
    deploy-lock.surgecart.io/migration-detail: "BackoffLimitExceeded: …"
    deploy-lock.surgecart.io/migration-updated-at: "2026-11-08T10:00:00Z"
    deploy-lock.surgecart.io/migration-terminal-at: ""          # 仅终态确认时非空
    deploy-lock.surgecart.io/migration-recovery-method: ""      # job-object | database-evidence
    deploy-lock.surgecart.io/migration-recovery-evidence: ""    # version=<N> dirty=<bool> latest=<M>
    # 上一轮归档（只保留最近一轮）
    deploy-lock.surgecart.io/migration-previous-state: "MIGRATION_COMPLETE"
    deploy-lock.surgecart.io/migration-previous-job: "surgecart-migrate-9f8e7d6c"
    deploy-lock.surgecart.io/migration-previous-job-uid: "8a7b…-uid"
    deploy-lock.surgecart.io/migration-previous-detail: ""
    deploy-lock.surgecart.io/migration-previous-terminal-at: "2026-11-08T09:00:00Z"
spec:
  holderIdentity: "deploy-<host>-<pid>-<utc>"   # 持有者身份
  leaseDurationSeconds: 45                       # 租期（释放时也保持该值，见 §5；T2-06.1.3 由 30 调整，理由见 §7）
  acquireTime: "<RFC3339>"                       # 本次获取时间
  renewTime: "<RFC3339>"                         # 最近一次租约写入请求的发出时刻（t_req，见 §7.5）
  leaseTransitions: 2                            # 被接管次数（排查用）
```

**Lease 不放进 Kustomize 入口**。原因：Lease 若出现在 Application 清单里，每次
`kubectl apply` 都会把 `holderIdentity` 覆盖成清单里的值（通常是空），等于发布流程自己
拆掉互斥。锁由脚本在需要时创建/接管，RBAC 示例在
`manifest/kubernetes/examples/deploy-lock/rbac.yaml`。

## 3. 迁移生命周期与 Lease 的绑定（AUD-01）

### 3.1 为什么必须绑定

Lease 只表达「谁在发布」，不表达「这次发布做到哪一步」。只靠租期，脚本一退出锁就自由了；
而 Migration Job 与 Lease 是两个对象，退出脚本不会让 Job 停下来。于是出现：

```text
Runner A: 拿锁 → apply Job → 脚本超时退出 → 释放锁
Runner B:                       拿锁 → apply 自己的 Job → 两个迁移并行
```

数据库 `GET_LOCK` 只能让两个迁移**排队**，不能阻止 B 在 A 的 DDL 中途启动；
更糟的是 B 的 Application 会带着「迁移可能失败」的状态上线。

### 3.2 数据契约

绑定信息写在 Lease 的注解上（为什么不是 ConfigMap/Job 标签：注解与持有者身份在同一个
对象上，一次 CAS 写入就能同时表达「谁持有」和「做到哪一步」，不会出现两者不一致）：

**当前记录**

| 注解键 | 含义 | 谁写 |
|---|---|---|
| `deploy-lock.surgecart.io/migration-state` | 状态机当前状态 | 持锁的 Runner |
| `deploy-lock.surgecart.io/migration-job` | 本轮**预期/实际**的 Job 名称 | apply 之前写 |
| `deploy-lock.surgecart.io/migration-job-uid` | 真实 `metadata.uid` | apply 之后回写 |
| `deploy-lock.surgecart.io/migration-detail` | 失败原因 / Pod 门禁结论（单行 ≤200 字符） | 失败或未确认时写 |
| `deploy-lock.surgecart.io/migration-updated-at` | 当前记录最后一次写入时间 | 每次写入 |
| `deploy-lock.surgecart.io/migration-terminal-at` | 被**确认**为终态的时间（未确认则空） | 终态写入 |
| `deploy-lock.surgecart.io/migration-recovery-method` | `job-object`（默认）或 `database-evidence` | 终态写入 |
| `deploy-lock.surgecart.io/migration-recovery-evidence` | `version=<N> dirty=<bool> latest=<M>` | 仅证据恢复 |

**上一轮归档**（数量有界，只保留最近一轮，不会无限增长）

| 注解键 | 含义 |
|---|---|
| `deploy-lock.surgecart.io/migration-previous-state` | 上一轮的状态 |
| `deploy-lock.surgecart.io/migration-previous-job` | 上一轮的 Job 名 |
| `deploy-lock.surgecart.io/migration-previous-job-uid` | 上一轮的 Job uid（可能为空） |
| `deploy-lock.surgecart.io/migration-previous-detail` | 上一轮的证据（恢复路径会追加判定） |
| `deploy-lock.surgecart.io/migration-previous-terminal-at` | 上一轮被确认终态的时间 |

约束（AUD-D2）：

1. **写入路径分为两类**，混用是 T2-06.1.1 的缺陷根因：
   - `create` / `takeover` / `renew` / `release` **必须原样保留**服务器上的记录；
   - 只有 `migration_record_write` 可以**有意识地更新**当前记录。
   `kubectl replace`（PUT）覆盖整个对象，因此写入前必须显式构造「记录快照」：
   保留路径从服务器读取结果填充，更新路径从本次参数填充。
2. `state` 为 `MIGRATION_PENDING`/`MIGRATION_RUNNING` 时，记录**不允许被清除**；
   唯一例外是「记录已写但本 Runner 从未发出 apply」——此时可以确定没有 Job 需要追踪。
3. **开始新一轮迁移时归档上一轮**：`archive=1` 的写入会把服务器上的当前记录复制到
   `previous-*`，再写新的 `PENDING`。归档与写新记录在同一次 CAS 内完成，
   不会出现「旧的清了、新的没写上」；CAS 重试时若记录已等于目标值则**不重复归档**。
4. `NO_MIGRATION` **不得**作为「未计划修改」时的默认值写入；任何路径都不允许用内存里的
   `NO_MIGRATION` 覆盖已有记录。
5. 注解只是「上一轮 Runner 的陈述」，**接管方必须用真实 Job 对象 + 该 Job 的 Pod 交叉核验**（§4）。

> 为什么需要 `previous-*`：迁移失败后必须保留失败证据；而下一轮发布成功又会覆盖当前记录。
> 只有把上一轮归档下来，才能同时满足「当前记录只描述本轮」和「历史证据不丢失」。代价是
> 13 个固定键 —— 数量恒定，不会随时间增长。

### 3.3 状态机

```text
NO_MIGRATION
    │  写记录（预期 Job 名）+ apply
    ▼
MIGRATION_PENDING ──(观察到 Job 存在且未终态)──► MIGRATION_RUNNING
    │                                                │
    │                                                ├──► MIGRATION_COMPLETE   (Job Complete=True)
    │                                                └──► MIGRATION_FAILED     (Job Failed=True)
    └──────────────(apply 失败且 Job 确定不存在)──────► NO_MIGRATION（复位）
```

状态与**真实 Job 状态**的关系：

| 记录状态 | 允许的真实 Job 状态 | Pod 要求 | 不一致时 |
|---|---|---|---|
| `NO_MIGRATION` | 无 Job，或历史 Job 已终态 | 无迁移 Pod 未终止 | 存在未终结 Job/Pod → 拒绝开始新一轮迁移 |
| `MIGRATION_PENDING` | 不存在（尚未 apply）/ 未终态 / 已终态 | 终态且 Pod 已终止 | 未终态 / Pod 未终止 → 拒绝接管 |
| `MIGRATION_RUNNING` | 未终态 / 已终态 | 同上 | 同上 |
| `MIGRATION_COMPLETE` | `Complete=True`，或对象已被 TTL 清理 | Pod 必须已终止 | 仍为 `active` 或 Pod 未终止 → 拒绝接管 |
| `MIGRATION_FAILED` | `Failed=True`，或对象已被 TTL 清理 | Pod 必须已终止 | 同上 |

**Job `NotFound` 不等于「迁移完成」**：只有在记录已经是 `COMPLETE`/`FAILED`
（写入时用真实对象确认过终态、并记录了 UID）时，对象消失才被当作 TTL 清理；
`PENDING`/`RUNNING` + `NotFound` 需要**可校验的数据库证据**才能恢复（§4.2）；
`COMPLETE`/`FAILED` 但 uid 为空同样需要证据（§4.2），否则一律退出码 7。

### 3.4 时间线（一次成功的发布）

```text
预检 → 命名空间 → 锁权限 → 获取锁（接管前核验上一轮 Job）
  → 锁内复检（Secret / 版本降级保护）
  → ① CAS 写入 state=MIGRATION_PENDING + 预期 Job 名（uid 为空）
  → ② kubectl apply Migration 清单
  → ③ 读取真实 Job，拿到 metadata.uid
  → ④ CAS 写入 state=MIGRATION_PENDING + 真实 uid
  → ⑤ 轮询 → 观察未终态时写 state=MIGRATION_RUNNING（只写一次）
  → ⑥ 终态：写 MIGRATION_COMPLETE 或 MIGRATION_FAILED
  → Application apply → 收敛 → 发布验证 → 释放锁（记录保留）
```

§2.3 要求处理的四个窗口与实现方式：

| 窗口 | 处理 |
|---|---|
| 迁移 apply 成功但客户端超时 | 不看 apply 的退出码：`await_job_object` 轮询真实对象（上限 10s） |
| 迁移 apply 报错但对象已创建 | 同上：读到对象就按已创建继续，并打印明确告警 |
| 迁移 apply 报错且对象确实不存在 | 复位记录为 `NO_MIGRATION`，释放锁，退出码 1 |
| Application apply 结果不确定 | `APPLICATION_OUTCOME_UNKNOWN`：不报告成功，提示「请求可能已被接受并生效」（§10.1） |
| 创建 Job 后 Runner 崩溃 | 记录停在 `MIGRATION_PENDING`/`RUNNING`；锁过期后下一个 Runner 拒绝接管 |
| 写入 Job UID 前崩溃 | 记录里 uid 为空：接管方**必须**取得数据库证据才能恢复（§4.2），不猜、不伪造 uid |

**状态不明确时一律 fail-closed**：退出码 7，保留锁与记录，交人工核实。

## 4. 新 Runner 接管前的核验（AUD-01 §2.4 / T2-06.1.2）

### 4.1 判据

接管（锁已释放或已过期）之前执行 `verify_previous_migration`：

| 上一轮 Job 状态 | 关联 Pod 状态 | 新 Runner 行为 |
|---|---|---|
| `Running` / `Pending` / `Terminating`（`deletionTimestamp` 非空） | 任意 | **拒绝接管**（退出码 7），等待其进入终态后重跑 |
| `Complete` | 全部 `Succeeded`/`Failed` | 允许接管 |
| `Complete` / `Failed` | 仍有 `Running`/`Pending`/`Unknown`/终止中 | **拒绝接管**（退出码 7） |
| `Failed` | 全部 `Succeeded`/`Failed` | 允许接管，但**不自动 force**、不自动删除 Job |
| `Failed` | Pod 查询失败 / 无法归属该 Job | **拒绝接管**（fail-closed） |
| 查询失败（4xx/5xx/超时） | — | 拒绝接管 |
| 状态无法识别（未知注解值、记录不完整） | — | 拒绝接管 |
| Job `NotFound`（`PENDING`/`RUNNING` 记录） | 无未终止 Pod | 需要数据库证据（§4.2），否则拒绝 |
| Job `NotFound`（`COMPLETE`/`FAILED` 记录且 uid 已记录） | 无未终止 Pod | 允许接管（TTL 清理） |
| Job `NotFound`（终态记录但 uid 为空） | 无未终止 Pod | 需要数据库证据（§4.2），否则拒绝 |
| 记录里 uid 与实际 uid 不一致（同名 Job 被删除重建） | — | 拒绝接管 |

**为什么 `Failed=True` 不足以放行（AUD-D3）**：`Failed` 是 Job controller 的判定，
不是「执行 DDL 的进程已经停止」。`backoffLimit` 用尽时最后一个 Pod 可能仍在 `Running`。
若此时创建 Job B，Pod A 与 Pod B 的 DDL 可能重叠；数据库 `GET_LOCK` 不能替代进程级判断
（Pod A 可能已经释放锁，也可能还在等锁）。

Pod 归属必须用 `ownerReferences[].uid == Job.metadata.uid` 校验，**不能只信 `job-name` 标签**：
同名 Job 被删除重建、手工创建的 Pod、TTL 后残留的孤儿 Pod 都可能带同样的标签。

### 4.2 数据库证据恢复（AUD-D4）

存在一种没有「真实 Job 对象」可核验的状态：

```text
Lease:  migration-state=PENDING, migration-job=surgecart-migrate-xxx, migration-job-uid=""
集群:   Job NotFound
```

（Runner 写完 PENDING、apply 后、回写 UID 前崩溃，随后 Job 被清理。）

此时唯一可用的**事实**是数据库里的迁移账本。恢复契约：

```text
deploy-lock.surgecart.io/migration-recovery-method   = database-evidence
deploy-lock.surgecart.io/migration-recovery-evidence = version=<N> dirty=<true|false> latest=<M>
```

`<N>/<M>` 是 14 位时间戳，来自只读命令 `my-shop migrate version` 的输出：

```text
version: 20261001000024 (dirty=false)
latest available: 20261001000024（已是最新）
```

判定（脚本执行，不由人工解释）：

| 证据 | 结论 | 记录动作 |
|---|---|---|
| `dirty=true` | 拒绝自动恢复 | 无（人工核对 schema，见 `docs/development/migrations.md` §7） |
| `version > latest` | 拒绝（数据库比本次构建新，方向不一致） | 无 |
| `version == latest` | 上一轮账本已到最新且干净 | 当前记录收敛为 `MIGRATION_COMPLETE`（uid 保持为空） |
| `version < latest` 且 `version > 0` | 上一轮没有完成 | 归档并复位 `NO_MIGRATION` |
| `version == 0` | 无法区分空库与未纳管库 | 拒绝 |
| 格式不匹配 | 拒绝（不接受自由文本） | 无 |
| 已有迁移 Pod 未终止 | 拒绝 | 无 |

三条硬约束：

1. **不伪造 Job UID**：恢复后的终态记录 `migration-job-uid` 仍为空，用
   `migration-recovery-method` 表达「这个终态是证据推出来的，不是 Job 对象确认的」；
2. **不 `migrate force`**：脚本永不调用；证据只决定「上一轮记录如何收敛」；
3. **不跳过迁移**：两种结论之后都会为目标版本创建新 Job 执行 `migrate up`。
   `version == latest` 时它是幂等的（`ErrNoChange`），`version < latest` 时它补齐剩余迁移。

证据来源必须留痕：收敛为 `COMPLETE` 时 detail 写入证据原文，归档时
`migration-previous-detail` 追加「恢复判定(database-evidence): …」。

### 4.3 补充规则

1. **没有记录也要扫描**：`state=NO_MIGRATION` 或注解缺失（升级前的老 Runner）时，
   Runner 会扫描两遍：
   - `kubectl get jobs -l app.kubernetes.io/component=database-migration`
   - `kubectl get pods -l app.kubernetes.io/component=database-migration`
   存在未终结的 Job **或**未终止的 Pod 就拒绝接管。Pod 扫描覆盖「Job 已被删除、
   Pod 被孤立」的情况。
2. **apply 之前再扫一次**：接管成功后、`apply` 之前会再扫描一次，缩小「核验 → 接管 →
   开始迁移」之间的窗口。
3. **接管仍然使用 resourceVersion CAS**：核验不改变「用 CAS 抢锁」的事实，
   核验失败时不会写入任何东西。
4. **接管后收敛记录**：若记录停在 `PENDING`/`RUNNING` 而 Job 已终结（或已按证据恢复），
   接管后会先把记录写成可核验的终态，再开始新一轮迁移 —— 这样归档下来的
   `previous-*` 不会是「一条含糊的 RUNNING」。
5. **检查与接管之间的竞态**：Job/Pod 只会从「未终态」走向「终态」，不会反向；
   因此「检查通过 → 接管」的窗口不会让一个未终态的 Job 变成终态。真正的残余风险是
   **旁路写入**（人工 `kubectl apply`、其他 GitOps 工具），本任务不覆盖（§10.2）。

## 5. Cleanup 规则（AUD-01 §2.5）

`cleanup`（`EXIT` trap，含 SIGTERM/SIGINT/HUP 路径）按**同一套**规则处理：

| 情形 | 行为 |
|---|---|
| 迁移尚未 apply | 正常释放锁 |
| 记录已写但从未 apply | 复位记录为 `NO_MIGRATION`，再释放锁 |
| 迁移已 apply 且 Job 已 `Complete` **且 Pod 已终止** | 可以释放，记录保留（含 uid / terminal-at） |
| 迁移已 apply 且 Job 已 `Failed` **且 Pod 已终止** | 可以释放，失败证据（state/job/uid/detail）保留 |
| Job 已 `Failed` 但 Pod 仍在执行/终止中 | **不释放**，记录写 `FAILED` 但 `terminal-at` 为空（未确认终结） |
| 迁移仍在运行 / 状态未知 | **不释放**，不删除 Job，记录全部保留（等锁自然过期） |
| 续租失败或已失锁 | **不尝试**清除其他 Runner 的 Lease（只读） |
| SIGTERM/SIGINT | 同上述规则（不会因为「收到信号」就放行） |

释放写入的形状（D6）：只把 `holderIdentity` 置空并刷新 `renewTime`，
`leaseDurationSeconds` **保持配置值**（此前写 1，会让「租期」这个字段在事后排查时
被误读成「1 秒后自动失效」；实际上 `holderIdentity` 为空即已释放）。

不变量：**「释放锁」与「迁移已确认终态（含 Pod 已停止执行）」严格绑定**。
为了让脚本退出而清除记录，等于允许下一个 Runner 在迁移仍在执行时开始新一轮发布。

## 6. 超时与二次判定（AUD-04 / AUD-06）

### 6.1 等待上限到达后必须重新读取

```text
达到 --migration-timeout
  ├─ Job Complete=True 且 Pod 已终止 → 继续发布（再次验证锁），不得报告超时
  ├─ Job Failed=True   且 Pod 已终止 → 迁移失败分支，退出码 2
  ├─ Job 已终态但 Pod 未终止        → 退出码 7（生命周期未确认，锁不释放）
  ├─ Job 仍运行                    → 退出码 6
  ├─ Job NotFound                  → 退出码 7（记录与事实不一致，人工核实）
  └─ 查询失败                      → 退出码 6（状态未知，不假定已结束；锁不释放）
```

### 6.2 与 `activeDeadlineSeconds` 的时间关系

```text
Job activeDeadlineSeconds = 900s
脚本等待上限 --migration-timeout = 1080s   （余量 180s）
```

- `MIGRATION_TIMEOUT <= JOB_ACTIVE_DEADLINE` 时脚本在任何资源变更之前拒绝（退出码 1）；
- 余量小于 180s 时不拒绝，但打印显式告警（自定义参数不会静默退化）；
- 余量的用途：Job 被 kubelet 终止后，Pod 终止与 Job controller 写 `Failed` 条件需要传播时间。

用户自定义参数同样受约束：`--migration-timeout 900` + Job 900 → 拒绝。

## 7. 续租参数约束（AUD-03 / AUD-D7 / T2-06.1.3-F02）

### 7.1 一次 attempt 是两个请求

以 `lease_renew` 的真实执行路径为准：

```text
attempt k（k = 1..attempts）
  ├─ GET Lease         ≤ get_timeout        （读取 rv / holder / renewTime）
  ├─ 校验 holder / uid / 是否已过期          （不过期才继续）
  └─ PUT Lease（CAS）  ≤ replace_timeout    （写 renewTime，resourceVersion 前置条件）
失败 → 退避 backoff_ms → 下一个 attempt
```

因此预算必须写成：

```text
renewal_budget = renew_interval
               + attempts × (get_timeout + replace_timeout)
               + total_backoff
               + scheduling_margin
               < lease_duration
默认：10 + 3×(3+3) + 1 + 5 = 34 < 45
```

### 7.2 为什么旧公式是错的（F-02 的审计结论）

| 公式 | 默认值下算出的最坏耗时 | 与 30s 租期的关系 |
|---|---|---|
| T2-06.1：`interval + 1×timeout + margin` | 10 + 30 + 5 = 45 | 45 > 30 ✗（续租请求本身比租期长） |
| T2-06.1.2：`interval + attempts×timeout + backoff + margin` | 10 + 9 + 1 + 5 = 25 | 25 < 30 ✓（**但漏了 GET**） |
| T2-06.1.3：`interval + attempts×(timeout+timeout) + backoff + margin` | 10 + 18 + 1 + 5 = 34 | 34 > 30 ✗（真实值） |

T2-06.1.2 的公式把每次 attempt 里的 **GET** 忘掉了，相当于把最坏耗时低估了
`attempts × timeout = 9s`。旧默认参数下续租链最坏要 29s 才结束，而租期只有 30s
（且 `renewTime` 是「请求发出时刻」，真实过期点在 27…30s 之间）—— 一次 API 抖动
就可能让续租链跨过过期点，表现为「自己已经被接管，却还在续租」。

### 7.3 默认值重新评估（不是为了让 guard 通过而放大租期）

| 参数 | 旧值 | 新值 | 依据 |
|---|---|---|---|
| `--lease-duration` | 30 | **45** | 预算 34 必须严格小于租期；45 留出 11s（24%）余量，吸收进程调度延迟与 `renewTime` 的秒级精度 |
| `--lease-renew-interval` | 10 | 10 | 45s 租期内有 4 个续租窗口，单次抖动不影响连续性 |
| `--lease-renew-timeout` | 3 | 3 | in-cluster 的 Lease GET/PUT 通常 <100ms，3s ≈ 30 倍抖动余量；收到 2s 会把正常抖动误判成失锁 |
| `attempts` | 3 | 3 | 主进程写迁移记录会推进 `resourceVersion`，409 是常态，需要重试 |
| `backoff` | 2×0.2s（预算 1s） | 不变 | 退避必须远短于续租间隔 |
| `safety_margin` | 5 | 5 | 吸收一次请求的调度延迟 |

取舍被明确记录：

1. **正常抖动下的续租成功率**：单次 attempt 最坏 6s（GET 3s + PUT 3s），三次 18s，
   仍远小于 45s；
2. **网络故障下的失锁检测速度**：续租链（3 次 attempt）最坏 18 + 1 = **19s**；
   加上最近一次成功续租到本次续租间隔的 **10s**，从上次成功续租算起的检测上界是
   **29s**（< 42s 的最早可接管时刻），即旧 Runner 在别人可接管之前就已经写 `lost` 并 SIGTERM；
3. **其他 Runner 的接管延迟**：崩溃后 ≤ 45s（+ 轮询 2s）。代价是比原来多 15s，
   但发布锁本来就是分钟级操作，而「两个发布并行」是不可接受的；
4. **续租链的最大耗时**：19s（含 1s 退避预算，真实退避 400ms），有明确上界；
   检测上界 29s = 间隔 10s + 链 19s；
5. **安全余量**：11s（≈ 预算的 32%）。

明确**不接受**的做法：把 `--lease-renew-timeout` 压到 1s 以保住 30s 租期
（正常抖动会被当成失锁，可用性下降且更难排查）；或把 `attempts` 降到 1
（主进程写记录造成的 409 会直接杀死发布）。

### 7.4 过期之后不得续租（F-02.4）

`lease_renew` 的每个 attempt 在 PUT 之前检查**服务器返回的** `renewTime +
leaseDurationSeconds`：一旦已过期，立即按失锁返回失败，不再写入。

- 旧实现会在 `require_lock_owned_server_side` 里「立即续租两次」把过期的锁**复活**；
  那是在接管窗口里继续写入 —— 对方可能已经核验完上一轮 Job、正准备 replace 接管；
- `resourceVersion` CAS 只能保证「不是我覆盖了别人的写」，不能保证「我没有在别人可以
  接管的时刻继续操作」；
- 该判断的依据是服务端时间戳，不是本地 `lost` 标记，也不是本地时钟推算的「我还没超时」。

代价：进程被长时间挂起后，即使锁其实还没被别人接管，本次发布也会失败（退出码 3）。
这是刻意的 fail-closed：`fail-closed` 的代价是一次重跑，`fail-open` 的代价是并发发布。

**该不变量只降低「正常网络延迟下锁提前过期」的概率**，不能消除 Runner 暂停、网络分区
与缺少 fencing 带来的问题（§10）。失锁窗口的推导见 §10.3。

### 7.5 `renewTime` 锚点语义（T2-06.1.4 / B-1 · B-2）

`renewTime` 该写哪个时刻，是审计 B-1 的根因。三个时刻必须分开：

| 符号 | 含义 | 能否观测 |
|---|---|---|
| `t_req` | 客户端构造并发出 Create/PUT 请求的时刻（**本地时钟**） | 能（写进清单） |
| `t_acc` | API Server 接受并提交该写入的时刻（服务端时钟） | **不能** |
| `t_resp` | 客户端收到响应的时刻（本地时钟） | 能，但可能永远等不到（超时） |

`t_req ≤ t_acc ≤ t_resp`（正常时钟下）。写入规则：

1. **create / takeover**：`renewTime = t_req`（请求发出前一刻）。不能用脚本启动时刻 ——
   旧实现用 `LOCK_ACQUIRED_AT`（脚本启动时刻），预检越长写得越旧；审计复现中预检 51s、
   租期 30s，**Lease 创建时就已经过期**（B-1）。也不能写未来时刻（等于人为延长租期）。
2. create/takeover 返回后**立即**做窗口断言 `lease_require_window`（`LEASE_WINDOW`
   证据行 + `LEASE_ANCHOR_INVALID` fail-closed，退出码 3），随后**再续租一次**，
   把锚点推到写入完成之后（B-2）。两步都不能省：请求耗时同样会吃掉窗口。
3. **renew**：每次 attempt 的 PUT 用本次的 `t_req`；只有写入成功才记录 `t_resp`
   （`LOCK_WRITE_CONFIRMED_AT`），失败分支不得改写 rv / uid / renewTime。
4. 服务端过期时刻是 **精确的 `renewTime + leaseDurationSeconds`**（不需要推断）。
   由于写入在 `t_acc ≥ t_req` 才生效，本 Runner 实际可用的持有窗口最多被
   `t_acc - t_req ≤ 一次请求耗时（≤ 3s）` 扣减 —— 这是**保守方向**，不会推迟过期。

对 §10.3 的影响：`t0` 就是写入的 `renewTime`（= `t_req`）。服务端最早可接管时刻是
`t0 + D`；`t0 + D - G` 只是「扣掉一次请求耗时」的保守下界，两者都远大于检测时刻
`t0 + I + A×(G+R) + B`。因此「停止发起新请求」的边界仍然成立，而「在途请求」不受影响。

时钟偏移（B-1 的另一半）：本机时钟比服务器慢 → 服务器认为租约更早过期（保守）；
比服务器快 → 服务器认为租约活得更久（**不安全方向**，可能让旧持有者以为锁还在）。
这一项无法用脚本消除，**NOT_VERIFIED**，见 §13 与 §10.2。

## 8. 正确性论证（脚本实际提供的保证）

| 要求 | 实现 | 保证强度 |
|---|---|---|
| 原子获取 | `kubectl create -f -`（同名对象只能创建一次，`AlreadyExists` 即占用） | API Server 事务级 |
| 防止同时获取 | 接管/续租/释放全部是 `kubectl replace`（PUT）+ `metadata.resourceVersion`；不匹配则 409 | 乐观并发（CAS） |
| 持有者身份 | `holderIdentity = deploy-<host>-<pid>-<utc>`，续租前先比对 | 强（记录自带） |
| 支持续租 | 后台子进程每 `--lease-renew-interval` 秒 CAS 刷新 `renewTime` | 强（同上） |
| 续租失败即停 | 续租失败写 `lost` 标记并 SIGTERM 主进程；主进程在**每次**资源变更前检查 | 强（脚本内所有变更点都检查） |
| 变更前服务端复核 | 迁移记录写入 / Job apply / Application apply / 发布判定前都重新读取 Lease 并比对 holder+uid | 强（缩小窗口，不能消除 TOCTOU） |
| 迁移未终态不放锁 | `migration_state_blocks_release` 在 `lease_release` 与 `cleanup` 双重生效 | 强 |
| 接管前核验旧 Job | 注解 + 真实对象（state/uid/终态/`deletionTimestamp`） | 强（但依赖 API 可读） |
| 接管前核验旧 Pod | `job-name` 标签列表 + `ownerReferences[].uid` 归属校验 + phase/deletionTimestamp 判定 | 强（fail-closed） |
| 无记录也不放行 | 命名空间级 Job **与 Pod** 扫描（`app.kubernetes.io/component=database-migration`） | 强（需要 `list jobs`/`list pods` 权限） |
| 记录不被意外清空 | 保留路径从服务器读取记录后回写；只有 `migration_record_write` 更新记录 | 强（写入路径显式区分） |
| 正常/异常/SIGTERM 释放 | `trap cleanup EXIT` + `trap on_signal TERM/INT/HUP` | 尽力而为 |
| SIGKILL/崩溃可恢复 | 租期到期后可接管；接管前先核验 Job + Pod | 最终一致 |
| 释放校验持有者 | 释放前读取记录，`holderIdentity != 自己` 时跳过 | 强（读取 + CAS） |
| 获取失败不得继续 | 获取失败直接 `exit 3`/`exit 7`，后续没有任何 apply | 强 |
| Application 结果可见性 | 返回非零时 read-after-write 观察实际状态，不确定则输出 `APPLICATION_OUTCOME_UNKNOWN` | **仅可观测性，不构成 fencing** |

## 9. 两个 Runner 的并发行为

```text
Runner A                       API Server / Lease                     Runner B
   |  create  ------------------>  (不存在，创建成功)  <-------------- create
   |  持有 holder=A                holderIdentity=A                    |  AlreadyExists
   |                              annotations: PENDING/RUNNING         |  读取记录（holder=A 未过期）
   |  迁移 / 应用 / 验证            renewTime=t0                        |  等待…（≤ --lock-wait-timeout）
   |  验证通过                                                          |
   |  replace(holder="") ------->  holderIdentity=""（记录保留 COMPLETE）|
   |                              <---- replace(holder=B, rv 匹配) ---   |  核验上一轮 Job（Complete → 放行）
   |  退出 0                                                             |  重新执行锁内复检 → 迁移 → 应用
                                                                         |  退出 0
```

超时场景：

```text
Runner A: apply Job → 轮询 → 达到上限 → 退出 6（锁不释放，记录 RUNNING，Job uid 保留）
                     ↓ 租期到期
Runner B: 读取过期锁 → 核验 Job → 仍 Running → 退出 7（不做任何变更）
                     ↓ Job 进入 Complete/Failed（人工或 controller 推进）
Runner B: 读取过期锁 → 核验 Job → 已终态 → CAS 接管 → 继续发布
```

行为矩阵：

| 场景 | 脚本行为 | 退出码 |
|---|---|---|
| 两个 Runner 同时 `create` | 一个成功；另一个 `AlreadyExists` → 读取 → 等待 | 成功方 0 |
| 锁被占用且未过期 | 等待到 `--lock-wait-timeout` 仍不成功 → 终止，不做任何变更 | 3 |
| 持有者崩溃（SIGKILL） | 锁不被释放；租期到期后下一次发布核验 Job 再接管 | 3（幸存方等待超时时） |
| 锁已过期且上一轮 Job 已终态 + Pod 已终止 | `replace` + rv 接管，`leaseTransitions+1` | 接管后正常发布 |
| 锁已过期但上一轮 Job 未终态 | 拒绝接管，保留现场 | 7 |
| 锁已过期、Job 已 `Failed` 但 Pod 仍在跑 | 拒绝接管，输出 Pod 名称/phase | 7 |
| 上一轮记录指向的 Job 不存在（有证据） | 按证据收敛记录后接管 | 0（或按后续步骤） |
| 上一轮记录指向的 Job 不存在（无证据） | 拒绝接管 + 人工恢复 | 7 |
| 续租失败（409/5xx/超时） | 写 `lost` → SIGTERM → 停止后续资源变更 | 3 |
| 锁被他人接管 | 续租时发现 `holder != 自己` → 失败中止；释放阶段跳过 | 3 |
| Application apply 在途时失锁 | 输出 `APPLICATION_OUTCOME_UNKNOWN`，不重试、不释放他人锁 | 3 |
| 迁移超时且 Job 仍运行 | 退出，**不释放锁**，Job 不删除 | 6 |
| 正常结束 | 释放（清空 holder，保留迁移记录） | 0 / 2 / 4 / 5 / 6 |

## 10. 安全边界与剩余风险（必须如实认知）

### 10.1 AUD-02 / AUD-05：Fencing 残余风险（**NOT_CLOSED**）

> 交付状态：`AUD-02 = NOT_CLOSED`，`AUD-05 = NOT_CLOSED`。
> 本任务**没有**实现强 fencing；是否接受该风险由 Owner 决策。

Kubernetes API Server 不会为 Lease 之外的写请求校验「谁持有锁」。一个被挂起
（`SIGSTOP`、长时间 GC、VM 冻结、网络分区）的旧持有者在租期过期、锁被接管之后，
**仍可能成功发起一次写请求**。T2-06.1.1 / T2-06.1.2 做了四件事，但都不构成 fencing：

1. 每次资源变更前检查本地 `lost` 标记；
2. 变更前**服务端复核** holder 与 uid（把「本地标记还没传播到」的窗口压小）；
3. **请求发出后**再复核一次归属（`observe_lock_after_request`），用于如实报告结果；
4. 每次写入都是幂等 `kubectl apply`，且发布成功判定会比对目标版本。

必须区分两种情形（这是本文档唯一允许的表述方式）：

| 情形 | 脚本能做什么 | 结果 |
|---|---|---|
| 失锁发生在**请求发出之前** | 直接停止，不发出请求 | 资源未变更（可控） |
| 失锁发生在**请求在途/已发出** | 只能停止后续动作 + 如实报告 | 请求**可能已生效**，无法撤销 |

`read-after-write` 的定位：它只能提高**状态可观测性**，不能撤销已经被 API Server 接受的
请求。因此：

- Application apply 返回非零时，脚本会读回 Deployment：
  - 已是目标版本 → 打印「该请求很可能已被接受并生效」，并输出 `APPLICATION_OUTCOME_UNKNOWN`；
  - 不是目标版本 → 同样输出 `APPLICATION_OUTCOME_UNKNOWN`（可能仍在传播，也可能只应用了一部分）；
  - 两种情况都**不打印 `DEPLOY_K8S_SUCCESS`**，退出码 1；
- 客户端返回非零**不等于**资源没有被修改（测试 `TestApplicationApplyAcceptedButClientTimesOut`
  明确断言 fake 里 `Applied == true`）；
- 失锁后脚本**不会**修改其他持有者的 Lease（测试 `TestInflightAbortDoesNotLeaseWrites`）。

明确**不可用**的「修复」：

```text
GET Lease → 检查 holder → kubectl apply        # 仍然是 TOCTOU，不能声称强 fencing
```

残余窗口的**具体数值**按真实时间戳推导（§10.3）。T2-06.1.3 之后：存活 Runner
**停止发起新请求**的时刻（检测到失锁并截断，≤29s 量级）早于服务端允许接管的时刻
（≥42s），因此「不再发起新请求」这一侧**可以做到**有界。

但这**不等于**「两个 Runner 的写入不可能重叠」，必须把两件事分开：

| 阶段 | 脚本能保证什么 | 保证强度 |
|---|---|---|
| **未发出**的请求 | 失锁检查在每次资源变更**之前**，检测到失锁后不再发起任何新请求 | 有界（≤29s 量级，见 §10.3） |
| **已发出（在途）**的请求 | 什么都保证不了：请求一旦离开进程就交给 API Server，客户端无法撤回 | **无保证**（AUD-05 = NOT_CLOSED） |

也就是说，所谓「窗口」只是「旧 Runner 不再**发起**新写」与「新 Runner **开始**写」之间的
时间关系；一旦旧 Runner 已经发出 `kubectl apply`，该请求可能在锁被接管之后仍被服务端
执行 —— 这与参数怎么调无关，也无法通过缩短租期或提高续租频率消除。
实证见 §10.4：T2-06.1.2 就有的 harness 用例（在途接管后 `Applied == true`）在
T2-06.1.4 更严格的 fake 语义下仍然通过；T2-06.1.4 **新增**的多进程用例
`TestMultiprocessPausedRunnerWriteLandsAfterTakeover` 进一步构造了「接管方甚至已经
释放锁、旧 Runner 恢复后其在途 apply 仍落地」的场景。

`APPLICATION_OUTCOME_UNKNOWN` 只是把「我不知道结果」说出来，不构成 fencing。

真正实现发布级 fencing 需要的架构（本任务**不做**，交给 Owner 决策）：

| 方案 | 机制 | 代价 |
|---|---|---|
| Admission Webhook（ValidatingAdmissionPolicy） | 拒绝「未携带当前 Lease 令牌」的 Deployment/Job 写入：请求需带 `deploy-lock.surgecart.io/token=lease.uid/transitions`，webhook 侧校验 | 需要集群级 webhook 与高可用部署；webhook 自身成为发布关键路径 |
| 专用发布控制器 | 只有控制器能写 Deployment；外部只能提交「发布意图」CR，控制器串行执行并在每次写入前校验令牌 | 架构改动大，需要新的 CRD 与控制器运维 |
| 写侧版本前置条件 | 给 Deployment 加 `metadata.resourceVersion` 前置检查 + 令牌注解 | 仍无法阻止「旧持有者读到新版本号再写」 |

在上述机制落地之前，本仓库**只声称** cooperative/advisory mutual exclusion。

### 10.2 其它剩余风险

1. **过期判定依赖 Runner 本地时钟**：偏快可能提前接管，偏慢会让死锁更久被当成活锁。
   要求 Runner 开启 NTP，偏移超过租期 1/3 视为不合规环境。
2. **锁的作用域是「命名空间里的一个 Lease」**：只保护 `deploy-k8s.sh` 这条路径；
   直接 `kubectl apply -k`、其他 GitOps 工具不受保护（旁路写入也无法被本脚本阻止）。
3. **`create` 无法用 `resourceNames` 限制**：拥有 `create leases` 的身份可创建任意 Lease
   （RBAC 固有限制），应把该权限限制在单命名空间。
4. **迁移记录依赖 API 可读**：`list jobs` / `list pods` 权限缺失或 API 抖动时脚本 fail-closed
   （退出码 3/7），表现为「发布被拒绝」而不是「并发被放过」。
5. **TTL 清理与记录的时序**：Job 被 `ttlSecondsAfterFinished` 清理后，`COMPLETE`/`FAILED`
   记录（uid 已记录且 Pod 已终止）仍会被放行；若 uid 缺失则必须有数据库证据。
   如果有人手工删除一个仍在运行的 Job，记录会停在 `RUNNING` → 后续 Runner 进入人工恢复，
   同时 Pod 扫描会拦住「Job 没了但 Pod 还在跑」的情况。
6. **证据是人工抄录的事实**：`migration-recovery-evidence` 由人工从 `my-shop migrate version`
   抄录，脚本只能校验格式与内部一致性（`dirty`、`version<=latest`、14 位时间戳），
   **不能**独立验证其真实性。拥有 `update leases` 权限的人可以写入错误证据 —— 这与
   「可以直接改 state」是同一类信任边界，只是现在至少留下了可审计的字段。
7. **恢复不覆盖「部分 DDL 已生效」**：证据只能说「账本干净且到某个版本」，
   不能证明 schema 与代码期望完全一致；dirty=true 一律要求人工核对。
8. **锁只能防并发，不能防错误版本**：版本正确性由「版本降级保护」（§11）负责。
9. **Lease 卡住需要人工介入**：见 `docs/development/kubernetes.md` §10.4。
10. **Pod 只读权限缺失时的表现是「发布被拒绝」**：`pods get/list` 缺失会让 Pod 门禁
    以「无法确认」fail-closed（退出码 7），不会退化成「放行」；但它也不会自动修好 ——
    需要按 RBAC 示例补授权（T2-06.1.3 / F-01）。

### 10.3 失锁窗口推导（T2-06.1.3 / F-05）

符号与默认值：`D` = 45s（租期）、`I` = 10s（续租间隔）、`G` = `R` = 3s（GET / PUT 超时）、
`A` = 3（attempts）、`B` = 1s（退避预算）。`t0` = 最近一次**成功**写入的 `renewTime`，
其取值是那次写入请求的**发出时刻** `t_req`（§7.5）。服务端过期时刻**精确等于**
`t0 + D`；`t0 + D - G` 是扣掉一次请求耗时（`t_acc - t_req ≤ G`）后的保守下界，
表中两处都用「≥ `t0+42s`」是为了不依赖 `t_acc` 这个不可观测值。

| # | 问题 | 推导 | 默认值 |
|---|---|---|---|
| 1 | 新 Runner 最早何时可以接管 | 服务端过期 = `renewTime + leaseDurationSeconds`；接管方还要轮询到它（`acquire_release_lock` 每 2s 读一次）并核验旧 Job + Pod | ≥ `t0+42s`（+ 2s 轮询 + 核验） |
| 2 | 存活 Runner 最晚何时 **发现**失锁 | 一次续租链的最坏完成时刻 = `t0 + I + A×(G+R) + B`；链失败后立即写 `lost` 并 SIGTERM | `t0+29s` |
| 3 | 两侧的边界关系（只讨论**未发出**的请求） | 29s < 42s ⇒ 「旧 Runner 停止发起新请求」早于「别人可以接管」 | 停止发起新请求：有界（≤29s） |
| 4 | 续租重试是否会延长失锁检测 | 会：`A×(G+R)+B` 已经计入（这正是 F-02 的修复点）。旧公式漏掉 `A×G`，最坏值被低估 9s | 29s vs 旧模型 20s |
| 5 | 崩溃 / SIGKILL 的 Runner | 不再续租，也不再写入；锁在 `t0+D` 后自然可接管 | 接管延迟 ≤ 47s |
| 6 | 被挂起（SIGSTOP / VM 冻结 / 分区）的 Runner | 恢复后**先 GET**：`renewTime+D ≤ now` ⇒ 立即按失锁停止（§7.4），不 PUT；若尚未过期，PUT 用旧 `resourceVersion`，被别人接管则 409，重试时读到 `holder ≠ 自己` 即停止 | 恢复后 ≤ `G` 发现 |
| 7 | 对非 Lease 资源（Deployment/Job）的**在途**写入 | 变更前的服务端复核只能缩小窗口，**已发出的请求无法撤销**（AUD-02/AUD-05 = NOT_CLOSED） | 由请求在途时长 + 挂起时长决定，**无上界保证** |

结论（本文档唯一允许的表述）：

```text
锁对象层面：旧持有者用陈旧 resourceVersion 的 PUT 一定被 409 拒绝；
            租约过期后的续租会被直接放弃（不复活）。

请求侧边界：只对「还没发出的请求」成立 —— 存活 Runner 在检测到失锁后不再发起新的写入，
            这一时刻早于服务端允许别人接管的时刻（默认参数下 29s < 42s）。

在途请求：  一旦请求已经发出，它就脱离了脚本的控制。客户端超时 / 失锁 / SIGTERM
            都不能撤回它，API Server 仍可能在锁被接管之后执行该请求。
            因此「跨接管的在途写入」依然可能发生，这是 AUD-05 = NOT_CLOSED 的含义，
            不是参数问题，也不因提高续租频率而消失。
```

上一轮审计给出的「约 58s」不作为结论：它把 `D + A×(G+R) + B + 轮询` 直接相加，
既没有区分「检测失锁」与「允许接管」两个不同的时钟，也没有考虑 §7.4 的过期即停。
上面每一项都从**实际时间戳**（`renewTime`、请求发出时刻）与脚本的真实执行路径推导；
「在途请求」一行**不是推导结论**，而是有测试实证的风险（§10.4），且**永不关闭**：
`AUD-05 = NOT_CLOSED`。

### 10.4 在途请求不可撤销（有测试实证，但只在 fake 上）

三个必须分开的时刻（这也是 `renewTime` 锚点争议的来源，见 §7.5）：

```text
t_req   客户端发出请求（写进 renewTime 的是这个时刻）
t_acc   API Server 接受并提交该请求（对象真正被修改）
t_resp  客户端收到响应（可能超时、连接断开，永远收不到）
```

`t_req ≤ t_acc ≤ t_resp`，但三者可以相差很远：客户端超时只说明 `t_resp` 没等到，
**不说明** `t_acc` 没有发生。同理，锁在 `t_acc` 之前丢失并没有阻止 API Server 在
`t_acc` 执行一个已经在路上的写请求。

必须区分的两种「失锁」：

| 失锁发生的时刻 | 脚本能做什么 | 结果 |
|---|---|---|
| 请求**尚未发出** | 检查 `lost` / 服务端复核 holder，直接停止 | 该请求不会发出 |
| 请求**已发出**（在途） | 停止后续动作 + 用 read-after-write 如实报告 | 请求**可能已生效**，无法撤销 |

因此：

- 客户端超时 **≠** 请求未生效。`kubectl apply` 返回非零、进程收到 SIGTERM、租约已过期，
  都不能证明 Deployment / Job 没被修改；
- 失锁之后仍然**可能**发生「旧 Runner 的写在途、新 Runner 已接管」的跨接管写入。
  这不是理论风险，有两条测试实证（都在 fake kubectl 上，**不是**真实集群）：

  | 测试 | 构造方式 | 断言的事实 | 归属 |
  |---|---|---|---|
  | `test/deployk8s/application_inflight_test.go` 的 `TestLockLostDuringApplicationApplyReportsUnknown` | harness 在 fake **之外**套延迟包装，`applyDelay: 1500ms`；marker 出现后用 `stealLease` 在途接管 | `state.Applied == true`、退出码 3、`APPLICATION_OUTCOME_UNKNOWN`、`apply.application` 恰好 1 次 | **T2-06.1.2** 引入；T2-06.1.4 收紧 fake 接管语义后仍通过 |
  | `test/deployk8s/multiprocess_test.go` 的 `TestMultiprocessPausedRunnerWriteLandsAfterTakeover` | 真实多进程：A 的 apply 延迟 6s，A 被 SIGSTOP；B 合法接管并**完成发布、释放锁**；A 恢复 | A 的**在途** apply 在接管之后仍然落地（`apply.application` 计数 +1、`state.Applied == true`），A 以退出码 3 + `APPLICATION_OUTCOME_UNKNOWN` 结束 | **T2-06.1.4 新增**（实测 6.37s PASS） |

  同族的 `TestPausedRunnerResumesAfterLockLoss`（SIGSTOP/SIGCONT）与
  `TestApplicationApplyAcceptedButClientTimesOut`（服务端已接受、客户端超时）覆盖相邻场景；
  **边界**：1500ms / 6s 都是**测试注入的延迟**，不是真实网络在途时长的测量值；
  真实集群上同类窗口的长度是 NOT_VERIFIED；
- 判断线上状态只能靠**只读核对**（`kubectl get -o yaml` / `get pods`），不能靠退出码；
- 真正的修复是写入侧 fencing（令牌校验），本任务不做（§10.1）。

## 11. 版本降级保护

设 `C` = 当前 Deployment 记录的版本，`T` = 本次目标版本。两者都必须是 **Git commit**。

- `C` 的来源：**Deployment 对象**的 Pod 模板标签 `app.kubernetes.io/version`。
  AUD-09 之后不再回退到镜像 tag：tag 可能是 digest/手工标签，会把「不可识别」伪装成
  「已知版本」，让降级判断建立在错误事实上；缺失时直接拒绝（退出码 4）；
- `T` 的来源：本次要发布的镜像 tag，并与清单里的 `app.kubernetes.io/version` 双向校验；
- 判定用 `git merge-base --is-ancestor`（真实祖先关系，不做「SHA 字典序」之类的猜测）。

| 情况 | 处理 | 退出码 |
|---|---|---|
| Deployment 不存在（首发布） | 允许 | — |
| `C == T` | 允许（幂等重发） | — |
| `C` 是 `T` 的祖先 | 允许（升级） | — |
| `T` 是 `C` 的祖先 | 拒绝（降级） | 4 |
| `C` 与 `T` 分叉 | 拒绝 | 4 |
| `C` 缺失 / `C` 或 `T` 无法在本地 Git 历史中解析 | 拒绝（fail-closed，不猜顺序） | 4 |

为什么必须 fail-closed：浅克隆、跨仓库镜像、手工打标签都会让「顺序」不可判定。
把「无法判断」当成「允许」，等价于把降级风险交给运气。

人工授权：`--allow-downgrade`（默认关闭，日志记录 `[AUDIT]` 授权行）。
CI 环境下必须额外设置 `DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1`。

版本检查在**锁内**执行：否则「检查 → 应用」之间会被另一个流水线插入。

## 12. RBAC 最小权限（T2-06.1.4：三类权限 + 预检前移）

权限的**唯一事实来源**是 `scripts/deploy-k8s.sh` 的 `REQUIRED_PERMISSIONS`
（每条都记录使用它的函数与脚本行号）。交付示例只有一份文件
`manifest/kubernetes/examples/deploy-lock/rbac.yaml`，但里面是**三个权限组**，
作用域与绑定对象不同，必须一起 apply：

| # | 权限组 | Kind | 作用域 | 适用主体 |
|---|---|---|---|---|
| 1 | `surgecart-deploy-lock` | `Role` + `RoleBinding` | 命名空间 `surgecart` | 发布身份（ServiceAccount / User / Group） |
| 2 | `surgecart-deploy-workload` | `Role` + `RoleBinding` | 命名空间 `surgecart` | 同一个发布身份 |
| 3 | `surgecart-deploy-cluster` | `ClusterRole` + `ClusterRoleBinding` | 集群级 | 同一个发布身份 |

`kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml` 会按文件顺序
创建这三组对象。**硬前提：`surgecart` 命名空间已存在** —— `Role`/`RoleBinding` 是命名空间
对象，命名空间不存在时那 4 个对象会报 `NotFound`（`ClusterRole`/`ClusterRoleBinding` 仍然
会成功，于是会出现「集群级权限有了、命名空间级权限没有」的半授权状态）。所以顺序是：

```bash
kubectl apply -f manifest/kubernetes/shared/namespace.yaml        # 1) 命名空间先存在
kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml  # 2) 三个权限组
# 3) 自检：受 resourceNames 限制的操作必须带**对象名**（T2-06.1.5 / 审计 F-01）
kubectl auth can-i update leases.coordination.k8s.io/surgecart-deploy-lock -n surgecart
kubectl auth can-i create leases.coordination.k8s.io -n surgecart   # create 没有对象名
```

### 12.1 命名空间级权限（Role + RoleBinding）

| 资源（group/resource） | 动词 | 用途 |
|---|---|---|
| `coordination.k8s.io/leases`（`resourceNames: surgecart-deploy-lock`） | `get` | 读取 holder / `renewTime` / rv / 迁移记录注解；预检必须带对象名 |
| `coordination.k8s.io/leases` | `create` | 首次原子获取锁（`resourceNames` 无法约束 create；预检**不带**对象名） |
| `coordination.k8s.io/leases` | `update` | 续租 / 接管 / 释放 / 写迁移记录（`replace` = PUT + rv CAS） |
| `coordination.k8s.io/leases` | `patch`（**operator-only**） | 脚本**不用**；只供人工恢复（`kubectl annotate lease surgecart-deploy-lock ...`，脚本的恢复提示里给出的正是这条命令）。清单用注解 `deploy-lock.surgecart.io/operator-only-verbs: "leases:patch"` 显式声明，契约测试把它作为唯一允许的「脚本不用的授权」并打印警告 |
| `batch/jobs` | `get`/`list` | 核验上一轮迁移 Job、扫描未终结迁移 |
| `batch/jobs` | `create`/`patch` | `kubectl apply -f migration.yaml` 的两个分支（新建 / 已存在则 PATCH） |
| `core/pods` | `get`/`list` | Pod 生命周期门禁（`job-name` 归属 + component 标签扫描） |
| `core/pods/log` | `get` | 迁移失败时读取 Job 日志（诊断能力） |
| `core/secrets` | `get` | 锁内复检 Secret 是否存在 |
| `apps/deployments` | `get`/`list` | 降级保护读取线上版本；发布后只读汇总 |
| `apps/deployments` | `create`/`patch` | apply Application 清单 |
| `core/configmaps` | `get`/`create`/`patch` | apply Application 清单 |
| `core/services` | `get`/`list`/`create`/`patch` | apply Application 清单 + 汇总 |
| `core/persistentvolumeclaims` | `get`/`create`/`patch` | apply Application 清单 |

### 12.2 集群级权限（ClusterRole + ClusterRoleBinding）

| 资源 | 动词 | 用途 |
|---|---|---|
| `core/namespaces` | `get` | 严格判断命名空间是否存在（只有 NotFound 才算「不存在」） |
| `core/namespaces` | `create` | 首次部署引导命名空间 |
| `core/namespaces` | `patch` | `kubectl apply -f shared/namespace.yaml` 对**已存在**的命名空间走 PATCH（只给 create 时，第二次发布会失败） |
| `authorization.k8s.io/selfsubjectaccessreviews` | `create` | `kubectl auth can-i` 本身就是一次 SSAR |

`REQUIRED_PERMISSIONS` 共 **29** 条：命名空间级 25 条 + 集群级 4 条。没有
`delete`/`deletecollection` —— 脚本永不删除 Job / Pod / Lease / Deployment / Namespace。
**唯一例外**是 `leases` 的 `patch`：它只出现在 Role 里、被清单显式标为
`operator-only`，供值班用 `kubectl annotate` 修记录，脚本自己从不调用。
因此「发布身份只有脚本用到的权限」这句话必须带上这个例外及其原因。

**`namespaces` 为什么必须走 ClusterRole**：`namespaces` 是**集群级资源**，`Role` 的作用域是
单个命名空间，无法授予集群级资源 —— 把 `namespaces get/create` 写进 `Role` 不会生效，
`kubectl auth can-i get namespaces` 仍然返回 `no`。`selfsubjectaccessreviews` 同理。
T2-06.1.3 及更早的示例完全没有这两项，因此「按示例授权 → 首次部署创建命名空间」必然 403
（T2-06.1.4 审计 A-1/A-2）。

### 12.3 resourceNames 与对象级预检（T2-06.1.5 / 审计 F-01）

`surgecart-deploy-lock` 这个 Role 用 `resourceNames` 把 leases 的 get/update/patch
限定到**一个**对象。这是最小权限的关键一步，也决定了权限预检必须怎么写。

**为什么 `resourceNames` 要求对象名称匹配。** Kubernetes RBAC 的授权判定里
（`plugin/pkg/auth/authorizer/rbac` → `ResourceNameMatches`）：

```go
if len(rule.ResourceNames) == 0 { return true }
return has(rule.ResourceNames, requestAttributes.GetName())
```

规则写了 `resourceNames` 时，请求必须带对象名并且**逐字相等**才会命中。因此：

| 预检命令 | 真实集群结果 | 含义 |
|---|---|---|
| `auth can-i get leases.coordination.k8s.io` | `no` | 请求**没有对象名**，不匹配受 resourceNames 限制的规则 |
| `auth can-i get leases.coordination.k8s.io/surgecart-deploy-lock` | `yes` | 具名请求，命中规则 |
| `auth can-i get leases.coordination.k8s.io/another-lock` | `no` | 名称不匹配，默认拒绝 |

`create` 是例外：真实 create 请求的授权属性没有 `name`（名字在请求体里），所以
create 规则永远不能用 `resourceNames` 约束，预检也只能写成
`auth can-i create leases.coordination.k8s.io`。`list` / `watch` /
`deletecollection` 同理（它们都没有对象名）。

> T2-06.1.4 的预检没有带对象名（`auth can-i get leases.coordination.k8s.io`），而伪
> kubectl 当时对「空对象名」错误放行 —— 于是「交付的最小权限 RBAC 跑不起发布」这个
> S1 缺陷在测试里是全绿的（F-01）。T2-06.1.5 同时修正两侧：脚本按 `TYPE/NAME` 检查，
> 替身按真实 RBAC 语义判定（无对象名 → 拒绝）。**不允许**通过删除 `resourceNames`
> 或放宽替身来让测试变绿。

**对象级检查的写法。** `kubectl auth can-i` 只接受两个位置参数
（`VERB` 与 `TYPE | TYPE/NAME | NONRESOURCEURL`，见 `kubectl/pkg/cmd/auth/cani.go`）：

```bash
kubectl auth can-i get    leases.coordination.k8s.io/surgecart-deploy-lock -n surgecart
kubectl auth can-i update leases.coordination.k8s.io/surgecart-deploy-lock -n surgecart
kubectl auth can-i create leases.coordination.k8s.io                        -n surgecart
```

第三个位置参数（`can-i get leases surgecart-deploy-lock`）会被真实 kubectl 拒绝
（`you must specify two arguments: verb resource or verb resource/resourceName`），
替身同样拒绝；`scripts/deploy-k8s.sh` 只用 `TYPE/NAME`。

子资源不能用 `TYPE/NAME` 表达：`pods/log` 会被 kubectl 解析成「名为 `log` 的 Pod」，
检查因此恒真（只要有 `pods get`）。读日志的权限必须用 `--subresource` 检查
（kubectl ≥ 1.24）：

```bash
kubectl auth can-i get pods --subresource=log -n surgecart
```

### 12.4 预检顺序（全部发生在第一个写请求之前）

```text
阶段 1  集群级权限（不含 -n）：namespaces get/create、selfsubjectaccessreviews create
阶段 2  严格判断命名空间是否存在：只有 `<资源> "<名字>" not found` 才是「不存在」
阶段 3  命名空间级权限： leases / jobs / pods / pods-log / secrets / deployments /
                          configmaps / services / pvc（按 REQUIRED_PERMISSIONS 逐项）
        leases 的 get/update **按对象名**检查（TYPE/NAME）；create 不带对象名；
        pods/log 用 --subresource=log 检查
阶段 4  命名空间引导（仅在确认不存在且权限齐备时；幂等）
```

任何一项 `denied`（`auth can-i` 明确返回 `no`）或 `unknown`（SSAR 本身 5xx/超时）
都在阶段 1/3 直接退出 **3**，不会发起锁/迁移/Deployment 的任何写请求。
阶段 3 的 `unknown` 有一个例外：命名空间尚不存在时 SSAR 可能无法判定，脚本会先创建
空命名空间再**复检**这些条目，复检仍失败则退出 3。

`REQUIRED_PERMISSIONS` 的条目是
`verb|group|resource|scope|name|调用点`：`name` 非空表示该检查必须带对象名
（目前只有 leases 的 get/update），为空表示真实请求没有对象名（create/list，
或不受 resourceNames 限制的 get）。`test/manifest/rbac_test.go` 会断言：
`name` 非空的条目必须由清单里**同一个名字**的 `resourceNames` 规则支撑；
leases 上除 create 外的每个动词都必须被 `resourceNames` 限定到 `LOCK_NAME`。

### 12.5 退出码与错误分类（T2-06.1.4 / T2-06.1.5）

`auth can-i` 的三种结果必须分开表述：

| `auth can-i` 结果 | 含义 | 输出 | 退出码 |
|---|---|---|---|
| `yes` | 有权限 | — | — |
| `no`（rc≠0 且输出 `no`） | **明确被拒绝** | `当前身份缺少 <resource>[/<name>] 的 <verb> 权限（Forbidden：auth can-i 明确返回 no）` | 3 |
| 其他（5xx / 超时 / kubectl 报错） | **无法确认**（不是被拒绝） | `无法确认…是否有 <resource>[/<name>] 的 <verb> 权限（查询失败，**不是**「被拒绝」）` | 3 |

受 `resourceNames` 限制的操作在错误信息里显示为对象级写法
（`leases.coordination.k8s.io/surgecart-deploy-lock`），错误信息下方还会打印一条可直接
复制的对象级自检命令 —— 否则运维会照着 `auth can-i get leases.coordination.k8s.io`
排半天，而那条命令在真实集群上永远是 `no`。

运行期对业务资源（Secret / Deployment / Job / Pod）的读失败不能用「不存在」概括。
脚本的 `classify_api_error` 把 stderr 分成五类，**分类标签必须出现在错误信息里**：

| 分类 | 判据 | 含义 | 典型处置 |
|---|---|---|---|
| `forbidden` | `(Forbidden)` / `forbidden:` | API Server 明确拒绝（403） | 权限问题；**不等于对象不存在** |
| `notfound` | 严格匹配 `<resource> "<name>" not found` | 对象确实不存在 | 需要创建/接受首次部署 |
| `api` | 5xx / InternalError / ServiceUnavailable | 服务端故障，状态未知 | fail-closed，先修 API |
| `timeout` | Client.Timeout / 连接拒绝 / i/o timeout / no such host | 客户端没等到响应，**请求可能已生效** | 只读核对，不能当作没发生 |
| `unknown` | 其余（例如读 Job 时收到 `namespaces "x" not found`） | 无法判定 | fail-closed |

对应到具体对象的退出码：

| 场景 | 分类 | 退出码 | 措辞要求 |
|---|---|---|---|
| Secret 读取 | `notfound` | 1 | 「缺少 Secret …（NotFound）」 |
| Secret 读取 | `forbidden` | **3** | 「无权读取 Secret…**这不是「Secret 不存在」**」 |
| Secret 读取 | `api`/`timeout`/`unknown` | 1 | 「无法确认是否存在…**不等于「缺少 Secret」**」 |
| 命名空间读取 | `forbidden` | **3** | 「无权读取命名空间…**这不是「命名空间不存在」**」 |
| 命名空间创建 | `forbidden` | 3 | 提示必须用 ClusterRole（Role 无法授予） |
| Deployment 读取（锁内版本判定） | 任意 | 1 | 「无法确认线上 Deployment 状态（fail-closed）」 |
| 迁移 Job 列表（迁移前门禁） | 任意 | **7** | 「无法确认迁移 Job 状态…不要当作「没有 Job」」 |
| 迁移 Pod 查询（Pod 门禁） | `forbidden`/`api`/`timeout` | **7** | 「无法确认 Pod 状态」≠「Pod 还在跑」 |
| 读取 Job 日志（诊断） | 任意 | 不改变退出码 | 必须打印分类，不得 `|| true` 静默吞掉 |

> 业务资源 403（例如 `deployments`/`configmaps`/`services` 的 `create`/`patch`）在预检阶段
> 就会被拦住并退出 3；如果预检通过后运行期仍收到 403（RBAC 在发布过程中被改动），
> 应用清单 apply 走「apply 失败」路径（退出码 1 + `APPLICATION_OUTCOME_UNKNOWN`），
> 不会被误报成「资源不存在」。

`test/manifest` 的 `TestDeliveredRBACMatchesRequiredPermissions`、
`TestRequiredPermissionsCoversAuditFindings`、`TestScriptKubectlUsageIsDeclared` 与
`TestDocRBACManifestsAndPermissions`（T2-06.1.4）自动比对
「脚本实际使用的资源操作 ⊆ 脚本声明的权限 ⊆ 交付 RBAC 的授权（按 namespace / cluster 作用域分组）」——
**不允许只靠手工检查 YAML 证明权限完整**。

## 13. 验证方式与边界

| 层级 | 手段 | 结论 |
|---|---|---|
| 脚本行为（锁/迁移生命周期/版本/判定/故障/续租参数/Pod 门禁/证据恢复/在途请求） | `go test ./test/deployk8s/`：真实执行脚本 + fake kubectl（真实进程、真实并发、真实 SIGTERM/SIGKILL/SIGSTOP、外部延迟包装） | 已验证（本仓库环境） |
| 续租预算与实际耗时（F-02） | `TestRenewChainStaysWithinBudget`：GET/REPLACE 各延迟 0.9s（超时上限 1s），用外部包装的毫秒时间戳测量 3 次 attempt 的真实耗时并与公式上界比对 | 已验证（脚本语义 + 实测耗时） |
| 租约过期后的续租（F-02.4） | `TestRenewAbortsWhenLeaseExpiresDuringRetries`：重试期间把服务端 `renewTime` 推过期，断言不再写 Lease | 已验证（脚本语义） |
| `renewTime` 锚点（B-1 / B-2） | `TestMultiprocessPreflightDelayKeepsRenewTimeFresh`（预检 16s > 租期 15s：旧实现必然创建即过期）、`TestMultiprocessStaleRenewTimeAtCreateFailsClosed`（`LEASE_ANCHOR_INVALID` + `LEASE_WINDOW … verdict=expired`，退出码 3，零 apply、不改写过期锁的 `renewTime`） | 已验证（脚本语义 + 证据行自证） |
| 过期锁不得被「续命」（B-3 / B-4） | `TestRecordWriteRefusedWhenLeaseExpired`：租约在服务端过期后，写 COMPLETE 记录被拒绝（退出码 3）、不写 Lease、不改 `renewTime` | 已验证（脚本语义） |
| 退避预算与实现同源（B-5） | `TestRenewBackoffBudgetIsSingleSourced`：`LOCK_RENEW_BACKOFF_MS` → 实际 sleep → 预算三者一致，且最后一次失败后不再 sleep | 已验证（源码契约） |
| 写路径强制授权（T2-06.1.4 / A-5） | `test/deployk8s/rbac_write_test.go`（A 交付，**无 `t.Skip`**）：`TestDeliveredRBACOnlyUpgradeSucceeds`、`TestDeliveredRBACOnlyFirstDeploySucceeds`（除退出码外还断言预检对象、调用次数与最终资源状态）；`TestWritePathDeniedAfterPreflightFailsClosed`（T2-06.1.5 改写为真实 TOCTOU：预检通过 → 在途把 update 规则换成别的对象名 → 写入被拒 → fail-closed） | 已验证（**fake 写路径**）；真实集群 RBAC/admission **NOT_VERIFIED** |
| 权限契约（F-01 / T2-06.1.4 A-1/A-2） | `test/manifest`：`TestDeliveredRBACMatchesRequiredPermissions`（脚本声明 ⊆ 交付 RBAC，按作用域 + 对象名）、`TestRequiredPermissionsCoversAuditFindings`、`TestRequiredPermissionsCoverBuiltManifests`、`TestScriptKubectlUsageIsDeclared`；`test/deployk8s/rbac_test.go`：`TestEveryRequiredPermissionIsEnforced` 等逐项拒绝 + Forbidden/unknown 区分 | 已验证（静态契约 + 脚本行为） |
| resourceNames 对象级语义（T2-06.1.5 / F-01） | `test/deployk8s/fakestate/rbac_test.go`：正确名称允许 / 错误名称拒绝 / 无对象名拒绝 / create 与 list 不受名称规则放行 / resourceNames 无通配 / 交付清单直连替身；`test/deployk8s/rbac_resource_name_test.go`：预检按 `TYPE/NAME` 检查（calls.log）、改坏名称或删除规则 → 退出码 3 且零写入、`create` 不带对象名、`pods/log` 用 `--subresource` | 已验证（替身语义 + 脚本行为）；真实 `auth can-i` **NOT_VERIFIED** |
| Pod 查询错误分类（F-07） | `test/deployk8s/podquery_test.go`：空列表/全终态/活动 Pod/403/500/超时/归属不一致 | 已验证（脚本语义） |
| 权限/业务资源错误分类（T2-06.1.4 / A-3 / AUD-09） | `test/deployk8s/rbac_test.go`：`TestSecretReadForbiddenIsNotMissingSecret`、`TestSecretReadFailureIsNotMissingSecret`、`TestSecretNotFoundIsReportedAsMissing`、`TestNamespaceReadFailureIsNotReportedAsAbsent`、`TestJobsListForbiddenIsNotUnfinishedJob`、`TestJobsListServerErrorIsNotUnfinishedJob`、`TestJobLogsForbiddenIsReported` | 已验证（脚本语义） |
| 文档契约（T2-06.1.4） | `test/manifest/doc_contract_test.go`：从脚本常量 / `usage()` / RBAC 清单反推事实，断言 6 个 CLI 默认值、退出码枚举、续租预算算式与失锁窗口数值、权限集合（按作用域）、`AUD-02`/`AUD-05` 结论、NOT_VERIFIED 项 | 已验证（静态契约，改任一侧都会失败） |
| 在途写跨接管生效（AUD-05） | ① `test/deployk8s/application_inflight_test.go`：`TestLockLostDuringApplicationApplyReportsUnknown`（T2-06.1.2 引入；注入 1500ms 延迟 + 在途接管 → `Applied == true`、退出码 3、`APPLICATION_OUTCOME_UNKNOWN`）、`TestPausedRunnerResumesAfterLockLoss`、`TestApplicationApplyAcceptedButClientTimesOut`；② `test/deployk8s/multiprocess_test.go`：`TestMultiprocessPausedRunnerWriteLandsAfterTakeover`（**T2-06.1.4 新增**，真实多进程；A 在途 apply 在 B 接管并释放锁之后仍落地，实测 6.37s PASS） | 已验证（**fake kubectl + 注入延迟**；真实集群的窗口长度 NOT_VERIFIED） |
| 接管语义（F-06） | 替身推进 `resourceVersion`；`TestStaleResourceVersionCannotOverwriteTakeover` 断言旧 rv 的 PUT 得到 409 且不覆盖新 holder | 已验证（**替身语义**，不等于真实 API Server） |
| 清单契约（版本标签、RBAC 名称与授权范围、超时与租期预算、help/文档默认值） | `test/manifest` 从 Kustomize 构建产物与源文件断言 | 已验证 |
| 真实 API Server 的 Lease 竞争 | 需要在真实集群并发跑两个 Runner | **NOT_VERIFIED** |
| RBAC 最小权限的实际效果（含 `jobs`/`pods` 读取） | 需要真实 `auth can-i` 与真实 RoleBinding | **NOT_VERIFIED** |
| 时钟偏移导致的误接管 | 需要可控时钟的真实集群 | **NOT_VERIFIED** |
| fencing 窗口的实际大小 | 需要真实暂停 Runner + 网络分区实验 | **NOT_VERIFIED** |
| Job 被 kubelet 以 `DeadlineExceeded` 终止后的条件传播时延 | 需要真实集群与真实 DDL 负载 | **NOT_VERIFIED** |
| 「Failed 但 Pod 仍在 Running」的真实时序 | 需要真实集群 + 真实的 MySQL 长 DDL | **NOT_VERIFIED**（仅在 fake 中构造） |

fake kubectl 能证明什么（T2-06.1.1 / T2-06.1.2 / T2-06.1.3 补齐，T2-06.1.4 扩展）：

- Lease 的 `create` 原子性、`replace` 的 resourceVersion / UID 校验、确定性 409；
- API 500 与客户端请求超时对续租判定的影响；**续租链的真实耗时**（外部包装的毫秒时间戳）；
- 接管 = 一次成功写入：推进 `resourceVersion`、保持 UID，旧 rv 的 PUT 得到 409；
- Job 状态序列（不存在 / 运行中 / 删除中 / Complete / Failed）、Job UID 变化；
- Pod 的 `ownerReferences`（含「属于另一个 Job」「没有 owner」）、phase、`deletionTimestamp`；
- Pod 查询的 403 / 500 / 超时 / 空列表 / 活动 Pod 的五种结果与对应措辞；
- 「apply 返回错误但对象已创建」「迁移后 Job 消失」「Application apply 在途失锁」这类窗口；
- 每次 Lease 写入**携带了什么记录**（`lease.write` 事件），因此可以逐次断言记录未被清空；
- 两个真实 bash 进程共享同一状态目录时的互斥与接管；
- T2-06.1.4：**按调用语义注入故障**（verb / group / resource / namespace / name / selector /
  intent，可带序号），以及**按交付的 RBAC 清单判定授权**（唯一入口 `Authorize`，默认拒绝）。
  **写路径**（create / replace / apply / delete）未授权即 403；`apply` 按真实 kubectl 语义
  区分动词：对象不存在用 `create`、已存在用 `patch`（被拒再退回 `update`）。
  **读路径**（get/list）的授权不由此判定，而是由脚本的 `auth can-i` 预检覆盖 ——
  不要把它读成「所有请求都强制鉴权」。
  交付的 deploy-lock RBAC 清单**自给自足**：只喂这份清单即可跑通一次完整发布
  （实测 `test-deploy-k8s.sh` PASS）；清单未覆盖的对象（如 `apps/statefulsets`）仍然 403。
  这些能力让「403 被误报成不存在」「权限缺失漏检」这类问题可以被测试直接证伪。

fake kubectl **不能**证明什么：

- etcd 事务与多 apiserver 下的真实乐观并发；
- 真实 watch 缓存的可见性延迟（本脚本用 `get` 直读，仍是缓存读）；
- **真实** RBAC / Admission / 网络分区 / 时钟偏移 / 真实 DDL 时序
  （fake 只按交付清单做授权判定，不执行真实 SubjectAccessReview，也没有真实 Admission 链）；
- 真实 kubelet 在 Job `Failed` 之后何时真正停掉 Pod；
- 已经发出的请求能否被撤销（**不能**，这是架构限制）；
- 「按调用语义注入」仍是替身行为：它能构造窗口，但窗口在真实集群上的长度是 NOT_VERIFIED。

两者证明的是不同的事情，报告里不能混为一谈。
