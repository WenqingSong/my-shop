# T2-06.1.3 审计报告 A — F-01：RBAC 权限与实际操作契约

## 0. 执行与取证方式

| 项 | 事实 |
|---|---|
| 独立审计子 Agent | `agent-a-rbac`（团队 `t2-06-1-3-audit`，独立上下文，只读源码，无 shell） |
| 主 Agent 复核 | 逐行阅读生产脚本 + `/tmp/audit-a` 四阶段 RBAC 放宽实验 + 仓库门禁实跑 |
| 交叉核对 | 与 `agent-c-cas` 关于「权限模型与真实授权脱节」的结论互相核验；两处表述冲突已由双方勘误收敛（见 §9） |

## 1. Git 基线（主 Agent 核验）

分支 `feat/k8s-t2-06-1-3-rbac-renewal-budget`；HEAD `1919d4a238dac431b194222547c30530f012105f`（= 远端）；HEAD^ `6919139`；`git status` 仅 `?? .codebuddy/` 与本次新增的报告文件。审计期间未修改任何受控文件。

## 2. 结论摘要

**核心问题：「按照交付的 `manifest/kubernetes/examples/deploy-lock/rbac.yaml` 授权后，脚本能否完成整个发布流程？」——不能。**

该示例只覆盖「发布锁 + 迁移生命周期」链路（leases / jobs get,list / pods get,list）。脚本还必需 `namespaces`（**集群级** get/create）、`secrets get`、`deployments get`、`jobs create/patch`、`configmaps/services/pvc create/patch`、`pods/log get`，示例一条都没有，仓库内也没有其他交付物提供。

首个失败点在权限预检**之前**：`deploy-k8s.sh:2239` 的 `get namespace` 对仅有示例权限的身份返回 Forbidden → 被当作「命名空间不存在」→ `apply -f namespace.yaml` 再次 403 → `die "创建命名空间 surgecart 失败"`（退出码 1）。

### F-01 分项判定

| 子项 | 判定 | 依据 |
|---|---|---|
| a. 示例补齐 core `pods get/list`（T2-06.1.2 的具体缺口） | **PASS** | `rbac.yaml:63-65` |
| b. 预检覆盖 pods 且在迁移前失败 | **PASS** | `deploy-k8s.sh:1046-1047`；预检 `:2248` 早于 `begin_migration` `:2293` |
| c. 预检覆盖发布流程实际全部权限 | **FAIL** | 仅 7 项（`:1037-1048`） |
| d. 契约测试能证明「按示例授权可完成发布」 | **FAIL** | `test/manifest/rbac_test.go:114` verbs 无 `apply`；`:52-56` 白名单丢弃业务资源 |
| e. 文档/帮助文本对权限来源的陈述准确 | **FAIL** | `deploy-k8s.sh:339-341`、`kubernetes.md:1084,1133`、`deploy-lock.md:646-659` |
| f. 按示例授权后能完成整个发布流程 | **FAIL（已实测）** | §5 实验 1-4；结构性原因见 A-1 |

## 3. 完整 API 操作矩阵（file:line）

`KUBECTL=(kubectl --request-timeout=30s)`（`:427`）、`LEASE_KUBECTL=(kubectl --request-timeout=<n>s)`（`:429`），子命令不在行首。

### 3.1 发布锁链路（示例已授权）

| # | 行号 | 调用 | group | resource | verb | 示例 |
|---|---|---|---|---|---|---|
| L1 | 731 | `get lease <LOCK_NAME> -o go-template` | coordination.k8s.io | leases | get | ✅ resourceNames |
| L2 | 765 | `create -f lease-create.yaml` | coordination.k8s.io | leases | create | ✅ |
| L3 | 781 | `replace -f lease-takeover.yaml` | coordination.k8s.io | leases | update | ✅ |
| L4 | 823 | `replace -f lease-renew.yaml`（前置 `:804`） | coordination.k8s.io | leases | update | ✅ |
| L5 | 880 | `replace -f lease-release.yaml` | coordination.k8s.io | leases | update | ✅ |
| L6 | 1570 | `replace -f lease-record.yaml` | coordination.k8s.io | leases | update | ✅ |
| L7 | 1608 | `get job <name>` | batch | jobs | get | ✅ |
| L8 | 1638 | `get jobs -l <selector>` | batch | jobs | list | ✅ |
| L9 | 1226 | `get pods -l job-name=<job>` | core | pods | list | ✅ |
| L10 | 1247 | `get pods -l component=database-migration` | core | pods | list | ✅ |
| L11 | 2131 | `get pods -l job-name=<JOB_NAME>` | core | pods | list | ✅ |
| L12 | 2600 | `get pods -l <POD_SELECTOR>` | core | pods | list | ✅ |
| L13 | 2386 / 2390 | `get pods -l` / `describe pods -l`（失败诊断） | core | pods | list / get | ✅ |
| L14 | 1064 | `auth can-i <verb> <res> -n <ns>` | authorization.k8s.io | selfsubjectaccessreviews | create | ❌ 依赖内置 `system:basic-user`（A-9） |

### 3.2 部署业务与前置（示例**全部未授权**）

| # | 行号 | 调用 | group | resource | verb | 示例 |
|---|---|---|---|---|---|---|
| B1 | 2239 | `get namespace <NAMESPACE>` | core | namespaces（**集群级**） | get | ❌ |
| B2 | 2241 | `apply -f shared/namespace.yaml` | core | namespaces | create/patch（+get） | ❌ |
| B3 | 2279 | `get secret <name>`（遍历 secretRef） | core | secrets | get | ❌ |
| B4 | 2085 | `get deployment <DEPLOYMENT_NAME>` | apps | deployments | get | ❌ |
| B5 | 1882 | `apply -f <migration 清单>`：Namespace + ConfigMap + Job | batch/core | jobs / configmaps / namespaces | create+patch（+get） | ❌ |
| B6 | 2486 | `apply -f <application 清单>`：Namespace + ConfigMap + PVC + Deployment + Service | apps/core | deployments / services / configmaps / pvc / namespaces | create+patch（+get） | ❌ |
| B7 | 2388 | `logs "job/<JOB_NAME>" --tail=200` | core | **pods/log** | get | ❌（`\|\| true` 静默） |
| B8 | 2673 | `get deployment,service,pod -l app.kubernetes.io/name=surgecart` | apps/core | deployments / services / pods | get / get / list | ❌ services 部分 |

无 RBAC 需求：`:2143 version`、`:2152/:2156 kustomize`（纯客户端）。

## 4. 示例未授权但必需（去重）

| 需要的权限 | 用途 | 缺失后果 |
|---|---|---|
| `namespaces get` | 命名空间引导 | 403 被误判「不存在」（**实测**） |
| `namespaces create/patch` | apply namespace.yaml（两个入口都含 Namespace） | 403（**实测**） |
| `secrets get` | 锁内 Secret 复检 | 403 报「缺少 Secret」（**实测**） |
| `deployments get` | 版本保护 / 收敛轮询 / 发布验证 | `die "无法确认线上 Deployment 状态"` exit 1（实测路径） |
| `jobs create/patch` | apply 迁移 Job | 403（**实测**） |
| `configmaps create/patch` | 两入口的数据库配置 | 迁移 apply 的下一个 403（推导） |
| `deployments/services/configmaps/pvc create/patch` | apply 应用清单 | `APPLICATION_OUTCOME_UNKNOWN` + exit 1 |
| `services get` | 末尾汇总查询 | `\|\| true` 静默 |
| `pods/log get` | 迁移失败诊断 | 日志静默丢失（A-8） |

**结构性矛盾（A-1 的核心）**：`namespaces` 是集群级资源，命名空间内的 `Role`（`rbac.yaml:34-65`）在 RBAC 语义下**不可能**授予；而 `test/manifest/rbac_test.go:268-269` 让任何 `ClusterRole/ClusterRoleBinding` 直接 `t.Fatalf`。交付物在当前形态下**无法表达**该权限。

## 5. 独立的四阶段实测（真实执行生产脚本 + 自写 RBAC 判定替身）

fixture：`/tmp/audit-a`（真实 `scripts/deploy-k8s.sh` + 独立 git 历史 + 独立 fake 状态）。替身按交付 Role 逐条判定 `auth can-i` 与真实资源操作。

| 实验 | 授权范围 | 退出码 | 关键输出 | 结论 |
|---|---|---|---|---|
| 1 strict | 完全等于交付 Role | `1` | `命名空间 surgecart 不存在…` → `Forbidden: namespaces … cannot create` → `创建命名空间 surgecart 失败` | 连命名空间引导都无法通过；权限错误被说成「不存在」 |
| 2 plus-ns | + `get namespaces` | `1` | `发布锁权限就绪（… pods: get/list）` → `已获取发布锁（新建 Lease）` → `缺少 Secret …：请先按 examples/secrets/ 创建` | 预检 7 项全过、Lease 已建，随后 `get secret` 403；错误归因为「Secret 不存在」 |
| 3 plus-secret | + `get secrets` | `1` | `读取 Deployment/surgecart 失败：Forbidden` → `无法确认线上 Deployment 状态（fail-closed）` | 又一次「预检通过后 403」；归类正确但仍是权限缺口 |
| 4 plus-business | + `get deployments` | `1` | `apply -f migration.yaml` → `Forbidden: jobs.batch … cannot create` → `Migration 清单应用失败（Job 未创建）` | 缺 `create jobs` 未预检；且只核验 Job，不含 ConfigMap |

实验 1 的调用序列证明失败发生在预检之前：

```text
--request-timeout=30s version
--request-timeout=30s kustomize …/migration/overlays/production
--request-timeout=30s kustomize …/overlays/production
--request-timeout=30s get namespace surgecart
--request-timeout=30s apply -f …/manifest/kubernetes/shared/namespace.yaml
```

仓库门禁：`bash scripts/test-deploy-k8s.sh` → exit 0（115 顶层用例 PASS）；`bash scripts/test-k8s-manifest.sh` → exit 0。**现有测试不能发现上述缺口。**

## 6. A2：权限预检机制

`permission_probe`（`:1059-1078`）：`rc==0 && out==yes*` → allowed；`rc!=0 && out==no*` → denied；其余 → unknown。`check_lease_permissions`（`:1082-1114`）对 denied/unknown 分别措辞，均 exit 3。

- **分类正确（PASS）**：API 故障/超时不会被误报为「权限被拒绝」；`TestPermissionPreflightDistinguishesForbiddenFromUnknown` 覆盖该分支。
- **覆盖失败（FAIL）**：
  1. 只查锁链路 7 项，B1–B8 一项未检 → 「预检通过、后续 403」是必然路径（实测 2/3/4）；
  2. 预检位置在命名空间引导之后（`:2239` → `:2248`），「预检先于任何资源调用」对 B1 不成立（实测 1 死在预检之前）；
  3. 两处把权限错误归因为对象不存在：`:2239`（任何非零都当「不存在」）、`:2279-2280`（`>/dev/null 2>&1 || die "缺少 Secret…"`，stderr 丢弃）；对照 `is_not_found_for`(`:616-626`) 与 `read_deployment_state`(`:2093`) 说明该模式已被掌握但未用于这两处；
  4. 退出码与文档承诺冲突：文档承诺「权限缺失 → exit 3」，实测 B1/B3/B5 均为 exit 1，`kubernetes.md:799-811` 排查表无对应条目。

## 7. A3：单一事实来源是否成立

`LEASE_PERMISSION_CHECKS`（`:1037-1048`）自称「脚本实际需要的唯一事实来源」，实际只是锁链路子集。`test/manifest/rbac_test.go` 的解析器有三处收窄：

1. `verbs` 集合 `{get,create,replace,delete,patch}`（`:114`）**不含 `apply`** → 三条 apply（`:1882/:2241/:2486`）在 `:134-155` 被投成 `verb="-"`，`:156` 整条丢弃；连带 `:174` 的「未知资源 → fatal」兜底对 apply **永不触发**。
2. `appResources`（`:52-56`）经 `:171-173` 显式 `continue` 丢弃 namespace/secret/deployment/pod 等。
3. `rawsToPermission`（`:183-207`）只识别 lease/leases/job/jobs/pod/pods。

实际推导：`used` = **6 项**（get/create/update leases、get/list jobs、list pods）；`declared` = 7 项（多一个无真实调用点的 `get pods`，见 A-10）。测试校验的是「手写表 ⊆ RBAC」，不是「脚本真实需要 ⊆ RBAC」。

**删除 RBAC 某项 → 哪个测试失败**：只有 `test/manifest` 的 `TestRBACGrantsEveryPermissionTheScriptNeeds`（`:317-321,:324-332`）会失败；**任何行为测试都不会失败**（见 A-5）。apply 类权限删无可删、新增需求也不会被检查。

## 8. A4：测试有效性

| 核验点 | 结论 | 证据 |
|---|---|---|
| 是否真实执行生产 Bash 脚本 | **是** | `harness_test.go:121-122` 拷贝脚本，`:861-881` 用 bash 执行 |
| 是否独立进程 | **是**（脚本 + 每次 kubectl 独立进程、独立 state） | `harness_test.go:160-186,861-881` |
| 行为测试是否加载 `rbac.yaml` | **否**（仅注释提及，`:13-20` 明确把一致性交给 test/manifest） | `test/deployk8s/rbac_test.go` 全文件无读取 |
| fake 的写路径是否校验权限 | **否**：`cmdCreate/cmdReplace/cmdApply/cmdGet*`（`main.go:320-554,608-892`）从不返回 403；只有 `auth can-i`（`:232-284`）读权限 | 主 Agent 实测：`permissions.update=false` 时 `replace` 仍成功 |
| 是否只测辅助函数 | 否（`TestStealLeaseAdvancesResourceVersion` 例外，见 C-13） | — |
| 删除 RBAC 某项能否被行为测试发现 | **不能** | 同上 |

## 9. 与 `agent-c-cas` 的两处交叉勘误（已收敛）

1. 关于「`test/deployk8s/` 是否引用 `rbac.yaml`」：正确表述是**只有注释级提及**（`rbac_test.go:13-15,20`），不是「全目录无引用」。实质结论（行为测试不加载 RBAC）不变。
2. 关于 A-12 的定性：`get configmap` 触发 `:174` fatal **符合**契约「实际使用必须登记」的设计意图，不应称为「误报」；真正的问题是**同一资源的覆盖随 verb 不同**，且 `:174` 的修复指引遗漏了 `appResources` 这个正确登记点。

## 10. 缺陷清单（13 条）

| 编号 | 严重度 | 缺陷 | 证据 |
|---|---|---|---|
| **A-1** | **Blocker** | 示例缺全部业务权限，按示例无法发布（实测）；namespaces 需 ClusterRole 而契约禁止 ClusterRole，交付物结构上无法表达 | `rbac.yaml:42-65`；`deploy-k8s.sh:2239,2241,2279,2085,1882,2486,2388,2673`；`rbac_test.go:268-269` |
| **A-2** | **High** | `get namespace` 任何失败（含 403/5xx/超时）被当作「不存在」→ apply 403 →「创建命名空间失败」；该步在预检之前（与报告 D 的 D-9 同源） | `:2239-2243,2248`；`is_not_found_for:616-626` |
| **A-3** | **High** | `get secret` 403 被归因为「缺少 Secret 对象」，stderr 丢弃 | `:2279-2280` |
| **A-4** | **High** | 契约解析器不含 `apply`，`appResources` 丢弃业务资源 → 最大权限类别永不检查；`deploy-lock.md:658` 的「==」声明不成立 | `test/manifest/rbac_test.go:114,134-158,52-56,171-173` |
| **A-5** | **High** | 行为测试不加载 RBAC，fake 写路径不校验权限 → 删除 RBAC 任一项都不会让其失败，无法证明「示例可用」 | `fakekubectl/main.go:232-284` vs `:320-554,608-892`；`rbac_test.go:13-20` |
| **A-6** | **Medium** | 帮助/文档称「身份只需 leases/jobs/pods，缺任一退出 3」；业务权限实际 exit 1 | `:339-341`；`kubernetes.md:1084,1133`；`deploy-lock.md:634-659` |
| **A-7** | **Medium** | Application apply 的确定性 403（已知失败）被报成 `APPLICATION_OUTCOME_UNKNOWN`（「可能已生效」），把已知说成未知 | `:2486-2531` |
| **A-8** | **Medium** | 失败诊断依赖 `kubectl logs`（需 pods/log），未授权且 `\|\| true` 静默；文档却让运维照抄 | `:2388`；`kubernetes.md:783` |
| **A-12** | **Medium** | 契约资源覆盖随 verb 不同（`configmap`：get 路径强制登记、apply 路径不可见）；`:174` 指引遗漏 `appResources`；`appResources` 含 `"pod"` 与 `"deployment,service,pod"`，属手工兜底名单 | `rbac_test.go:52-56,114,134-156,171-174,183-207` |
| **A-9** | **Low** | 预检依赖 `selfsubjectaccessreviews create`（内置 `system:basic-user`），示例未授予 | `:1064,1108-1111` |
| **A-10** | **Low** | 预检要求 `get pods`，但脚本所有 Pod 读取均为 `-l`（list 语义，已 grep 确认无单对象 `get pod`） | `:1046` vs `:1226,1247,2131,2600` |
| **A-11** | **Low** | fake 的 `auth can-i` 忽略 `-n` 与 `resourceNames` → 命名空间限定与单对象限定未被行为测试模拟 | `main.go:232-247` |
| **A-13** | **Low** | 契约层（复数 get→list）与替身层（`list` 复用 `Permissions.Get`）建模不一致，无对齐测试 | `main.go:271-273`；`rbac_test.go:190-203,123-134` |

## 11. 最终判定

**F-01：FAIL（部分修复）。** 修复了「示例漏 pods」这一具体项与预检的 Forbidden/unknown 分类；未修复「示例范围 ≠ 发布所需权限范围」，且契约测试与行为测试在两个层面都无法发现该类缺陷。「按示例授权能否完成发布」——源码层面可由 A-1 判定为不可行，shell 层面已由四阶段实验证实不成立。

## 12. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| 真实 API Server 上的 `auth can-i` 返回值与真实 RoleBinding 效果 | 无集群；本报告使用自写判定替身模拟 RBAC 规则 |
| client-side `kubectl apply` 的精确 verb 序列（PATCH vs POST） | 未抓取审计日志；`--dry-run=server --as` 可关闭 |
| `namespaces` 的 RBAC 作用域判定（命名空间 Role 不可授予集群级资源） | 依据 Kubernetes 通用语义推导，未实测 |
| 其余 `auth can-i` 组合（deployments/services/pvc/pods-log）与失败文案 | 无集群 |
| 若发布身份另有其他绑定（cluster-admin / edit）会掩盖 A-1 | 属「示例之外」权限，无法从仓库得知；反而反证「按示例授权即可发布」不成立 |
