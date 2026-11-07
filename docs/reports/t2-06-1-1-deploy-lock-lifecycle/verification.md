# T2-06.1.1 验证报告：Migration Job 生命周期与 Lease 生命周期绑定

- 基线：`a333e26c1bb24f22517f344c62c04f9af5b3e9d5`（分支 `feat/k8s-t2-06-1-deploy-safety`）
- 任务分支：`feat/k8s-t2-06-1-1-deploy-lock-lifecycle`
- 结论：`T2-06.1.1_PASS_WITH_RISKS`
  - AUD-01 / AUD-03 / AUD-04 / AUD-06 / AUD-08 / AUD-09 / AUD-10 / AUD-11 已关闭（脚本语义层面，fake kubectl 验证）；
  - **AUD-02 未修复**：当前仍是 cooperative/advisory 互斥，列入残余架构风险，等待 Owner 决策；
  - 真实集群相关项为 NOT_VERIFIED（§8）。

本报告记录「改了什么、怎么验证的、什么还没验证」。设计论证见
`docs/design/deploy-lock.md`，操作手册见 `docs/development/kubernetes.md`。

## 1. Git 基线与分支

| 项 | 值 |
|---|---|
| 基线 HEAD | `a333e26c1bb24f22517f344c62c04f9af5b3e9d5` |
| 基线分支 | `feat/k8s-t2-06-1-deploy-safety` |
| 远端同名分支 SHA | `a333e26c1bb24f22517f344c62c04f9af5b3e9d5`（与基线一致） |
| 新分支 | `feat/k8s-t2-06-1-1-deploy-lock-lifecycle` |
| 工作区（开始） | 干净 |
| 历史提交 | 未修改 T2-06 / T2-06.1 的历史提交；未合并 `develop` / `main` |
| 业务代码 | 未修改订单、库存、Kafka、Outbox、迁移 SQL 等无关逻辑 |

## 2. 设计变更

| 文件 | 变化 |
|---|---|
| `scripts/deploy-k8s.sh` | 迁移生命周期与 Lease 绑定（注解记录 + 接管核验 + cleanup 规则）；续租参数不变量与专用请求超时；超时二次判定；Job deadline 时间关系；服务端 holder/uid 复核；严格 NotFound 解析；多容器 Pod 只认 app 容器；退出码 7 |
| `test/deployk8s/fakestate/state.go` | Lease 注解、Job UID / deleting、Job 列表、续租专用故障注入、请求超时、`JobGetFailAt`、`apply 报错但已创建`、`DeploymentGetError`、多容器 Pod |
| `test/deployk8s/fakekubectl/main.go` | replace 校验 resourceVersion **与 UID**；确定性 409；API 500 / 客户端超时；`get jobs` 列表；Job UID 稳定性；apply 错误但对象已创建；锁在「写入指定状态后」被接管 |
| `test/deployk8s/*_test.go` | 新增 `lifecycle_test.go`(11)、`renew_test.go`(8)、`inflight_test.go`(2)、`read_test.go`(7)；`migration_test.go` 扩到 14；`fault_test.go` / `lease_test.go` / `harness_test.go` 同步 |
| `test/manifest/manifest_test.go` | 新增静态契约：脚本 `MIGRATION_TIMEOUT` 与 Job `activeDeadlineSeconds` 的关系；租期预算不变量 |
| `manifest/kubernetes/examples/deploy-lock/rbac.yaml` | 新增 `batch/jobs get,list`（迁移生命周期核验所需的只读权限） |
| `docs/design/deploy-lock.md` | 重写：数据契约、状态机、接管规则、cleanup 规则、超时、续租参数、fencing 边界与替代架构 |
| `docs/development/kubernetes.md` | 发布步骤、参数、退出码 7、超时后 Lease/Job 状态、`§10.8` 人工恢复、`§9.7` fencing 边界、验证分级 |
| `docs/design/deployment.md` / `docs/development/migrations.md` | 同步发布流程与退出码语义 |

## 3. Lease 与 Job 生命周期设计

### 3.1 数据契约（Lease 注解）

```text
deploy-lock.surgecart.io/migration-state    NO_MIGRATION | MIGRATION_PENDING |
                                            MIGRATION_RUNNING | MIGRATION_COMPLETE | MIGRATION_FAILED
deploy-lock.surgecart.io/migration-job      本轮预期/实际 Job 名称
deploy-lock.surgecart.io/migration-job-uid  真实 metadata.uid（apply 后回写）
deploy-lock.surgecart.io/migration-detail   失败原因（单行 ≤200 字符）
```

约束：每次 CAS 写入都整体回写四个键；`PENDING`/`RUNNING` 记录不得清除（唯一例外：
记录已写但本 Runner 从未发出 apply）；注解必须用真实 Job 对象交叉核验。

### 3.2 状态机与写入顺序

```text
NO_MIGRATION → MIGRATION_PENDING → MIGRATION_RUNNING → MIGRATION_COMPLETE
                                                   ↘ MIGRATION_FAILED
```

apply 之前写「预期 Job 名」（uid 空）→ apply → 读真实 Job 拿 uid → 回写 uid →
等待中观察未终态时写 RUNNING（只写一次）→ 终态写 COMPLETE/FAILED。

四个窗口的处理：

| 窗口 | 处理 |
|---|---|
| apply 成功但客户端超时 | 不看 apply 退出码，轮询真实对象（上限 10s，不超过脚本上限） |
| apply 报错但对象已创建 | 读到对象即继续，打印告警；不重复 apply |
| apply 报错且对象确实不存在 | 复位记录 `NO_MIGRATION`，释放锁，退出码 1 |
| 创建 Job 后 / 写 UID 前崩溃 | 记录停在 `PENDING`/`RUNNING`；接管方用「按名称读取 + 终态检查」（uid 可缺失）核验 |

### 3.3 接管核验（真实 Job 为准）

| 上一轮 Job | 行为 |
|---|---|
| `Running` / `Pending` / `Terminating` | 拒绝接管（退出码 7） |
| `Complete` | 允许接管 |
| `Failed` | 允许接管；不 force、不删 Job |
| 查询失败 / 未知状态 / 记录不完整 | 拒绝接管（7） |
| 记录存在但 Job `NotFound` | 拒绝接管 + 人工恢复（`kubernetes.md` §10.8） |
| uid 不一致（同名 Job 重建） | 拒绝接管（7） |
| 无记录 + 存在未终结迁移 Job | 拒绝接管（7，命名空间级 Job 扫描兜底） |

接管仍使用 `resourceVersion` CAS；接管成功后、apply 之前再扫描一次 Job 列表。

### 3.4 Cleanup 规则

| 情形 | 行为 |
|---|---|
| 迁移未 apply | 释放锁 |
| 记录已写但未 apply | 复位 `NO_MIGRATION` 后释放 |
| Job `Complete` / `Failed` | 释放，记录（含 uid / 失败原因）保留 |
| 迁移运行中 / 状态未知 | **不释放**，不删 Job，记录保留 |
| 续租失败 / 已失锁 | 不尝试清除他人 Lease |
| SIGTERM / SIGINT | 同上述规则 |

## 4. 失败恢复机制

| 场景 | 退出码 | 恢复方式 |
|---|---|---|
| 迁移 `Failed=True` | 2 | 修根因 → 确认无 Pod → 删除失败 Job 重跑；脚本不 force / 不删 Job |
| 等待上限到达、Job 仍运行 | 6 | 继续 `kubectl wait`；锁不释放，下一个 Runner 会被拒绝接管 |
| 等待上限到达、Job 已 Complete | 0 | 二次判定后继续发布 |
| 等待上限到达、Job 已 Failed | 2 | 同失败分支 |
| Job 查询失败（超时/5xx） | 6 | 状态未知：锁保留，人工核实 |
| Job `NotFound` 而记录非终态 | 7 | 按 `kubernetes.md` §10.8 核实数据库后手工标注 |
| 记录与事实冲突（uid / 未知状态） | 7 | 同上 |
| 锁被接管 / 续租失败 | 3 | 查看 Lease 与续租日志；不手工清迁移记录 |

## 5. Fencing 残余风险（AUD-02，**未修复**）

T2-06.1.1 增加了「变更前服务端复核 holder + uid」，但没有、也不能消除 TOCTOU：

- API Server 不校验「写 Deployment 的请求是否持有锁」；
- 被挂起 / 网络分区的旧持有者在锁被接管后**仍可能成功写入一次**；
- 已经发出的 `kubectl apply` 不能被撤销（测试 `TestInflightLockLossStopsNewOperations`
  明确断言「失锁后 Job 仍在集群里」）；
- 残余窗口 `0 ≤ 窗口 ≤ 续租间隔 + 单次 kubectl 调用时长`（默认约 10s + 30s）。

因此文档统一表述为 **cooperative/advisory mutual exclusion**；
真正 fencing 需要 Admission Webhook（ValidatingAdmissionPolicy）或专用发布控制器，
分析见 `docs/design/deploy-lock.md` §10.1。**本任务不声称已实现强 Fencing。**

## 6. 测试与真实结果

用例总数 75（基线 40，新增 35），全部真实执行 `scripts/deploy-k8s.sh`：

| 文件 | 用例 | 覆盖 |
|---|---|---|
| `lifecycle_test.go`（新，11） | 超时后锁与记录保留、第二个 Runner 被拒绝、Job 终态后接管、Failed 接管不 force、查询失败拒绝、Job `NotFound` 人工恢复、崩溃后识别、apply 报错但已创建、Application apply 失败、无记录扫描兜底、未知状态拒绝 | AUD-01 |
| `renew_test.go`（新，8） | 非法/临界合法参数、续租专用超时生效、续租超时/500/409/UID 冲突、后台续租与 cleanup 并发 | AUD-03 |
| `inflight_test.go`（新，2） | 迁移记录写入后失锁 → 不再发起新操作、已发出的请求不可撤销、失锁后不写 Lease | AUD-02 边界 |
| `read_test.go`（新，7） | Pod 查询失败 ≠ 无 Pod、命名空间 NotFound ≠ 首次部署、标签缺失不猜版本、多容器 Pod 只认 app | AUD-08/09/11 |
| `migration_test.go`（14） | 完成/失败/超时分离、Job 消失、超时二次判定（Complete→继续 / Failed→2 / 未知→6）、超时后 Job 不存在、deadline 关系（拒绝/建议余量/余量告警） | AUD-04/06 |
| `lease_test.go`（10） | 首次创建、并发竞争、串行化、占用等待、过期接管、续租、续租失败、旧持有者不释放、权限预检、释放 CAS | T2-06.1 回归 |
| `version_test.go`（10）/ `verify_test.go`（7） | 降级保护、发布成功判定 | T2-06.1 回归 |
| `fault_test.go`（6） | kubectl 缺失、集群不可达、apply 失败、SIGTERM（迁移未终态不释放）、崩溃后过期接管、临时文件清理 | 故障路径 |

fake kubectl 本轮修复：`replace` 校验 `resourceVersion` **与 uid**、确定性 409、
Job 状态变化与 UID 稳定、Job `NotFound`、API 500、客户端请求超时、
「apply 报错但对象已创建」、共享状态下的独立 Runner（两个真实 bash 进程）。

### 6.1 门禁真实结果（本机环境，全部为真实退出码）

| 门禁 | 命令 | 退出码 |
|---|---|---|
| 格式 | `gofmt -l .`（无输出） | 0 |
| 构建 | `go build ./...` | 0 |
| 静态检查 | `go vet ./...` | 0 |
| 单元测试 | `bash scripts/test-unit.sh` | 0 |
| 发布脚本行为 | `bash scripts/test-deploy-k8s.sh`（75 用例，PASS 75） | 0 |
| Kustomize 清单 | `bash scripts/test-k8s-manifest.sh`（kubeconform 9/9 Valid） | 0 |
| 竞态 | `bash scripts/test-race.sh` | 0 |
| 完整入口 | `bash scripts/test.sh` | 0 |
| 迁移门禁 | `bash scripts/test-migration.sh` | 0 |
| 容器运行时 | `bash scripts/test-container-runtime.sh`（16 项断言） | 0 |

### 6.2 原审计问题回归

| 审计项 | 复现要点 | 回归结果 |
|---|---|---|
| AUD-01 | 迁移仍在运行 → 脚本退出 → 旧实现释放锁 → 另一个 Runner 接管并开始新迁移 | 已复现并修复：`TestMigrationTimeoutKeepsLeaseAssociation`、`TestMigrationTimeoutBlocksNextRunnerWithActiveJob`、`TestSigtermDuringMigrationKeepsLease`、`TestApplyErrorButJobCreatedIsNotMisjudged` 全部 PASS；`apply.migration` 计数保持 1，锁仍由旧 Runner 持有 |
| AUD-02 | 旧持有者失锁后仍可能写入 | **未修复**，改为显式陈述边界：`TestInflightLockLossStopsNewOperations` 证明「失锁后不再发起新操作」且「已发出的请求仍在集群里」 |
| AUD-03 | `10 + 30 > 30` 导致锁先过期 | 已复现并修复：`TestLeaseParameterInvariantRejectsIllegalCombinations`（5 组，含旧默认值）退出码 1 且**零 kubectl 调用**；`TestRenewRequestTimeoutAbortsDeployment` 等 4 个续租故障用例 PASS |
| AUD-04 | 等待超时后不再读 Job，Complete 被误报为超时 | 已复现并修复：`TestMigrationTimeoutThenCompleteContinues`（继续发布）、`...ThenFailedExits2`（退出 2）、`...ThenQueryFailureExits6`（退出 6）PASS |
| AUD-05 | 任务正文与仓库均未给出该条目的描述/复现步骤 | **无法逐条回归**（见 §8）。已回归最接近的路径：`verify_test.go` 7 项 + `TestApplicationApplyFailureReleasesLeaseAndKeepsRecord` 全 PASS。若 AUD-05 指其它具体缺陷，需要 Owner 提供复现步骤 |
| AUD-06 | Job deadline 900 = 脚本超时 900 | 已复现并修复：`TestMigrationTimeoutMustExceedJobDeadline`（拒绝）、`TestMigrationTimeoutMarginSatisfied`（900+180=1080）、`TestMigrationTimeoutMarginTooSmallWarns`（告警）+ `test/manifest` 静态契约 PASS |

## 7. 审计缺陷逐项关闭情况

| 审计项 | 结论 | 证据 |
|---|---|---|
| AUD-01 迁移生命周期与锁绑定 | **已关闭**（脚本语义） | §3、§6.1、§6.2 |
| AUD-02 缺少 fencing | **未关闭（残余架构风险）** | `deploy-lock.md` §10.1；文档不再声称强 fencing |
| AUD-03 续租参数不变量 | **已关闭** | 参数校验 + 专用请求超时 + 冲突重试 + 6 个用例 |
| AUD-04 超时后二次判定 | **已关闭** | 二次判定分支 + 3 个用例 |
| AUD-05 | **未验证（缺少描述）** | 见 §6.2 / §8 |
| AUD-06 Job deadline 与脚本超时 | **已关闭** | 1080 vs 900 + 校验 + 3 个用例 + 静态契约 |
| AUD-08 Pod 查询失败 ≠ 无 Pod | **已关闭** | `read_job_pods` 返回码区分 + `TestPodQueryFailureIsNotReportedAsNoPods` |
| AUD-09 NotFound 严格解析 / 版本标签来源 | **已关闭** | `is_not_found_for`（资源名+对象名）+ 回归用例；删除镜像 tag 回退 |
| AUD-10 Timeout 恢复文档 | **已关闭** | `kubernetes.md` §10.2 重写（Lease/Job 状态、二次判定、下一个 Runner 行为） |
| AUD-11 多容器 Pod 的 app 状态 | **已关闭** | Pod 模板只取 `name=app`；3 个用例（sidecar 不影响 / app 未 Ready 失败 / 无 app 容器失败） |

## 8. NOT_VERIFIED

1. **Lease 在真实 API Server 上的并发语义**：fake 用文件锁串行化，真实环境是 etcd 事务 +
   多 apiserver；两者证明的不是同一件事。
2. **RBAC 最小权限的实际效果**（含新增的 `batch/jobs get,list`）：未在真实集群 apply。
3. **Job 被 kubelet 以 `DeadlineExceeded` 终止后的条件传播时延**：需要真实集群与真实 DDL 负载，
   180s 余量是保守估计，未实测。
4. **fencing 窗口实测**：未做「暂停 Runner + 网络分区」实验。
5. **时钟偏移导致的误接管**：未在可控时钟环境验证。
6. **真实集群的 rollout / preStop / PVC 绑定 / 迁移 Job 调度**（沿用 T2-06 / T2-06.1 的未验证项）。
7. **server-side dry-run**：无集群，未执行。
8. **AUD-05 的具体内容**：任务正文与仓库均未提供描述与复现步骤，无法逐条回归。

## 9. 剩余风险

1. **没有 fencing token（AUD-02）**：见 §5，列为残余架构风险，等待 Owner 决策。
2. **过期判定依赖 Runner 本地时钟**：偏移过大会误接管（偏快）或让死锁更久（偏慢）。
3. **锁只保护脚本路径**：直接 `kubectl apply` 或其他 GitOps 工具不受保护（旁路写入无法阻止）。
4. **`create leases` 无法用 `resourceNames` 限制**：RBAC 固有限制，应限制在单命名空间。
5. **迁移记录依赖 API 可读**：`list jobs` 缺失或 API 抖动时表现为「发布被拒绝」（fail-closed 的代价）。
6. **TTL 与记录时序**：`COMPLETE`/`FAILED` 记录在 Job 被 TTL 清理后仍放行；若有人手工删除
   仍在运行的 Job，记录停在 `RUNNING` → 后续 Runner 进入人工恢复。
7. **浅克隆**：版本保护 fail-closed，需 `git fetch --unshallow`。

## 10. Commit SHA

| 提交 | SHA | 内容 |
|---|---|---|
| 主提交 | `b4ceb234af2a68b23dced6ca671d7d67271f91d3` | `scripts/deploy-k8s.sh`、`test/deployk8s/`、`test/manifest/`、RBAC 示例、设计/操作文档 |
| 报告提交 | 见 `git log -1 --format=%H`（即包含本文件的提交） | 本验证报告 |

## 11. 远端同步

分支 `feat/k8s-t2-06-1-1-deploy-lock-lifecycle` 已推送到 `origin`，
`git ls-remote origin refs/heads/feat/k8s-t2-06-1-1-deploy-lock-lifecycle` 返回的 SHA
与本地 HEAD 一致（交付说明中给出实际值）。

## 12. 工作区状态

提交并推送后 `git status --porcelain` 为空（干净）。
