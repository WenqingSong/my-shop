# T2-06.1 验证报告：Kubernetes 发布串行化与恢复加固

- 基线：T2-06 commit `77d6b6b8b0b176939c2e3f187e02f55198508e1b`
  （分支 `feat/k8s-t2-06-kustomize-deployment`）
- 任务分支：`feat/k8s-t2-06-1-deploy-safety`
- 结论：`T2-06.1_PASS_WITH_RISKS`（脚本语义已验证；真实集群的 Lease 并发 / RBAC / fencing
  窗口为 NOT_VERIFIED，见 §7）

本报告记录「做了什么、怎么验证的、什么还没验证」。设计论证见
`docs/design/deploy-lock.md`，操作手册见 `docs/development/kubernetes.md`。

## 1. 审计问题与本次修复

| 审计结论 | 本次交付 |
|---|---|
| Migration Job 有数据库 `GET_LOCK`，只保护迁移 | 发布级互斥：`coordination.k8s.io/v1` Lease/`surgecart-deploy-lock`，覆盖「迁移 → 应用 → 验证」全生命周期 |
| 两个发布流水线可以交错执行 | 第二个 Runner 等待锁（默认 60s），拿不到即以退出码 3 终止，不做任何变更 |
| 旧版本可能覆盖新版本且脚本返回成功 | 版本降级保护（Git 祖先关系）+ 发布成功判定（镜像/版本标记/generation/副本/Pod） |
| 脚本无行为级自动化测试 | `test/deployk8s`：40 个用例，真实执行脚本 + fake kubectl（真实进程/并发/信号） |
| Failed 与 Timeout 提示相似，可能误删 Job | 两者彻底分离：Failed → 退出码 2 + 恢复步骤；Timeout → 退出码 6 + 「禁止删除 Job」 |

## 2. 交付物

| 文件 | 变化 |
|---|---|
| `scripts/deploy-k8s.sh` | 重写：发布锁、锁权限预检、命名空间引导、版本降级保护、发布成功判定、Failed/Timeout 分离、信号与临时文件清理 |
| `scripts/test-deploy-k8s.sh` | 新增：发布脚本行为门禁入口（`bash -n` + `go test ./test/deployk8s/`） |
| `test/deployk8s/` | 新增：`harness_test.go`、`lease_test.go`、`migration_test.go`、`version_test.go`、`verify_test.go`、`fault_test.go` |
| `test/deployk8s/fakekubectl/` | 新增：fake kubectl（Lease 原子 create/CAS replace、Deployment/Job/Pod 状态机） |
| `test/deployk8s/fakestate/` | 新增：fake 集群状态模型（flock 串行化，跨进程真实竞争） |
| `manifest/kubernetes/base/deployment.yaml` | Pod 模板与 Deployment 增加 `app.kubernetes.io/version`（发布版本事实来源） |
| `manifest/kubernetes/overlays/production/version-patch.yaml` | 新增：把版本标记设置为发布短 SHA |
| `manifest/kubernetes/overlays/production/kustomization.yaml` | 引入 `version-patch.yaml`，注释改为「三处版本同值」 |
| `manifest/kubernetes/examples/deploy-lock/rbac.yaml` | 新增：发布锁最小权限（Role/RoleBinding，`resourceNames` 限定） |
| `test/manifest/manifest_test.go` | 新增 `TestVersionLabelTracksImageTag`、`TestDeployLockContract`（13 组断言） |
| `Makefile` / `scripts/test.sh` / `README.md` | 接入 `test-deploy-k8s` 门禁 |
| `docs/design/deploy-lock.md` | 新增：发布锁设计、CAS 正确性、剩余风险 |
| `docs/development/kubernetes.md` | 重写 §9–§15：发布流程/锁、退出码、降级、Failed/Timeout 恢复、锁卡住的人工处理、验证分级、约束 |
| `docs/development/migrations.md` | 同步：超时≠失败、发布锁与数据库锁的职责边界 |
| `docs/design/deployment.md` | 发布顺序图、回滚方式（`--allow-downgrade`）、边界 |

## 3. 发布锁机制（摘要）

资源：`Lease/surgecart-deploy-lock`（namespace `surgecart`），租期 30s，续租 10s。

| 动作 | 实现 | 并发安全来源 |
|---|---|---|
| 获取 | `kubectl create -f -` | 同名对象只能创建一次（`AlreadyExists` 即占用） |
| 接管（已释放/已过期） | `kubectl replace` + `metadata.resourceVersion` + `uid` | PUT 的乐观并发，冲突返回 409 |
| 续租 | 同上，后台每 10s | 同上 |
| 释放 | 先读 holder，确认是自己，再 CAS 清空 `holderIdentity` | 读取 + CAS；不删除对象（避免与接管者竞态） |
| 旧持有者 | 续租时发现 holder 已不是自己 → 中止；释放时跳过 | 强 |

失败路径：续租失败 → 写 `lost` 标记 + SIGTERM 主进程；主进程在每个资源变更点检查标记，
一旦发现立即 `exit 3`，不再做后续 apply。

## 4. 版本降级保护（摘要）

`C` = Deployment Pod 模板 `app.kubernetes.io/version`（缺失时回退镜像 tag），
`T` = 本次镜像 tag（与清单版本标记双向校验）。判定用 `git merge-base --is-ancestor`：

| 情况 | 结果 |
|---|---|
| Deployment 不存在 / `C == T` / `C` 是 `T` 的祖先 | 允许（首发布 / 幂等 / 升级） |
| `T` 是 `C` 的祖先 / 分叉 / 无法解析 | 拒绝，退出码 4（`--allow-downgrade` 可人工绕过，CI 需额外开关） |

## 5. 发布成功判定（摘要）

逐项比对后才打印 `DEPLOY_K8S_SUCCESS`：Deployment 存在、Pod 模板镜像、版本标记、
`observedGeneration == generation`、`replicas/updatedReplicas/availableReplicas` 对齐且
`unavailableReplicas == 0`、每个非终止 Pod 为 `Running` 且容器 Ready 且镜像/版本匹配。
任一不符 → 打印实际值与期望值 → `exit 5`，**不打印成功标记**。

## 6. 新增测试清单（40 个用例）

| 类别 | 用例 |
|---|---|
| 发布锁（10） | 首次创建并释放、并发竞争（恰好一个成功/一个退出码 3）、两 Runner 串行化、锁被占用、过期接管、正常续租、续租失败阻断 apply、被接管后旧持有者不释放新锁、权限不足在迁移前失败、释放使用 CAS |
| Migration（7） | Complete 才应用、Job 不存在、Failed 阻断应用并打印日志、Running→Complete、Timeout 不声称失败、Pod 终止中禁止删除、无 Pod 时要求先确认数据库 |
| 版本保护（10） | 首发布、同版本幂等、升级、降级拒绝、分叉拒绝、当前版本无法识别、目标版本无法解析、Git 历史缺失、`--allow-downgrade` 授权、CI 禁止降级（含额外开关放行） |
| 发布成功判定（7） | 成功、镜像不匹配、版本标记不匹配、未收敛、Pod 未 Ready、被其他操作改写、Pod 数量不符 |
| 故障处理（6） | kubectl 缺失、集群不可达、apply 失败释放锁、SIGTERM 释放锁并清理、SIGKILL 后过期接管、临时文件清理 |

断言方式：真实执行 `bash scripts/deploy-k8s.sh`，读取 fake kubectl 记录的事件序列、
Lease 最终状态、脚本退出码与输出。**不使用**「脚本源码包含某段文字」作为行为结论
（`test/manifest` 中的 `TestDeployLockContract` 是静态一致性断言，仅用于交叉校验
RBAC 示例与脚本的锁名，不冒充行为验证）。

## 7. 门禁结果

| 门禁 | 命令 | 退出码 |
|---|---|---|
| gofmt | `gofmt -l .`（无输出） | 0 |
| 构建 | `go build ./...` | 0 |
| 静态检查 | `go vet ./...` | 0 |
| 单元测试 | `bash scripts/test-unit.sh` | 0 |
| 完整门禁 | `bash scripts/test.sh` | 0 |
| 竞态门禁 | `bash scripts/test-race.sh` | 0 |
| 迁移门禁 | `bash scripts/test-migration.sh` | 0 |
| Kustomize 清单门禁 | `bash scripts/test-k8s-manifest.sh` | 0 |
| 发布脚本行为门禁 | `bash scripts/test-deploy-k8s.sh` | 0 |
| 容器运行时门禁 | `bash scripts/test-container-runtime.sh`（16 项断言） | 0 |

## 8. NOT_VERIFIED（本仓库无目标集群）

1. **Lease 在真实 API Server 上的并发语义**：fake kubectl 用文件锁串行化，真实环境是
   etcd 事务 + 多 apiserver；两者证明的不是同一件事。
2. **RBAC 最小权限的实际效果**：`examples/deploy-lock/rbac.yaml` 未在真实集群 apply，
   `auth can-i` 的真实返回值未验证。
3. **fencing 窗口实测**：暂停 Runner + 网络分区下「旧持有者仍能写入」的窗口未测量。
4. **时钟偏移导致的误接管**：未在可控时钟环境验证。
5. **真实集群的 rollout / preStop / PVC 绑定 / 迁移 Job 调度**（沿用 T2-06 的未验证项）。
6. **server-side dry-run**：无集群，未执行。

## 9. 剩余风险

1. **没有 fencing token**：租期过期并被接管后，被暂停的旧持有者仍可能成功发起一次写入；
   脚本用「变更前检查续租状态 + 幂等 apply + 发布后验证」压缩窗口，但无法消除。
2. **时钟依赖**：过期判定基于 Runner 本地时钟，偏移过大会导致误接管（偏快）或死锁更久（偏慢）。
3. **锁只保护脚本路径**：直接 `kubectl apply` 或其他 GitOps 工具不受保护。
4. **`create` 无法用 `resourceNames` 限制**：拥有 `create leases` 的身份可创建任意 Lease
   （RBAC 固有限制），应把该权限限制在单命名空间。
5. **浅克隆**：历史不足时版本保护 fail-closed，需要 `git fetch --unshallow` 才能发布。
6. **人工 `--allow-downgrade`**：降级不回滚 schema，数据库兼容性风险由操作者承担。

## 10. 明确未做（保留为后续问题）

D3（ConfigMap checksum 自动滚动）、D5（Job immutable 字段排障提示）、D6（Secret 预检补全）、
D7（镜像 digest 正则兼容）、D8（镜像 digest 不可变保证）、D10（迁移数据库锁等待策略）、
D11（退出码文档统一）均未在本次改动。

实现发布锁时唯一被调整的相邻项是：`scripts/deploy-k8s.sh` 的退出码扩展（新增 3/4/5/6），
因为它必须能区分「锁失败 / 版本拒绝 / 验证失败 / 迁移超时」；D11 要求的**全仓库**退出码文档
统一仍未做，本次只在本脚本与 `docs/development/kubernetes.md` §9.4 中记录。
