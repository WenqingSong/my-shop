# T2-06.1.5 — 最终集成与正式交付报告

> 任务：在实现 B（`3085522`）之上完成 T2-06.1.5 的最终集成，选择性吸收实现 A 的必要改进，
> 执行全部门禁并正式收尾。
>
> 最终状态：**`T2-06.1.5_FINAL_INTEGRATION_PASS`**

## 1. Git 基线与最终分支

所有值都是在本次开工后**重新从 Git 读取**的，没有采信历史审计记录的结论。

| 项 | 实测值 |
|---|---|
| 主仓库开工分支 / HEAD | `main` / `be930608db4173331f60a6094b7a9a6e8e8ea658` |
| 开工工作区 | 干净（`git status --porcelain` 无输出） |
| 共同基线 | `c8e92acecfcf4ea92b06f384de7072921727c721` |
| 实现 A | `1d7b4747ac8dc919d29eb00b7668dd156ca0b355`（远端 `feat/k8s-t2-06-1-5-rbac-resource-name-preflight`） |
| 实现 B | `308552201e10ee96f8ec33fe1be8a5a55bebc7a9`（远端 `feat/k8s-t2-06-1-5-rbac-resource-name-preflight-impl-3085522`） |
| A/B 共同祖先（`git merge-base`） | `c8e92ac`（与预期一致） |
| 最终集成分支 | `feat/k8s-t2-06-1-5-rbac-resource-name-final`（**开工前不存在**，从 `3085522` 创建） |
| 最终实现提交 | `c9e8e2889db4e76d366afd2a1de7f89b21722cb4` |
| A/B 原分支 | 未被移动、未被覆盖、未被 rebase |

核验方式：`git cat-file -t` 确认三个 SHA 均为 commit；`git merge-base 1d7b474 3085522`
返回 `c8e92ac`；`git branch -a --contains` 确认 A/B 各自只被自己的远端分支包含；
`git show-ref | grep final` 在开工时无输出（分支不存在）。

未执行：force push、rebase、reset、merge、cherry-pick、删除任何分支或历史审计证据。

## 2. A/B 实现选择依据

依据独立对比审计 `T2-06.1.5-IMPLEMENTATION-COMPARISON-AUDIT.md`（`/tmp/t2-0615-comparison/`），
并复核其引用的代码位置。结论：**采用实现 B**。

| 权重 | 理由 |
|---|---|
| 1 | B 保留了「预检通过后写路径仍强制授权」的**脚本级**端到端回归（改造为真实 TOCTOU：预检后撤销 resourceNames → replace 403 → fail-closed）；A 删除了该基线用例且无替代 |
| 2 | B 的 `requireDeliveredRBACPreflightShape` 断言 can-i 调用次数 == 权限表条数（29），能抓住「漏检一项」 |
| 3 | B 把授权语义测试放进独立 `fakestate` 包并修改门禁脚本纳入 `./test/deployk8s/...`，替身是否忠实有独立门禁 |
| 4 | B 的 `pods/log` 用例具备区分性（授予 pods get 后 `pods/log` 允许、`--subresource=log` 拒绝） |
| 5 | B 的静态契约逐动词断言 leases；文档更完整 |

未合并两份实现：两者对同一批文件做了互斥修改（权限表字段顺序不同），合并只会产生冲突
且无技术收益。审计结论同样指出两份实现均无 S1/S2 阻断问题。

## 3. 最终修改文件

相对实现 B（`3085522`）的最小增量，共 4 个实现/文档文件 + 2 份报告：

| 文件 | 改动 |
|---|---|
| `scripts/deploy-k8s.sh` | 原内联自检改写为 `permission_verb_is_name_scoped` + `validate_permission_table`：校验字段数 6、scope 枚举、必填字段、**name 只能用于 get/update/patch/delete** 且必须是 `LOCK_NAME`；失败退出码 **1**，且发生在任何 `auth can-i` 之前 |
| `test/manifest/rbac_test.go` | 抽出纯函数 `leaseResourceNamesViolations`（resourceNames 必须**精确等于** `[LOCK_NAME]`，create 不得带）；新增 `TestResourceNamesContractDetectsWeakenedManifest` 负控元测试与 `cloneRBACDocs` / `patchLeaseRules` 辅助 |
| `test/deployk8s/rbac_resource_name_test.go` | 新增 `TestPermissionTableSelfCheckRejectsInvalidEntry`（给 create 加对象名、字段数 6→7），断言退出码 1、无任何 can-i、零资源写 |
| `docs/design/deploy-lock.md` | §12.4 预检顺序补「阶段 0 权限表自检」及退出码 1 的理由；静态契约段落补充精确匹配与负控元测试 |
| `T2-06.1.5_IMPLEMENTATION_REPORT.md` | 头部追加最终状态指引（原文保留） |
| `T2-06.1.5_FINAL_INTEGRATION_REPORT.md` | 本文件 |

未修改：`manifest/kubernetes/examples/deploy-lock/rbac.yaml`（授权范围零变化）、
`test/deployk8s/fakestate/*`、`test/deployk8s/fakekubectl/*`、业务代码、迁移与依赖。

## 4. 吸收的 A 实现改进

先确认 B 是否已具备等价能力，只补缺口，不重复实现。

### 4.1 运行时权限表自检（B 有部分、缺动词校验）

B 原本的自检只校验「带 name 的条目必须是 leases 且等于 `LOCK_NAME`」。
给 `create` 加对象名（`create|...|leases|namespace|surgecart-deploy-lock|...`）能通过启动校验，
只能靠静态测试拦截 —— 不是 fail-closed（审计 B-1，S3）。

最终实现扩为三类校验（字段数 / scope / 动词作用域 + 原 name 一致性），
并明确使用**退出码 1**（参数/预检失败）而非 A 的 3（发布锁失败/权限不足）：
这是脚本自身的配置错误，报成「权限不足」会把排查方向引向 RBAC 绑定。

> 与 A 的差异：功能等价、退出码语义按本仓库契约修正（不吸收 A 的 exit 3）。

### 4.2 静态契约负控元测试（B 无）

B 原本把 leases 逐动词的 `resourceNames` 断言直接写在
`TestDeliveredRBACMatchesRequiredPermissions` 里，断言的有效性没有自证。

最终实现把该段抽成纯函数 `leaseResourceNamesViolations`，新增
`TestResourceNamesContractDetectsWeakenedManifest`：在**人为削弱**的清单副本上重跑同一函数，
要求必须报错。5 个负控子用例：

1. 删除 `resourceNames`（退化为整资源授权）
2. 改成错误的 Lease 名称
3. **扩大** `resourceNames`（追加第二个 Lease）
4. 给 `create` 加 `resourceNames`
5. 删除整条带 `resourceNames` 的 leases 规则

第 3 项是本次任务回归清单要求的「扩大 resourceNames 授权范围必须被契约测试发现」：
B 原断言用的是「包含 LOCK_NAME」，追加第二个名字不会被发现；现在改为精确相等。

## 5. RBAC 关键不变量

### 5.1 对象级授权

| 不变量 | 实现位置 | 证据 |
|---|---|---|
| Lease 的 get/update 使用正确对象名 | `permission_probe` 拼 `ref + "/" + name` | `TestPreflightUsesObjectLevelLeaseChecks`：`auth can-i get/update leases.coordination.k8s.io/surgecart-deploy-lock` |
| 对象名必须与 `LOCK_NAME` 一致 | `validate_permission_table` + 静态契约 | `TestRequiredPermissionsCoversAuditFindings`、`TestPermissionTableSelfCheckRejectsInvalidEntry` |
| create 不得要求对象名 | 权限表条目 name 为空；`requireNoAuthCheck` | `TestPreflightRejectsObjectNameForCreate`、预检形状断言 `auth can-i create leases.coordination.k8s.io `（无 `/`） |
| `resourceNames` 不得被扩大 | `leaseResourceNamesViolations` 精确相等 | 负控子用例「扩大 resourceNames」 |
| `pods/log` 按子资源处理 | 拆 `--subresource=log` | `requireAuthCheck "get pods --subresource=log"` + `requireNoAuthCheck "get pods/log "` |
| 预检语义与实际 API 调用一致 | 预检/写入都经 fake 的同一 `Authorize` | `TestWritePathDeniedAfterPreflightFailsClosed`（预检 allowed → 写入 forbidden → 退出 3 → 零 apply） |

### 5.2 Fake Kubernetes

| 不变量 | 实现位置 | 证据 |
|---|---|---|
| 无对象名不匹配带 `resourceNames` 的规则 | `Rule.ruleMatches`：`len(ResourceNames)>0 && name=="" → false` | `TestResourceNamesRuleDoesNotAuthorizeUnnamedRequests`、`TestResourceNamesObjectLevelSemantics/T7_*` |
| 不允许通配符绕过 | `n == name` 逐字比较，`"*"` 是字面量；`create/list` 由「无对象名」自然排除 | 同上 + `TestDeliveredRBACLeaseSemantics` |
| 错误对象名必须拒绝 / 正确对象名必须通过 | 同一 `ruleMatches` | `TestResourceNamesObjectLevelSemantics`（T3/T4/T5 允许，T6 拒绝） |
| create 与 update 的授权属性符合真实语义 | `ParseResourceArg` 按 kubectl 的 `SplitN("/",2)` | `TestParseResourceArgFollowsKubectl`（8 组）；第三个位置参数被拒 |

替身**未放宽**任何一条：改动方向全部是「更像真实集群」。

### 5.3 回归测试对照（任务书 5.3 七项）

| # | 要求 | 用例 | 结果 |
|---|---|---|---|
| 1 | 正确 Lease 名称通过预检 | `TestPreflightUsesObjectLevelLeaseChecks`、`fakestate/T3·T4·T5` | PASS |
| 2 | 错误 Lease 名称不能通过 | `TestWrongResourceNameFailsPreflightBeforeAnyWrite`、`fakestate/T6` | 退出 3 / DENY |
| 3 | 无对象名的受限检查不能通过 | `TestResourceNamesRuleDoesNotAuthorizeUnnamedRequests`、`fakestate/T7` | DENY |
| 4 | 删除对应 RBAC 权限必须失败 | `TestMissingLeasePermissionFailsPreflightWithZeroWrites`（get/update 各一例）、`TestEveryRequiredPermissionIsEnforced`（29 项逐项） | 退出 3、零写 |
| 5 | 扩大 `resourceNames` 必须被契约测试发现 | `TestResourceNamesContractDetectsWeakenedManifest/扩大_resourceNames` | 报错（负控通过） |
| 6 | 只授予交付 RBAC 的正常部署路径通过 | `TestDeliveredRBACOnlyFirstDeploySucceeds`、`TestDeliveredRBACOnlyUpgradeSucceeds` | PASS |
| 7 | 无权限的写入路径必须被拒绝 | `TestWritePathDeniedAfterPreflightFailsClosed`（TOCTOU） | 退出 3、零 apply |

## 6. Fake Kubernetes 测试结果

| 套件 | 结果 |
|---|---|
| `test/deployk8s`（真实执行 `deploy-k8s.sh` + fake kubectl） | 249 PASS / 0 FAIL / 0 SKIP |
| `test/deployk8s/fakestate`（授权语义单元测试） | 含在 249 内；`TestParseResourceArgFollowsKubectl`、`TestResourceNamesObjectLevelSemantics`(9 子用例)、`TestResourceNamesRuleDoesNotAuthorizeUnnamedRequests`、`TestDeliveredRBACLeaseSemantics` 全 PASS |

数量核算（相对实现 B 的 246）：新增 `TestPermissionTableSelfCheckRejectsInvalidEntry`
（1 个父用例 + 2 个子用例）= 249，与门禁输出一致；**原有 246 项全部仍然通过**，
没有删除或跳过任何用例。

## 7. 门禁及真实退出码

执行环境：Linux，Go `go1.24.1 linux/amd64`，Docker `29.9.2`，`git 2.39.5`。

为真实执行 Kustomize 构建，在**仓库外**安装并仅通过 `PATH` 使用：

| 工具 | 版本 | 安装位置 | 说明 |
|---|---|---|---|
| `kustomize` | v5.8.3 | `/tmp/t2-0615-tools/bin/kustomize` | 仓库未跟踪；`test-k8s-manifest.sh` 走「独立 kustomize CLI」分支（无 kubectl） |

| # | 命令 | 退出码 | 结果摘要 |
|---|---|---|---|
| 1 | `gofmt -l .` | **0** | 无输出 |
| 2 | `go build ./...` | **0** | |
| 3 | `go vet ./...` | **0** | |
| 4 | `bash scripts/test-unit.sh` | **0** | 纯内存单元测试全部通过 |
| 5 | `bash scripts/test-deploy-k8s.sh` | **0** | **249 PASS / 0 FAIL / 0 SKIP** |
| 6 | `bash scripts/test-k8s-manifest.sh` | **0** | Kustomize 真实构建 + **34 PASS / 0 FAIL / 0 SKIP**（29 顶层用例 + 5 子用例） |
| 7 | `bash scripts/test-container-runtime.sh` | **0** | 16 项断言（非 root / 只读根 / SIGTERM drain / 无残留进程） |
| 8 | `bash scripts/test-container.sh` | **0** | 容器化静态断言 |
| 9 | `bash scripts/test-race.sh` | **0** | 需 MySQL/Redis：已用 `scripts/deps-start.sh` 启动本地 compose MySQL+Redis 后执行 |
| 10 | `bash scripts/test-migration.sh` | **0** | 真实 MySQL + 多进程迁移（86s） |
| 11 | `bash scripts/test.sh` | **0** | 完整门禁（vet + unit + 集成 + migration + registry + prometheus + container + k8s manifest + deploy-k8s + nginx） |

补充：`test.sh` 内 `test-nginx.sh` 因未构建 `my-shop-nginx:local` 跳过容器内 `nginx -t`，
静态断言仍全部执行并通过（脚本内既有的显式跳过，非本次引入）。

## 8. NOT_VERIFIED

| 项 | 原因 | 影响 |
|---|---|---|
| 真实 Kubernetes 集群上的 `auth can-i` / SSAR 结果、RBAC 传播延迟、admission webhook 影响 | 环境无集群、无 kubectl | 只证明脚本按正确语法发起对象级检查 + 替身符合真实 RBAC 语义；真实集群行为未验证 |
| 真实 `kubectl --subresource` 行为 | 无 kubectl 二进制 | 依赖 kubectl ≥ 1.24（已写入前置条件）；旧版会以「无法确认」退出 3（fail-closed，不误放行） |
| `kubectl apply --dry-run=server` 与真实部署 | 无集群 | 未执行 |
| `kubeconform` API Schema 校验 | 未安装 | `test-k8s-manifest.sh` 打印 WARN 后跳过（脚本既有行为） |
| `test-nginx.sh` 的 `nginx -t` | 未构建 nginx 镜像 | 静态断言已通过 |

以上均为**环境限制**，不是实现缺陷；未伪造 PASS、未删除或绕过任何测试。

## 9. 剩余风险

| 风险 | 级别 | 说明 |
|---|---|---|
| `AUD-02`（缺少 fencing token） | **NOT_CLOSED** | 本次未触碰任何 fencing 机制；不声称实现了强 fencing |
| `AUD-05`（已发出的请求无法撤销） | **NOT_CLOSED** | `read-after-write` 只提高可观测性，不是写入撤销机制 |
| 真实集群预检未验证 | 中 | 见 §8 |
| TOCTOU 用例的时序依赖 | 低 | `wrapKubectlOnceWithMarker` + 2s sleep；本次 3.91s PASS |
| `scriptKubectlUsage` 不解析 `apply` | 低 | 基线遗留（审计 C-1）：apply 的 get/create/patch 由构建产物推导 + 端到端行为测试覆盖 |
| 无集群下的 `kubeconform` / dry-run | 低 | 见 §8 |

未扩大安全保证的表述：本次只是在同一机制上收紧配置自检与静态契约，没有新增任何
「旧 Runner 的写入一定会被拒绝」之类的承诺。

## 10. Commit SHA

| 项 | 值 |
|---|---|
| 实现 B（最终实现基线） | `308552201e10ee96f8ec33fe1be8a5a55bebc7a9` |
| 最终实现提交（代码 + 测试 + 文档） | `c9e8e2889db4e76d366afd2a1de7f89b21722cb4` |
| 报告提交 | 本文件所在提交；`git log -1 --format=%H` 为准确值（提交 SHA 无法写入自身） |
| 本地提交链 | `c8e92ac` → `b1af31a` → `976eaad` → `7f49e38` → `0458e7c` → `d68e508` → `3085522` → `c9e8e28`(实现) → 报告提交 |

## 11. 远端分支 SHA

| 项 | 值 |
|---|---|
| 分支 | `feat/k8s-t2-06-1-5-rbac-resource-name-final` |
| 远端 HEAD | 与本地 HEAD 一致；实测值 = 本文件所在提交（报告提交） |
| 核对命令 | `git ls-remote --heads origin feat/k8s-t2-06-1-5-rbac-resource-name-final` 与 `git rev-parse HEAD` 必须输出同一个 SHA |
| 推送方式 | 新建分支的常规 push（`-u`），**未** force push |
| A 原分支 | `feat/k8s-t2-06-1-5-rbac-resource-name-preflight` = `1d7b474`（未变） |
| B 原分支 | `feat/k8s-t2-06-1-5-rbac-resource-name-preflight-impl-3085522` = `3085522`（未变） |

## 12. 工作区状态

- 提交完成后 `git status --porcelain` 为空（工作区干净）。
- 未合并 `main` / `develop`；未开始 T2-07；未进入 Kubernetes 部署任务。
- 未修改业务代码、未新增依赖、未改动迁移。

## 13. 范围声明

本次**未**做：解决 AUD-02/AUD-05、增加新的发布锁机制、引入分布式锁、重构无关 Bash 代码、
修改业务代码、开始 T2-07、扩大安全保证表述、增加无实际价值的测试。

发现但未处理的范围外问题：`scriptKubectlUsage` 对 `apply` 的静态覆盖仍需依赖端到端行为测试
（基线遗留 C-1），已记录在 §9，不在本次修改。
