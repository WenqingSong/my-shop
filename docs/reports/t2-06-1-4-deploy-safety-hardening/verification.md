# T2-06.1.4 文档包验证记录（Agent D）

- 分支：`feat/t2-06-1-4-d-docs`（基线 `1919d4a`）
- 工作区：`/root/projects/wt-d-docs`
- 范围：`docs/design/deploy-lock.md`、`docs/development/kubernetes.md`、
  `docs/design/deployment.md`、`docs/development/migrations.md`、
  新增 `test/manifest/doc_contract_test.go`

## 1. 结论摘要

1. 文档里所有**数值与行为**都能追溯到四类事实来源之一：`scripts/deploy-k8s.sh` 的常量与
   函数实现、交付的 RBAC 清单、Agent A/B/C 的报告与测试代码、或本文件记录的命令输出。
   没有凭空写入的数字。
2. 「零重叠 / 重叠 0s」这类过度声明已从两份主文档中删除，改为可证伪的弱化表述：
   **「不发起新请求」这一侧有界；已发出的在途请求无法撤销，跨接管写入仍可能发生**
   （`AUD-02 = NOT_CLOSED`、`AUD-05 = NOT_CLOSED`）。
3. 新增的文档契约测试把「文档 ↔ 脚本 ↔ 清单」三方拉进同一个门禁：改任一侧而不同步文档都会
   让 `bash scripts/test-k8s-manifest.sh` 失败。
4. **没有**任何一条结论来自真实 Kubernetes 集群。fake kubectl 构造的窗口不等于真实
   API Server / etcd 的并发语义，本文件与文档均按此表述。

## 2. 文档变更清单

| 文件 | 变更 |
|---|---|
| `docs/design/deploy-lock.md` | 删除 §10.1/§10.3 的「零重叠」结论，改为「未发出请求有界 + 在途请求不可撤销」；新增 §10.4 在途请求不可撤销（三个时刻 t_req/t_acc/t_resp）；新增 §7.5 `renewTime` 锚点语义；重写 §12 RBAC（三类权限组、29 项 `REQUIRED_PERMISSIONS`、预检四阶段、`classify_api_error` 五类分类与各场景退出码、`leases` patch 的 operator-only 例外）；§13 增加权限分类、文档契约、在途写（含 T2-06.1.2 / T2-06.1.4 两条证据的归属）、renewTime 锚点、退避同源的验证条目 |
| `docs/development/kubernetes.md` | §9.1 发布步骤改为「权限预检 → 命名空间引导 → 取锁 → 迁移」并加入锚点步骤；§9.2 增加 `renewTime` 锚点与退避单一来源；§9.4 退出码 1/3/143 语义补齐（信号三结论）；§9.7 请求侧边界与在途请求不可撤销；§10.3 权限/锁失败表按错误分类重写；§13.1 改为「一份文件、三个权限组」+ 3 处 `REPLACE_WITH_DEPLOYER_*` 占位符 + 29 条 `auth can-i` 自检 + ClusterRole 必要性 + operator-only 例外；§14 验证分级补充条目 |
| `docs/design/deployment.md` | §12 发布顺序中的权限预检改为集群级 / 命名空间级两段，并注明命名空间引导在其后 |
| `docs/development/migrations.md` | §6 补充 T2-06.1.4 的两处收紧：`renewTime` 锚点 + 过期锁不得写迁移记录续命 |
| `test/manifest/doc_contract_test.go` | 新增（本工作包独占） |

## 3. 文档契约测试覆盖（`test/manifest/doc_contract_test.go`）

| 测试 | 断言 | 事实来源 |
|---|---|---|
| `TestDocCLIDefaultsMatchScript` | 6 个带数值选项（`--migration-timeout` 1080、`--rollout-timeout` 600、`--lock-wait-timeout` 60、`--lease-duration` 45、`--lease-renew-interval` 10、`--lease-renew-timeout` 3）在「脚本常量 = `usage()` = 文档 §9.2 = deploy-lock §7.3」四处一致；选项集合完全一致（不多写不存在的参数） | 脚本 `^NAME=<int>$` 常量 + `usage()` 文本 |
| `TestDocExitCodesMatchScript` | `usage()` 退出码块 == 文档 §9.4 表格（+ 信号码 143）；脚本里 `die`/`exit` 用到的每个码都被 `usage()` 声明；`1` 的多义语义（`APPLICATION_OUTCOME_UNKNOWN` / 不等于 / 可能已发出）不得被简化 | `usage()` 文本 + 文档 + 脚本 `die/exit` |
| `TestDocLeaseBudgetMatchesScript` | 从 `LOCK_*` 常量重算 `interval + attempts×(get+replace) + backoff + margin < duration`（当前 34 < 45）、续租链 19s（3×6+1）、检测上界 29s（间隔 10s + 链 19s）、最早可接管 42s；并禁止把「检测上界 29s」写成「续租链最坏 29s」 | `LOCK_*` 常量 |
| `TestDocRBACManifestsAndPermissions` | 磁盘上的清单文件名集合 == 文档 §13.1 提到的集合；`REQUIRED_PERMISSIONS`（29 项，`verb\|group\|resource\|scope\|callers@Lline`）逐项被对应作用域的清单覆盖（`cluster` → `ClusterRole`，`namespace` → `Role`）；`@L` 行号不超出文件；调用者函数名在脚本中存在；出现 `namespaces` 时文档必须解释 `ClusterRole`；清单不得出现 `*` / `delete` / `deletecollection` | 脚本 `REQUIRED_PERMISSIONS` + `manifest/kubernetes/examples/deploy-lock/*.yaml` |
| `TestDocDoesNotClaimZeroOverlap` | 四份文档中不得出现 `零重叠` / `无重叠` / `不会重叠` / `重叠 0s` / `重叠为 0` | 纯文本断言 |
| `TestDocKeepsInFlightIrreversibility` | 两份主文档都必须写明「已发出的请求无法撤销」与「客户端超时 ≠ 请求未生效」 | 纯文本断言 |
| `TestDocKeepsAuditFindingsOpen` | 两份主文档都必须同时出现 `AUD-02`/`AUD-05` 与 `NOT_CLOSED`（80~120 字符内），且不得出现「= CLOSED / 已关闭 / 已修复」 | 纯文本断言 |
| `TestDocPreservesNotVerified` | 四份文档都保留 `NOT_VERIFIED`；`deploy-lock.md` §13 的两条历史未验证项（「真实 API Server 的 Lease 竞争」「fencing 窗口的实际大小」）不得删除 | 纯文本断言 |

## 4. 实测命令与真实退出码

### 4.1 本工作区（`/root/projects/wt-d-docs`，基线脚本 + 基线 RBAC）

```text
gofmt -l .                        exit=0   输出为空
go build ./...                    exit=0
go vet ./...                      exit=0
go test -count=1 ./test/manifest/ exit=0   ok  1.470s
bash scripts/test-k8s-manifest.sh exit=0
bash scripts/test-deploy-k8s.sh   exit=0   ok test/deployk8s 30.328s
```

### 4.2 集成分支本地合并验证（`/tmp/d-integ`，D + C + A + B 合并后）

合并顺序：`d-docs` → `c-fake` → `a-rbac` → `b-lease`（三次 merge 均无冲突，脚本自动合并）。

```text
gofmt -l .                        exit=0   输出为空
go build ./...                    exit=0
go vet ./...                      exit=0
go test -run TestDoc ./test/manifest/  exit=0  8/8 PASS（RBAC 日志：29 项权限、Role 命名空间 surgecart）
bash scripts/test-k8s-manifest.sh exit=0   （含 kubeconform：9 resources Valid）
bash scripts/test-deploy-k8s.sh   exit=0   ok test/deployk8s 49.295s
```

> 这次合并只是为了验证「文档契约在 A/B/C 的实现落地后仍然成立」，合并只在 `/tmp` 下进行，
> 没有 push、没有修改其他 Agent 的分支。

## 5. 与其他 Agent 的事实对齐

| 事实 | 来源（已核对） |
|---|---|
| `REQUIRED_PERMISSIONS` 29 项、格式 `verb\|group\|resource\|scope\|callers@Lline`、单文件 6 个对象（2 Role + 2 RoleBinding + ClusterRole + ClusterRoleBinding） | Agent A 的 `scripts/deploy-k8s.sh` 与 `manifest/kubernetes/examples/deploy-lock/rbac.yaml`；本文件的契约测试直接从两者解析 |
| 预检四阶段（集群级 → 命名空间存在性 → 命名空间级 → 命名空间引导） | 脚本 `check_required_permissions` |
| `classify_api_error` 五类（forbidden / notfound / api / timeout / unknown）与各场景退出码 | 脚本 `classify_api_error` + `require_secrets` / `namespace_status` / `read_deployment_state` 的调用点 |
| `renewTime` = 写入请求发出时刻（t_req）；create/takeover 后 `lease_require_window` + 立即续租 | 脚本 `lease_create` / `lease_takeover` / `lease_renew` / `lease_require_window`；Agent B 的 `TestMultiprocessPreflightDelayKeepsRenewTimeFresh`、`TestMultiprocessStaleRenewTimeAtCreateFailsClosed` |
| 过期锁不得写迁移记录续命 | Agent B 的 `TestRecordWriteRefusedWhenLeaseExpired` |
| 退避预算与实现同源 | Agent B 的 `TestRenewBackoffBudgetIsSingleSourced` |
| 在途写跨接管仍生效（T2-06.1.2 的 harness 用例） | `test/deployk8s/application_inflight_test.go` 的 `TestLockLostDuringApplicationApplyReportsUnknown`（注入 1500ms 延迟 + 在途接管 → `Applied == true`） |
| 在途写跨接管仍生效（T2-06.1.4 新增的多进程实证） | `test/deployk8s/multiprocess_test.go` 的 `TestMultiprocessPausedRunnerWriteLandsAfterTakeover`（A 在途 apply 在 B 接管并释放锁后仍落地；集成树上实测 `--- PASS (6.37s)`） |
| fake 按调用语义注入、按真实 RBAC 清单判定 | Agent C 的 `INTERFACE_CONTRACT.md`（FROZEN，v1.2）与 `concurrency_test.go` |
| A-5 写路径授权在 fake 层闭环 | `test/deployk8s/rbac_write_test.go`（**无 `t.Skip`**）：`TestDeliveredRBACOnlyUpgradeSucceeds` PASS 1.50s、`TestDeliveredRBACOnlyFirstDeploySucceeds` PASS 1.60s、`TestWritePathDeniedByResourceNamesFailsAfterPreflight` PASS 3.60s（本机在 A 的 worktree `4fe15b1` 上定向复跑，exit 0） |

## 6. NOT_VERIFIED（本工作包相关）

- **真实 Kubernetes API Server / etcd 的并发与 Lease 语义**：环境无集群、无 kubectl，
  所有「并发」结论都来自 fake kubectl（单文件状态 + flock）。
- **真实 `auth can-i` / RoleBinding 的效果**：文档里的 29 条自检命令没有在真实集群执行过。
  A-5 的写路径授权只在 **fake** 上闭环（交付清单即可跑通完整发布、`resourceNames` 收紧会
  fail-closed），真实集群的 RBAC / admission 行为仍未验证。
- **在途写窗口的真实长度**：fake 里用 1500ms 的 apply 延迟构造窗口；真实集群上的窗口
  取决于网络与 apiserver 负载，未测量。
- **时钟偏移导致的误接管/误失锁**：需要可控时钟的真实环境；本文只记录方向
  （本机时钟偏快 → 服务器认为租约更久 → 不安全方向）。
- **`scripts/test.sh` / `test-race.sh` / `test-migration.sh`**：环境无 MySQL(3306)/Redis(6379)，
  按公共规则不运行、不伪造 PASS。
- **「0.7s 级在途写」未在仓库中找到**：`application_inflight_test.go` 用的是注入
  1500ms 延迟（`fixtureOptions{applyDelay: 1500 * time.Millisecond}`），
  `TestPausedRunnerResumesAfterLockLoss` 同样不是 0.7s。任务书提到的 0.7s 级反例无法核对，
  文档因此**不写**任何 0.7s 数值；契约测试直接禁止 `0.7s` 出现在 `deploy-lock.md`。
- **在途写窗口的真实长度**：1500ms / 6s 都是测试注入的延迟，不是网络在途时长的测量值。

## 7. 审计结论状态

| 项 | 状态 | 说明 |
|---|---|---|
| `AUD-02`（缺少 fencing token） | **NOT_CLOSED** | 本任务未实现服务端 fencing |
| `AUD-05`（已发出的请求无法撤销） | **NOT_CLOSED** | 已由故障注入实验直接观测到跨接管写入 |
