# 发布锁、迁移生命周期绑定与版本降级保护（T2-06.1 / T2-06.1.1）

本文是 `scripts/deploy-k8s.sh` 发布期并发控制的**设计文档**：为什么用 Kubernetes Lease、
锁如何与 Migration Job 的生命周期绑定、迁移生命周期如何核验、它到底保证了什么、
**没有**保证什么，以及剩余风险。

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
    deploy-lock.surgecart.io/migration-state: "MIGRATION_RUNNING"   # 生命周期状态
    deploy-lock.surgecart.io/migration-job: "surgecart-migrate-a1b2c3d4"
    deploy-lock.surgecart.io/migration-job-uid: "0f5c…-uid"
    deploy-lock.surgecart.io/migration-detail: "BackoffLimitExceeded: …"
spec:
  holderIdentity: "deploy-<host>-<pid>-<utc>"   # 持有者身份
  leaseDurationSeconds: 30                       # 租期
  acquireTime: "<RFC3339>"                       # 本次获取时间
  renewTime: "<RFC3339>"                         # 最近续租时间
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

| 注解键 | 含义 | 谁写 |
|---|---|---|
| `deploy-lock.surgecart.io/migration-state` | 状态机当前状态 | 持锁的 Runner |
| `deploy-lock.surgecart.io/migration-job` | 本轮**预期/实际**的 Job 名称 | apply 之前写 |
| `deploy-lock.surgecart.io/migration-job-uid` | 真实 `metadata.uid` | apply 之后回写 |
| `deploy-lock.surgecart.io/migration-detail` | 失败原因等单行证据（≤200 字符） | Failed 时写 |

约束：

1. **每次 Lease 的 CAS 写入都必须整体回写这四个键**——`kubectl replace`（PUT）覆盖整个对象，
   漏写等于丢记录；
2. `state` 为 `MIGRATION_PENDING`/`MIGRATION_RUNNING` 时，记录**不允许被清除**；
   唯一例外是「记录已写但本 Runner 从未发出 apply」——此时可以确定没有 Job 需要追踪；
3. 注解只是「上一轮 Runner 的陈述」，**接管方必须用真实 Job 对象交叉核验**（§4）。

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

| 记录状态 | 允许的真实 Job 状态 | 不一致时 |
|---|---|---|
| `NO_MIGRATION` | 无 Job，或历史 Job 已终态 | 存在未终结 Job → 拒绝开始新一轮迁移 |
| `MIGRATION_PENDING` | 不存在（尚未 apply）/ 未终态 / 已终态 | `Complete/Failed` 视为已终态（允许） |
| `MIGRATION_RUNNING` | 未终态 / 已终态 | 同上 |
| `MIGRATION_COMPLETE` | `Complete=True`，或对象已被 TTL 清理 | 仍为 `active` → 拒绝接管 |
| `MIGRATION_FAILED` | `Failed=True`，或对象已被 TTL 清理 | 仍为 `active` → 拒绝接管 |

**Job `NotFound` 不等于「迁移完成」**：只有在记录已经是 `COMPLETE`/`FAILED`
（写入时用真实对象确认过终态、并记录了 UID）时，对象消失才被当作 TTL 清理；
`PENDING`/`RUNNING` + `NotFound` 一律进入人工恢复（§10）。

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
| apply 成功但客户端超时 | 不看 apply 的退出码：`await_job_object` 轮询真实对象（上限 10s） |
| apply 报错但对象已创建 | 同上：读到对象就按已创建继续，并打印明确告警 |
| apply 报错且对象确实不存在 | 复位记录为 `NO_MIGRATION`，释放锁，退出码 1 |
| 创建 Job 后 Runner 崩溃 | 记录停在 `MIGRATION_PENDING`/`RUNNING`；锁过期后下一个 Runner 拒绝接管 |
| 写入 Job UID 前崩溃 | 记录里 uid 为空：接管方用「按名称读取 + 终态检查」核验，不信任缺失的 uid |

**状态不明确时一律 fail-closed**：退出码 7，保留锁与记录，交人工核实。

## 4. 新 Runner 接管前的核验（AUD-01 §2.4）

接管（锁已释放或已过期）之前执行 `verify_previous_migration`：

| 上一轮 Job 状态 | 新 Runner 行为 |
|---|---|
| `Running` / `Pending` / `Terminating`（`deletionTimestamp` 非空） | **拒绝接管**（退出码 7），等待其进入终态后重跑 |
| `Complete` | 允许接管 |
| `Failed` | 允许接管，但**不自动 force**、不自动删除 Job |
| 查询失败（4xx/5xx/超时） | 拒绝接管 |
| 状态无法识别（未知注解值、记录不完整） | 拒绝接管 |
| 记录存在但 Job `NotFound` | 拒绝接管 + 人工恢复流程（§10） |
| 记录里 uid 与实际 uid 不一致（同名 Job 被删除重建） | 拒绝接管 |

补充规则：

1. **没有记录也要扫描**：`state=NO_MIGRATION` 或注解缺失（升级前的老 Runner）时，
   Runner 会 `kubectl get jobs -l app.kubernetes.io/component=database-migration`
   扫描命名空间；只要存在未终结的迁移 Job 就拒绝接管。这覆盖「上一轮没写注解」。
2. **apply 之前再扫一次**：接管成功后、`apply` 之前会再扫描一次，缩小「核验 → 接管 →
   开始迁移」之间的窗口。
3. **接管仍然使用 resourceVersion CAS**：核验不改变「用 CAS 抢锁」的事实，
   核验失败时不会写入任何东西。
4. **检查与接管之间的竞态**：Job 只会从「未终态」走向「终态」，不会反向；
   因此「检查通过 → 接管」的窗口不会让一个未终态的 Job 变成终态。真正的残余风险是
   **旁路写入**（人工 `kubectl apply`、其他 GitOps 工具），本任务不覆盖（§12）。

## 5. Cleanup 规则（AUD-01 §2.5）

`cleanup`（`EXIT` trap，含 SIGTERM/SIGINT/HUP 路径）按**同一套**规则处理：

| 情形 | 行为 |
|---|---|
| 迁移尚未 apply | 正常释放锁 |
| 记录已写但从未 apply | 复位记录为 `NO_MIGRATION`，再释放锁 |
| 迁移已 apply 且 Job 已 `Complete` | 可以释放，记录保留（含 uid） |
| 迁移已 apply 且 Job 已 `Failed` | 可以释放，失败证据（state/job/uid/detail）保留 |
| 迁移仍在运行 / 状态未知 | **不释放**，不删除 Job，记录全部保留（等锁自然过期） |
| 续租失败或已失锁 | **不尝试**清除其他 Runner 的 Lease（只读） |
| SIGTERM/SIGINT | 同上述规则（不会因为「收到信号」就放行） |

不变量：**「释放锁」与「迁移已确认终态」严格绑定**。为了让脚本退出而清除记录，
等于允许下一个 Runner 在迁移仍在执行时开始新一轮发布。

## 6. 超时与二次判定（AUD-04 / AUD-06）

### 6.1 等待上限到达后必须重新读取

```text
达到 --migration-timeout
  ├─ Job Complete=True → 继续发布（再次验证锁），不得报告超时
  ├─ Job Failed=True   → 迁移失败分支，退出码 2
  ├─ Job 仍运行        → 退出码 6
  ├─ Job NotFound      → 退出码 7（记录与事实不一致，人工核实）
  └─ 查询失败          → 退出码 6（状态未知，不假定已结束；锁不释放）
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

## 7. 续租参数约束（AUD-03）

```text
renew_interval + renew_request_timeout + safety_margin < lease_duration
默认：10 + 5 + 5 = 20 < 30
```

- 旧默认值 `10 + 30 > 30`：续租请求本身比租期还长，锁会在请求返回之前过期；
- 续租使用**专用请求超时**（`--lease-renew-timeout`），其他 kubectl 调用仍是 30s；
- 非法组合在**任何资源变更之前**拒绝（退出码 1，连 kubectl 都不会调用）；
- 续租冲突（409）会短退避重试：主进程写迁移记录也会推进 `resourceVersion`，
  把这种「自己造成的冲突」当成失锁会在发布中途误报失败。

**该不变量只降低「正常网络延迟下锁提前过期」的概率**，不能消除 Runner 暂停、网络分区
与缺少 fencing 带来的问题（§12）。

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
| 无记录也不放行 | 命名空间级 Job 扫描（`app.kubernetes.io/component=database-migration`） | 强（需要 `list jobs` 权限） |
| 正常/异常/SIGTERM 释放 | `trap cleanup EXIT` + `trap on_signal TERM/INT/HUP` | 尽力而为 |
| SIGKILL/崩溃可恢复 | 租期到期后可接管；接管前先核验 Job | 最终一致 |
| 释放校验持有者 | 释放前读取记录，`holderIdentity != 自己` 时跳过 | 强（读取 + CAS） |
| 获取失败不得继续 | 获取失败直接 `exit 3`/`exit 7`，后续没有任何 apply | 强 |

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
| 锁已过期且上一轮 Job 已终态 | `replace` + rv 接管，`leaseTransitions+1` | 接管后正常发布 |
| 锁已过期但上一轮 Job 未终态 | 拒绝接管，保留现场 | 7 |
| 上一轮记录指向的 Job 不存在 | 拒绝接管 + 人工恢复 | 7 |
| 续租失败（409/5xx/超时） | 写 `lost` → SIGTERM → 停止后续资源变更 | 3 |
| 锁被他人接管 | 续租时发现 `holder != 自己` → 失败中止；释放阶段跳过 | 3 |
| 迁移超时且 Job 仍运行 | 退出，**不释放锁**，Job 不删除 | 6 |
| 正常结束 | 释放（清空 holder，保留迁移记录） | 0 / 2 / 4 / 5 / 6 |

## 10. 安全边界与剩余风险（必须如实认知）

### 10.1 当前是协作式（advisory）互斥，不是强 fencing（AUD-02，**未修复**）

Kubernetes API Server 不会为 Lease 之外的写请求校验「谁持有锁」。一个被挂起
（`SIGSTOP`、长时间 GC、VM 冻结、网络分区）的旧持有者在租期过期、锁被接管之后，
**仍可能成功发起一次写请求**。T2-06.1.1 做了三件事，但都不构成 fencing：

1. 每次资源变更前检查本地 `lost` 标记；
2. 变更前**服务端复核** holder 与 uid（把「本地标记还没传播到」的窗口压小）；
3. 每次写入都是幂等 `kubectl apply`，且发布成功判定会比对目标版本。

明确**不可用**的「修复」：

```text
GET Lease → 检查 holder → kubectl apply        # 仍然是 TOCTOU，不能声称强 fencing
```

已经发出的请求**不能被撤销**：脚本发现失锁后能做的只是「不再发起新请求」。
残余窗口：`0 ≤ 窗口 ≤ 续租间隔 + 单次 kubectl 调用时长`（默认约 10s + 30s）。
这个窗口只能缩小，不能消除。

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
4. **迁移记录依赖 API 可读**：`list jobs` 权限缺失或 API 抖动时脚本 fail-closed
   （退出码 3/7），表现为「发布被拒绝」而不是「并发被放过」。
5. **TTL 清理与记录的时序**：Job 被 `ttlSecondsAfterFinished` 清理后，
   `COMPLETE`/`FAILED` 记录仍会被放行（依赖「写入时确认过终态 + uid」）。
   如果有人手工删除一个仍在运行的 Job，记录会停在 `RUNNING` → 后续 Runner 进入人工恢复。
6. **锁只能防并发，不能防错误版本**：版本正确性由「版本降级保护」（§11）负责。
7. **Lease 卡住需要人工介入**：见 `docs/development/kubernetes.md` §10.4。

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

## 12. RBAC 最小权限

| 资源 | 动词 | 用途 | 说明 |
|---|---|---|---|
| `leases`（`resourceNames: surgecart-deploy-lock`） | `get` | 读取锁记录与迁移记录注解 | — |
| `leases` | `create` | 首次获取锁 | 无法用 `resourceNames` 限制（RBAC 固有限制） |
| `leases` | `update` | 续租 / 接管 / 释放 / 写迁移记录（PUT + rv 的 CAS） | `kubectl replace` 需要 `update` |
| `leases` | `patch`（可选） | 人工恢复 runbook | 脚本不调用 |
| `jobs`（namespace 内） | `get` | 按名称核验上一轮迁移 Job（uid/终态） | T2-06.1.1 新增 |
| `jobs` | `list` | 扫描「是否存在未终结的迁移 Job」 | T2-06.1.1 新增 |

为什么脚本检查的是 `get/create/update` 而不是 `patch`：脚本用 `replace`（PUT）实现 CAS。
权限预检发生在**迁移之前**；缺少 `jobs get/list` 时脚本在迁移之前失败（退出码 3），
而不是带着未知的迁移状态继续发布。

## 13. 验证方式与边界

| 层级 | 手段 | 结论 |
|---|---|---|
| 脚本行为（锁/迁移生命周期/版本/判定/故障/续租参数） | `go test ./test/deployk8s/`：真实执行脚本 + fake kubectl（真实进程、真实并发、真实 SIGTERM/SIGKILL） | 已验证（本仓库环境） |
| 清单契约（版本标签、RBAC 名称、超时关系） | `test/manifest` 从 Kustomize 构建产物断言 | 已验证 |
| 真实 API Server 的 Lease 竞争 | 需要在真实集群并发跑两个 Runner | **NOT_VERIFIED** |
| RBAC 最小权限的实际效果（含 `jobs get/list`） | 需要真实 `auth can-i` 与真实 RoleBinding | **NOT_VERIFIED** |
| 时钟偏移导致的误接管 | 需要可控时钟的真实集群 | **NOT_VERIFIED** |
| fencing 窗口的实际大小 | 需要真实暂停 Runner + 网络分区实验 | **NOT_VERIFIED** |
| Job 被 kubelet 以 `DeadlineExceeded` 终止后的条件传播时延 | 需要真实集群与真实 DDL 负载 | **NOT_VERIFIED** |

fake kubectl 能证明什么（T2-06.1.1 补齐）：

- Lease 的 `create` 原子性、`replace` 的 resourceVersion / UID 校验、确定性 409；
- API 500 与客户端请求超时对续租判定的影响；
- Job 状态序列（不存在 / 运行中 / 删除中 / Complete / Failed）、Job UID 变化；
- 「apply 返回错误但对象已创建」「迁移后 Job 消失」这类窗口；
- 两个真实 bash 进程共享同一状态目录时的互斥与接管。

fake kubectl **不能**证明什么：

- etcd 事务与多 apiserver 下的真实乐观并发；
- 真实 watch 缓存的可见性延迟（本脚本用 `get` 直读，仍是缓存读）；
- RBAC、Admission、网络分区、时钟偏移、真实 DDL 时序。

两者证明的是不同的事情，报告里不能混为一谈。
