# T2-06.1.3 最终集成审计与 Owner 验收报告

## 0. 执行方式、并行子 Agent 与合并口径

| 项 | 事实 |
|---|---|
| 并行子 Agent | 一次调用中并发启动 4 个具名成员：`agent-a-rbac`、`agent-b-lease`、`agent-c-cas`、`agent-d-contracts`（团队 `t2-06-1-3-audit`，各自独立上下文） |
| 子 Agent 能力边界 | 只读检索/阅读（无 shell）：不能执行 `git`、`go test`、构造 `/tmp` fixture |
| 子 Agent 产出 | **四份最终报告全部返回**（含多轮交叉勘误）；本报告逐条合并，未采信任何未经验证的断言 |
| 主 Agent 职责 | 基线核验、门禁实跑、`/tmp` 故障注入与黑盒验证、对争议项独立复核，并在合并时纠正子 Agent 的 3 处偏差（见 §0.1） |
| 只读约束 | 未修改受控文件；未提交/推送/合并/变基；未部署 K8s 资源；未触碰历史审计容器（`t2audit-mysql` 全程未被操作） |

### 0.1 合并中的交叉核验与纠偏（重要）

| 项 | 子 Agent 原结论 | 主 Agent 复核 | 最终处理 |
|---|---|---|---|
| 检测延迟 | 文档 29s；B 推导 **31.6s** | 我最初按 28.4s 推导，漏了「最后一次成功 PUT 的 `δ_succ ≤ R`」与「最后一次退避 0.2s」 | **采纳 31.6s**；可接管修正为 `[t0+44, t0+45]`（秒截断）；余量 12.4s |
| 参数枚举容差 | 我最初称「开销 ≤6.0s 无越界」 | 按 31.6s 精确模型重算：最紧合法组合的容差只有 **3.4s**，3.0s 时无越界、3.4s 起 2408 个组合越界 | **推翻我自己的旧结论**，采纳精确值 |
| C-1（注入 409 不推进 rv） | Agent C 提出 | 黑盒复现：409 后 rv 仍为 2，同一 rv 立即重试 **成功** | 确认，写入报告 C |
| C-4（无 delete） | Agent C 提出 | 黑盒执行 `delete` → exit 1「未实现的命令」 | 确认 |
| 「无 rv 的 PUT 真实会成功」 | 我原报告曾断言 | Agent C 指出属 `AllowUnconditionalUpdate`，无集群证据 | **降级为 NOT_VERIFIED** |
| D-9（命名空间 403 折叠） | Agent D 原列为边缘情况 | 主 Agent 实测证明这是 least-privilege 身份的**常规首次部署路径** | **上调为 High** |
| 最终判定 | A/B/D 均建议 FAIL（部分）；C 建议 PASS（替身） | 独立复核后维持 | 见 §10 |

## 1. Git 与交付基线（独立核验）

| 项 | 实测值 |
|---|---|
| 当前分支 | `feat/k8s-t2-06-1-3-rbac-renewal-budget` |
| HEAD | `1919d4a238dac431b194222547c30530f012105f` |
| HEAD^ | `6919139713d2b05baa2a8390b5c500b9089d68bd` |
| 远端同名分支 | `1919d4a238dac431b194222547c30530f012105f`（逐字符一致） |
| 上一阶段基线 | `df68fc4de180d896a6b6a0ad9e81d350f7c6baac` |
| 工作区 | 仅 `?? .codebuddy/` 与本次 5 份报告（新增、未跟踪）；无受控文件改动 |
| 变更范围 | 21 个文件，+2288/−235（脚本、RBAC 示例、测试、4 份文档、1 份交付报告） |

## 2. 审计范围

- `scripts/deploy-k8s.sh`（2674 行，逐行）；`manifest/kubernetes/examples/deploy-lock/rbac.yaml`。
- `test/deployk8s/`（20 个文件，含 `fakestate`/`fakekubectl`/`harness`）；`test/manifest/`。
- `docs/design/deploy-lock.md`、`docs/design/deployment.md`、`docs/development/kubernetes.md`、`docs/development/migrations.md`、`docs/reports/t2-06-1-*`。
- 独立实验：`/tmp/audit-a`（RBAC 四阶段）、`/tmp/audit-b`（50s 预检延迟 → 创建即过期）、`/tmp/audit-c`（fake 黑盒 CAS）、`/tmp/trap-delay.sh`（bash trap 延迟）。

## 3. 四份专项报告结论

| 报告 | 主题 | 结论 | 关键证据 |
|---|---|---|---|
| A | F-01 RBAC | **FAIL（部分）**，13 条缺陷（A-1 Blocker） | 四阶段实测：严格按示例 → `get namespace` 403 被当「不存在」→ apply 403 → exit 1；放宽后预检 7 项全绿、Lease 已建，`get secret` 403 报「缺少 Secret」；再放宽 `apply migration` 403。namespaces 为集群级，Role 无法授予，而契约测试禁止 ClusterRole |
| B | F-02 / F-05 | **F-02 FAIL（部分）/ F-05 FAIL（结论句）**，16 条缺陷 | 续租循环公式与源码一致；但 `renewTime` 取脚本启动时刻，`D=51s` 实测使 Lease 创建即过期 → exit 3；检测 31.6s / 可接管 44s / 余量 12.4s；「重叠 0」仅对「不再发起新请求」成立 |
| C | F-06 Fake CAS | **PASS（原始缺陷）/ 部分通过（保真度）**，17 条缺陷 | rv 推进、旧 rv→409 且不改 Lease、UID 前置条件 409 均实测成立；但注入型 409 不推进 rv（同一 rv 重试成功）、无 delete、接管 4 套实现 |
| D | F-03 / F-04 / F-07 / 文档 | **F-03 PASS；F-04 FAIL（部分）；F-07 PASS；文档 PARTIAL**，9 条缺陷 | 8 参数四方一致；命名空间 apply 失败无 UNKNOWN；迁移 apply 只核验 Job；Pod 403 → exit 7 且措辞正确；jobs 路径仍把查询失败说成「存在未终结 Job」 |

## 4. F-01 至 F-07 验收总表

| 缺陷 | 专项 | 独立复核 | 最终状态 | 依据 |
|---|---|---|---|---|
| **F-01** RBAC 示例缺 pods | A | 是（4 阶段实测） | **FAIL（部分）** | pods 已补齐并纳入契约；但按示例授权无法完成发布（A-1），且行为/静态测试结构上均无法发现（A-4/A-5） |
| **F-02** 续租预算 | B | 是（枚举 + 50s 延迟实测） | **FAIL（部分）** | 续租循环公式正确；「create → 首次续租」不在预算内（B-1 已实测）；守卫余量仅 2.4s |
| **F-03** 默认值 | D | 是（`--help` + 非法参数实跑） | **PASS** | 8 参数代码/help/文档一致；`--lease-renew-timeout` 已从 5 改为 3 并被测试锁死 |
| **F-04** 退出码 | D | 是（namespace apply 403 实测） | **FAIL（部分）** | 文档语义已修正；D-1/D-2/D-9 三条常规路径不满足「发出写 ⇒ 结果未知须标记」 |
| **F-05** 失锁窗口 | B | 是（trap 延迟实测 2.7s） | **FAIL（结论句）** | 四类场景已分开且推导基本成立；「重叠 0」作为「旧 Runner 仍在写」不成立（0.7s 级反例） |
| **F-06** Fake CAS | C | 是（黑盒驱动 fake） | **PASS（原始）/ 部分（保真度）** | `stealLease` 已推进 rv 并回归；C-1/C-3/C-4/C-5 为替身保真度缺陷 |
| **F-07** Pod 查询三态 | D | 是（403 注入实测 exit 7） | **PASS** | 三态在全部入口一致；403/500/超时不误报「确实存在」；D-8 覆盖缺口；jobs 路径残留同类缺陷（D-3） |

## 5. D2 / D3 / D4 回归检查 —— 无回归

| 组 | 检查点 | 源码 | 测试（全部 PASS） |
|---|---|---|---|
| D2 | takeover 保留上一轮记录 | 675-689、777-781 | `TestTakeoverKeepsPreviousRecordWhenVersionGuardRejects`、`TestCrashAfterTakeoverKeepsPreviousRecord` |
| D2 | renew 不修改记录 | 817 | `TestRenewPreservesRecordAnnotations` |
| D2 | release 保留记录 | 876 | `TestReleasePreservesRecordAnnotations`、`TestFailedMigrationReleaseKeepsFailureEvidence` |
| D2 | CAS 重试不重复归档 | 1544-1559 守卫 | `TestNewMigrationArchivesPreviousRecordOnce`（`LeaseRecordConflicts=1`） |
| D2 | FAILED / COMPLETE 历史保留 | 1527-1535 | `TestTTLFailedRecordAllowsTakeover`、`TestTTLCompleteRecordAllowsTakeover` |
| D3 | Job Failed ≠ Pod 已终止 | 1938-1947（`unconfirmed`，terminal-at 留空，`MIG_TERMINAL=0`） | `TestOwnMigrationFailedWithRunningPodExits7`、`TestFailedJobPodGateMatrix` |
| D3 | 活动 Pod 阻止新迁移 | 1861-1867 | `TestFailedJobPodBecomesTerminalAllowsRetry`、`TestScanBlocksActiveMigrationWithoutRecord` |
| D3 | 查询失败 fail-closed | 1298-1302、1865-1867 | `TestOwnMigrationPodQueryFailureExits7`、`TestPodQueryFailureIsNotReportedAsNoPods` |
| D3 | 归属 UID 不匹配拒绝 | 1307-1316 | `TestPodGateMatrixForTerminalRecord`、`TestFailedJobPodGateMatrix` |
| D4 | dirty=true 拒绝 | 1441-1446 | `TestRecoveryEvidenceDirtyRejected` |
| D4 | version=0 拒绝 | 1452-1457 | `TestRecoveryEvidenceZeroRejected` |
| D4 | version>latest 拒绝 | 1447-1451 | `TestRecoveryEvidenceFutureRejected` |
| D4 | version==latest && clean → COMPLETE | 1461-1472 | `TestRecoveryEvidenceCompleteAllowsResume` |
| D4 | version<latest && clean → 归档复位且仍执行 migrate up | 1475-1480 | `TestRecoveryEvidenceBehindResetsRecord` |
| D4 | 不 force / 不伪造 / 不跳过 | 1399-1410、1476-1479 | `TestRecoveryRejectedWhenMigrationPodStillRuns`、`TestRecoveryEvidenceMalformedRejected` |

## 6. AUD-02 / AUD-05 架构残余风险

| 项 | 独立确认 |
|---|---|
| 真正的 Fencing Token | **否**（`grep fencing|token|admission|webhook` 仅注释） |
| 服务端写入屏障 | **否**（变更前 `GET Lease → 比对`，与 `apply` 之间存在 TOCTOU：2481→2486） |
| 已发出的 kubectl 请求能否撤销 | **否**（`TestLockLostDuringApplicationApplyReportsUnknown` 断言 `Applied==true`） |
| 旧 Runner 恢复后是否可能在途写入 | **是**（`TestPausedRunnerResumesAfterLockLoss`） |
| 是否如实披露 cooperative/advisory | **是**（脚本 36-44、`deploy-lock.md` §10.1、`kubernetes.md` §9.7） |

**`AUD-02 = NOT_CLOSED`，`AUD-05 = NOT_CLOSED`。** 不因 F-02 修复而关闭；本报告不代表 Owner 接受风险。

## 7. 门禁执行记录（真实退出码）

环境：Linux；Docker 可用；无 kubectl / 无 K8s API Server；3306/6379 未开放；宿主到自建容器（33399/36379）的 TCP 被环境阻断。

| # | 命令 | 退出码 | 判定 | 备注 |
|---|---|---|---|---|
| 1 | `gofmt -l .` | `0` | PASS | 无输出 |
| 2 | `go build ./...` | `0` | PASS | |
| 3 | `go vet ./...` | `0` | PASS | |
| 4 | `scripts/test-unit.sh` | `0` | PASS | |
| 5 | `scripts/test-deploy-k8s.sh` | `0` | PASS | 115 顶层用例 PASS / 0 FAIL；续租链实测 5.928s |
| 6 | `scripts/test-k8s-manifest.sh` | `0` | PASS | kubeconform 9/9 Valid；RBAC 契约通过（声明 7 / 使用 6） |
| 7 | `scripts/test-container.sh` | `1` → `0,0,0,0` | PASS（偶发） | 首次与 test-unit 并发时误报缺 `stop_grace_period`（文件第 123 行存在）；4 次隔离重跑全 PASS |
| 8 | `scripts/test-container-runtime.sh` | `0` | PASS | 16 项断言 |
| 9 | `scripts/test-race.sh` | `1` | **FAIL（环境）→ NOT_VERIFIED** | 需 compose 管理的 MySQL |
| 10 | `scripts/test-migration.sh` | `1` | **FAIL（环境）→ NOT_VERIFIED** | `127.0.0.1:3306 refused`；自建 33399 同样不可达 |
| 11 | `scripts/test.sh` | `1` | **FAIL（环境）→ NOT_VERIFIED** | 集成/e2e 阶段 3306 refused |

未修改测试、未伪造依赖、未把 SKIP 当 PASS。交付报告 §6 声称第 9-11 项 exit 0，本环境**无法复现**。

## 8. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| 真实 API Server 的 Lease 并发语义（etcd 事务、多 apiserver、watch 缓存） | 无集群 |
| 真实 RBAC 效果（`auth can-i` + RoleBinding + 真实 403 形态 + 写请求是否被拒） | 无集群 |
| server-side dry-run / 真实部署 / rollout 行为 | 无集群 |
| `test-race` / `test-migration` / `test.sh` 的代码结论 | 缺 MySQL/Redis |
| kubectl 单次调用的真实请求数（discovery）与进程墙钟上界 | 无集群；直接影响 F-02 的 ε 上界 |
| rv 为空时真实 API 是否无条件更新；UID 前置条件在普通 PUT 上的行为 | 无集群 |
| 时钟偏移导致的误接管 | 需可控时钟 |
| 「Failed 但 Pod 仍 Running」的真实时序 | 需真实集群 + 长 DDL |
| B-3 复活窗口的稳定复现 | 仅代码路径推导 |
| fencing 窗口实测大小 | 需真实暂停 Runner + 网络分区 |

## 9. 最终缺陷清单（合并去重）

### 阻塞（Blocker / High，未修复即不得通过）

| 编号 | 缺陷 | 报告 |
|---|---|---|
| A-1 | 按交付 RBAC 示例授权无法完成发布（缺 namespaces/secrets/deployments/jobs-create/configmaps/services/pvc/pods-log；namespaces 需 ClusterRole 而契约禁止） | A |
| A-2 / D-9 | `get namespace` 的任何失败（403/5xx）被当作「不存在」，触发写并在预检之前失败；least-privilege 首次部署必现 | A/D |
| A-3 | `get secret` 403 被报成「缺少 Secret」，stderr 丢弃 | A |
| A-4 / A-12 | 权限契约解析器不含 `apply`、白名单丢弃业务资源 → 最大权限类别永不检查；资源覆盖随 verb 不同 | A |
| A-5 | 行为测试不加载 RBAC、fake 写路径永不 403 → 删除 RBAC 任一项都不会失败 | A/C |
| B-1 | 预算 t0 = 脚本启动时刻，`D` 未计入且无上界；`D≥45` 时 Lease 创建即过期（实测） | B |
| B-3 | `migration_record_write` 不校验过期，lost 之后仍可写并给过期锁续命一个租期 | B |
| B-7/B-8 | 文档推导数值错误（29s/42s/16s → 31.6s/44s/12.4s）；「重叠 0」过度声明 | B/D |
| C-1 | 注入型 409 不推进 rv，同一 rv 可重试成功 → CAS 冲突不可证伪 | C |
| D-1/D-2 | 迁移 apply 失败「应用未更新」过度断言且无 UNKNOWN；命名空间引导失败不在 exit-1 语义内 | D |

### 非阻塞（需修复/记录）

| 编号 | 缺陷 | 严重度 |
|---|---|---|
| A-6 | 帮助/文档称「缺权限退出 3」，业务权限实际 exit 1 | Medium |
| A-7 | Application apply 的确定性 403 被报成 `APPLICATION_OUTCOME_UNKNOWN` | Medium |
| A-8 | `pods/log` 未授权、`logs` 静默失败；文档仍让运维照抄 | Medium |
| A-9 | 预检依赖 `selfsubjectaccessreviews create`（内置 `system:basic-user`） | Low |
| A-10 | 预检要求 `get pods`，但脚本只有 list 语义 | Low |
| A-11 | fake `auth can-i` 忽略 `-n` 与 `resourceNames` | Low |
| A-13 | 契约层与替身层对 get/list 建模不一致 | Low |
| B-2 | create 后无「未过期」断言、无立即续租 | Medium |
| B-4 | `lease_release` 不校验过期 | Medium-Low |
| B-5 | `LOCK_RENEW_BACKOFF_MS` 死常量；真实退避 0.6s ≠ 校验 0.4s | Low |
| B-6 | GET 失败不重试，一次抖动即终止发布 | Low |
| B-9 | 续租进程无监督，死亡/冻结时不写 lost | Medium-Low |
| B-10 | `observe_lock_after_request` 不查过期，退出可迟至 600s | Low |
| B-11 | apply 已返回后信号到达时不打印 UNKNOWN | Low |
| B-12 | 预算未计进程启动/discovery 开销（容差仅 3.0s） | Low/NV |
| B-13 | 迁移 apply 失锁后无 read-after-write/UNKNOWN | Low |
| B-14 | renewTime 不可解析时按「未过期」→ 永久卡锁 | Info |
| B-15 | 注入型 409 不推进 rv；record 用例无法钉住「重读后 rv 是新的」 | Medium-Low |
| B-16 | 注释把 wait 语义写成「bash 延迟处理信号」 | Info |
| C-2 | 接管 CAS 失败分支零覆盖 | High（测试面） |
| C-3 | 接管 4 套实现、2 套缺字段；GET 内做写 | High |
| C-4 | 无 delete → 删除重建不可达；uid 校验与一条断言永真 | High |
| C-5 | harness 的 `updateState`/`stealLease` 绕过 flock（丢更新风险） | High |
| C-6 | `LeaseStealAfterGets` 在 GET 内写并返回写后状态 | Medium |
| C-7 | 故障注入按文件名 | Medium |
| C-8 | 契约/预检/写路径三层不连接真实授权 | Medium |
| C-9 | rv 计数器化、重建回退、非数字退化；函数双份实现 | Medium |
| C-10 | ack-before-commit | Medium |
| C-11 | 失败请求上改写 renewTime/rv | Medium |
| C-12 | 接管注入排在其它失败注入前并持久化 | Medium |
| C-13 | 替身自测被当作脚本回归，副本已漂移 | Low |
| C-14 … C-17 | apply 无服务端校验 / 单对象模型 / 状态混装 / 不支持 jsonpath | Low |
| D-3 | jobs 列表查询失败被断言为「存在未终结 Job」 | Medium |
| D-4 | exit 3 文档未覆盖「迁移已发后失锁」「验证已过后复核失败」 | Medium-Low |
| D-5 | create 失败被归因为等待超时；「不执行任何变更」不成立 | Medium-Low |
| D-6 | 文档 6/7 归属矛盾、§8.3 漏 NotFound→7 | Low |
| D-7/D-8 | 默认值契约与 Pod/jobs 测试覆盖缺口 | Low |

## 10. 最终验收结论

| # | 标准 | 结果 |
|---|---|---|
| 1 | F-01 至 F-07 全部通过 | **否**：F-01/F-02/F-04/F-05 未完全通过；F-03/F-07 通过；F-06 原始缺陷通过、保真度部分 |
| 2 | D2/D3/D4 无回归 | **是** |
| 3 | 续租预算与实际代码路径一致 | **否**（续租循环一致；获取锁路径不一致，B-1） |
| 4 | 失锁窗口无未经证明的结论 | **否**（「重叠 0」结论句不成立，B-8） |
| 5 | Fake CAS 与关键语义一致 | **部分**（rv/UID 骨架成立；C-1/C-3/C-4/C-5 不达标） |
| 6 | 参数、退出码、文档无实质冲突 | **否**（D-1/D-2/D-4/D-6/D-9、B-7/B-8、A-6） |
| 7 | 所有可运行门禁真实通过 | **否**（第 9-11 项缺 MySQL/Redis；第 7 项曾偶发失败） |
| 8 | NOT_VERIFIED 全部披露 | **是**（§8） |
| 9 | AUD-02/AUD-05 未被伪称关闭 | **是**（均 NOT_CLOSED） |
| 10 | Git 基线与远端状态正确 | **是** |

```text
T2-06.1.3_FINAL_BLOCKED
```

阻塞理由：

1. **F-01 未闭环**：按交付 RBAC 示例授权仍无法发布（A-1/A-2/A-3 实测），且静态契约（A-4/A-12）与行为测试（A-5）结构上都无法发现。
2. **F-02 未覆盖全部执行路径**：`renewTime` 取脚本启动时刻，慢预检下 Lease 创建即过期（B-1 实测 exit 3）；另有 lost 之后仍可写 Lease 的复活窗口（B-3）。
3. **F-05 结论未被证据支持**：「重叠 0」只成立于「不再发起新请求」；在途写可跨接管生效（0.7s 级反例），与 AUD-05 = NOT_CLOSED 一致。
4. **F-06 替身保真度不足**：注入型 409 不推进 rv（C-1，黑盒复现），接管语义 4 套实现（C-3），删除重建不可达（C-4）。
5. **门禁不完整**：`test-race` / `test-migration` / `test.sh` 在本环境无法通过，交付报告的 exit 0 无法复现。

本报告不代表 Owner 接受 Fencing 残余风险；即使 Owner 接受 AUD-02/AUD-05，也不能据此把上述 1-4 项视为已修复。

## 11. 审计产物与工作区影响

| 文件 | 性质 |
|---|---|
| `T2-06.1.3-AUDIT-A-RBAC.md` | 新增（未跟踪） |
| `T2-06.1.3-AUDIT-B-LEASE.md` | 新增（未跟踪） |
| `T2-06.1.3-AUDIT-C-CAS.md` | 新增（未跟踪） |
| `T2-06.1.3-AUDIT-D-CONTRACTS.md` | 新增（未跟踪） |
| `T2-06.1.3-FINAL-AUDIT.md` | 新增（未跟踪） |

未修改任何受版本控制的文件；未提交、未推送、未合并、未开始 T2-07。
