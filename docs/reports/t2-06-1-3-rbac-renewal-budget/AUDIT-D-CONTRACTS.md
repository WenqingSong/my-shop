# T2-06.1.3 审计报告 D — F-03 默认值、F-04 退出码、F-07 Pod 三态、文档一致性

## 0. 执行与取证方式

| 项 | 事实 |
|---|---|
| 独立审计子 Agent | `agent-d-contracts`（独立上下文，只读源码，无 shell） |
| 主 Agent 复核 | `--help` 实跑、非法参数实跑、`/tmp` fixture 的 403 注入实验、全仓文档检索、`APPLICATION_OUTCOME_UNKNOWN` 计数 |
| 交叉核对 | 子 Agent 的静态结论与主 Agent 实测逐条比对，5 项吻合；其中 1 项（命名空间引导）因实测**上调**为 High（D-9） |

## 1. Git 基线

分支 `feat/k8s-t2-06-1-3-rbac-renewal-budget`；HEAD `1919d4a238dac431b194222547c30530f012105f`（= 远端）；工作区仅未跟踪文件。

## 2. F-03：参数默认值四方对照

代码：`scripts/deploy-k8s.sh:95-128`；help：`:304-353`；文档：`docs/development/kubernetes.md:444-454`。

| 参数 / 常量 | 代码 | `--help` 实跑 | 文档 | 测试断言 | 结论 |
|---|---|---|---|---|---|
| `--context` | `""`（117） | 默认当前 context | 一致 | 无 | 一致（无契约） |
| `--migration-timeout` | 1080（119） | 1080 | 1080 | 仅下界 `>= deadline+180`（`manifest_test.go:1007-1032`） | 一致；改成 1200 仍绿 |
| `--rollout-timeout` | 600（124） | 600 | 600 | 无 | 一致（覆盖缺口） |
| `--lock-wait-timeout` | 60（108） | 60 | 60 | 无 | 一致（覆盖缺口） |
| `--lease-duration` | 45（95） | 45 | 45 | `renew_test.go:101`；`manifest_test.go:1093,1115` | **精确一致** |
| `--lease-renew-interval` | 10（96） | 10 | 10 | `renew_test.go:136,148`；`manifest_test.go:1094,1116` | **精确一致** |
| `--lease-renew-timeout` | 3（99） | 「默认 3；GET 与 REPLACE 各算一次」 | 3 | `renew_test.go:137`；`manifest_test.go:1095,1117` | **F-03 原文案缺陷已修复** |
| `--allow-downgrade` | 0（125）+ CI 二次开关（420-425） | 默认禁止 | 一致 | `version_test.go:145-183` | 一致 |
| `POLL_INTERVAL` | 2（126） | 未展示 | 「轮询 2s」 | 无 | 一致 |
| `MIGRATION_APPLY_VISIBILITY_TIMEOUT` | 10（123） | 未展示 | 「上限 10s」 | 无 | 一致 |
| `LOCK_RENEW_MAX_ATTEMPTS` | 3（101） | 预算文案含 attempts(3) | 一致 | `manifest_test.go:1065,1077` | 一致 |
| `LOCK_RENEW_BACKOFF_MS` / BUDGET | 200 / 1（102,105） | 预算文案含 backoff(1) | 一致 | `manifest_test.go:1068-1073` | **常量未被生产代码使用（B-5）** |
| `MIGRATION_TIMEOUT_MARGIN` | 180（121） | 未展示 | 一致 | 测试内重复硬编码 180 | 值一致；双向漂移不失败 |

**F-03：PASS。** 不存在「文案与代码不一致」的现行缺陷；唯一契约只锁 3 个租期参数（D-7 覆盖缺口）。

## 3. F-04：退出码完整语义

定义处：脚本头 `:46-60`、help `:329-335`、`kubernetes.md:528-572`、`deploy-lock.md:480-494`。

| 码 | 实际路径 | 已发出写？ | 已确认生效？ | 文档 |
|---|---|---|---|---|
| 0 | `:2651-2674` | 是 | 是（镜像/版本/generation/副本/Pod） | 一致 |
| 1 | 见 §4 全路径 | **混合** | 多数未变更；3 条不确定 | **部分不一致**（D-1/D-2/D-9） |
| 2 | `:2378-2398` | 迁移已发；应用未发 | 迁移确定失败 | 一致 |
| 3 | 权限/锁等待/create 失败/锁丢失/复核/记录失锁/在途 apply | 多数未发；`2491-2508` 已发未知；`971-979` 迁移轮询中失锁时迁移写早已发出 | 否/不确定 | **不完整**（D-4） |
| 4 | `:423-425`、`:2006-2023` | 否 | 否 | 一致 |
| 5 | `:2651-2658` | 是 | 已更新但不符合目标 | 一致 |
| 6 | `:2400-2470`、二次判定读失败 `:2341-2343` | 迁移已发 | 未确认 | 与 7 的边界在文档中互相矛盾（D-6） |
| 7 | 拒绝接管/收敛失败/记录失败/begin_migration 拒绝/Job 不可见/uid 变化/Job 消失/Pod 未终止/二次判定 | 前 4 条未发；`1887+` 已发 | 否 | **部分不完整**（D-4） |
| 143 | `:552/558/565` | 可能已发 | 否/不确定 | 一致 |

3 与 7 的映射核对：`acquire_release_lock` 返回 3 只用于「拒绝接管 / 接管后收敛失败」，main 映射为 exit 7（`:2255-2257`），与 `deploy-lock.md:486-489` 一致；返回 2（create 非 AlreadyExists 失败）映射为 3，退出码正确但原因文案错误（D-5）。

## 4. exit 1 全路径清单（分类）

(a) 写未发出：(1) `:367` 未知参数 (2) `:373` 非整数 (3) `:375-377` ≤0 (4) `:402` attempts (5) `:406` 退避预算 (6) `:415` 租期预算 (7) `:440` 无 kubectl (8) `:2144` 集群不可达 (9) `:2153/:2157` kustomize (10) `:2166` 占位符 (11) `:2173/:2179/:2182` 镜像 (12) `:2188/:2191/:2194/:2197` 清单结构 (13) `:2205/:2207` 版本标记 (14) `:2217` 超时关系 (15) `:2277/:2280` Secret (16) `:2284` Deployment 读取失败。

(b) 写已发出、结果未知：
- `:2242` **命名空间 apply 失败** —— **无 `APPLICATION_OUTCOME_UNKNOWN`、无分类说明**（D-2/D-9，已实测）；
- `:1891-1895` **迁移 apply 失败且 Job 不存在** —— 迁移清单是 **Namespace + ConfigMap + Job**，只证明 Job 不存在，却输出「应用未更新」；**不打印 UNKNOWN**（D-1，已实测）；
- `:2530` Application apply 失败 —— 正确打印 UNKNOWN + read-after-write（唯一正确者）。

(c) 已确认成功：无 exit 1。

(d) 已失败且确认未生效：`await_job_object` 返回 absent 的迁移 apply —— 对 Job 成立，对 ConfigMap/Namespace 不成立。

`APPLICATION_OUTCOME_UNKNOWN` 全脚本仅 5 处（`:545,561,2492,2504,2524`），**迁移路径与命名空间路径均无**。

## 5. F-07：Pod 三态验证矩阵

实现：`scan_migration_pods`（1237-1269，0/1/2）、`migration_pod_gate`（1289-1337，0/1/2/3/4）、`report_migration_pod_scan`（1273-1286）、`report_pod_gate`（1340-1354）。

| # | 场景 | 实现 | 测试 | 证据 |
|---|---|---|---|---|
| 1 | 空列表 | CLEAN | ✅ | `podquery_test.go:53-58,151-154` |
| 2 | Running | ACTIVE | ✅ | `podquery_test.go:66-72`；`podgate_test.go:43-47` |
| 3 | Pending | ACTIVE | 部分（gate 有、scan 无） | `podgate_test.go:50-55` |
| 4 | **Unknown** | ACTIVE | ✘ | `deploy-k8s.sh:1324` |
| 5 | Terminating（deletionTimestamp） | ACTIVE | 部分 | `podgate_test.go:56-61` |
| 6 | Succeeded | CLEAN | 部分 | `podgate_test.go:62-67` |
| 7 | Failed | CLEAN | 部分 | `podgate_test.go:68-73` |
| 8 | **ownerReferences 缺失** | UNKNOWN(3) | ✘（fake 的 `Orphan` 字段零引用） | `1307-1311`；`grep Orphan: true` 为空 |
| 9 | UID 不匹配 | UNKNOWN(3) | ✅ | `podquery_test.go:97-103` |
| 10 | Forbidden | UNKNOWN(2) | ✅ | `podquery_test.go:73-80`；主 Agent 实测 exit 7 |
| 11 | API 500 | UNKNOWN(2) | ✅ | `podquery_test.go:81-88` |
| 12 | 超时 | UNKNOWN(2) | ✅ | `podquery_test.go:89-96` |
| 附 | 空 phase | ACTIVE(1) | ✘ | `1324` |
| 附 | 不可识别 phase（code 4） | 实现存在但枚举不可达 | ✘ | `1329-1332` |

**入口一致性**（全部同一套三态，无语义分叉）：`verify_previous_migration` 五分支（1774-1799）、`recovery_resolve_record`（1415-1423）、`begin_migration`（1861-1867，1 与 ≠0 两条不同 die）、`wait_for_migration` complete/failed（1921-1951）、二次判定（2305-2333）、超时分支（2420-2452）。

**独立实测**（`/tmp/audit-b`，component 扫描注入 403）：

```text
[ERROR] 无法列出迁移 Pod（…）：Error from server (Forbidden): … cannot list resource "pods"
[ERROR]   这是「无法确认迁移 Pod 状态」，**不是**「Pod 确实还在运行」，也不是「Pod 已终止」。
[ERROR] 上一轮没有迁移记录，拒绝接管：**无法确认**迁移 Pod 状态（查询失败：…）
→ EXIT=7
```

**F-07（Pod）：PASS。** 403/500/超时均未被描述为「存在活动 Pod」。测试缺 5 项（Unknown、空 phase、孤儿 Pod、scan 路径的终态/终止中）。
**同类缺陷残留在 jobs 路径（D-3）**：`scan_active_migrations` 对「确实发现」与「查询失败」返回同一码，两处调用方都输出断言式「存在未终结的迁移 Job」（已 grep 确认）。

## 6. 文档一致性

### 6.1 现行文档与代码冲突

| # | 位置 | 文档 | 代码事实 | 严重度 |
|---|---|---|---|---|
| Doc-1 | `kubernetes.md:536,545-554` | apply 失败会打印 `APPLICATION_OUTCOME_UNKNOWN` | 迁移 apply 失败**不打印** | 中（D-1） |
| Doc-2 | `kubernetes.md:533-536` | exit 1 未列「命名空间引导失败」 | `:2242` 存在，且写已发出 | 中低（D-2） |
| Doc-3 | 全 docs 无「创建命名空间失败」条目 | — | `grep -rn '创建命名空间' docs/` 为空 | 低 |
| Doc-4 | `kubernetes.md:538,563-564` | exit 3 只在途 apply 未知 | 未覆盖「迁移 Job 已创建后失锁」「应用已验证后复核失败」 | 中低（D-4） |
| Doc-5 | `deploy-lock.md:679-680,696` | Pod 分类「含没有 owner」已验证 | `Orphan` 零用例 | 低（D-8） |
| Doc-6 | `deploy-lock.md:594-601`、`kubernetes.md:687` | 「重叠 0」 | 见报告 B §7 | 中（B-8） |
| Doc-7 | `deploy-lock.md:580-589`、`kubernetes.md:669-675` | 29s / 42s / 16s | 31.6s / 44s / 12.4s | 中（B-7） |
| Doc-8 | `kubernetes.md:339-341` 与 `rbac.yaml:7` | 示例即身份权限来源 | 缺业务权限 | 中（A-6） |

### 6.2 历史值（可接受，均已标注为历史/变更）

`deploy-lock.md:14`（30s→45s 摘要）、`:34`（历史缺陷）、`:379-387`（历史推导）、`kubernetes.md:473-477`（显式「旧默认值」）、`docs/reports/t2-06-1-*`（历史报告）。

**未发现「未标注的旧默认值被当作现行值」。**

### 6.3 文档互相矛盾

| # | A | B | 代码事实 |
|---|---|---|---|
| X-1 | `kubernetes.md:570-572`：7 = 「查询失败」 | `deploy-lock.md:336`：查询失败 → 6 | 分阶段：接管期查询失败 → 7；超时后二次判定读失败 → 6；`Job NotFound` → 7 |
| X-2 | `kubernetes.md:355`：「只有仍运行/查询失败才是 6」 | `deploy-lock.md:335`：NotFound → 7 | 代码 `:2334-2335` 为 7；§8.3 漏了 NotFound→7 |

## 7. 缺陷清单（D-1 … D-9）

| 编号 | 严重度 | 缺陷 | 证据 |
|---|---|---|---|
| **D-9** | **High** | 命名空间存在性判断用 `if ! kubectl get namespace`，**任何失败**（403/5xx/超时）都被当成「不存在」→ 进入 `apply namespace` → exit 1、无 UNKNOWN；且 RBAC 示例（namespace 级 Role）无法授予集群级 `get namespaces`，按示例授权的身份**必然**走入该分支（与 A-2 同源，从退出码/文档角度独立记录） | `:2239-2243`；`is_not_found_for:608-626`；`rbac.yaml:44-65`；实测 |
| **D-1** | Medium-High | 迁移 apply 失败路径：①「应用未更新」只由 Job 不存在推出，同清单 ConfigMap/Namespace 未核验；② 不打印 `APPLICATION_OUTCOME_UNKNOWN`，与 help/§9.4 冲突 | `:1882-1896`；`:1828-1848`；`migration/overlays/production/kustomization.yaml`；`shared/database-configmap.yaml`；实测 |
| **D-2** | Medium | exit 1 的分支清单（脚本头、help、§9.4）均未包含命名空间引导失败；全 docs 无对应条目 | `:46-50,331-332`；`kubernetes.md:533-554` |
| **D-3** | Medium | **jobs 列表**查询失败被断言成「存在未终结的迁移 Job」（F-07 同类缺陷残留在 jobs 路径）；fake 支持 `JobsListFailures/Timeouts` 但无用例 | `:1650-1656,1777-1779,1856-1857`；`fakestate/state.go:197-198` |
| **D-4** | Medium-Low | exit 3 文档未覆盖：(a) 迁移 Job 已创建后失锁（`:1913`、`:2537`）；(b) 应用已 apply 且已验证后复核失败（`:2651`） | `kubernetes.md:538,563-564` |
| **D-5** | Medium-Low | Lease create 失败（403/5xx）返回 2 → 与「等待超时」合并为同一句「未能在 60s 内获取发布锁…本次发布不执行任何变更」：归因错误；且此前可能刚创建命名空间，「不执行任何变更」不成立（退出码 3 正确） | `:1135-1138,2253-2263` |
| **D-6** | Low | 文档矛盾：6/7 的「查询失败」归属（X-1）；§8.3 漏 `NotFound→7`（X-2） | `kubernetes.md:355,570-572`；`deploy-lock.md:335-336`；`:2334-2343` |
| **D-7** | Low（覆盖缺口） | 默认值四方契约只锁 3 个租期参数；migration 仅下界断言；rollout/lock-wait/context 无断言；margin 180 在测试中重复硬编码 | `manifest_test.go:1007-1032,1092-1124`；`renew_test.go:89-149` |
| **D-8** | Low（覆盖缺口） | Pod 矩阵缺 5 项（Unknown、空 phase、孤儿 Pod、scan 终态/终止中）；`POD_GATE_CODE=4` 无用例；命名空间引导分支与 jobs 列表故障均无用例 | `:1259-1263,1307-1332`；`podquery_test.go:39-217` |

## 8. 最终判定

| 项 | 判定 |
|---|---|
| F-03 默认值 | **PASS**（含 D-7 覆盖缺口） |
| F-04 退出码 | **FAIL（部分）**：文档主体已修正；D-1/D-2/D-9 三条常规路径仍不满足「发出写⇒结果未知须标记」 |
| F-07 Pod 三态 | **PASS**（含 D-8 覆盖缺口）；jobs 路径残留同类缺陷（D-3） |
| 文档一致性 | **PARTIAL**：现行默认值全一致；D-1/D-2/D-4/D-6 与 B-7/B-8/A-6 为实质冲突 |

## 9. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| `kubectl apply` 超时/网关错误时命名空间对象是否真的创建 | 无集群；只证明「脚本未标记结果未知」 |
| `kubectl kustomize` 实际产物 | 未生成；「迁移清单 = Namespace + ConfigMap + Job」由 kustomization 静态推断（实测 403 路径间接印证） |
| `kubectl logs`/`describe` 在真实 RBAC 下的报错形态 | 无集群 |
| Pod `Unknown` 相位的真实成因与持续时长 | 无集群 + 无 kubelet |
| `POD_GATE_CODE=4` 实际不可达 | 依据 PodPhase 枚举推断 |
| `set -e` 下 `mktemp`/`mapfile` 等未捕获失败的退出码 | 范围外残余风险 |
