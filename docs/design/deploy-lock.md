# 发布锁、发布串行化与版本降级保护（T2-06.1）

本文是 `scripts/deploy-k8s.sh` 发布期并发控制的**设计文档**：为什么用 Kubernetes Lease、
锁的获取/续租/释放如何做到 CAS 安全、它到底保证了什么、**没有**保证什么，以及剩余风险。

操作手册（怎么发布、怎么排障、怎么人工解锁）见 `docs/development/kubernetes.md`；
清单与探针语义见 `docs/development/kubernetes.md` §5–§8。

## 1. 问题

T2-06-FINAL-AUDIT 确认的缺口：

1. Migration Job 有数据库级 `GET_LOCK`，**只保护迁移本身**；
2. 应用侧 `kubectl apply` 完全不经过数据库，因此不受该锁保护；
3. 两个发布流水线可以交错执行：A 迁移 → B 迁移 → B 应用 → A 应用；
4. 交错的结果是「旧版本覆盖新版本」，而两个流水线都返回成功；
5. 脚本没有行为级测试，无法证明上述语义。

目标（本任务的核心不变量）：

> 任何通过 `deploy-k8s.sh` 执行的发布，都在**完整发布生命周期**内受发布级互斥保护；
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

## 3. 生命周期

```text
                          ┌──────────────────────────┐
                          │ Lease 不存在              │
                          └───────────┬──────────────┘
                     create（原子）   │  AlreadyExists
                          ▼           ▼
    ┌──────────────────────────────────────────────┐
    │ 读取记录：holder / renewTime / resourceVersion │
    └───────┬───────────────────┬──────────────────┘
            │ holder 为空        │ renewTime+duration < now
            ▼                   ▼
       replace + rv（CAS 接管，leaseTransitions+1）
            │ 409 Conflict → 回到「读取记录」
            ▼
    ┌──────────────────────────┐
    │ 持有锁：发布开始           │
    └───────────┬──────────────┘
                │ 后台每 10s：replace + rv（续租）
                │ 续租失败（409 / 5xx / holder 变成别人）
                │   → 写 lost 标记 → SIGTERM 主进程 → 退出码 3
                ▼
    ┌──────────────────────────────────────────────┐
    │ 锁内复检（Secret / 当前版本 / 降级判断）        │
    │   → Migration apply → 等待 Job                 │
    │   → Application apply → 等待 Deployment 收敛   │
    │   → 发布成功判定（镜像/版本/generation/副本/Pod）│
    └───────────┬──────────────────────────────────┘
                ▼
    replace + rv：holderIdentity=""（释放，不删对象）
```

对应脚本中的步骤（`scripts/deploy-k8s.sh`）：

```text
预检（kubectl/集群/清单构建/占位符/版本一致性）
  → 命名空间引导（幂等，锁的前置条件）
  → 锁权限预检（get/create/update leases）
  → 获取发布锁
  → 启动后台续租
  → 锁内复检：Secret + 版本降级保护
  → Migration apply + 等待
  → Application apply + 等待收敛
  → 发布成功判定
  → 释放发布锁
  → 打印 DEPLOY_K8S_SUCCESS
```

### 3.1 为什么迁移完成后不释放锁

迁移完成只意味着「schema 就绪」。此后的 Application apply、Deployment 收敛、发布成功判定
仍然必须独占：否则第二个流水线可以在这一段插入自己的 Application apply，把刚发布的版本
覆盖掉，而第一个流水线依然打印成功——这正是审计发现的问题。锁必须覆盖到「验证通过」。

### 3.2 为什么释放是「清空 holder」而不是删除 Lease

删除对象会让下一次获取退化为 `create`，而「删除」与「他人 create」之间存在竞态：
旧持有者可以在新持有者 create 成功之后删除它刚创建的锁。清空 `holderIdentity` 用的是
CAS 写入（`resourceVersion` 必须匹配），因此旧持有者无法覆盖新持有者的记录。

## 4. 正确性论证（脚本实际提供的保证）

| 要求 | 实现 | 保证强度 |
|---|---|---|
| 原子获取 | `kubectl create -f -`（同名对象只能创建一次，`AlreadyExists` 即占用） | API Server 事务级 |
| 防止同时获取 | 接管/续租/释放全部是 `kubectl replace`（PUT）+ `metadata.resourceVersion`；不匹配则 409 | 乐观并发（CAS） |
| 持有者身份 | `holderIdentity = deploy-<host>-<pid>-<utc>`，续租前先比对 | 强（记录自带） |
| 支持续租 | 后台子进程每 `--lease-renew-interval` 秒 CAS 刷新 `renewTime` | 强（同上） |
| 续租失败即停 | 续租失败写 `lost` 标记并 SIGTERM 主进程；主进程在**每次**资源变更前检查 | 强（脚本内所有变更点都检查） |
| 正常/异常/SIGTERM 释放 | `trap cleanup EXIT` + `trap on_signal TERM/INT/HUP` | 尽力而为 |
| SIGKILL/崩溃可恢复 | 租期（30s）到期后他人可接管 | 最终一致 |
| 释放校验持有者 | 释放前读取记录，`holderIdentity != 自己` 时跳过并告警 | 强（读取 + CAS） |
| 获取失败不得继续 | 获取失败直接 `exit 3`，后续没有任何 apply | 强 |
| 不依赖本地 flock | 全部状态在 API Server 的 Lease 对象里 | 强 |

补充实现细节：

- **同时校验 `uid` 与 `resourceVersion`**：接管与释放的清单里同时回写 `metadata.uid`，
  避免「同名对象被删除重建」后误写到新对象上。
- **无法解析 `renewTime` 时按「仍被持有」处理**：宁可不接管，也不打断一个可能正在
  执行 DDL 的发布；需要人工确认（见 `docs/development/kubernetes.md` §10.4）。
- **锁等待有上限**（`--lock-wait-timeout`，默认 60s）：等待超时以退出码 3 结束，
  不会无限挂住流水线，也不会「等不到就硬来」。
- **锁状态目录**（临时目录内的 `lock/`）只用于进程内协作（`lost`/`stop` 标记），
  不承担跨 Runner 的互斥职责。

## 5. 两个 Runner 的并发行为

```text
Runner A                       API Server / Lease                     Runner B
   |  create  ------------------>  (不存在，创建成功)  <-------------- create
   |  持有 holder=A                holderIdentity=A                    |  AlreadyExists
   |  迁移 / 应用 / 验证            renewTime=t0                        |  读取记录（holder=A 未过期）
   |                              <---- renew (CAS, rv+1) ---            |  等待…（≤ --lock-wait-timeout）
   |  验证通过                                                                 |
   |  replace(holder="") ------->  holderIdentity=""                       |
   |                              <---- replace(holder=B, rv 匹配) ---     |  接管（leaseTransitions+1）
   |  退出 0                                                              |  重新执行锁内复检 → 迁移 → 应用
                                                                          |  退出 0
```

行为矩阵：

| 场景 | 脚本行为 | 退出码 |
|---|---|---|
| 两个 Runner 同时 `create` | 一个成功；另一个 `AlreadyExists` → 读取 → 等待 | 成功方 0 |
| 锁被占用且未过期 | 等待到 `--lock-wait-timeout` 仍不成功 → 终止，不做任何变更 | 3 |
| 持有者崩溃（SIGKILL） | 锁不被释放；租期到期后下一次发布接管 | 3（幸存方等待超时时） |
| 锁已过期 | `replace` + rv 接管，`leaseTransitions+1` | 接管后正常发布 |
| 续租失败（409/5xx） | 写 `lost` → SIGTERM → 停止后续资源变更 | 3 |
| 锁被他人接管 | 续租时发现 `holder != 自己` → 失败中止；释放阶段跳过（不碰他人锁） | 3 |
| 正常结束 | 释放（清空 holder） | 0 / 2 / 4 / 5 / 6 |

## 6. 安全边界与剩余风险（必须如实认知）

1. **没有 fencing token。** Kubernetes API Server 不会为 Lease 之外的写请求校验「谁持有锁」。
   一个被挂起（`SIGSTOP`、长时间 GC、VM 冻结、网络分区）的旧持有者在租期过期、锁被接管
   之后，**仍可能成功发起一次 `kubectl apply`**。
   - 缓解：① 每次资源变更前检查续租状态；② 续租失败立即中止；③ 变更是幂等的
     `kubectl apply`；④ 发布成功判定会比对目标镜像/版本，被覆盖时会以退出码 5 失败。
   - 残余窗口：`0 ≤ 窗口 ≤ 续租间隔 + 单次 kubectl 调用时长`（默认约 10s + 30s 超时）。
     该窗口只能缩小，不能消除；真正的 fencing 需要写入侧（API Server 或数据层）校验令牌，
     超出本任务范围。
2. **过期判定依赖 Runner 本地时钟。** 时钟明显偏移的 Runner 会误判过期：
   - 时钟偏快 → 可能提前接管一把仍然活着的锁 → 两个发布并行（`leaseTransitions` 会留下痕迹）；
   - 时钟偏慢 → 一把死锁会被更久地认为「仍被持有」（只影响可用性，不影响安全）。
   - 要求：Runner 必须开启 NTP/chrony；时钟偏移超过租期的 1/3 视为不合规环境。
3. **锁的作用域是「命名空间里的一个 Lease」。** 它只保护 `deploy-k8s.sh` 这一条路径；
   直接 `kubectl apply -k` 的人工操作、其他工具（Argo CD 等）不受保护。
4. **`create` 无法用 `resourceNames` 限制。** Kubernetes RBAC 的固有限制：拥有 `create`
   的身份可以创建任意名称的 Lease。授予发布身份 `create leases` 时应当把该身份限制在
   单一命名空间（Role 而非 ClusterRole）。
5. **锁只能防并发，不能防错误版本。** 版本正确性由「版本降级保护」（§7）负责。
6. **Lease 卡住需要人工介入。** 见 `docs/development/kubernetes.md` §10.4。

## 7. 版本降级保护

设 `C` = 当前 Deployment 记录的版本，`T` = 本次目标版本。两者都必须是 **Git commit**。

- `C` 的来源：Deployment Pod 模板标签 `app.kubernetes.io/version`（新清单写入），
  必要时回退到当前镜像 tag；
- `T` 的来源：本次要发布的镜像 tag，并与清单里的 `app.kubernetes.io/version` 双向校验；
- 判定用 `git merge-base --is-ancestor`（真实祖先关系，不做「SHA 字典序」之类的猜测）。

| 情况 | 处理 | 退出码 |
|---|---|---|
| Deployment 不存在（首发布） | 允许 | — |
| `C == T` | 允许（幂等重发） | — |
| `C` 是 `T` 的祖先 | 允许（升级） | — |
| `T` 是 `C` 的祖先 | 拒绝（降级） | 4 |
| `C` 与 `T` 分叉 | 拒绝 | 4 |
| `C` 或 `T` 无法在本地 Git 历史中解析 | 拒绝（fail-closed，不猜顺序） | 4 |

为什么必须 fail-closed：浅克隆、跨仓库镜像、手工打标签都会让「顺序」不可判定。
把「无法判断」当成「允许」，等价于把降级风险交给运气。

人工授权：`--allow-downgrade`（默认关闭，日志记录 `[AUDIT]` 授权行）。
CI 环境下必须额外设置 `DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1`，避免回滚开关被写进流水线后
长期生效。降级的数据库风险见 `docs/development/kubernetes.md` §9.5。

版本检查在**锁内**执行：否则「检查 → 应用」之间会被另一个流水线插入。

## 8. RBAC 最小权限

| 动词 | 用途 | 说明 |
|---|---|---|
| `get` | 读取锁记录（holder/renewTime/resourceVersion） | — |
| `create` | 首次获取锁 | 无法用 `resourceNames` 限制（RBAC 固有限制） |
| `update` | 续租 / 接管 / 释放（PUT + rv 的 CAS） | `kubectl replace` 需要 `update` |
| `patch`（可选） | 人工恢复 runbook | 脚本不调用；授予它是为了让值班人员能手工清空 holder |

为什么脚本检查的是 `get/create/update` 而不是 `patch`：脚本用 `replace`（PUT）实现 CAS，
它需要的是 `update`。检查一个流程里根本不用的动词，只会让授权范围无谓扩大。

权限预检发生在**迁移之前**（`kubectl auth can-i <verb> leases.coordination.k8s.io -n surgecart`）：
没有 leases 权限就无法建立互斥，此时继续迁移等于放弃本任务的核心不变量。

## 9. 验证方式与边界

| 层级 | 手段 | 结论 |
|---|---|---|
| 脚本行为（锁/迁移/版本/判定/故障） | `go test ./test/deployk8s/`：真实执行脚本 + fake kubectl（真实进程、真实并发、真实 SIGTERM/SIGKILL） | 已验证（本仓库环境） |
| 清单契约（版本标签、RBAC 名称一致性） | `test/manifest` 从 Kustomize 构建产物断言 | 已验证 |
| 真实 API Server 的 Lease 竞争 | 需要在真实集群并发跑两个 Runner | **NOT_VERIFIED** |
| RBAC 最小权限的实际效果 | 需要真实 `auth can-i` 与真实 RoleBinding | **NOT_VERIFIED** |
| 时钟偏移导致的误接管 | 需要可控时钟的真实集群 | **NOT_VERIFIED** |
| fencing 窗口的实际大小 | 需要真实暂停 Runner + 网络分区实验 | **NOT_VERIFIED** |

fake kubectl 只能证明「脚本在给定状态序列下的行为」，不能冒充 API Server 的并发语义：
fake 的 `create` 用文件锁串行化，真实环境是 etcd 事务 + API Server 的乐观并发。
两者证明的是不同的事情，报告里不能混为一谈。
