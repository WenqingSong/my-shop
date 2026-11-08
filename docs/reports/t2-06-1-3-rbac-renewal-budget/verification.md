# T2-06.1.3 验证报告：RBAC 权限闭环与续租预算修复

- 基线：`df68fc4de180d896a6b6a0ad9e81d350f7c6baac`（分支 `feat/k8s-t2-06-1-2-migration-recovery-safety`）
- 任务分支：`feat/k8s-t2-06-1-3-rbac-renewal-budget`
- 结论：`T2-06.1.3_IMPLEMENTATION_PASS_PENDING_OWNER_DECISION`

本报告记录「改了什么、怎么验证的、什么还没验证」。设计论证见
`docs/design/deploy-lock.md`（§7 续租预算 / §10.3 失锁窗口推导 / §12 RBAC），
操作手册见 `docs/development/kubernetes.md`（§9.2 参数 / §9.4 退出码 / §9.7 边界 / §13.1 授权）。

本任务只修复独立审计（T2-06.1.2 FINAL-AUDIT）列出的 F-01…F-07，**没有**重新设计发布系统，
**没有**修改审计标准。

## 1. Git 基线与分支

| 项 | 值 |
|---|---|
| 基线 HEAD | `df68fc4de180d896a6b6a0ad9e81d350f7c6baac` |
| 基线分支 | `feat/k8s-t2-06-1-2-migration-recovery-safety` |
| 远端同名分支 SHA | `df68fc4de180d896a6b6a0ad9e81d350f7c6baac`（与基线一致） |
| 新分支 | `feat/k8s-t2-06-1-3-rbac-renewal-budget` |
| 新分支创建点 | `df68fc4de180d896a6b6a0ad9e81d350f7c6baac`（未重写历史） |
| 工作区（开始） | 干净 |
| 合并 `develop` / `main` | 未合并 |
| 旧分支历史 | 未修改 |
| 业务代码（`internal/`、`api/`） | 未修改 |

## 2. 修改文件

| 文件 | 变更 |
|---|---|
| `scripts/deploy-k8s.sh` | 续租预算公式（GET+REPLACE）、默认租期 45s、租约过期后不续租、权限预检表 `LEASE_PERMISSION_CHECKS` + Forbidden/unknown 区分、Pod 查询三态分类、`read_job_pods`/`read_migration_pods_for_job` 改用全局变量返回（错误文本不再丢失）、退出码注释与帮助文本 |
| `manifest/kubernetes/examples/deploy-lock/rbac.yaml` | 补齐 core `pods get,list`；补充「为什么需要」与「契约测试」说明 |
| `test/deployk8s/fakestate/state.go` | 按资源拒绝权限 `Permissions.Denied`、`AuthCanIError`、Pod 403 注入、`LeaseStealBeforeRenewReplace`、`LeaseExpireAfterRenewConflicts` |
| `test/deployk8s/fakekubectl/main.go` | `auth can-i` 资源感知、Pod 403、续租 CAS 前的接管注入、租约过期注入 |
| `test/deployk8s/harness_test.go` | `stealLease` 推进 `resourceVersion`（F-06）、外部包装记录毫秒时间线、租期延迟注入 |
| `test/deployk8s/renew_test.go` | 续租预算/过期/实际耗时/帮助默认值用例（12 个） |
| `test/deployk8s/rbac_test.go` | 新增：权限预检（F-01） |
| `test/deployk8s/podquery_test.go` | 新增：Pod 查询错误分类（F-07） |
| `test/deployk8s/takeover_test.go` | 新增：接管语义与陈旧 resourceVersion（F-06） |
| `test/deployk8s/lease_test.go` / `inflight_test.go` / `record_test.go` / `recovery_test.go` | 跟随新默认值与新措辞调整断言（语义未削弱） |
| `test/manifest/manifest_test.go` | 续租预算契约（GET+REPLACE）、help/文档默认值一致性（F-03） |
| `test/manifest/rbac_test.go` | 新增：脚本所需权限 ⊆ 交付 RBAC（F-01 契约测试） |
| `scripts/test-deploy-k8s.sh` | 门禁覆盖范围说明 |
| `docs/design/deploy-lock.md` | §7 预算重写、§10.3 失锁窗口推导、§12 RBAC、§13 验证分级、§2 租期示例 |
| `docs/development/kubernetes.md` | §9.1/§9.2/§9.4/§9.7/§10.3/§10.4/§13.1/§14/§15 |
| `docs/design/deployment.md` | 发布顺序加入权限预检、续租预算与失锁窗口引用 |
| `docs/development/migrations.md` | 补齐 pods 权限与「无法确认 ≠ 仍在运行」说明 |

## 3. F-01 至 F-07 修复情况

| 编号 | 问题 | 修复 | 证据 |
|---|---|---|---|
| **F-01** | RBAC 示例缺 `pods`，生产脚本需要 `pods get/list` | 示例补 `apiGroups: [""] / resources: ["pods"] / verbs: ["get","list"]`；`check_lease_permissions` 逐项预检 7 对 (verb, resource)；新增 `TestRBACGrantsEveryPermissionTheScriptNeeds`：**脚本实际使用 == 脚本声明 ⊆ RBAC 授权** | `test-deploy-k8s.sh`（pods get/list 拒绝、Forbidden/unknown）、`test-k8s-manifest.sh`（契约） |
| **F-02** | 续租预算漏算 GET；默认参数在真实路径下必然违反不变量 | 预算改为 `interval + attempts×(get+replace) + backoff + margin`；默认租期 30s → 45s（34 < 45）；每次 attempt 先检查租约是否过期，过期即停（不复活）；新增实际耗时测量 | `TestRenewChainStaysWithinBudget`（实测 5.85s ≤ 上界 7s）、`TestRenewAbortsWhenLeaseExpiresDuringRetries`、`TestLeaseDefaultParametersPassInvariant`、`TestLeaseParameterInvariantRejectsIllegalCombinations`（含「预算 == 租期」「旧默认 30s」） |
| **F-03** | `--help` 的 `--lease-renew-timeout` 默认值与代码不一致（写 5，实际 3） | 帮助改为「默认 3；GET 与 REPLACE 各算一次」，预算算式同步；新增 `TestHelpDefaultsMatchCode`（help / 运行时日志一致）与 `TestLeaseRenewBudgetContract`（代码 / help / 文档三方一致） | 同上两个用例 + `test-k8s-manifest.sh` |
| **F-04** | 文档把退出码 1 统一解释为「资源未修改」 | §9.4 重写：新增「本次是否发出过变更请求」列，明确 1 = 未发出（参数/预检/清单构建）**或** 已发出但结果未知（apply 失败 → `APPLICATION_OUTCOME_UNKNOWN`）；3 的在途情形同理 | `docs/development/kubernetes.md` §9.4、脚本头部与 `--help` |
| **F-05** | 失锁窗口用「约 10s + 30s」粗略估计 | §9.7 + `deploy-lock.md` §10.3 按真实时间戳推导：服务端过期 `[t0+D-G, t0+D]`=42…45s，存活 Runner 检测 ≤29s，重叠 0；暂停的 Runner 恢复后 ≤G 内停止；**不采用上一轮审计的 58s** | 文档推导 + `TestRenewChainStaysWithinBudget`（续租链实测） |
| **F-06** | `harness_test.go` 的 `stealLease` 不推进 `resourceVersion` | `stealLease` 改名推进 rv、保持 uid；fake 新增「续租 replace 的 CAS 之前接管」注入；新增回归：A 用 rv=10 的 PUT 得到 409 且不覆盖 B 的 holder | `TestStealLeaseAdvancesResourceVersion`、`TestStaleResourceVersionCannotOverwriteTakeover` |
| **F-07** | Pod 查询失败被描述成「Pod 确实存在」 | `scan_migration_pods` 返回三态（干净 / 确实活动 / 无法确认）；新增 `report_migration_pod_scan` 与 `report_pod_gate`；`migration_pod_gate` 的 2/3/4 统一输出「无法确认」；`read_*_pods` 改用全局变量返回，403/5xx/超时文本不再退化为「未知错误」 | `TestPodGateMatrixForTerminalRecord`（7 例）、`TestMigrationPodScanMatrix`（5 例）、`TestOwnMigrationPodQueryFailureExits7` |

## 4. 关键设计决策

### 4.1 续租预算（F-02）

以 `lease_renew` 的真实源码路径为准，一次 attempt 是**两个**请求：

```text
GET Lease（≤ get_timeout）→ 校验 holder / uid / 是否过期 → PUT Lease（CAS，≤ replace_timeout）
失败 → 退避 → 下一个 attempt（最多 attempts 次）
```

```text
renewal_budget = renew_interval + attempts × (get_timeout + replace_timeout)
               + total_backoff + scheduling_margin
默认            = 10 + 3×(3+3) + 1 + 5 = 34 < 45
续租链最坏      = 10 + 18 + 1 = 29s（早于服务端过期 42…45s）
```

### 4.2 默认值重估的依据（不是为了让 guard 通过）

| 参数 | 旧 → 新 | 依据 |
|---|---|---|
| lease-duration | 30 → **45** | 34 必须严格小于租期；45 留 11s（24%）余量，吸收调度延迟与 `renewTime` 秒级精度 |
| renew-interval | 10 → 10 | 45s 内有 4 个续租窗口，单次抖动不影响连续性 |
| renew-timeout | 3 → 3 | in-cluster Lease GET/PUT 通常 <100ms，3s ≈ 30 倍余量；压到 1–2s 会把正常抖动当失锁 |
| attempts | 3 → 3 | 主进程写迁移记录会推进 rv，409 是常态 |
| backoff | 2×0.2s（预算 1s） | 远短于续租间隔；脚本会断言预算 ≥ 实际退避 |
| safety-margin | 5 → 5 | 吸收一次请求的调度延迟 |

代价（明确记录）：崩溃后接管延迟由 ≤30s 变为 ≤45s（+轮询 2s）。发布锁是分钟级操作，
而「两个发布并行」不可接受，因此选择放大租期而不是压缩请求超时。

### 4.3 租约过期后不续租（F-02.4）

每个 attempt 在 PUT 前检查**服务器返回的** `renewTime + leaseDurationSeconds`；过期即返回失败。
旧实现会在 `require_lock_owned_server_side` 里「立即续租两次」把锁复活 —— 那是在接管窗口里
继续写入。CAS 只能保证「不是我覆盖了别人的写」，不能保证「我没有在别人可以接管的时刻继续操作」。
判断依据是服务端时间戳，不是本地 `lost` 标记。

### 4.4 失锁窗口（F-05）

见 `docs/design/deploy-lock.md` §10.3。结论：

```text
锁对象层面：旧持有者用陈旧 resourceVersion 的 PUT 一定被 409；租约过期后的续租被放弃。
非锁资源层面：正常参数下「旧 Runner 仍在写 + 新 Runner 已接管」重叠为 0（29s < 42s）；
             只有进程被暂停时才可能重叠，长度 = 暂停时长，恢复后立即截断。
```

## 5. 新增测试

| 文件 | 新增用例 |
|---|---|
| `test/deployk8s/rbac_test.go` | 5 个（含 7 个按权限项的子用例） |
| `test/deployk8s/podquery_test.go` | 3 个（含 7 + 5 个子用例矩阵） |
| `test/deployk8s/takeover_test.go` | 3 个 |
| `test/deployk8s/renew_test.go` | 新增 5 个（默认值通过 / help 一致 / 续租 GET 超时 / 重试期间过期 / 实测耗时），改写 2 个（连续三次 CAS、REPLACE 超时） |
| `test/manifest/rbac_test.go` | 1 个 |

`test/deployk8s` 包的顶层用例数：基线 100 → 115（新增 15 个，其余为既有用例的参数/措辞跟进）；
其中带子用例的矩阵共 25 条（6 组非法参数、7 个权限项、7 个 Pod 门禁、5 个扫描结果）。

回归保护（未退化）：D2（13 个注解保留）、D3（Failed Job + Pod 生命周期）、D4（证据恢复）、
D6（release 使用真实 duration）、`APPLICATION_OUTCOME_UNKNOWN`、AUD-02/AUD-05 的风险披露。

## 6. 门禁真实退出码

| 命令 | 退出码 |
|---|---|
| `gofmt -l .` | `0`（无输出） |
| `go build ./...` | `0` |
| `go vet ./...` | `0` |
| `bash scripts/test-unit.sh` | `0` |
| `bash scripts/test-deploy-k8s.sh` | `0` |
| `bash scripts/test-k8s-manifest.sh` | `0` |
| `bash scripts/test-race.sh` | `0` |
| `bash scripts/test.sh` | `0` |
| `bash scripts/test-migration.sh` | `0` |
| `bash scripts/test-container-runtime.sh` | `0` |

环境说明（如实记录）：

- 需要 MySQL/Redis 的门禁（`test-race` / `test.sh` / `test-migration` / `test-container-runtime`）
  通过 `scripts/deps-start.sh` 启动**本地一次性** compose 依赖后执行；凭据用占位值
  （`MYSQL_ROOT_PASSWORD=test-root`、`DATABASE_DEFAULT_PASS=test-root`）。
  上一轮审计留下的 `my-shop_mysql_data` 卷**未被删除或修改**，本次门禁使用独立的临时卷，
  结束后已清理该临时卷与 compose 容器；`t2audit-mysql` 与 `/tmp/t2audit/` 未被触碰。
- `test-k8s-manifest.sh` 中的 `kubeconform` 校验已执行（9 resources，Valid 9）。

## 7. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| 真实 API Server 的 Lease 并发语义（etcd 事务、多 apiserver） | 无集群 |
| RBAC 最小权限的真实效果（`auth can-i` + 真实 RoleBinding） | 无集群（静态契约已用 `test/manifest` 验证） |
| server-side dry-run / 真实部署 | 无集群 |
| 时钟偏移导致的误接管 | 需要可控时钟的真实集群 |
| fencing 窗口的实测大小 | 需要真实暂停 Runner + 网络分区实验 |
| Job 被 `DeadlineExceeded` 终止后的条件传播时延 | 需要真实集群与真实 DDL 负载 |
| 「Failed 但 Pod 仍在 Running」的真实时序 | 需要真实集群 + 真实 MySQL 长 DDL（仅在 fake 中构造） |
| 续租链在真实 API 抖动下的实测分布 | 仅有 fake + 注入延迟的实测（5.85s） |

## 8. 剩余风险

1. **AUD-02 = NOT_CLOSED，AUD-05 = NOT_CLOSED**：API Server 不校验写入者是否持锁；
   已发出的请求无法撤销；`read-after-write` 只提高可观测性。**本任务没有声称实现强 fencing。**
2. **被长时间暂停的 Runner**：恢复后立即停止（≤G），但暂停期间无法阻止其已经发出的写入。
3. **过期判定依赖本地时钟**：要求 NTP；偏移超过租期 1/3 视为不合规。
4. **失败的表现是「发布被拒绝」**（退出码 3/7）：Pod/Job 只读权限缺失、API 抖动都会
   fail-closed，这是刻意选择，代价是一次重跑。
5. **锁只保护 `scripts/deploy-k8s.sh` 路径**：直接 `kubectl apply` / 其他 GitOps 工具不受保护。
6. **`create leases` 无法用 `resourceNames` 限制**（RBAC 固有限制），需把权限限制在单命名空间。
7. **崩溃后接管延迟上限为 45s + 2s 轮询**：比 T2-06.1.2 长 15s，是预算修复的已知代价。

## 9. Commit SHA

| 提交 | SHA | 内容 |
|---|---|---|
| 主实现 | `6919139713d2b05baa2a8390b5c500b9089d68bd` | `scripts/deploy-k8s.sh`、RBAC 示例、`test/deployk8s/`、`test/manifest/`、设计/操作文档 |
| 报告 | 见 `git log -1 --format=%H`（包含本文件的提交） | 本验证报告 |

## 10. 工作区状态

提交并推送后 `git status --porcelain` 为空。

## 11. 远端同步

| 项 | 值 |
|---|---|
| 推送分支 | `refs/heads/feat/k8s-t2-06-1-3-rbac-renewal-budget` |
| 本地 HEAD | 分支最新提交（含本文件） |
| 远端 SHA | 与本地 HEAD 逐字符一致（`git rev-parse HEAD` == `git ls-remote origin refs/heads/…`） |
