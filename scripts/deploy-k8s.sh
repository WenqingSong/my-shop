#!/usr/bin/env bash
# T2-06.1.2 Kubernetes 发布脚本：**先拿发布锁，再迁移，后应用，最后验证**。
#
#   bash scripts/deploy-k8s.sh [--context <name>] [--migration-timeout <sec>] [--rollout-timeout <sec>]
#                              [--lock-wait-timeout <sec>] [--lease-duration <sec>]
#                              [--lease-renew-interval <sec>] [--lease-renew-timeout <sec>]
#                              [--allow-downgrade]
#
# 执行顺序（任一步失败立即非零退出，后续步骤不执行）：
#   1. 环境预检：kubectl / 集群可达 / 构建两个清单 / 占位符 / 镜像与版本标记一致 /
#      迁移等待上限与 Job activeDeadlineSeconds 的时间关系
#   2. 权限预检（REQUIRED_PERMISSIONS：集群级 → 命名空间级）+ 命名空间引导：
#      全部权限判定发生在**第一次写请求之前**；只有命名空间确实不存在时才做幂等创建
#      （审计 A-1/A-2/A-5：namespaces 是集群级资源，Role 无法授予；权限判定不得晚于写）。
#      受 resourceNames 限制的权限（Lease 的 get/update）必须按对象名检查：
#      `auth can-i <verb> leases.coordination.k8s.io/<LOCK_NAME>`（T2-06.1.5 / F-01）
#   4. 获取发布锁 coordination.k8s.io/v1 Lease/surgecart-deploy-lock（原子 create + resourceVersion CAS）
#      接管已释放/已过期的锁之前，必须核验上一轮关联的 Migration Job（T2-06.1.1 / AUD-01）
#   5. 锁内复检：Secret 就绪 + 版本降级保护（当前 Deployment 版本 vs 本次目标版本）
#   6. 迁移：先把「预期 Job 名」以 CAS 写入 Lease 注解（MIGRATION_PENDING）→ 再 apply Job →
#      再用真实 Job UID 回写（MIGRATION_PENDING + uid）→ 轮询 → 终态写 COMPLETE/FAILED
#   7. 应用 Application 清单 → 轮询 Deployment 收敛
#   8. 发布成功判定：镜像 / 版本标签 / generation / 副本数 / 实际 Pod 全部匹配
#   9. 释放发布锁 → 打印 DEPLOY_K8S_SUCCESS
#
# 迁移生命周期与发布锁的绑定（T2-06.1.1 的核心不变量，T2-06.1.2 加固）：
#
#   NO_MIGRATION → MIGRATION_PENDING → MIGRATION_RUNNING → MIGRATION_COMPLETE
#                                                        ↘ MIGRATION_FAILED
#
#   - 记录写在 Lease 的注解上（当前记录 + 上一轮归档，契约见下方「数据契约」）；
#   - 记录只能由 migration_record_write 有意识地修改；接管/续租/释放**原样保留**记录，
#     不会用内存里的 NO_MIGRATION 覆盖上一轮证据（T2-06.1.2 / AUD-D2）；
#   - 注解**不单独作为事实来源**：接管前必须用真实 Job 对象核验（state / uid / 终态），
#     并且必须确认该 Job 的 Pod 已停止执行（T2-06.1.2 / AUD-D3）；
#   - 迁移未确认终态时，cleanup **不释放发布锁**，关联信息全部保留，等锁自然过期后由后续
#     Runner 核验；因此「Job 还在跑」时不可能有第二个 Runner 拿到锁开始新一轮发布。
#
# 明确不做：
#   - 不执行 `migrate force`（人工风险操作，脚本永不调用）；
#   - 不修改清单里的镜像版本（版本是 Git 里的事实，脚本只校验和部署）；
#   - 不删除 Job（迁移失败或超时都不自动删除，见 docs/development/kubernetes.md §10）；
#   - 不在迁移完成后提前释放发布锁（锁覆盖到「验证通过」为止）；
#   - 不使用本地 flock（不同 CI Runner 不共享文件系统，本地锁等于没有锁）；
#   - 不声称实现了强 fencing：Lease CAS 只能保护 Lease 对象，不能原子地保护 Deployment 写入。
#     **AUD-02 = NOT_CLOSED，AUD-05 = NOT_CLOSED**：已发出的请求无法撤销，read-after-write
#     只能提高可观测性（见 docs/design/deploy-lock.md §10）。
#
# 退出码（语义必须区分「请求未发出」「已发出但结果未知」「已确认成功」）：
#   0  发布成功且已验证（stdout 打印 DEPLOY_K8S_SUCCESS）
#   1  参数 / 预检 / 清单构建失败（**未发出任何变更请求**）；
#      或 Migration/Application apply 失败（**请求可能已经发出**，结果未知时额外打印
#      APPLICATION_OUTCOME_UNKNOWN）—— 不能把 1 统一解释为「资源没有被修改」
#   2  迁移失败（Job Failed=True 且 Pod 已终止；应用 Deployment 未更新）
#   3  发布锁失败（权限不足 / 获取超时 / 续租失败 / 租约已过期 / 服务端复核发现失锁）；
#      变更请求未发出，或在途时已发出但结果未知（此时打印 APPLICATION_OUTCOME_UNKNOWN）
#   4  版本降级保护拒绝（降级、分叉、当前或目标版本无法在 Git 历史中验证；未发出请求）
#   5  发布后验证失败（实际镜像/版本/副本与目标不一致，或未在超时内收敛）
#   6  迁移等待超时（Job 未确认终态，Pod 可能仍在执行 DDL；锁与关联记录保留）
#   7  迁移生命周期未确认/未终结（fail-closed：拒绝接管、拒绝开始新一轮迁移、Job 与记录不一致、
#      Job 已 Failed 但 Pod 仍在执行/终止中、缺少可校验的数据库证据）
#   143 收到 SIGTERM（按同一套生命周期规则处理：迁移未终结时不释放锁）
#
# 设计与剩余风险见 docs/design/deploy-lock.md；操作手册见 docs/development/kubernetes.md。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

APP_ENTRYPOINT="manifest/kubernetes/overlays/production"
MIGRATION_ENTRYPOINT="manifest/kubernetes/migration/overlays/production"
NAMESPACE_MANIFEST="manifest/kubernetes/shared/namespace.yaml"
IMAGE_REPOSITORY="docker.cnb.cool/framework-station/my-shop"
# 生产 overlay 中的占位版本；必须与 manifest 中的值一致（由 test/manifest 断言）。
PLACEHOLDER_TAG="0000000"

# 发布锁资源（T2-06.1）。Lease 不写进 Kustomize 入口：把锁放进部署清单意味着每次 apply
# 都可能把别的 Runner 的 holderIdentity 覆盖成空，等于绕过锁。
LOCK_NAME="surgecart-deploy-lock"
# 租期 45s（T2-06.1.3 / F-02 重新评估，不再是 30s）。
#
# 一次续租 attempt 实际执行**两个**请求（GET 读 + PUT 写），各受
# --lease-renew-timeout 约束；失败后退避重试，最多 attempts 次。最坏耗时：
#
#   renewal_budget = renew_interval
#                  + attempts × (get_timeout + replace_timeout)
#                  + total_backoff
#                  + scheduling_margin
#                  = 10 + 3×6 + 1 + 5 = 34s
#
# 30s 的租期在这个模型下**必然不成立**（34 > 30）：一次 API 抖动就可能让续租链
# 在锁过期之后才完成，表现为「自己已经被接管，却还在续租」。把租期放到 45s 后：
#   - 续租链最坏 29s 完成（10 + 18 + 1），比服务端过期早 16s；
#   - 留出 11s 余量吸收进程调度延迟与 renewTime 的秒级精度（公式要求 < 45）；
#   - 代价是「崩溃后接管延迟」由 ≤30s 变为 ≤45s（发布锁是分钟级操作，可接受）。
# 不通过缩小 --lease-renew-timeout 到 2s 换取 30s 租期：in-cluster 的 Lease GET/PUT
# 通常 <100ms，3s 已相当于 30 倍抖动余量；再收紧会把正常抖动误判成失锁。
LOCK_LEASE_DURATION=45
LOCK_RENEW_INTERVAL=10
# 续租专用请求超时：续租是最不能慢的一类请求，单独收紧（AUD-03）。
# GET 与 REPLACE **各自**受该超时约束（不是一个 attempt 只算一次）。
LOCK_RENEW_REQUEST_TIMEOUT=3
# 单次续租允许的尝试次数与退避时长（AUD-D7：重试的最坏耗时也必须计入租期预算）。
LOCK_RENEW_MAX_ATTEMPTS=3
# 相邻两次 attempt 之间的退避（毫秒）。这是退避时长的**唯一事实来源**：
#   - lease_renew 的真实 sleep 由它换算（不再硬编码 0.2s）；
#   - 退避预算 LOCK_RENEW_BACKOFF_BUDGET 由它推导，参数校验断言两者一致。
# 真实退避总时长 = (LOCK_RENEW_MAX_ATTEMPTS - 1) × LOCK_RENEW_BACKOFF_MS = 400ms。
# 只在 attempt **之间**退避：最后一次失败后不再 sleep —— 旧实现在 3 次失败后各 sleep
# 一次（共 600ms），与「预算 400ms」不一致（T2-06.1.4 / B-5：常量成了死常量）。
LOCK_RENEW_BACKOFF_MS=200
# 退避总耗时的保守上界（秒）：上面的真实值 400ms 向上取整为 1s。
# 参数校验会断言 LOCK_RENEW_BACKOFF_BUDGET == ceil((attempts-1)×BACKOFF_MS/1000)，
# 避免"校验用一份预算、实现用另一份退避"（B-5）。
LOCK_RENEW_BACKOFF_BUDGET=1
# 参数不变量中的安全余量：吸收单次续租请求的网络抖动与进程调度延迟。
LOCK_SAFETY_MARGIN=5
LOCK_WAIT_TIMEOUT=60

# 迁移 Job 选择器：必须与 migration/base/migration-job.yaml 的 metadata.labels 一致
# （发布前的「是否存在未终结迁移」扫描依赖它）。
MIGRATION_JOB_SELECTOR="app.kubernetes.io/component=database-migration"
# 迁移 Pod 选择器：与 migration-job.yaml 的 Pod 模板标签一致。用于「Job 已被删除但 Pod
# 仍在执行」的兜底扫描（AUD-D3/D4：仅靠 Job 对象无法证明 DDL 已经停止）。
MIGRATION_POD_SELECTOR="app.kubernetes.io/component=database-migration"

CONTEXT=""
# 脚本等待上限默认 = Job activeDeadlineSeconds(900) + 180s 余量（AUD-04/06）。
MIGRATION_TIMEOUT=1080
# 建议的时间余量：Job 被 kubelet 终止后，Pod 终止 + Job controller 写 Failed 条件需要时间。
MIGRATION_TIMEOUT_MARGIN=180
# apply 之后等待 Job 对象可见的上限（apply 成功但客户端超时的窗口）。
MIGRATION_APPLY_VISIBILITY_TIMEOUT=10
ROLLOUT_TIMEOUT=600
ALLOW_DOWNGRADE=0
POLL_INTERVAL=2
# 从迁移清单解析出的 Job 自身截止时间；0 表示清单未声明（此时不做时间关系校验）。
JOB_ACTIVE_DEADLINE=0

# Pod 选择器必须与 base/deployment.yaml 的 Pod 模板标签一致（test/manifest 断言，
# Service selector 用同一组标签，因此这里不会选到别的负载）。
POD_SELECTOR="app.kubernetes.io/name=surgecart,app.kubernetes.io/component=backend"
# 应用容器名：发布判定只认这个名字的容器（AUD-11：多容器 Pod 不能把 sidecar 的状态
# 或镜像当成应用容器）。
APP_CONTAINER_NAME="app"

# ---------------------------------------------------------------------------
# 迁移生命周期记录的数据契约（写在 Lease metadata.annotations 上，T2-06.1.2 重构）
#
# 当前记录（本轮/最近一轮）：
#   deploy-lock.surgecart.io/migration-state              NO_MIGRATION | MIGRATION_PENDING |
#                                                         MIGRATION_RUNNING | MIGRATION_COMPLETE |
#                                                         MIGRATION_FAILED
#   deploy-lock.surgecart.io/migration-job                本轮预期/实际的 Job 名称
#   deploy-lock.surgecart.io/migration-job-uid            真实 Job metadata.uid（apply 后回写）
#   deploy-lock.surgecart.io/migration-detail             失败原因/门禁结论等单行证据（≤200 字符）
#   deploy-lock.surgecart.io/migration-updated-at         当前记录最后一次写入时间
#   deploy-lock.surgecart.io/migration-terminal-at        当前记录被确认为终态的时间（未确认则空）
#   deploy-lock.surgecart.io/migration-recovery-method    终态判定方式：job-object（默认，缺省即空）|
#                                                         database-evidence（人工提供数据库证据）
#   deploy-lock.surgecart.io/migration-recovery-evidence  数据库证据原文：version=<N> dirty=<bool> latest=<N>
#
# 上一轮归档（只保留最近一轮，annotation 数量有界，不会无限增长）：
#   deploy-lock.surgecart.io/migration-previous-state / -job / -job-uid / -detail / -terminal-at
#
# 约束（T2-06.1.2 / AUD-D2）：
#   - 「当前记录」只允许由 migration_record_write 有意识地修改；create / takeover / renew /
#     release **必须**原样回写服务器上的记录（PUT 覆盖整个对象，漏写或写默认值等于丢记录）；
#   - 开始新一轮 Migration 之前，若当前记录仍指向上一轮，先把上一轮归档到 previous-*，
#     再写新的 PENDING（同一 次 CAS 完成，避免中间态丢证据）；
#   - state 为 MIGRATION_PENDING/RUNNING 时，记录**不允许**被清除，除非已用真实 Job 证明
#     「迁移没有发生过」（记录已写但本 Runner 从未发出 apply）；
#   - 终态记录缺少 job/uid 时**不能**仅凭 state 放行：必须有可校验的数据库证据（§AUD-D4）；
#   - 注解只是「上一轮 Runner 的陈述」，接管方必须用真实 Job 对象（以及其 Pod）交叉核验。
# ---------------------------------------------------------------------------
LEASE_ANN_PREFIX="deploy-lock.surgecart.io"
LEASE_ANN_STATE="${LEASE_ANN_PREFIX}/migration-state"
LEASE_ANN_JOB="${LEASE_ANN_PREFIX}/migration-job"
LEASE_ANN_JOB_UID="${LEASE_ANN_PREFIX}/migration-job-uid"
LEASE_ANN_DETAIL="${LEASE_ANN_PREFIX}/migration-detail"
LEASE_ANN_UPDATED_AT="${LEASE_ANN_PREFIX}/migration-updated-at"
LEASE_ANN_TERMINAL_AT="${LEASE_ANN_PREFIX}/migration-terminal-at"
LEASE_ANN_RECOVERY_METHOD="${LEASE_ANN_PREFIX}/migration-recovery-method"
LEASE_ANN_RECOVERY_EVIDENCE="${LEASE_ANN_PREFIX}/migration-recovery-evidence"
LEASE_ANN_PREV_STATE="${LEASE_ANN_PREFIX}/migration-previous-state"
LEASE_ANN_PREV_JOB="${LEASE_ANN_PREFIX}/migration-previous-job"
LEASE_ANN_PREV_JOB_UID="${LEASE_ANN_PREFIX}/migration-previous-job-uid"
LEASE_ANN_PREV_DETAIL="${LEASE_ANN_PREFIX}/migration-previous-detail"
LEASE_ANN_PREV_TERMINAL_AT="${LEASE_ANN_PREFIX}/migration-previous-terminal-at"

# 恢复方式：数据库证据（人工执行 `my-shop migrate version` 得到的只读事实）。
RECOVERY_METHOD_DATABASE="database-evidence"
# 证据必须是单行、可机器校验的固定格式（不接受自由文本，避免「只改 state 就假装验证成功」）。
#   version=<14位时间戳> dirty=<true|false> latest=<14位时间戳>
RECOVERY_EVIDENCE_PATTERN='^version=([0-9]{14}) dirty=(true|false) latest=([0-9]{14})$'

MIG_STATE_NONE="NO_MIGRATION"
MIG_STATE_PENDING="MIGRATION_PENDING"
MIG_STATE_RUNNING="MIGRATION_RUNNING"
MIG_STATE_COMPLETE="MIGRATION_COMPLETE"
MIG_STATE_FAILED="MIGRATION_FAILED"

# ---------------------------------------------------------------------------
# 结构化读取模板
#
# 为什么不用 `kubectl rollout status` 判成功（T2-06.1 审计结论）：
#   rollout status 只看「当前 ReplicaSet 是否可用」，无法区分「本次发布的版本」和
#   「另一个流水线刚推上去的版本」。发布成功判定必须自己比对镜像与版本标记（§8）。
#
# 为什么用 go-template 而不是 jsonpath：jsonpath 命中不存在的字段会**整条命令报错**，
# 而 Deployment.status.* 与 Lease.spec.* 里多数字段是 omitempty（未收敛/已释放时不存在）。
# go-template 的 index 对缺失键返回零值，缺失被明确折叠成空串，不会把「还没收敛」
# 误判成「读取失败」。
# ---------------------------------------------------------------------------

# Deployment 快照：generation ~ observedGeneration ~ spec.replicas ~ status.replicas ~
# status.updatedReplicas ~ status.availableReplicas ~ status.unavailableReplicas ~
# 容器 app 的镜像 ~ Pod 模板 app.kubernetes.io/version
DEPLOYMENT_STATE_TEMPLATE='{{index .metadata "generation"}}~'\
'{{if .status}}{{index .status "observedGeneration"}}{{else}}0{{end}}~'\
'{{index .spec "replicas"}}~'\
'{{if .status}}{{index .status "replicas"}}~{{index .status "updatedReplicas"}}~'\
'{{index .status "availableReplicas"}}~{{index .status "unavailableReplicas"}}{{else}}0~0~0~0{{end}}~'\
'{{range .spec.template.spec.containers}}{{if eq .name "app"}}{{.image}}{{end}}{{end}}~'\
'{{index .spec.template.metadata.labels "app.kubernetes.io/version"}}'

# Lease 快照：resourceVersion ~ uid ~ holderIdentity ~ acquireTime ~ renewTime ~
# leaseDurationSeconds ~ leaseTransitions ~
# 当前记录（state / job / job-uid / detail / updated-at / terminal-at /
#           recovery-method / recovery-evidence）~
# 上一轮归档（previous-state / previous-job / previous-job-uid / previous-detail /
#            previous-terminal-at）
LEASE_STATE_TEMPLATE='{{$ann := index .metadata "annotations"}}'\
'{{index .metadata "resourceVersion"}}~{{index .metadata "uid"}}~'\
'{{index .spec "holderIdentity"}}~{{index .spec "acquireTime"}}~{{index .spec "renewTime"}}~'\
'{{index .spec "leaseDurationSeconds"}}~{{index .spec "leaseTransitions"}}~'\
'{{if $ann}}{{index $ann "deploy-lock.surgecart.io/migration-state"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-job"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-job-uid"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-detail"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-updated-at"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-terminal-at"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-recovery-method"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-recovery-evidence"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-previous-state"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-previous-job"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-previous-job-uid"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-previous-detail"}}~'\
'{{index $ann "deploy-lock.surgecart.io/migration-previous-terminal-at"}}{{end}}'

# Job 快照：uid ~ deletionTimestamp ~ Complete.status ~ Failed.status ~ Failed.reason ~
# Failed.message（Complete/Failed 都可能不存在，用条件遍历而不是 jsonpath）
JOB_STATE_TEMPLATE='{{$complete := ""}}{{$failed := ""}}{{$reason := ""}}{{$message := ""}}'\
'{{index .metadata "uid"}}~{{index .metadata "deletionTimestamp"}}~'\
'{{if .status}}{{range .status.conditions}}'\
'{{if eq .type "Complete"}}{{$complete = .status}}{{end}}'\
'{{if eq .type "Failed"}}{{$failed = .status}}{{$reason = .reason}}{{$message = .message}}{{end}}'\
'{{end}}{{end}}'\
'{{$complete}}~{{$failed}}~{{$reason}}~{{$message}}'

# Job 列表（每行一个）：name ~ uid ~ deletionTimestamp ~ Complete.status ~ Failed.status
JOBS_STATE_TEMPLATE='{{range .items}}'\
'{{$complete := ""}}{{$failed := ""}}'\
'{{if .status}}{{range .status.conditions}}'\
'{{if eq .type "Complete"}}{{$complete = .status}}{{end}}'\
'{{if eq .type "Failed"}}{{$failed = .status}}{{end}}'\
'{{end}}{{end}}'\
'{{index .metadata "name"}}~{{index .metadata "uid"}}~{{index .metadata "deletionTimestamp"}}~'\
'{{$complete}}~{{$failed}}{{"\n"}}'\
'{{end}}'

# 一行一个 Pod：name ~ phase ~ deletionTimestamp ~ app.kubernetes.io/version ~
# app 容器 ready（true/false/missing）~ app 容器镜像
#
# AUD-11：只取名为 app 的容器。原实现把所有 containerStatuses 的 ready 拼在一起、
# 取 containers[0] 的镜像，sidecar 或 initContainer 变化都会让判定失真。
PODS_STATE_TEMPLATE='{{range .items}}'\
'{{$ready := "missing"}}{{$image := ""}}'\
'{{if .status}}{{range .status.containerStatuses}}'\
'{{if eq .name "app"}}{{$ready = printf "%v" .ready}}{{end}}{{end}}{{end}}'\
'{{range .spec.containers}}{{if eq .name "app"}}{{$image = .image}}{{end}}{{end}}'\
'{{index .metadata "name"}}~{{index .status "phase"}}~{{index .metadata "deletionTimestamp"}}~'\
'{{index .metadata.labels "app.kubernetes.io/version"}}~{{$ready}}~{{$image}}{{"\n"}}'\
'{{end}}'

# 迁移 Pod 快照（T2-06.1.2 / AUD-D3）：一行一个 Pod
#   name ~ phase ~ deletionTimestamp ~ ownerRef.kind ~ ownerRef.uid
#
# 为什么必须读 ownerReferences：`job-name` 标签只是「声明」，同名 Job 被删除重建、
# 手工创建的 Pod、或 TTL 清理后残留的孤儿 Pod 都可能带着同样的标签。只有
# ownerReferences[].uid 与目标 Job 的 metadata.uid 一致，才能证明「这个 Pod 在执行该 Job」。
MIGRATION_PODS_TEMPLATE='{{range .items}}'\
'{{$kind := ""}}{{$owner := ""}}'\
'{{range .metadata.ownerReferences}}{{if eq .kind "Job"}}{{$kind = .kind}}{{$owner = .uid}}{{end}}{{end}}'\
'{{index .metadata "name"}}~{{index .status "phase"}}~{{index .metadata "deletionTimestamp"}}~'\
'{{$kind}}~{{$owner}}{{"\n"}}'\
'{{end}}'

log_info()  { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }
log_warn()  { printf '\033[33m[WARN]\033[0m %s\n' "$*"; }
log_error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; }

# die [退出码] <消息...>
die() {
  local code=1
  if [[ "${1:-}" =~ ^[0-9]+$ ]]; then
    code="$1"
    shift
  fi
  log_error "$*"
  exit "${code}"
}

usage() {
  cat <<'EOF'
用法：bash scripts/deploy-k8s.sh [选项]

发布顺序：获取发布锁（Lease）→ 版本降级保护 → 数据库迁移（独立 Job）→
应用 Deployment → 发布成功判定 → 释放发布锁。任一步失败立即非零退出。

选项：
  --context <name>              kubeconfig context（默认使用当前 context）
  --migration-timeout <sec>     等待迁移 Job 进入终态的上限（默认 1080）
                                必须大于 Job 的 activeDeadlineSeconds（默认 900）
  --rollout-timeout <sec>       等待 Deployment 收敛的上限（默认 600）
  --lock-wait-timeout <sec>     等待其他发布释放锁的上限（默认 60）
  --lease-duration <sec>        发布锁租期（默认 45；崩溃后最多这么久即可被接管）
  --lease-renew-interval <sec>  发布锁续租间隔（默认 10）
  --lease-renew-timeout <sec>   单次续租请求的超时（默认 3；GET 与 REPLACE 各算一次）
  --allow-downgrade             允许把 Deployment 降级到更旧的 commit（默认禁止）
  -h, --help                    显示帮助

租期参数必须满足（AUD-03 / AUD-D7，续租链的最坏耗时也计入预算）：
  renew_interval + attempts(3) × (get_timeout + replace_timeout) + backoff(1) + safety_margin(5)
    < lease_duration
默认值：10 + 3×(3+3) + 1 + 5 = 34 < 45。不满足时脚本在任何资源变更之前拒绝执行
（退出码 1，且不产生任何 kubectl 资源调用）。

退出码：
  0 成功（打印 DEPLOY_K8S_SUCCESS）；
  1 参数/预检/清单构建失败（未发出请求）**或** apply 失败（请求可能已发出、结果未知，
    此时额外打印 APPLICATION_OUTCOME_UNKNOWN）—— 1 不等于「资源一定没有被修改」；
  2 迁移失败；3 发布锁失败；4 版本降级保护拒绝；5 发布后验证失败；6 迁移等待超时；
  7 迁移生命周期未确认/未终结（fail-closed：拒绝接管、Job 与记录不一致、
    Job 已 Failed 但 Pod 仍在执行、无法确认 Pod 状态）。

所需 RBAC（完整清单见 manifest/kubernetes/examples/deploy-lock/rbac.yaml，
逐项事实来源是 scripts/deploy-k8s.sh 的 REQUIRED_PERMISSIONS）：
权限预检在**任何写请求之前**执行；缺少任何一项都以退出码 3 失败，且不发起工作负载变更。

  命名空间级（Role + RoleBinding，命名空间取自 Application 清单）
    leases.coordination.k8s.io   get / create / update
                                 （读锁 / 原子创建 / PUT+CAS 续租·接管·释放。
                                   get 与 update 在清单里被 resourceNames 限定到
                                   Lease surgecart-deploy-lock，预检必须用对象级写法：
                                   auth can-i get leases.coordination.k8s.io/surgecart-deploy-lock。
                                   create 不能带对象名 —— resourceNames 对 create 无效）
    jobs.batch                   get / list / create / patch（核验上一轮 / 创建本轮迁移 Job）
    pods                         get / list              （Pod 生命周期门禁）
    pods/log                     get                     （迁移失败时读 Job 日志）
    secrets                      get                     （锁内复检 Secret 是否存在）
    deployments.apps             get / list / create / patch（降级保护 / 汇总 / apply）
    configmaps                   get / create / patch    （apply Application 清单）
    services                     get / list / create / patch
    persistentvolumeclaims       get / create / patch    （apply Application 清单）

  集群级（**必须** ClusterRole + ClusterRoleBinding；Role 无法授予，这是审计 A-1 的根因）
    namespaces                   get / create            （首次部署引导命名空间）
    selfsubjectaccessreviews.authorization.k8s.io  create （`kubectl auth can-i` 自身是 SSAR）

  不授予 delete/deletecollection：脚本永不删除 Job / Pod / Lease / Deployment。

  对象级预检（T2-06.1.5 / F-01）：
    RBAC 的 resourceNames 只对「带对象名的请求」生效。因此受它限制的权限必须用
      kubectl auth can-i <verb> <type>/<name>
    检查；不带对象名的 can-i 在这类规则上恒为 no（真实集群会直接判定无权限）。
    - 只有 get/update/patch/delete 能带对象名；create 的对象名在授权时不可知，
      list/watch/deletecollection 是集合请求，都不能带；
    - 子资源只能用 --subresource 表达：kubectl auth can-i get pods --subresource=log。
      位置参数 `pods/log` 会被 kubectl 解析成「名为 log 的 Pod」，不是子资源。
    - 需要 kubectl >= 1.24（--subresource）。

前置条件：
  - 已安装 kubectl，且 kubeconfig 指向目标集群；
  - 已按 manifest/kubernetes/examples/secrets/ 创建 Secret；
  - 两个 overlay 的镜像版本 / Job 名称后缀 / app.kubernetes.io/version 已替换为同一个
    真实短 SHA，REPLACE_WITH_* 占位符已全部替换；
  - 本地 Git 历史包含当前线上版本与目标版本（浅克隆需 git fetch --unshallow），
    否则版本顺序无法验证，脚本会按 fail-closed 拒绝发布。

--allow-downgrade 是人工应急开关：默认关闭，CI 环境（CI=true）下必须额外设置
DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1 才生效。降级可能让旧代码读到新 schema/新数据。

完整流程与故障处理见 docs/development/kubernetes.md；
发布锁的并发语义与剩余风险见 docs/design/deploy-lock.md。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context)              CONTEXT="${2:?--context 需要参数}"; shift 2 ;;
    --migration-timeout)    MIGRATION_TIMEOUT="${2:?--migration-timeout 需要参数}"; shift 2 ;;
    --rollout-timeout)      ROLLOUT_TIMEOUT="${2:?--rollout-timeout 需要参数}"; shift 2 ;;
    --lock-wait-timeout)    LOCK_WAIT_TIMEOUT="${2:?--lock-wait-timeout 需要参数}"; shift 2 ;;
    --lease-duration)       LOCK_LEASE_DURATION="${2:?--lease-duration 需要参数}"; shift 2 ;;
    --lease-renew-interval) LOCK_RENEW_INTERVAL="${2:?--lease-renew-interval 需要参数}"; shift 2 ;;
    --lease-renew-timeout)  LOCK_RENEW_REQUEST_TIMEOUT="${2:?--lease-renew-timeout 需要参数}"; shift 2 ;;
    --allow-downgrade)      ALLOW_DOWNGRADE=1; shift ;;
    -h|--help)              usage; exit 0 ;;
    *)                      die "未知参数: $1（使用 --help 查看用法）" ;;
  esac
done

for value in "${MIGRATION_TIMEOUT}" "${ROLLOUT_TIMEOUT}" "${LOCK_WAIT_TIMEOUT}" \
             "${LOCK_LEASE_DURATION}" "${LOCK_RENEW_INTERVAL}" "${LOCK_RENEW_REQUEST_TIMEOUT}"; do
  [[ "${value}" =~ ^[0-9]+$ ]] || die 1 "超时/租期必须是非负整数，实际: ${value}"
done
(( LOCK_LEASE_DURATION > 0 )) || die 1 "--lease-duration 必须大于 0"
(( LOCK_RENEW_INTERVAL > 0 )) || die 1 "--lease-renew-interval 必须大于 0"
(( LOCK_RENEW_REQUEST_TIMEOUT > 0 )) || die 1 "--lease-renew-timeout 必须大于 0"

# AUD-03 / AUD-D7 / T2-06.1.3-F02：租期预算不变量。
#
# 一次续租 attempt 实际执行 **两个** 请求（以 lease_renew 的真实源码路径为准）：
#
#   GET Lease（读 rv / holder / renewTime）→ 校验 holder+uid → PUT Lease（CAS 写 renewTime）
#
# 两个请求各自受 --lease-renew-timeout 约束，CAS 冲突/API 错误后最多重试
# attempts 次、期间有 (attempts-1) 次退避。因此最坏耗时是：
#
#   renewal_budget = renew_interval
#                  + attempts × (get_timeout + replace_timeout)
#                  + total_backoff
#                  + scheduling_margin
#
# 旧公式 attempts × 单次 timeout 漏掉了每次 attempt 的 GET，低估 attempts × timeout
# （默认 9s）：默认参数下 10 + 3×3 + 1 + 5 = 25 < 30 看似安全，真实最坏值却是
# 10 + 3×(3+3) + 1 + 5 = 34 > 30 —— 续租链会在锁过期之后才结束。
#
# 该不变量表达的是「单条续租链必须在服务端租期到期之前完成」，所以必须严格小于
# lease_duration（相等也不行：没有留给下一次续租的窗口）。它只能降低「正常网络延迟
# 下锁提前过期」的概率，**不能**消除 Runner 暂停、网络分区与缺少 fencing 的问题
# （见 docs/design/deploy-lock.md §7 / §10）。
if (( LOCK_RENEW_MAX_ATTEMPTS < 1 )); then
  die 1 "内部参数错误：LOCK_RENEW_MAX_ATTEMPTS(${LOCK_RENEW_MAX_ATTEMPTS}) 必须 >= 1"
fi
lock_backoff_actual_ms=$(( (LOCK_RENEW_MAX_ATTEMPTS - 1) * LOCK_RENEW_BACKOFF_MS ))
# 预算必须**等于**向上取整后的真实退避（不是"不小于"就够）：
# 只写"不小于"会允许实现与校验各用一套数字 —— 旧实现正是这样变成死常量的
# （校验 400ms、代码里 3 次 sleep×0.2s=600ms，B-5）。
lock_backoff_budget_min=$(( (lock_backoff_actual_ms + 999) / 1000 ))
if (( LOCK_RENEW_BACKOFF_BUDGET != lock_backoff_budget_min )); then
  die 1 "内部参数错误：退避预算 ${LOCK_RENEW_BACKOFF_BUDGET}s 与真实退避 (${LOCK_RENEW_MAX_ATTEMPTS}-1)×${LOCK_RENEW_BACKOFF_MS}ms=${lock_backoff_actual_ms}ms 不一致（应向上取整为 ${lock_backoff_budget_min}s）；预算与实现必须同源，否则最坏耗时会被低估"
fi
# 每个 attempt 的 GET / REPLACE 各自使用同一个 --lease-renew-timeout（LEASE_KUBECTL）。
lock_get_timeout="${LOCK_RENEW_REQUEST_TIMEOUT}"
lock_replace_timeout="${LOCK_RENEW_REQUEST_TIMEOUT}"
lock_attempt_budget=$(( lock_get_timeout + lock_replace_timeout ))
lock_chain_budget=$(( LOCK_RENEW_MAX_ATTEMPTS * lock_attempt_budget + LOCK_RENEW_BACKOFF_BUDGET ))
lock_budget=$(( LOCK_RENEW_INTERVAL + lock_chain_budget + LOCK_SAFETY_MARGIN ))
if (( lock_budget >= LOCK_LEASE_DURATION )); then
  die 1 "租期参数不合法：renew_interval(${LOCK_RENEW_INTERVAL}) + attempts(${LOCK_RENEW_MAX_ATTEMPTS})×(get_timeout(${lock_get_timeout}) + replace_timeout(${lock_replace_timeout})) + backoff(${LOCK_RENEW_BACKOFF_BUDGET}) + safety_margin(${LOCK_SAFETY_MARGIN}) = ${lock_budget} 必须严格小于 lease_duration(${LOCK_LEASE_DURATION})；否则一次续租失败后的重试链会让锁先过期、被其他 Runner 接管（续租链最坏 ${lock_chain_budget}s，必须在 ${LOCK_LEASE_DURATION}s 内完成）"
fi
log_info "租期参数校验通过：${LOCK_RENEW_INTERVAL} + ${LOCK_RENEW_MAX_ATTEMPTS}×(${lock_get_timeout}+${lock_replace_timeout}) + ${LOCK_RENEW_BACKOFF_BUDGET} + ${LOCK_SAFETY_MARGIN} = ${lock_budget} < ${LOCK_LEASE_DURATION}（单次 attempt 最坏 ${lock_attempt_budget}s，续租链最坏 $(( LOCK_RENEW_INTERVAL + lock_chain_budget ))s，真实退避 ${lock_backoff_actual_ms}ms=(${LOCK_RENEW_MAX_ATTEMPTS}-1)×${LOCK_RENEW_BACKOFF_MS}ms）"

# CI 不允许「默认携带」降级授权：流水线必须显式再加一层开关，避免回滚开关被写进流水线后长期生效。
is_ci_env() {
  [[ "${CI:-}" =~ ^(1|true|TRUE|True)$ ]]
}
if (( ALLOW_DOWNGRADE == 1 )) && is_ci_env && [[ "${DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI:-}" != "1" ]]; then
  die 4 "CI 环境禁止自动降级：--allow-downgrade 在 CI 中需要额外设置 DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1（人工应急）"
fi

KUBECTL=(kubectl --request-timeout=30s)
# 续租/迁移记录写入使用专用请求超时：与租期参数不变量配套（AUD-03）。
LEASE_KUBECTL=(kubectl --request-timeout="${LOCK_RENEW_REQUEST_TIMEOUT}s")
if [[ -n "${CONTEXT}" ]]; then
  KUBECTL+=(--context "${CONTEXT}")
  LEASE_KUBECTL+=(--context "${CONTEXT}")
fi

cd "${ROOT_DIR}"

# ---------------------------------------------------------------------------
# 运行时状态与清理
# ---------------------------------------------------------------------------
command -v kubectl >/dev/null 2>&1 || die "未安装 kubectl：无法构建 Kustomize 清单或部署集群"

TMP_DIR="$(mktemp -d)"
LOCK_STATE_DIR="${TMP_DIR}/lock"
mkdir -p "${LOCK_STATE_DIR}"

LOCK_HELD=0
LOCK_UID=""
RENEWER_PID=""
DOWNGRADE_AUTHORIZED=0
# Application apply 的在途标记：失锁/信号路径据此输出 APPLICATION_OUTCOME_UNKNOWN。
APPLICATION_INFLIGHT=0
APPLICATION_APPLY_ISSUED=0
# apply 是否**已经返回**（无论退出码）、其结果是否**已经核实**（verify_release 通过）。
# B-11：apply 返回后、核实前的信号必须落到「结果未知」，既不能说"已成功"，
# 也不能说"未应用"（客户端的返回码不能证明资源现状）。
APPLICATION_APPLY_RETURNED=0
APPLICATION_OUTCOME_VERIFIED=0
# 迁移 apply 的结果是否只能标记为未知（D-1/D-2/B-13）。
MIGRATION_OUTCOME_UNKNOWN=0
HOLDER_IDENTITY="deploy-$(hostname 2>/dev/null || echo unknown)-$$-$(date -u +%Y%m%dT%H%M%SZ)"
# holderIdentity 只保留 DNS 子集字符：避免影响锁记录解析与可读性。
HOLDER_IDENTITY="$(printf '%s' "${HOLDER_IDENTITY}" | tr -c 'A-Za-z0-9._-' '-')"

# ---------------------------------------------------------------------------
# 时间语义（T2-06.1.4 / B-1）：Lease 的 renewTime 到底该取哪个时刻
#
# 三个时刻必须分开，混用会把「客户端发起请求之前」的时刻写成服务端 renewTime：
#   t_req  ：客户端构造并发出 Create/PUT 请求的时刻（本地时钟）
#   t_acc  ：API Server 接受并写入对象的时刻（服务端时钟，**客户端不可观测**）
#   t_resp ：客户端收到响应的时刻（本地时钟）
#
# 正常时钟下 t_req <= t_acc <= t_resp；但客户端时钟可能偏快或偏慢，所以：
#   - 不能把「比 t_req 更早的本地时刻」当 renewTime（旧实现用脚本启动时刻
#     LOCK_ACQUIRED_AT，实测 preflight 51s 时，Lease 一创建就已过期 —— B-1）；
#   - t_acc 不可观测，因此**唯一诚实**的取值是「本 Runner 观测到的、不早于写入
#     完成」的时刻：创建时先写 t_req，create 返回后立即续租一次，把 renewTime
#     推到这个写入完成之后（t_resp 之后）的时刻（B-2）；
#   - 客户端时钟偏移无法消除：本机时钟比服务器慢 → 服务器认为租约更早过期（保守），
#     比服务器快 → 服务器认为租约活得更久（**不安全方向**）。这一项如实记为
#     NOT_VERIFIED，见 reports/agent-b-done.md 的「时间语义」一节。
# ---------------------------------------------------------------------------
LOCK_ACQUIRED_AT=""          # 仅作 acquireTime 的回退；由 create/takeover 的**服务端观测值**填充
LOCK_REQUEST_ISSUED_AT=""    # t_req：最近一次 Lease 写入请求发出前的本地时刻
LOCK_WRITE_CONFIRMED_AT=""   # t_resp：最近一次成功写入返回后的本地时刻

# 迁移生命周期状态（内存镜像；写入 Lease 注解后才对其他 Runner 可见）。
MIG_STATE="${MIG_STATE_NONE}"
MIG_JOB=""
MIG_JOB_UID=""
MIG_DETAIL=""
# 记录是否已成功写入 Lease（用于 cleanup 判断能否安全复位）。
MIG_RECORD_WRITTEN=0
# 本 Runner 是否真的发出过 apply（决定 cleanup 能否释放锁）。
MIG_APPLY_ISSUED=0
# 关联的 Job 是否已用真实对象确认为终态（Complete/Failed **且 Pod 已确认不再执行**）。
MIG_TERMINAL=0

# 写入 Lease 时的「记录快照」：lease_manifest 只读 REC_*，从不直接读 MIG_*/LEASE_*。
# 这样「保留服务器记录」与「有意识更新记录」是两条显式的代码路径，不会因为漏写某个
# 变量而把上一个 Runner 的证据覆盖成默认值（AUD-D2）。
REC_STATE=""
REC_JOB=""
REC_JOB_UID=""
REC_DETAIL=""
REC_UPDATED_AT=""
REC_TERMINAL_AT=""
REC_RECOVERY_METHOD=""
REC_RECOVERY_EVIDENCE=""
REC_PREV_STATE=""
REC_PREV_JOB=""
REC_PREV_JOB_UID=""
REC_PREV_DETAIL=""
REC_PREV_TERMINAL_AT=""

# 接管核验的结果：接管成功后据此把上一轮记录收敛为终态（不让它停在 PENDING/RUNNING）。
#   ""                         无需处理（记录已经是终态，或本来就是 NO_MIGRATION）
#   MIGRATION_COMPLETE/FAILED  用真实 Job 对象确认过终态
#   RESET                      用数据库证据确认「上一轮没有完成」→ 归档并复位 NO_MIGRATION
PREV_FINAL_STATE=""
PREV_FINAL_DETAIL=""
PREV_FINAL_METHOD=""
PREV_FINAL_EVIDENCE=""

JOB_PROBE_STATE=""
JOB_PROBE_ERROR=""
JOB_UID=""
JOB_COMPLETE=""
JOB_FAILED=""
JOB_REASON=""
JOB_MESSAGE=""
MIGRATION_LIST_ERROR=""
# MIGRATION_LIST_CLASS：查询失败的分类（forbidden/api/timeout/...），
# MIGRATION_LIST_ROWS ：列表结果。两者与 MIGRATION_LIST_ERROR 一样**必须**通过全局
# 变量返回：`rows="$(job_list_migrations)"` 会开子 shell，错误文本与分类都会丢失。
MIGRATION_LIST_CLASS=""
MIGRATION_LIST_ROWS=""
# Pod 读取结果通过全局变量返回（避免命令替换开子 shell 丢掉错误文本）：
#   JOB_PODS_ROWS / JOB_PODS_ERROR            —— read_job_pods（本轮 Job 的 Pod 快照）
#   MIGRATION_PODS_ROWS / MIGRATION_PODS_ERROR —— read_migration_pods_for_job / scan_migration_pods
JOB_PODS_ROWS=""
JOB_PODS_ERROR=""
MIGRATION_PODS_ROWS=""

# Pod 生命周期门禁的结果（AUD-D3）：0 = 允许（没有仍在执行的 Pod）。
POD_GATE_CODE=0
POD_GATE_SUMMARY=""
MIGRATION_PODS_ERROR=""

# 清理阶段标记与「清理期间收到的信号」的延后退出码（T2-06.1.4 加固）。
#
# 代码依据（真实调用链 + 实验证据，见 test/deployk8s/multiprocess_test.go
# TestSignalsDuringCleanupDoNotCorruptCleanup）：
#
#   SIGTERM/SIGINT → on_signal() → exit → EXIT trap = cleanup()
#   cleanup → stop_lease_renewer → [migration_record_write] → lease_release
#           → rm -rf TMP_DIR → exit code
#
#   cleanup 执行到一半（例如正阻塞在 lease_release 的 kubectl replace 上）时若再收到
#   信号，bash 会在该子进程返回后的**下一个命令边界**执行信号 trap，即重入
#   on_signal；而 on_signal 的每条分支都以 exit 结束，于是 cleanup 被中途截断：
#   锁可能不释放、TMP_DIR 一定不删除。实测证据（T2-06.1.4）：
#   stderr 在同一进程里先后出现完整的「收到 SIGTERM」「收到 SIGINT」两轮处理，
#   且 TMP_DIR 残留。极端时序下 bash 还会在 EXIT trap 内求值信号 trap 时以 2 退出
#   （`trap: line 2: unexpected EOF while looking for matching )`），把既有的
#   3（失锁）/143（信号）覆盖掉 —— 这正是集成期间观察到 2 次、未复现的失败形态。
#
# 因此：CLEANUP_ACTIVE=1 期间，信号只记录意图、不再调用 exit，由 cleanup 收尾时
# 决定最终退出码。lease_release 本身已经幂等（入口 LOCK_HELD 守卫 + 每条分支置 0），
# 所以这里不需要再引入「是否已释放」的额外状态。
CLEANUP_ACTIVE=0
# 清理期间收到的信号要采用的退出码；0 = 未收到。
# 只有 cleanup 原本要以 0 退出时才采用它：非 0 的既有结论（失锁 3、失败 1/2/5/6/7）
# 信息量更大，不允许被信号覆盖。
DEFERRED_SIGNAL_CODE=0

cleanup() {
  local code=$?
  CLEANUP_ACTIVE=1
  # 清理路径不允许再触发 set -e 退出：这里必须尽最大努力释放锁并删除临时文件。
  set +e
  # 续租进程只有在 start_lease_renewer 之后才存在，此时 stop_lease_renewer 已定义。
  if [[ -n "${RENEWER_PID}" ]]; then
    stop_lease_renewer || true
  fi
  if (( LOCK_HELD )); then
    # 记录已写入但本 Runner 从未 apply：可以安全复位（没有 Job 需要被追踪）。
    if (( MIG_RECORD_WRITTEN )) && (( ! MIG_APPLY_ISSUED )); then
      log_warn "清理：迁移记录已写入但未发出 apply，复位为 ${MIG_STATE_NONE}"
      migration_record_write "${MIG_STATE_NONE}" "" "" "" || true
    fi
    lease_release || true
  fi
  if (( MIG_APPLY_ISSUED )) && (( ! MIG_TERMINAL )); then
    log_warn "清理：迁移 Job/${MIG_JOB:-<未知>} 未确认终态（state=${MIG_STATE}），关联记录保留在 Lease 注解上"
  fi
  rm -rf "${TMP_DIR}"
  # 清理期间到达的信号（on_signal 已延后处理）：清理已经完成，这里才决定退出码。
  if (( DEFERRED_SIGNAL_CODE != 0 )); then
    if (( code == 0 )); then
      log_warn "清理期间收到信号：清理已完成（锁已按规则释放、TMP_DIR 已删除），退出码 0 → ${DEFERRED_SIGNAL_CODE}"
      code="${DEFERRED_SIGNAL_CODE}"
    else
      log_warn "清理期间收到信号：清理已完成，保留既有退出码 ${code}（不被 ${DEFERRED_SIGNAL_CODE} 覆盖）"
    fi
  fi
  exit "${code}"
}

# 信号路径的**结果确定性**（T2-06.1.4 / B-11）。
#
# 三条互斥的结论，任何一条信号都必须落到其中一条，不允许含糊：
#   1) 结果未知 → 打印 APPLICATION_OUTCOME_UNKNOWN（apply 在途，或 apply 已返回但
#      **尚未核实**）。此时既不能打印"已成功"，也不能打印"未应用"：
#      - 客户端返回 0 不代表对象都写成功了（清单里可能有多个对象）；
#      - 客户端返回非 0 也不代表没写成功（请求可能已被 API Server 接受）。
#   2) 已核实生效 → 打印 APPLICATION_OUTCOME_VERIFIED（verify_release 已经通过，
#      结论来自集群事实，不是本次请求的返回码）。
#   3) 明确未开始应用 → 打印 APPLICATION_NOT_STARTED（本次从未发出 apply）。
#
# 注意：这里的「未知」不是保守话术，而是**事实**——已经发出的请求无法撤销
# （AUD-02 = NOT_CLOSED）。
on_signal() {
  local name="$1"
  # 重入保护（见 CLEANUP_ACTIVE 注释）：cleanup 正在释放锁 / 删除临时文件，
  # 此时**不能**调用 exit —— 那会截断清理，并可能在 EXIT trap 内求值信号 trap
  # 时失败（bash 以 2 退出，覆盖既有的 3/143）。只记录信号意图，由 cleanup 采用。
  if (( CLEANUP_ACTIVE )); then
    if (( DEFERRED_SIGNAL_CODE == 0 )); then
      DEFERRED_SIGNAL_CODE=143
    fi
    log_warn "清理进行中：已记录 SIG${name}，不在清理阶段调用 exit（不中断释放锁/删除临时文件；清理完成后按既有退出码或 ${DEFERRED_SIGNAL_CODE} 结束）"
    return 0
  fi
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    log_error "收到 SIG${name} 且发布锁续租已失败：中止发布，不再变更任何资源"
    if (( APPLICATION_INFLIGHT )) || { (( APPLICATION_APPLY_ISSUED )) && (( ! APPLICATION_OUTCOME_VERIFIED )); }; then
      # 已经发出的请求无法撤销：客户端中止 ≠ 服务端没有生效（AUD-02/AUD-05）。
      printf 'APPLICATION_OUTCOME_UNKNOWN\n'
      log_error "APPLICATION_OUTCOME_UNKNOWN: 失锁时 Application apply 在途或尚未核实，该请求可能已被 API Server 接受并生效。"
      log_error "  本 Runner 不会（也不能）撤销它；请按输出核对 Deployment 实际状态后再决定下一步。"
    fi
    if declare -F report_lock_lost >/dev/null; then
      report_lock_lost
    fi
    exit 3
  fi
  if (( MIG_APPLY_ISSUED )) && (( ! MIG_TERMINAL )); then
    log_error "收到 SIG${name}：中止发布。迁移 Job/${MIG_JOB:-<未知>} 未确认终态 → **不释放发布锁**"
    log_error "  关联记录（state=${MIG_STATE}）保留在 Lease 注解上；锁按租期（${LOCK_LEASE_DURATION}s）自然过期，"
    log_error "  后续 Runner 会先核验该 Job 的真实状态，不会直接开始新一轮迁移。"
    if (( MIGRATION_OUTCOME_UNKNOWN )) || (( APPLICATION_INFLIGHT )); then
      printf 'APPLICATION_OUTCOME_UNKNOWN\n'
      log_error "APPLICATION_OUTCOME_UNKNOWN: 收到 SIG${name} 时迁移/应用请求在途，结果未核实（不得据此断言未应用）。"
    fi
    exit 143
  fi
  if (( APPLICATION_INFLIGHT )); then
    printf 'APPLICATION_OUTCOME_UNKNOWN\n'
    log_error "APPLICATION_OUTCOME_UNKNOWN: 收到 SIG${name} 时 Application apply 仍在途，请求可能已被 API Server 接受并生效。"
  elif (( APPLICATION_APPLY_ISSUED )) && (( APPLICATION_OUTCOME_VERIFIED )); then
    # 结论来自集群事实（verify_release 已经通过），不是"最后一次请求成功了"。
    printf 'APPLICATION_OUTCOME_VERIFIED\n'
    log_error "收到 SIG${name}：发布**已验证通过**但尚未释放锁；已应用的资源保持现状（不需要重跑）。"
  elif (( APPLICATION_APPLY_RETURNED )) && (( ! APPLICATION_OUTCOME_VERIFIED )); then
    # B-11 的核心场景：apply 已返回，但结果还没核实。
    printf 'APPLICATION_OUTCOME_UNKNOWN\n'
    log_error "APPLICATION_OUTCOME_UNKNOWN: 收到 SIG${name} 时 Application apply 已返回但**结果尚未核实**。"
    log_error "  不能声称「发布成功」（未验证），也不能声称「应用未更新」（请求可能已生效且无法撤销）。"
    log_error "  请核对：${KUBECTL[*]} -n ${NAMESPACE} get deployment/${DEPLOYMENT_NAME} -o yaml"
  elif (( MIG_APPLY_ISSUED )) && (( ! MIG_TERMINAL )) && (( MIGRATION_OUTCOME_UNKNOWN )); then
    printf 'APPLICATION_NOT_STARTED\n'
    log_error "收到 SIG${name}：迁移 apply 的结果未知（不得断言未应用），本次未开始应用更新。"
  else
    printf 'APPLICATION_NOT_STARTED\n'
    log_error "收到 SIG${name}：本次尚未发出 Application apply（应用清单未被本 Runner 应用）。"
  fi
  log_error "收到 SIG${name}：中止发布，释放发布锁（已应用的资源保持现状）"
  exit 143
}

trap cleanup EXIT
trap 'on_signal TERM' TERM
trap 'on_signal INT' INT
trap 'on_signal HUP' HUP

# ---------------------------------------------------------------------------
# 通用工具
# ---------------------------------------------------------------------------
timestamp_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# 把 RFC3339（含小数秒、可带 Z）转成 epoch 秒；无法解析时返回非零。
epoch_of_rfc3339() {
  local value="$1" base epoch
  [[ -n "${value}" ]] || return 1
  if epoch="$(date -u -d "${value}" +%s 2>/dev/null)"; then
    printf '%s' "${epoch}"
    return 0
  fi
  base="${value%Z}"
  base="${base%%.*}"
  if epoch="$(date -u -j -f '%Y-%m-%dT%H:%M:%S' "${base}" +%s 2>/dev/null)"; then
    printf '%s' "${epoch}"
    return 0
  fi
  return 1
}

# epoch 秒 → RFC3339（UTC，秒级）。时间语义归 B（见 COMMON_RULES §5）：A 只调用不实现。
rfc3339_from_epoch() {
  local epoch="$1"
  [[ "${epoch}" =~ ^[0-9]+$ ]] || return 1
  date -u -d "@${epoch}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || return 1
}

# 毫秒 → `sleep` 参数（秒，三位小数）。退避时长只能有一个数值来源
# （LOCK_RENEW_BACKOFF_MS），否则常量会退化成"只用于校验"的死常量（B-5）。
sleep_ms_arg() {
  local ms="$1"
  [[ "${ms}" =~ ^[0-9]+$ ]] || ms=0
  printf '%d.%03d' $(( ms / 1000 )) $(( ms % 1000 ))
}

# 非数字 / 空值统一折叠成 0：用于 status 计数比较。
to_int() {
  case "${1:-}" in
    ''|*[!0-9]*) printf '0' ;;
    *)           printf '%s' "$1" ;;
  esac
}

# go-template 的缺失键输出是 "<no value>"：折叠成空串，避免被当成真实取值。
strip_template_noise() {
  printf '%s' "${1//<no value>/}"
}

# 严格的 NotFound 判定（AUD-09）：只接受
#   Error from server (NotFound): <resource>[.<group>] "<name>" not found
# 这一种形态。
#
# 反例（必须**不能**命中）：命名空间不存在时的
#   Error from server (NotFound): namespaces "surgecart" not found
# 旧实现只 grep "not found"，会把「命名空间/权限错误」误判成「对象不存在」，
# 从而把一次读取失败当成「首次部署」放行。
is_not_found_for() {
  local file="$1" resource="$2" name="$3" line
  [[ -f "${file}" ]] || return 1
  while IFS= read -r line; do
    [[ "${line}" == *"(NotFound)"* ]] || continue
    if [[ "${line}" =~ (^|[^[:alnum:].-])${resource}(\.[a-z0-9.-]+)?[[:space:]]+\"?${name}\"?[[:space:]]+not[[:space:]]+found ]]; then
      return 0
    fi
  done < "${file}"
  return 1
}

# Annotation 值安全化：单行、去掉引号/反斜杠/分隔符 `~`、截断到 200 字符。
# （Lease 清单用双引号字符串书写，未转义的值会破坏 YAML；`~` 是快照解析的分隔符。）
sanitize_annotation() {
  printf '%s' "${1:-}" | tr -d '\r\n~"\\' | cut -c1-200
}

# ---------------------------------------------------------------------------
# 发布锁：coordination.k8s.io/v1 Lease
#
# 并发安全边界（必须如实理解）：
#   - 获取是原子的：首次用 create（服务端保证同名对象只能创建一次）；
#     接管/续租/释放用 PUT（kubectl replace）+ metadata.resourceVersion 做 CAS，
#     版本号不匹配时 API Server 返回 409 Conflict，不会覆盖别人的写入；
#   - 过期判定依赖 runner 本地时钟与 Lease.renewTime 比较。时钟明显偏移的 Runner 会
#     误判锁是否过期 —— 这是「普通 Lease 而非 etcd/数据库锁」的固有限制；
#   - Kubernetes API Server **不提供 fencing token**：一个被暂停（SIGSTOP / 长时间 GC /
#     网络分区）的旧持有者在租期过期、锁被接管之后，仍可能成功发起一次写请求。
#     本脚本用「每次变更前检查续租状态 + 变更前服务端复核 holder/uid + CAS」把窗口压到最小，
#     但无法做到「旧持有者的写一定被拒绝」。当前语义是 cooperative/advisory mutual
#     exclusion，不是强 fencing（AUD-02，见 docs/design/deploy-lock.md §6）。
#   - T2-06.1.4 之后的统一前置条件：**每一条**写 Lease 的路径（create / takeover /
#     renew / record-write / release）在写入前都必须同时满足「holder 是自己」+
#     「服务器上的 renewTime 未过期」。旧实现只有 renew 检查过期，record/release 只比
#     holder，于是失锁后仍能写入并给过期锁续命一个租期（B-3/B-4）。
#     这条前置条件只能阻止**新的**写入：已经发出的 kubectl 请求不可撤销（AUD-02 =
#     NOT_CLOSED、AUD-05 = NOT_CLOSED），本脚本不声称能回滚在途请求。
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# 记录快照：lease_manifest 的唯一数据来源
#
# 三条写入路径必须有明确区别（AUD-D2 的根因就是它们混用了内存状态）：
#   1) lease_record_clear     —— 新建 Lease：没有上一轮，记录为空 + NO_MIGRATION；
#   2) lease_record_from_lease—— 接管/续租/释放：**原样保留**服务器上的记录；
#   3) lease_record_set       —— 只有 migration_record_write（开始新迁移/记录终态/归档）才用。
# ---------------------------------------------------------------------------
lease_record_clear() {
  REC_STATE="${MIG_STATE_NONE}"
  REC_JOB=""
  REC_JOB_UID=""
  REC_DETAIL=""
  REC_UPDATED_AT=""
  REC_TERMINAL_AT=""
  REC_RECOVERY_METHOD=""
  REC_RECOVERY_EVIDENCE=""
  REC_PREV_STATE=""
  REC_PREV_JOB=""
  REC_PREV_JOB_UID=""
  REC_PREV_DETAIL=""
  REC_PREV_TERMINAL_AT=""
}

# 保留服务器上的当前记录与上一轮归档（不解释、不推断、不清空）。
lease_record_from_lease() {
  REC_STATE="${LEASE_MIG_STATE:-${MIG_STATE_NONE}}"
  REC_JOB="${LEASE_MIG_JOB:-}"
  REC_JOB_UID="${LEASE_MIG_UID:-}"
  REC_DETAIL="${LEASE_MIG_DETAIL:-}"
  REC_UPDATED_AT="${LEASE_MIG_UPDATED_AT:-}"
  REC_TERMINAL_AT="${LEASE_MIG_TERMINAL_AT:-}"
  REC_RECOVERY_METHOD="${LEASE_MIG_RECOVERY_METHOD:-}"
  REC_RECOVERY_EVIDENCE="${LEASE_MIG_RECOVERY_EVIDENCE:-}"
  REC_PREV_STATE="${LEASE_PREV_STATE:-}"
  REC_PREV_JOB="${LEASE_PREV_JOB:-}"
  REC_PREV_JOB_UID="${LEASE_PREV_UID:-}"
  REC_PREV_DETAIL="${LEASE_PREV_DETAIL:-}"
  REC_PREV_TERMINAL_AT="${LEASE_PREV_TERMINAL_AT:-}"
}

# 生成 Lease 清单：holder / acquireTime / renewTime / duration / transitions / rv / uid
# + 迁移生命周期注解（全部来自 REC_* 快照）。
lease_manifest() {
  local holder="$1" acquire="$2" renew="$3" duration="$4" transitions="$5" rv="$6" uid="$7"
  printf 'apiVersion: coordination.k8s.io/v1\n'
  printf 'kind: Lease\n'
  printf 'metadata:\n'
  printf '  name: %s\n' "${LOCK_NAME}"
  printf '  namespace: %s\n' "${NAMESPACE}"
  if [[ -n "${rv}" ]]; then
    printf '  resourceVersion: "%s"\n' "${rv}"
  fi
  if [[ -n "${uid}" ]]; then
    printf '  uid: "%s"\n' "${uid}"
  fi
  printf '  annotations:\n'
  printf '    "%s": "%s"\n' "${LEASE_ANN_STATE}" "$(sanitize_annotation "${REC_STATE}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_JOB}" "$(sanitize_annotation "${REC_JOB}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_JOB_UID}" "$(sanitize_annotation "${REC_JOB_UID}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_DETAIL}" "$(sanitize_annotation "${REC_DETAIL}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_UPDATED_AT}" "$(sanitize_annotation "${REC_UPDATED_AT}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_TERMINAL_AT}" "$(sanitize_annotation "${REC_TERMINAL_AT}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_RECOVERY_METHOD}" "$(sanitize_annotation "${REC_RECOVERY_METHOD}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_RECOVERY_EVIDENCE}" "$(sanitize_annotation "${REC_RECOVERY_EVIDENCE}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_PREV_STATE}" "$(sanitize_annotation "${REC_PREV_STATE}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_PREV_JOB}" "$(sanitize_annotation "${REC_PREV_JOB}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_PREV_JOB_UID}" "$(sanitize_annotation "${REC_PREV_JOB_UID}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_PREV_DETAIL}" "$(sanitize_annotation "${REC_PREV_DETAIL}")"
  printf '    "%s": "%s"\n' "${LEASE_ANN_PREV_TERMINAL_AT}" "$(sanitize_annotation "${REC_PREV_TERMINAL_AT}")"
  printf 'spec:\n'
  printf '  holderIdentity: "%s"\n' "${holder}"
  printf '  leaseDurationSeconds: %s\n' "${duration}"
  printf '  acquireTime: "%s"\n' "${acquire}"
  printf '  renewTime: "%s"\n' "${renew}"
  printf '  leaseTransitions: %s\n' "${transitions}"
}

# 读取锁记录到 LEASE_* 全局变量；锁不存在或读取失败返回非零。
lease_read() {
  local err="${TMP_DIR}/lease-read.err" out
  if ! out="$("${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" get lease "${LOCK_NAME}" \
    -o "go-template=${LEASE_STATE_TEMPLATE}" 2>"${err}")"; then
    LEASE_READ_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
    return 1
  fi
  out="$(strip_template_noise "${out}")"
  IFS='~' read -r LEASE_RV LEASE_UID LEASE_HOLDER LEASE_ACQUIRE LEASE_RENEW \
    LEASE_DURATION LEASE_TRANSITIONS LEASE_MIG_STATE LEASE_MIG_JOB LEASE_MIG_UID \
    LEASE_MIG_DETAIL LEASE_MIG_UPDATED_AT LEASE_MIG_TERMINAL_AT \
    LEASE_MIG_RECOVERY_METHOD LEASE_MIG_RECOVERY_EVIDENCE \
    LEASE_PREV_STATE LEASE_PREV_JOB LEASE_PREV_UID LEASE_PREV_DETAIL \
    LEASE_PREV_TERMINAL_AT <<< "${out}"
  LEASE_MIG_STATE="${LEASE_MIG_STATE:-${MIG_STATE_NONE}}"
  return 0
}

# 锁是否已过期（renewTime + leaseDurationSeconds <= now）。
# 无法解析 renewTime 时返回「未过期」：宁可不接管，也不打断一个可能仍在执行的发布。
#
# 注意：返回 1 包含「真的没过期」与「无法判断」两种情形。需要 fail-closed 的写路径
# 不能只看返回码，必须用 lease_window_evidence 拿到可核验的证据行。
lease_expired() {
  local renew_epoch expiry now
  if ! renew_epoch="$(epoch_of_rfc3339 "${LEASE_RENEW}")"; then
    log_warn "无法解析锁的 renewTime=${LEASE_RENEW:-<空>}：按「仍被持有」处理（需要人工确认后才能接管）"
    return 1
  fi
  expiry=$(( renew_epoch + $(to_int "${LEASE_DURATION:-0}") ))
  now="$(date -u +%s)"
  (( now >= expiry ))
}

# 打印服务器上剩余租期的证据（秒级，全部来自**服务器返回的** renewTime）。
#
# 固定格式，便于测试与值班 grep：
#   LEASE_WINDOW stage=<阶段> renewTime=<rfc3339> renew_epoch=<n|unparsable>
#                leaseDurationSeconds=<n> now_epoch=<n> remaining=<n|unknown> verdict=ok|expired|unparsable
# 返回 0=窗口为正；1=已过期；2=无法解析（无法判断）。
lease_window_evidence() {
  local stage="$1" renew_epoch now duration remaining
  duration="$(to_int "${LEASE_DURATION:-0}")"
  now="$(date -u +%s)"
  if ! renew_epoch="$(epoch_of_rfc3339 "${LEASE_RENEW}")"; then
    printf 'LEASE_WINDOW stage=%s renewTime=%s renew_epoch=unparsable leaseDurationSeconds=%s now_epoch=%s remaining=unknown verdict=unparsable\n' \
      "${stage}" "${LEASE_RENEW:-<empty>}" "${duration}" "${now}"
    return 2
  fi
  remaining=$(( renew_epoch + duration - now ))
  if (( remaining > 0 )); then
    printf 'LEASE_WINDOW stage=%s renewTime=%s renew_epoch=%s leaseDurationSeconds=%s now_epoch=%s remaining=%s verdict=ok\n' \
      "${stage}" "${LEASE_RENEW}" "${renew_epoch}" "${duration}" "${now}" "${remaining}"
    return 0
  fi
  printf 'LEASE_WINDOW stage=%s renewTime=%s renew_epoch=%s leaseDurationSeconds=%s now_epoch=%s remaining=%s verdict=expired\n' \
    "${stage}" "${LEASE_RENEW}" "${renew_epoch}" "${duration}" "${now}" "${remaining}"
  return 1
}

# 写入完成后的窗口断言（T2-06.1.4 / B-2）：服务器上的 renewTime 必须仍在有效窗口内。
#
# 为什么必须在**创建之后立刻**做：Create/PUT 的耗时（以及 preflight 的耗时，如果
# renewTime 取错时刻）都会吃掉租期窗口。若 renewTime + duration <= now，其他 Runner
# **此刻**就可以合法接管；继续发布就等于在接管窗口里写入。这里 fail-closed，
# 并且打印 renewTime / duration / now 的 epoch，让值班能直接核验而不是猜。
#
# 不通过延长 leaseDurationSeconds 来"修好"这个断言：那只会把问题推迟到下一次抖动。
lease_require_window() {
  local stage="$1" evidence rc=0
  evidence="$(lease_window_evidence "${stage}")" || rc=$?
  printf '%s\n' "${evidence}"
  if (( rc == 0 )); then
    return 0
  fi
  log_error "发布锁在「${stage}」之后就不满足窗口不变量（fail-closed）：服务器上的 renewTime 已无法覆盖当前时刻。"
  log_error "  证据：${evidence}"
  log_error "  含义：其他 Runner 现在就可以合法接管这把锁；本 Runner 若继续发布，就会在接管窗口里写入。"
  log_error "  根因方向：renewTime 必须锚定在「请求发出那一刻」并在 create 返回后立即续租一次（B-1/B-2），"
  log_error "  不能用脚本启动时刻。不得通过延长 leaseDurationSeconds 绕过本检查。"
  printf 'LEASE_ANCHOR_INVALID stage=%s %s\n' "${stage}" "${evidence}"
  die 3 "发布锁在「${stage}」后不满足窗口不变量（LEASE_ANCHOR_INVALID，fail-closed）"
}

# 新建 Lease：renewTime 锚定在**请求发出前一刻**（t_req），而不是脚本启动时刻。
#
# 为什么不能更晚：t_resp 在请求返回之前不可知，把未来时刻写进 renewTime 等于人为
# 延长租期（禁止）。为什么不能更早：旧实现用脚本启动时刻，preflight 越长，写进去的
# 租约越接近/已经过期（B-1，实测 preflight 51s → 创建即过期 → exit 3）。
# create 成功后由 acquire_release_lock 立即续租一次，把 renewTime 推到这个写入完成
# 之后（t_resp 之后）的时刻（B-2）。
lease_create() {
  # 新建 Lease 意味着「没有上一轮」：记录必须显式清空，不能沿用内存里的残留值。
  lease_record_clear
  LOCK_REQUEST_ISSUED_AT="$(timestamp_utc)"
  lease_manifest "${HOLDER_IDENTITY}" "${LOCK_REQUEST_ISSUED_AT}" "${LOCK_REQUEST_ISSUED_AT}" \
    "${LOCK_LEASE_DURATION}" 0 "" "" > "${LOCK_STATE_DIR}/lease-create.yaml"
  # 客户端在请求返回前观测不到 t_acc：这里写的 renewTime 只可能 ≤ t_acc。
  # 请求自身的耗时（Create 延迟）会消耗掉同样长度的窗口，因此返回后必须立刻做
  # 窗口断言 + 立即续租，二者都不可省略。
  "${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" create -f "${LOCK_STATE_DIR}/lease-create.yaml" >/dev/null
}

# 接管一把已释放或已过期的锁：CAS 写入，resourceVersion 不匹配即失败（别人抢先）。
#
# AUD-D2：接管**必须**原样保留服务器上的迁移记录。旧实现用内存里的 MIG_*（此时还是
# NO_MIGRATION）整体回写，等于把上一轮的 COMPLETE/FAILED + job + uid + detail 清空；
# 而接管方随后可能因为版本降级保护退出，导致证据永久丢失。
#
# 时间语义与 create 相同：renewTime = 接管请求发出前一刻（t_req），返回后立即做
# 窗口断言 + 续租（接管是 replace，窗口只被请求耗时消耗，但同样不能假设耗时为 0）。
lease_takeover() {
  local transitions
  transitions=$(( $(to_int "${LEASE_TRANSITIONS:-0}") + 1 ))
  lease_record_from_lease
  LOCK_REQUEST_ISSUED_AT="$(timestamp_utc)"
  lease_manifest "${HOLDER_IDENTITY}" "${LOCK_REQUEST_ISSUED_AT}" "${LOCK_REQUEST_ISSUED_AT}" \
    "${LOCK_LEASE_DURATION}" "${transitions}" "${LEASE_RV}" "${LEASE_UID}" \
    > "${LOCK_STATE_DIR}/lease-takeover.yaml"
  "${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-takeover.yaml" >/dev/null
}

# 续租：以 resourceVersion 做 CAS；持有者不是自己时立即失败（锁已被接管）。
#
# 一次 attempt 的真实执行路径（租期预算就是按这条路径推导的，改这里必须同步 §7）：
#
#   GET Lease（≤ --lease-renew-timeout）→ 校验 holder / uid / 是否已过期 → PUT Lease（CAS，≤ 同一超时）
#
# 409 重试是必需的：主进程写迁移记录也会推进 resourceVersion，若把这种「自己造成的
# 冲突」当成失锁，会在发布中途误报失败（AUD-03 的副作用）。重试时重新读取并重新确认
# holder —— 只有 holder 真的变成别人，才判定失锁。
#
# T2-06.1.3 / F-02.4：**每次 attempt 都必须先确认服务器上的租约尚未过期**。
# 一旦 renewTime + leaseDurationSeconds <= now，其他 Runner 就已经可以合法接管：
#   - 此时仍然 PUT 等于「复活一把别人已经可以抢走的锁」，会在接管窗口里继续写入；
#   - resourceVersion CAS 只能保证「不是我覆盖了别人的写」，不能保证「我没有在
#     别人可以接管的时刻继续操作」，因为对方可能还没写、也可能正在核验 Job。
# 因此过期即判定失锁（fail-closed），不重试、不续租。这条判断依赖**服务器返回的
# renewTime**，不是本地 lost 标记，也不是本地时钟推算的「我还没超时」。
lease_renew() {
  local attempt
  for (( attempt = 1; attempt <= LOCK_RENEW_MAX_ATTEMPTS; attempt++ )); do
    lease_read || return 1
    [[ "${LEASE_HOLDER}" == "${HOLDER_IDENTITY}" ]] || return 1
    if [[ -n "${LOCK_UID}" && -n "${LEASE_UID}" && "${LOCK_UID}" != "${LEASE_UID}" ]]; then
      log_warn "续租失败：Lease uid 已变化（记录=${LOCK_UID}，实际=${LEASE_UID}）：锁对象被删除重建"
      return 1
    fi
    if lease_expired; then
      # 证据行：renewTime / duration / now 的 epoch 必须能直接核验（B-1/B-2）。
      lease_window_evidence "renew-attempt-${attempt}" >&2 || true
      log_error "续租中止：服务器上的租约已过期（renewTime=${LEASE_RENEW:-<空>}，leaseDurationSeconds=${LEASE_DURATION:-<空>}）"
      log_error "  其他 Runner 已可合法接管，本 Runner 不再写入 Lease（第 ${attempt}/${LOCK_RENEW_MAX_ATTEMPTS} 次 attempt，fail-closed）。"
      log_error "  不通过「立即续租」复活一把可能已被抢走的锁：那会在接管窗口里继续写入。"
      return 1
    fi
    # AUD-D2：续租只刷新租期字段，迁移记录必须原样保留（读者可能是任何后续 Runner）。
    lease_record_from_lease
    # renewTime 的取值（时间语义见文件头 B-1 注释）：
    #   只能取「本 Runner 观测到的、不早于上一次写入完成」的时刻。这里取的是本次
    #   PUT 发出前一刻（t_req）；PUT 完成后 renewTime 会略早于 t_resp，差值 ≤ 一次
    #   请求耗时（受 --lease-renew-timeout 约束，已计入续租链预算）。
    #   不写未来时刻（会人为延长租期），也不使用脚本启动时刻（B-1 的根因）。
    LOCK_REQUEST_ISSUED_AT="$(timestamp_utc)"
    lease_manifest "${HOLDER_IDENTITY}" "${LEASE_ACQUIRE:-${LOCK_ACQUIRED_AT}}" \
      "${LOCK_REQUEST_ISSUED_AT}" "${LOCK_LEASE_DURATION}" \
      "$(to_int "${LEASE_TRANSITIONS:-0}")" "${LEASE_RV}" "${LEASE_UID}" \
      > "${LOCK_STATE_DIR}/lease-renew.yaml"
    if "${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-renew.yaml" \
        >/dev/null 2>"${LOCK_STATE_DIR}/lease-renew.err"; then
      # 只有**写入成功**才更新本地状态：失败分支不得改写 rv/uid/renewTime，
      # 否则一次 409 之后本地会以为"已经续上了"，下一次 attempt 会带着错误的
      # 前提继续写（B-2 的失败分支要求）。
      LOCK_UID="${LEASE_UID}"
      LOCK_WRITE_CONFIRMED_AT="$(timestamp_utc)"
      return 0
    fi
    log_warn "续租 CAS 冲突（第 ${attempt}/${LOCK_RENEW_MAX_ATTEMPTS} 次）：$(tr '\n' ' ' < "${LOCK_STATE_DIR}/lease-renew.err" 2>/dev/null || true)"
    # 退避时长的唯一来源是 LOCK_RENEW_BACKOFF_MS（预算由它推导，不再硬编码 0.2s）。
    # 只在 attempt **之间**退避：最后一次失败后 sleep 等于把 (attempts-1) 次退避
    # 变成 attempts 次，实测会比预算多 0.2s（B-5）。
    if (( attempt < LOCK_RENEW_MAX_ATTEMPTS )); then
      sleep "$(sleep_ms_arg "${LOCK_RENEW_BACKOFF_MS}")"
    fi
  done
  return 1
}

# 迁移未确认终态时，禁止释放锁（AUD-01 的核心保护）。
# 返回 0 表示「被阻止」。
migration_state_blocks_release() {
  (( MIG_APPLY_ISSUED )) || return 1
  (( MIG_TERMINAL )) && return 1
  return 0
}

# 释放：先确认持有者仍是自己，再用 CAS 把持有者清空（不删除对象，避免与接管者竞态）。
# 释放失败不算发布失败：锁会按租期自然过期。
# 重试是必要的：后台续租可能刚刚用旧 resourceVersion 提交过一次 renew，导致第一次 CAS 冲突。
lease_release() {
  (( LOCK_HELD )) || return 0
  if migration_state_blocks_release; then
    log_warn "不释放发布锁：迁移 Job/${MIG_JOB:-<未知>} 未确认终态（state=${MIG_STATE}，uid=${MIG_JOB_UID:-<未记录>}）"
    log_warn "  锁将在 ${LOCK_LEASE_DURATION}s 后自然过期；关联信息保留在 Lease 注解上供后续 Runner 核验。"
    log_warn "  禁止为了让脚本退出而清除该记录：那等于允许下一个 Runner 在迁移仍在执行时开始新一轮发布。"
    LOCK_HELD=0
    return 1
  fi
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    log_warn "跳过释放发布锁：本 Runner 已失去锁（续租失败/被接管），不尝试清除他人的 Lease"
    LOCK_HELD=0
    return 0
  fi
  local attempt now
  for attempt in 1 2 3 4 5; do
    if ! lease_read; then
      log_warn "释放发布锁失败：无法读取锁记录（${LEASE_READ_ERROR:-未知错误}）；锁将在 ${LOCK_LEASE_DURATION}s 后过期"
      LOCK_HELD=0
      return 1
    fi
    if [[ "${LEASE_HOLDER}" != "${HOLDER_IDENTITY}" ]]; then
      log_warn "跳过释放发布锁：当前持有者是 ${LEASE_HOLDER:-<空>}，不是本 Runner（不释放他人的锁）"
      LOCK_HELD=0
      return 0
    fi
    if lease_expired; then
      # T2-06.1.4 / B-4：过期锁**不得**通过释放把 renewTime 推进到 now。
      # 过期意味着其他 Runner 此刻就能合法接管；我们若仍写入（即便是"清空 holder"），
      # 就是在接管窗口里写一把不再属于自己的锁，而且会给一把过期锁续命一个租期，
      # 让真正的新持有者读到「刚刚续过」的假象。
      lease_window_evidence "release" >&2 || true
      log_warn "跳过释放发布锁：本 Runner 的租约已过期（renewTime=${LEASE_RENEW:-<空>}，leaseDurationSeconds=${LEASE_DURATION:-<空>}）"
      log_warn "  其他 Runner 已可合法接管；不为过期锁续命（不把 renewTime 推进到 now），也不写他人的锁。"
      log_warn "  锁会在服务器上按原 renewTime 自然过期，无需人工处理。"
      LOCK_HELD=0
      return 0
    fi
    now="$(timestamp_utc)"
    # AUD-D2：释放只清 holder；迁移记录（含上一轮归档）原样保留。
    # D6：leaseDurationSeconds 保持真实配置值。旧实现写 1 会让「锁的租期」在下次读取时
    # 变成 1s —— 字段语义是「持有者租期」，不是「释放后多久失效」（holder 为空即已释放），
    # 写 1 只会让后续读取 leaseDurationSeconds 的排查产生误判。
    lease_record_from_lease
    lease_manifest "" "${now}" "${now}" "${LOCK_LEASE_DURATION}" \
      "$(to_int "${LEASE_TRANSITIONS:-0}")" "${LEASE_RV}" "${LEASE_UID}" \
      > "${LOCK_STATE_DIR}/lease-release.yaml"
    if "${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-release.yaml" >/dev/null; then
      LOCK_HELD=0
      log_info "已释放发布锁 ${NAMESPACE}/${LOCK_NAME}（迁移记录保留：state=${MIG_STATE}${MIG_JOB:+ job=${MIG_JOB}}${MIG_JOB_UID:+ uid=${MIG_JOB_UID}}）"
      return 0
    fi
    sleep 0.2
  done
  log_warn "释放发布锁失败（CAS 冲突或 API 错误）；锁将在 ${LOCK_LEASE_DURATION}s 后过期"
  LOCK_HELD=0
  return 1
}

# 后台续租：失败时写 lost 标记并向主进程发 SIGTERM，主进程不再做任何资源变更。
lease_renewer_loop() {
  # 续租进程的输出去向固定到日志文件：
  #   1) 不占用调用方的 stdout/stderr 管道（否则退出时调用方要等它结束才能读到 EOF）；
  #   2) 失败原因仍然保留，主进程发现 lost 标记时会把它打印出来。
  exec >>"${LOCK_STATE_DIR}/renew.log" 2>&1
  while :; do
    sleep "${LOCK_RENEW_INTERVAL}"
    if [[ -f "${LOCK_STATE_DIR}/stop" ]]; then
      exit 0
    fi
    if ! lease_renew; then
      # 正常收尾（已写 stop）时的续租失败不是「丢锁」，无需告警或中止。
      if [[ -f "${LOCK_STATE_DIR}/stop" ]]; then
        exit 0
      fi
      : > "${LOCK_STATE_DIR}/lost"
      log_error "发布锁续租失败（holder=${HOLDER_IDENTITY}）：后续资源变更已禁止"
      kill -TERM "$$" 2>/dev/null || true
      exit 1
    fi
  done
}

start_lease_renewer() {
  lease_renewer_loop &
  RENEWER_PID=$!
}

# 停止续租：必须在释放锁之前调用，否则续租的 CAS 写入可能与释放的 CAS 写入冲突
# （表现为「发布成功却退出码 3」）。
#
# B-16：旧注释说「bash 会延迟处理信号，所以 wait 会多等一个续租周期」——这是错的。
# 实测（bash 5.x，子 shell 阻塞在外部 sleep 上）：收到 SIGTERM 后 1~2ms 内即终止，
# 因为**默认动作**是立即终止；被延迟的只是 trap 的执行，而不是信号本身。
# 这里省略 wait 的真实理由是：
#   1) 续租子 shell 没有安装任何 trap（bash 会把子 shell 中被捕获的信号复位为默认），
#      所以 kill -TERM 后它立刻死掉，wait 也只会立即返回；
#   2) 残留的 `sleep` 子进程最多存活一个续租间隔，它不持有任何状态、不影响锁；
#   3) 子 shell 的 stdout/stderr 已重定向到 renew.log，不会占住调用方的管道。
# 换句话说：不 wait 不是因为信号会被延迟，而是因为「等它」没有收益。
stop_lease_renewer() {
  [[ -n "${RENEWER_PID}" ]] || return 0
  : > "${LOCK_STATE_DIR}/stop"
  kill -TERM "${RENEWER_PID}" 2>/dev/null || true
  RENEWER_PID=""
  return 0
}

# 锁丢失时打印续租日志尾部（续租进程的输出不在主进程的 stderr 上）。
report_lock_lost() {
  if [[ -s "${LOCK_STATE_DIR}/renew.log" ]]; then
    log_error "续租日志（最后 20 行）："
    tail -n 20 "${LOCK_STATE_DIR}/renew.log" >&2 || true
  fi
}

# 请求**发出之后**的归属观察（AUD-02/AUD-05）。
#
# 与 require_lock_owned_server_side 的区别：那个在「发出前」，失败时请求还没发；
# 这个在「发出后」，失败说明请求可能已经在别人持有锁期间生效 —— 只能如实报告，
# 不能撤销，也不能据此认为「资源没有变」。
#
# T2-06.1.4 / B-10：只比 holder **不够**。租约过期后 holder 字段仍是自己，但其他
# Runner 此刻已经可以合法接管 —— 「还是自己持有」不等于「本次请求期间没有别人能接管」。
# 因此这里必须同时看 renewTime + leaseDurationSeconds。
observe_lock_after_request() {
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    return 1
  fi
  if ! lease_read; then
    log_warn "请求发出后无法读取发布锁（${LEASE_READ_ERROR:-未知错误}）：归属未知（按失锁处理）"
    return 1
  fi
  if [[ "${LEASE_HOLDER}" != "${HOLDER_IDENTITY}" ]]; then
    : > "${LOCK_STATE_DIR}/lost"
    log_error "请求发出后发布锁已不属于本 Runner（holder=${LEASE_HOLDER:-<空>}）"
    return 1
  fi
  if [[ -n "${LOCK_UID}" && -n "${LEASE_UID}" && "${LOCK_UID}" != "${LEASE_UID}" ]]; then
    : > "${LOCK_STATE_DIR}/lost"
    log_error "请求发出后 Lease uid 已变化（记录=${LOCK_UID}，实际=${LEASE_UID}）：锁对象被删除重建"
    return 1
  fi
  if lease_expired; then
    : > "${LOCK_STATE_DIR}/lost"
    lease_window_evidence "observe-after-request" >&2 || true
    log_error "请求发出后本 Runner 的租约已过期（renewTime=${LEASE_RENEW:-<空>}，leaseDurationSeconds=${LEASE_DURATION:-<空>}）："
    log_error "  holder 仍写着本 Runner，但其他 Runner 此刻已经可以合法接管 —— 归属不等于互斥。"
    log_error "  已发出的请求无法撤销，只能如实报告结果未核实（APPLICATION_OUTCOME_UNKNOWN）。"
    return 1
  fi
  LOCK_UID="${LEASE_UID}"
  return 0
}

# 每个可能变更集群状态的步骤之前调用：锁丢了就不许继续。
require_lock_alive() {
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    log_error "发布锁已失去（续租失败或被其他 Runner 接管）：停止后续资源变更。"
    report_lock_lost
    log_error "排查：${KUBECTL[*]} -n ${NAMESPACE} get lease ${LOCK_NAME} -o yaml"
    exit 3
  fi
  (( LOCK_HELD )) || die 3 "内部状态异常：未持有发布锁却继续发布"
}

# AUD-02：变更前的**服务端**复核（holder + uid）。
#
# 这不能消除 TOCTOU：复核与随后的 apply 之间仍有一个窗口，且已经发出的请求不能被撤销。
# 它只是把「本地 lost 标记还没观察到」的窗口压小，属于纵深防御，不是 fencing。
#
# 明确区分两种「失锁」：
#   - 复核失败：**尚未发出**本次写请求 → 直接停止，代价只是本次发布失败；
#   - 请求在途/已发出：无法撤销，只能在返回后如实报告（APPLICATION_OUTCOME_UNKNOWN）。
require_lock_owned_server_side() {
  require_lock_alive
  if ! lease_read; then
    log_error "服务端复核发布锁失败：${LEASE_READ_ERROR:-未知错误}（fail-closed，停止后续资源变更）"
    log_error "  本次尚未发出新的写请求；已发出的历史请求不在本检查的范围内（无法撤销）。"
    exit 3
  fi
  if [[ "${LEASE_HOLDER}" != "${HOLDER_IDENTITY}" ]]; then
    : > "${LOCK_STATE_DIR}/lost"
    log_error "服务端复核发现发布锁已不属于本 Runner（holder=${LEASE_HOLDER:-<空>}，本 Runner=${HOLDER_IDENTITY}）：停止资源变更"
    log_error "  本次尚未发出新的写请求；已经发出的历史请求无法撤销，只能按输出核对实际状态。"
    exit 3
  fi
  if [[ -n "${LOCK_UID}" && -n "${LEASE_UID}" && "${LOCK_UID}" != "${LEASE_UID}" ]]; then
    : > "${LOCK_STATE_DIR}/lost"
    log_error "服务端复核发现 Lease uid 变化（记录=${LOCK_UID}，实际=${LEASE_UID}）：对象被删除重建，停止资源变更"
    log_error "  本次尚未发出新的写请求；已经发出的历史请求无法撤销，只能按输出核对实际状态。"
    exit 3
  fi
  if lease_expired; then
    # T2-06.1.3 / F-02.4：租约已过期 ⇒ 其他 Runner **此刻**就可以合法接管。
    # 旧实现会在这里「立即续租两次」把锁复活，但那是在接管窗口里继续写入：
    # 对方可能已经通过 verify_previous_migration，正准备 replace 接管，也可能已经接管。
    # 这里不再尝试复活，直接按失锁处理（尚未发出新的写请求，代价只是本次发布失败）。
    : > "${LOCK_STATE_DIR}/lost"
    log_error "服务端复核发现本 Runner 的租约已过期（renewTime=${LEASE_RENEW:-<空>}，leaseDurationSeconds=${LEASE_DURATION:-<空>}）：停止资源变更（fail-closed）"
    log_error "  过期意味着其他 Runner 已可合法接管；本 Runner 不会通过「立即续租」复活该锁。"
    log_error "  本次尚未发出新的写请求；已经发出的历史请求无法撤销，只能按输出核对实际状态。"
    exit 3
  fi
  LOCK_UID="${LEASE_UID}"
}

# ---------------------------------------------------------------------------
# 权限契约（T2-06.1.4：审计 A-1 / A-2 / A-5 / A-8 / A-9 / D-3；
#             T2-06.1.5：审计 F-01 —— resourceNames 的对象级预检）
#
# REQUIRED_PERMISSIONS 是「脚本实际需要的 (verb, group, resource, scope, name)」的
# **唯一事实来源**，替换 T2-06.1.3 的 LEASE_PERMISSION_CHECKS。旧表只有 7 项
# （leases get/create/update + jobs get/list + pods get/list），漏掉了
# namespaces、secrets、deployments、pods/log、configmaps、services、pvc 以及
# 全部写动词 create/patch —— 按它授权的身份在首次发布时必然失败（审计 A-1/A-2）。
#
# 稳定格式（5 个必填字段 + 1 个可选字段，禁止在字段内使用 "|"）：
#
#   <verb>|<group>|<resource>|<scope>|<call site>|<resourceName>
#
#   verb     ：get / list / create / patch / update
#              （`kubectl replace` 才是 update；RBAC 里不存在 apply 这个 verb）
#   group    ：core（core/v1，RBAC 清单里写作 ""）或 API group（batch/apps/...）
#   resource ：资源名；子资源用 "/"，例如 pods/log
#   scope    ：namespace（可以写在 Role 里）| cluster（必须是 ClusterRole，
#              写进 Role 完全无效 —— 这正是审计 A-1 的根因）
#   call site：调用点（函数名；Lnnnn 只是定位提示，会随改动漂移，函数名才是契约）
#   resourceName：**可选**。非空表示该权限在交付的 RBAC 里被 `resourceNames`
#              限定到这一个对象，预检必须做**对象级检查**：
#
#                kubectl auth can-i get leases.coordination.k8s.io/surgecart-deploy-lock -n surgecart
#
#              真实 RBAC 的 ResourceNameMatches 只有在「请求带对象名且精确命中」
#              时才返回 true；不带对象名的 can-i 对受 resourceNames 限制的规则
#              **恒为 no**。T2-06.1.4 的缺陷（F-01）正是预检不带名字 + fake 对空
#              对象名特殊放行，于是 Fake 通过、真实集群拒绝。
#
#              只有 get/update/patch/delete 允许填这个字段：
#                - create 的对象名在授权时不可知，RBAC 的 resourceNames 无法限制它
#                  （交付清单里 create 是另一条不带 resourceNames 的规则）；
#                - list/watch/deletecollection 是集合请求，没有对象名。
#              validate_permission_table 在发出任何 can-i 之前检查这条不变量。
#
# 谁在读这张表：
#   - check_required_permissions（本文件）：逐项 `kubectl auth can-i` 预检；
#   - test/manifest/rbac_test.go：与 RBAC 清单做**双向**比对
#     （脚本用到的必须被授权；被授权但脚本不用的报「过度授权」；
#       RBAC 的 resourceNames 与第 6 字段必须一一对应）；
#   - test/deployk8s/rbac_test.go：逐条删掉一项 → 发布必须 fail-closed；
#   - docs/development/kubernetes.md（Agent D）：授权说明。
#
# 关于 `apply`：kubectl apply = 「GET 读现状 → 不存在则 CREATE、存在则 PATCH」，
# 因此每个被 apply 的资源都需要 get + create + patch 三个动词。
#
# **delete 一个都没有**：脚本永不删除 Job/Pod/Lease/Deployment（删除必须人工执行）。
# ---------------------------------------------------------------------------
REQUIRED_PERMISSIONS=(
  # --- 发布锁 leases（coordination.k8s.io）--------------------------------
  # 交付的 Role 用 resourceNames 把 get/update/patch 限定到 LOCK_NAME，
  # 因此这两条预检**必须**带对象名（第 6 字段）。不带名字的
  # `auth can-i get leases.coordination.k8s.io` 在真实集群上是 no（F-01）。
  #
  # lease_read：读 holderIdentity / renewTime / resourceVersion / 迁移记录注解
  "get|coordination.k8s.io|leases|namespace|lease_read@L755|surgecart-deploy-lock"
  # lease_create：首次获取锁（原子 create；AlreadyExists 表示已被占用）。
  # create 的对象名在授权时不可知，RBAC 无法用 resourceNames 限制它，
  # 所以这里**不能**带对象名（交付清单里 create 是独立的不受限规则）。
  "create|coordination.k8s.io|leases|namespace|lease_create@L789"
  # lease_takeover / lease_renew / lease_release / migration_record_write：
  # `kubectl replace` = PUT + metadata.resourceVersion 做 CAS。
  # 同样受 resourceNames 限制，必须按对象名检查。
  "update|coordination.k8s.io|leases|namespace|lease_takeover,lease_renew,lease_release,migration_record_write@L805,L847,L904,L1968|surgecart-deploy-lock"

  # --- 迁移 Job（batch）--------------------------------------------------
  # job_probe：按名称读取 Job 条件 / uid / deletionTimestamp
  "get|batch|jobs|namespace|job_probe@L2006"
  # job_list_migrations：按 MIGRATION_JOB_SELECTOR 列出迁移 Job
  # （接管核验 verify_previous_migration 与 begin_migration 都依赖它，审计 D-3）
  "list|batch|jobs|namespace|job_list_migrations@L2042"
  # begin_migration：`kubectl apply -f migration.yaml` 创建带版本后缀的新 Job
  "create|batch|jobs|namespace|begin_migration/apply@L2306"
  # 同一个 apply：Job 已存在时走 PATCH（kubectl apply 的第二条分支）
  "patch|batch|jobs|namespace|begin_migration/apply@L2306"

  # --- 迁移 Pod 与日志（core/v1）-----------------------------------------
  # read_migration_pods_for_job / read_job_pods：按 job-name 读 Pod（phase / 归属）
  "get|core|pods|namespace|read_migration_pods_for_job,read_job_pods@L1624,L2555"
  # scan_migration_pods / verify_release：按 component 标签与发布 selector 列表扫描
  "list|core|pods|namespace|scan_migration_pods,verify_release@L1645,L3040"
  # 失败诊断：`kubectl logs job/<name>`（审计 A-8：以前失败被 `|| true` 吞掉）
  # 子资源（pods/log）在 `auth can-i` 里只能用 `--subresource=log` 表达：
  # 位置参数 `pods/log` 会被 kubectl 解析成「名为 log 的 Pod」（TYPE/NAME 语法），
  # 于是既没检查子资源、又会被 pods 的普通 get 授权误判为 yes（T2-06.1.5）。
  "get|core|pods/log|namespace|print_job_logs@L1261"

  # --- 前置条件读取（core/v1 / apps）-------------------------------------
  # 锁内 Secret 复检（审计 A-3：以前把 403 报成「缺少 Secret」）
  "get|core|secrets|namespace|require_secrets@L2705"
  # read_deployment_state：读线上 Deployment 的镜像与版本标记（降级保护的事实来源）
  "get|apps|deployments|namespace|read_deployment_state@L2509"
  # verify_release 之后的只读汇总：`get deployment,service,pod -l ...`
  "list|apps|deployments|namespace|release_summary@L3113"

  # --- Application 清单 apply 的资源 -------------------------------------
  # `kubectl apply -f application.yaml` 覆盖 Deployment / Service / ConfigMap /
  # PersistentVolumeClaim：每条都需要 get（读现状）+ create + patch
  "create|apps|deployments|namespace|apply_application@L2926"
  "patch|apps|deployments|namespace|apply_application@L2926"
  "get|core|configmaps|namespace|apply_application@L2926"
  "create|core|configmaps|namespace|apply_application@L2926"
  "patch|core|configmaps|namespace|apply_application@L2926"
  "get|core|services|namespace|apply_application@L2926"
  "create|core|services|namespace|apply_application@L2926"
  "patch|core|services|namespace|apply_application@L2926"
  "list|core|services|namespace|release_summary@L3113"
  "get|core|persistentvolumeclaims|namespace|apply_application@L2926"
  "create|core|persistentvolumeclaims|namespace|apply_application@L2926"
  "patch|core|persistentvolumeclaims|namespace|apply_application@L2926"

  # --- 集群级权限（只能由 ClusterRole + ClusterRoleBinding 授予）---------
  # namespace_status：`kubectl get namespace <ns>` 判断命名空间是否存在
  "get|core|namespaces|cluster|namespace_status@L1390"
  # ensure_namespace：首次部署时 `kubectl apply -f shared/namespace.yaml`
  "create|core|namespaces|cluster|ensure_namespace@L1414"
  # **apply 部署清单也会写 Namespace**：migration 与 application 两个构建产物都包含
  # `shared/namespace.yaml`（kind: Namespace），命名空间已存在时 kubectl apply 走
  # PATCH —— 缺这一条会让「命名空间已存在」的每一次发布在第一次 apply 就 403。
  "patch|core|namespaces|cluster|begin_migration/apply,apply_application@L2306,L2926"
  # 权限预检自身：`kubectl auth can-i` 就是对 selfsubjectaccessreviews 的 create。
  # 内置 system:basic-user 通常已授予，但交付的清单必须显式声明，否则审计 A-9
  # 无法闭环：身份缺少它时预检只能报「无法确认」，原因却指向不到 SSAR。
  "create|authorization.k8s.io|selfsubjectaccessreviews|cluster|permission_probe@L1293,L1295"
)

# 预检状态（由 probe_permission_scope / permission_probe 维护）
PERMISSION_DENIED_COUNT=0
PERMISSION_UNKNOWN_COUNT=0
PERMISSION_PROBED=0
PERMISSION_DENIED_ENTRIES=()
PERMISSION_UNKNOWN_ENTRIES=()

# 命名空间存在性判定结果（namespace_status 维护）
NAMESPACE_EXISTS=0
NAMESPACE_PROBE_ERROR=""
NAMESPACE_PROBE_CLASS=""
NAMESPACE_CREATED=0

# permission_ref <group> <resource> → `leases.coordination.k8s.io` / `pods` / `pods/log`
permission_ref() {
  local group="$1" resource="$2"
  if [[ -z "${group}" || "${group}" == "core" ]]; then
    printf '%s' "${resource}"
  else
    printf '%s.%s' "${resource}" "${group}"
  fi
}

# permission_verb_is_name_scoped <verb>
#
# 只有「按对象名寻址」的动词才受 RBAC `resourceNames` 约束：
#   - get / update / patch / delete：请求带 name，ResourceNameMatches 才可能为真；
#   - create：对象尚不存在，授权时拿不到名字 —— resourceNames 对它**无效**；
#   - list / watch / deletecollection：集合请求，没有对象名。
# 因此 `kubectl auth can-i` 的对象级检查（TYPE/NAME）只对这些动词有意义。
permission_verb_is_name_scoped() {
  case "$1" in
    get|update|patch|delete) return 0 ;;
    *) return 1 ;;
  esac
}

# permission_subresource_of <resource>：`pods/log` → `log`，其余为空。
permission_subresource_of() {
  if [[ "$1" == */* ]]; then
    printf '%s' "${1#*/}"
  fi
}

# permission_resource_base <resource>：`pods/log` → `pods`，其余原样。
permission_resource_base() {
  printf '%s' "${1%%/*}"
}

# permission_display_ref <group> <resource> [name]：错误信息里展示的授权对象。
#
#   leases.coordination.k8s.io + leases + surgecart-deploy-lock
#     → leases.coordination.k8s.io/surgecart-deploy-lock
#   core + pods/log + ""  → pods/log
permission_display_ref() {
  local group="$1" resource="$2" name="$3" ref
  ref="$(permission_ref "${group}" "${resource}")"
  if [[ -n "${name}" ]]; then
    printf '%s/%s' "${ref}" "${name}"
  else
    printf '%s' "${ref}"
  fi
}

# permission_probe_target <group> <resource> [name]：`auth can-i` 的位置参数。
#
# kubectl 的解析规则（`kubectl auth can-i VERB [TYPE | TYPE/NAME | NONRESOURCEURL]`）：
#   1. TYPE 部分可以是 `leases` 或 `leases.coordination.k8s.io`（快捷名/组名会被解析）；
#   2. TYPE 之后**第一个** "/" 之后的内容是**对象名**（SplitN(arg, "/", 2)），
#      不是子资源 —— `pods/log` 会被 kubectl 当成「名为 log 的 Pod」；
#   3. 子资源只能由 `--subresource` 指定（见 permission_subresource_of）。
# 所以这里只输出 TYPE 或 TYPE/NAME，子资源由调用方单独加 flag。
permission_probe_target() {
  local group="$1" resource="$2" name="$3" ref
  ref="$(permission_ref "${group}" "$(permission_resource_base "${resource}")")"
  if [[ -n "${name}" ]]; then
    printf '%s/%s' "${ref}" "${name}"
  else
    printf '%s' "${ref}"
  fi
}

# validate_permission_table：在发出**任何** auth can-i 之前校验权限表本身。
#
# 两类错误都会让「预检结论 ≠ 真实 RBAC 结论」，必须 fail-closed（调用方退出 3）：
#   1. scope 只能是 namespace / cluster（拼错会让规则被静默跳过）；
#   2. 只有 get/update/patch/delete 允许携带对象名（第 6 字段）：
#      create 的对象名在授权时不可知、list/watch/deletecollection 是集合请求，
#      给它们带名字会得到与真实写入相反的结论。
validate_permission_table() {
  local entry verb group resource scope site name
  local -a fields
  for entry in "${REQUIRED_PERMISSIONS[@]}"; do
    # 字段数只能是 5 或 6：多余字段会被 `read` 静默吞进最后一个变量，
    # 从而改变语义（例如把 "lock|extra" 当成对象名）。
    IFS='|' read -r -a fields <<< "${entry}"
    if (( ${#fields[@]} < 5 || ${#fields[@]} > 6 )); then
      log_error "权限表自检失败：条目 ${entry} 有 ${#fields[@]} 个字段，期望 5 或 6 个（<verb>|<group>|<resource>|<scope>|<call site>[|<resourceName>]）。"
      return 1
    fi
    IFS='|' read -r verb group resource scope site name <<< "${entry}"
    if [[ -z "${verb}" || -z "${resource}" ]]; then
      log_error "权限表自检失败：条目 ${entry} 缺少 verb/resource。"
      return 1
    fi
    if [[ "${scope}" != "namespace" && "${scope}" != "cluster" ]]; then
      log_error "权限表自检失败：条目 ${entry} 的 scope 只能是 namespace 或 cluster（实际 ${scope:-<空>}）。"
      return 1
    fi
    if [[ -n "${name}" ]] && ! permission_verb_is_name_scoped "${verb}"; then
      log_error "权限表自检失败：条目 ${entry} 给 ${verb} 指定了对象名 ${name}。Kubernetes RBAC 的 resourceNames 只对 get/update/patch/delete 生效：create 的对象名在授权时不可知，list/watch/deletecollection 是集合请求。"
      return 1
    fi
  done
  return 0
}

# 作用域的中文描述：错误信息必须让读者知道「缺的是命名空间级还是集群级」。
permission_scope_label() {
  case "$1" in
    cluster) printf '集群级（cluster-scoped，Role 无法授予）' ;;
    *)       printf '命名空间 %s' "${NAMESPACE}" ;;
  esac
}

# ---------------------------------------------------------------------------
# classify_api_error <stderr 文件> [resource] [name]
#
# 输出（全局变量）：
#   API_ERROR_CLASS ∈ forbidden | notfound | api | timeout | unknown
#   API_ERROR_TEXT  ：原始 stderr 压成单行（**必须**出现在错误信息里，不得丢弃）
#
# 四类语义冻结：
#   forbidden : API Server 明确拒绝（403）。**不等于**「对象不存在」。
#   notfound  : 严格匹配 `<resource> "<name>" not found`，只表示对象确实不存在。
#   api       : API Server 自身故障（5xx / InternalError / ServiceUnavailable）。
#   timeout   : 客户端超时 / 网络不可达 / 连接被拒绝 —— 请求可能已生效。
#   unknown   : 以上都不是（例如读 Job 时收到
#               `Error from server (NotFound): namespaces "x" not found`
#               —— 这只能说明命名空间不见了，**不能**据此断言 Job 不存在）。
#
# 冻结规则：不允许把「查询失败」说成「不存在」，也不允许把「不存在」说成「无权限」。
# ---------------------------------------------------------------------------
classify_api_error() {
  local file="$1" resource="${2:-}" name="${3:-}" text=""
  API_ERROR_CLASS="unknown"
  API_ERROR_TEXT=""
  if [[ -f "${file}" ]]; then
    text="$(tr '\n' ' ' < "${file}" 2>/dev/null || true)"
  fi
  # 去掉首尾空白（tr 之后仍可能带空格）
  text="${text#"${text%%[![:space:]]*}"}"
  text="${text%"${text##*[![:space:]]}"}"
  API_ERROR_TEXT="${text}"
  [[ -n "${text}" ]] || return 0

  # 1) 明确 Forbidden（403）
  if [[ "${text}" == *"(Forbidden)"* || "${text}" == *"forbidden:"* ]]; then
    API_ERROR_CLASS="forbidden"
    return 0
  fi
  # 2) 严格 NotFound：必须同时匹配资源与名称（AUD-09）
  if [[ -n "${resource}" && -n "${name}" ]]; then
    if is_not_found_for "${file}" "${resource}" "${name}"; then
      API_ERROR_CLASS="notfound"
      return 0
    fi
  fi
  # 3) 网络 / 超时
  case "${text}" in
    *"Unable to connect to the server"* | *"connection refused"* | \
      *"Client.Timeout exceeded"* | *"Timeout exceeded while awaiting"* | \
      *"i/o timeout"* | *"TLS handshake timeout"* | *"no such host"* | \
      *"connection reset by peer"* | *"request canceled"* | *"context deadline exceeded"*)
      API_ERROR_CLASS="timeout"
      return 0
      ;;
  esac
  # 4) API Server 故障（5xx 等）
  case "${text}" in
    *"(InternalError)"* | *"(ServiceUnavailable)"* | *"(ServerTimeout)"* | \
      *"(TooManyRequests)"* | *"(UnexpectedServerResponse)"* | \
      *"Internal error occurred"* | *"the server is currently unable to handle the request"*)
      API_ERROR_CLASS="api"
      return 0
      ;;
  esac
  return 0
}

# api_error_class_label <class>：错误信息里必须出现分类，否则排查方向会跑偏。
api_error_class_label() {
  case "$1" in
    forbidden) printf 'Forbidden（权限不足：API Server 明确拒绝）' ;;
    notfound)  printf 'NotFound（对象确实不存在）' ;;
    api)       printf 'API 故障（服务端 5xx，状态未知）' ;;
    timeout)   printf '网络超时/不可达（状态未知：请求可能已生效）' ;;
    *)         printf '无法分类的错误（状态未知）' ;;
  esac
}

# print_job_logs <job> [tail]
#
# 失败诊断：读取迁移 Pod 的日志。**失败必须被报告**（审计 A-8：旧实现用
# `... || true` 静默吞掉 stderr，运维在「迁移失败」时看不到任何原因）：
#   - Forbidden → 发布身份缺少 pods/log 的 get 权限（诊断能力缺失，**不等于**没有日志）
#   - NotFound  → Job/Pod 可能已被 ttlSecondsAfterFinished 清理
#   - 其余      → 输出分类 + 原始 stderr
# 该函数只影响可观测性，不改变退出码（调用点已在失败分支里）。
print_job_logs() {
  local job="$1" tail_lines="${2:-200}" err rc=0
  err="${TMP_DIR}/job-logs.err"
  "${KUBECTL[@]}" -n "${NAMESPACE}" logs "job/${job}" --tail="${tail_lines}" --all-containers=true >&2 2>"${err}" || rc=$?
  if (( rc == 0 )); then
    return 0
  fi
  classify_api_error "${err}" "pods/log" ""
  if [[ "${API_ERROR_CLASS}" == "forbidden" ]]; then
    log_error "  无法读取 Job/${job} 的日志（Forbidden：发布身份缺少 pods/log 的 get 权限，见 manifest/kubernetes/examples/deploy-lock/rbac.yaml）。这**不等于**「Job 没有日志」。stderr: ${API_ERROR_TEXT:-无输出}"
  elif [[ "${API_ERROR_TEXT}" == *"(NotFound)"* ]]; then
    log_error "  无法读取 Job/${job} 的日志（NotFound：Job/Pod 可能已被 ttlSecondsAfterFinished 清理）。stderr: ${API_ERROR_TEXT:-无输出}"
  else
    log_error "  无法读取 Job/${job} 的日志（$(api_error_class_label "${API_ERROR_CLASS}")）：${API_ERROR_TEXT:-无输出}"
  fi
  return 0
}

# permission_probe <verb> <group> <resource> <scope> [name]
#
# 结果写入 PERMISSION_STATE：allowed | denied | unknown。
#   - allowed：`auth can-i` 返回 "yes" + 0
#   - denied ：`auth can-i` 返回 "no"  + 非 0（明确的 Forbidden / 无绑定）
#   - unknown：其他情况（SSAR 无法完成、HTTP 5xx、超时、kubectl 报错）
#
# 对象级检查（T2-06.1.5 / F-01）：name 非空时必须用 TYPE/NAME 形式，
# 因为 `resourceNames` 限定的规则只在请求带对象名时匹配（真实 RBAC 的
# ResourceNameMatches）。不带名字的 can-i 对这类规则**恒为 no**，用它做预检
# 会让「按交付清单授权的身份」在真实集群上被判成无权限。
# 子资源（pods/log）改用 `--subresource=<sub>`，不能写进 TYPE。
#
# 为什么必须区分 denied 与 unknown：两者都要 fail-closed，但把「API 不可用」说成
# 「权限被拒绝」会把排查方向引到错误的 RBAC 绑定上（审计 A-9 的同类问题）。
permission_probe() {
  local verb="$1" group="$2" resource="$3" scope="$4" name="${5:-}"
  local out err rc=0 target sub
  local -a probe_args
  err="${TMP_DIR}/auth-can-i.err"
  PERMISSION_STATE="unknown"
  PERMISSION_DETAIL=""
  target="$(permission_probe_target "${group}" "${resource}" "${name}")"
  sub="$(permission_subresource_of "${resource}")"
  probe_args=("auth" "can-i" "${verb}" "${target}")
  if [[ -n "${sub}" ]]; then
    probe_args+=("--subresource=${sub}")
  fi
  if [[ "${scope}" == "namespace" ]]; then
    probe_args+=("-n" "${NAMESPACE}")
  fi
  # 集群级资源不能带 -n。SelfSubjectAccessReview 只做授权判定，
  # 不校验命名空间是否存在 —— 这是「预检可以先于命名空间创建」的依据。
  out="$("${KUBECTL[@]}" "${probe_args[@]}" 2>"${err}")" || rc=$?
  out="$(printf '%s' "${out}" | tr -d '\r' | tr '\n' ' ')"
  out="${out# }"
  out="${out% }"
  if (( rc == 0 )) && [[ "${out}" == yes* ]]; then
    PERMISSION_STATE="allowed"
    return 0
  fi
  if (( rc != 0 )) && [[ "${out}" == no* ]]; then
    PERMISSION_STATE="denied"
    return 0
  fi
  PERMISSION_DETAIL="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  PERMISSION_DETAIL="${PERMISSION_DETAIL:-${out:-kubectl 退出码 ${rc}}}"
  return 0
}

# probe_permission_scope <scope> [entry...]
#
# 逐项预检并**立即**输出每一项的判定（不留到最后统一报，避免只报第一条）。
# 省略 entry 时检查该作用域在 REQUIRED_PERMISSIONS 里的全部条目。
probe_permission_scope() {
  local scope="$1" entry verb group resource entry_scope site name ref
  shift || true
  PERMISSION_DENIED_COUNT=0
  PERMISSION_UNKNOWN_COUNT=0
  PERMISSION_PROBED=0
  PERMISSION_DENIED_ENTRIES=()
  PERMISSION_UNKNOWN_ENTRIES=()
  local entries=("$@")
  if (( ${#entries[@]} == 0 )); then
    for entry in "${REQUIRED_PERMISSIONS[@]}"; do
      IFS='|' read -r verb group resource entry_scope site name <<< "${entry}"
      if [[ "${entry_scope}" == "${scope}" ]]; then
        entries+=("${entry}")
      fi
    done
  fi
  for entry in "${entries[@]}"; do
    IFS='|' read -r verb group resource entry_scope site name <<< "${entry}"
    # 展示用的对象名与 can-i 的位置参数不是一回事：pods/log 展示成子资源，
    # 但传给 kubectl 的是 `pods --subresource=log`（见 permission_probe）。
    ref="$(permission_display_ref "${group}" "${resource}" "${name}")"
    permission_probe "${verb}" "${group}" "${resource}" "${entry_scope}" "${name}"
    PERMISSION_PROBED=$(( PERMISSION_PROBED + 1 ))
    case "${PERMISSION_STATE}" in
      allowed)
        ;;
      denied)
        PERMISSION_DENIED_COUNT=$(( PERMISSION_DENIED_COUNT + 1 ))
        PERMISSION_DENIED_ENTRIES+=("${entry}")
        log_error "当前身份在 $(permission_scope_label "${entry_scope}") 缺少 ${ref} 的 ${verb} 权限（Forbidden：auth can-i 明确返回 no；调用点 ${site}）。"
        ;;
      *)
        PERMISSION_UNKNOWN_COUNT=$(( PERMISSION_UNKNOWN_COUNT + 1 ))
        PERMISSION_UNKNOWN_ENTRIES+=("${entry}")
        log_error "无法确认当前身份在 $(permission_scope_label "${entry_scope}") 是否有 ${ref} 的 ${verb} 权限（查询失败，**不是**「被拒绝」）：${PERMISSION_DETAIL:-未知错误}"
        if [[ "${resource}" == "selfsubjectaccessreviews" ]]; then
          log_error "  权限预检本身就是一次 SelfSubjectAccessReview：无法确认时，最常见的原因是当前身份缺少 create selfsubjectaccessreviews.authorization.k8s.io（内置 system:basic-user 通常已授予）。"
        fi
        ;;
    esac
  done
}

# report_permission_failures <phase>：输出结论并返回 1（调用方退出 3）。
report_permission_failures() {
  local phase="$1"
  if (( PERMISSION_DENIED_COUNT > 0 )); then
    if [[ "${phase}" == "cluster" ]]; then
      log_error "权限不足：无法读取/创建目标命名空间 ${NAMESPACE}（缺少集群级权限）。本次发布不执行任何工作负载变更。"
    else
      log_error "权限不足，无法建立发布互斥或核验迁移生命周期：本次发布不执行任何资源变更。"
    fi
    log_error "授权示例：kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml"
    log_error "自检：${KUBECTL[*]} auth can-i --list -n ${NAMESPACE}"
    log_error "对象级自检（受 resourceNames 限制的 Lease 必须带对象名）：${KUBECTL[*]} auth can-i get leases.coordination.k8s.io/${LOCK_NAME} -n ${NAMESPACE}"
    return 1
  fi
  if (( PERMISSION_UNKNOWN_COUNT > 0 )); then
    log_error "权限预检无法完成（auth can-i 查询失败）：不能假定「有权限」，按 fail-closed 处理。"
    log_error "自检：${KUBECTL[*]} auth can-i get leases.coordination.k8s.io/${LOCK_NAME} -n ${NAMESPACE}"
    return 1
  fi
  return 0
}

# 严格判定命名空间是否存在（AUD-09 / A-1）。
#
# 旧实现 `if ! kubectl get namespace ...; then 当作不存在` 把 403 / 5xx / 超时
# 全部当成「不存在」，随后 apply → 首次 least-privilege 部署必现 403。
# 现在：只有严格 NotFound 才是「不存在」，其余一律返回非零（调用方 fail-closed）。
namespace_status() {
  local err="${TMP_DIR}/get-namespace.err" out
  NAMESPACE_EXISTS=0
  NAMESPACE_PROBE_ERROR=""
  NAMESPACE_PROBE_CLASS=""
  if out="$("${KUBECTL[@]}" get namespace "${NAMESPACE}" 2>"${err}")"; then
    NAMESPACE_EXISTS=1
    return 0
  fi
  classify_api_error "${err}" "namespaces" "${NAMESPACE}"
  NAMESPACE_PROBE_CLASS="${API_ERROR_CLASS}"
  if [[ "${API_ERROR_CLASS}" == "notfound" ]]; then
    NAMESPACE_EXISTS=0
    return 0
  fi
  NAMESPACE_PROBE_ERROR="$(api_error_class_label "${API_ERROR_CLASS}")：${API_ERROR_TEXT:-无输出}"
  return 1
}

# 幂等地创建命名空间。
#
# 这是**唯一**允许发生在完整命名空间级预检之前的写操作，理由：
#   - 它不触碰锁、迁移 Job、Deployment 或任何工作负载，只创建一个空命名空间；
#   - 只有在「命名空间不存在 + 某些 SSAR 无法完成」时才会走到这里（见
#     check_required_permissions 阶段 3 的注释），之后会立即复检，失败即终止；
#   - 反过来（先建后查）会让「缺 namespaces create 权限」在执行到一半才暴露。
ensure_namespace() {
  local err="${TMP_DIR}/apply-namespace.err"
  log_info "创建命名空间 ${NAMESPACE}（按 ${NAMESPACE_MANIFEST}；幂等，不触碰锁/迁移/应用）"
  if ! "${KUBECTL[@]}" apply -f "${ROOT_DIR}/${NAMESPACE_MANIFEST}" >/dev/null 2>"${err}"; then
    classify_api_error "${err}" "namespaces" "${NAMESPACE}"
    case "${API_ERROR_CLASS}" in
      forbidden)
        log_error "创建命名空间 ${NAMESPACE} 被拒绝（Forbidden：缺少 namespaces 的 create 权限）。namespaces 是集群级资源，Role 无法授予，必须用 ClusterRole + ClusterRoleBinding（审计 A-1）。stderr: ${API_ERROR_TEXT:-无输出}"
        ;;
      notfound)
        log_error "创建命名空间 ${NAMESPACE} 失败（NotFound：${NAMESPACE_MANIFEST} 或其中的对象缺失）。stderr: ${API_ERROR_TEXT:-无输出}"
        ;;
      *)
        log_error "创建命名空间 ${NAMESPACE} 失败（$(api_error_class_label "${API_ERROR_CLASS}")）：${API_ERROR_TEXT:-无输出}"
        ;;
    esac
    exit 3
  fi
  if ! namespace_status; then
    log_error "命名空间 ${NAMESPACE} 创建后仍然无法确认是否存在（${NAMESPACE_PROBE_ERROR}）：终止发布。"
    exit 3
  fi
  if (( NAMESPACE_EXISTS == 0 )); then
    log_error "命名空间 ${NAMESPACE} 的 apply 返回成功，但对象仍然不可见：拒绝继续（应用结果不可信）。"
    exit 3
  fi
  NAMESPACE_CREATED=1
}

# ---------------------------------------------------------------------------
# check_required_permissions：权限预检 + 命名空间引导（顺序是整个工作包的关键）
#
# 不变量：**所有权限判定都发生在第一次工作负载写请求之前**。
# 真实调用顺序：
#   阶段 1  集群级预检（namespaces get/create、selfsubjectaccessreviews create）
#           —— 纯读，不需要命名空间存在，不写任何资源；
#   阶段 2  命名空间存在性判定（读；严格分类，403/5xx/超时 ≠ 不存在）；
#   阶段 3  命名空间级预检（leases/jobs/pods/pods-log/secrets/deployments/
#           configmaps/services/pvc）—— 仍然是纯读，发生在任何写之前；
#   阶段 4  命名空间引导（只在确实不存在时创建；幂等，不触碰锁/迁移/应用）；
#   阶段 5  （调用方）获取发布锁 → 之后才是迁移与应用。
#
# 为什么阶段 3 在阶段 4 之前：SSAR 只做授权判定、不校验命名空间是否存在，因此
# 首次部署也能在创建命名空间之前拿到全部判定结果。只有一种情况会退回到
# 「先建空命名空间再复检」：某个授权器（webhook / 需命名空间已存在的实现）
# 让 SSAR 无法完成（unknown）。那时只复检 unknown 的条目，且一旦复检仍失败就
# 立即终止 —— 代价是留下一个空命名空间，换来「绝不带着未知权限进入锁/迁移」。
#
# 这就是 fail-closed 的含义：宁可留下一个空命名空间，也不在权限未知时碰锁。
# ---------------------------------------------------------------------------
check_required_permissions() {
  local phase_unknown=()

  # ---- 阶段 0：权限表自检（在任何 can-i 之前；表坏了就不能相信预检结论）----
  if ! validate_permission_table; then
    log_error "权限预检无法完成（权限表自检失败）：按 fail-closed 处理，本次发布不执行任何变更。"
    exit 3
  fi

  # ---- 阶段 1：集群级权限（任何写操作之前）----
  probe_permission_scope cluster
  if (( PERMISSION_DENIED_COUNT > 0 || PERMISSION_UNKNOWN_COUNT > 0 )); then
    report_permission_failures cluster || true
    exit 3
  fi

  # ---- 阶段 2：命名空间是否存在（严格分类）----
  if ! namespace_status; then
    if [[ "${NAMESPACE_PROBE_CLASS}" == "forbidden" ]]; then
      log_error "无权读取命名空间 ${NAMESPACE}（Forbidden）：发布身份需要集群级 namespaces 的 get 权限（Role 无法授予）。**这不是「命名空间不存在」**，不能据此去 apply 命名空间（审计 A-1/A-2）。stderr: ${API_ERROR_TEXT:-无输出}"
    else
      log_error "无法确认命名空间 ${NAMESPACE} 是否存在（${NAMESPACE_PROBE_ERROR}）：**这不是「命名空间不存在」**，按 fail-closed 处理，本次发布不执行任何变更。"
    fi
    exit 3
  fi

  # ---- 阶段 3：命名空间级权限（仍然没有任何写操作）----
  probe_permission_scope namespace
  if (( PERMISSION_DENIED_COUNT > 0 )); then
    report_permission_failures namespace || true
    exit 3
  fi

  if (( PERMISSION_UNKNOWN_COUNT > 0 )); then
    phase_unknown=("${PERMISSION_UNKNOWN_ENTRIES[@]}")
    if (( NAMESPACE_EXISTS )); then
      log_error "命名空间 ${NAMESPACE} 已存在，但仍有 ${#phase_unknown[@]} 项权限无法确认：不能假定「有权限」，按 fail-closed 处理。"
      report_permission_failures namespace || true
      exit 3
    fi
    log_warn "命名空间 ${NAMESPACE} 尚不存在，且有 ${#phase_unknown[@]} 项权限无法确认（SSAR 可能需要命名空间已存在）：先创建空命名空间，再复检这些条目。"
    ensure_namespace
    probe_permission_scope namespace "${phase_unknown[@]}"
    if (( PERMISSION_DENIED_COUNT > 0 || PERMISSION_UNKNOWN_COUNT > 0 )); then
      report_permission_failures namespace || true
      exit 3
    fi
    log_info "权限预检完成（命名空间创建后复检 ${PERMISSION_PROBED} 项，全部通过）"
    return 0
  fi

  # ---- 阶段 4：命名空间引导（预检全部通过之后；幂等）----
  if (( NAMESPACE_EXISTS == 0 )); then
    ensure_namespace
  fi

  log_info "权限预检通过（${#REQUIRED_PERMISSIONS[@]} 项）：leases/jobs/pods/pods-log/secrets/deployments/configmaps/services/pvc @ ${NAMESPACE}；namespaces/selfsubjectaccessreviews @ 集群级；本次发布尚未发起任何工作负载写请求"
}

lock_is_free() {
  [[ -z "${LEASE_HOLDER}" ]]
}

# 创建/接管成功之后**立刻**执行（T2-06.1.4 / B-2），三步缺一不可：
#   1) 读回服务器状态，断言 renewTime + duration > now（不满足 → die 3 并打印 epoch 证据）。
#      Create/PUT 的耗时（以及 preflight，如果 renewTime 取错时刻）都会吃掉窗口；
#      一旦窗口为负，其他 Runner 此刻就能合法接管，继续发布就是在接管窗口里写入。
#   2) **立即续租一次**，把 renewTime 推到本次写入完成之后（t_resp 之后）的时刻。
#      这样「创建完成 → 首次过期」至少有完整的 leaseDurationSeconds 可用窗口。
#   3) 再读一次并断言窗口，确认第 2 步真的把锚点推进了（而不是原地不动）。
#
# 注意：这里不延长 leaseDurationSeconds，也不做"续租时间校准"——那两种做法都会
# 让「服务器上的 renewTime」比真实写入时刻更晚，从而掩盖失锁窗口。
finalize_lock_acquisition() {
  local mode="$1"
  LOCK_WRITE_CONFIRMED_AT="$(timestamp_utc)"

  if ! lease_read; then
    log_error "已${mode}发布锁，但无法读回服务器状态（${LEASE_READ_ERROR:-未知错误}）：无法证明持有的锁在有效窗口内（fail-closed）"
    die 3 "发布锁${mode}后无法读回状态（fail-closed）"
  fi
  LOCK_UID="${LEASE_UID}"
  LOCK_ACQUIRED_AT="${LEASE_ACQUIRE:-${LOCK_REQUEST_ISSUED_AT}}"
  log_info "锁时间锚点：t_req=${LOCK_REQUEST_ISSUED_AT} t_resp=${LOCK_WRITE_CONFIRMED_AT} 服务器 renewTime=${LEASE_RENEW} acquireTime=${LEASE_ACQUIRE}"
  lease_require_window "${mode}-created"

  # 立即续租：把 renewTime 从「请求发出时刻」推到「请求完成之后」。
  if ! lease_renew; then
    log_error "发布锁${mode}后的即时续租失败：无法把 renewTime 推进到本次写入之后（fail-closed）"
    log_error "  不复用一把 renewTime 可能仍停留在 t_req 的锁继续发布：窗口已被请求耗时侵蚀。"
    die 3 "发布锁${mode}后即时续租失败（fail-closed）"
  fi
  if ! lease_read; then
    log_error "即时续租后无法读回服务器状态（${LEASE_READ_ERROR:-未知错误}）：fail-closed"
    die 3 "发布锁${mode}后即时续租无法核验（fail-closed）"
  fi
  LOCK_UID="${LEASE_UID}"
  lease_require_window "${mode}-anchor-renewed"
  log_info "发布锁窗口已锚定在写入完成之后：renewTime=${LEASE_RENEW}（leaseDurationSeconds=${LOCK_LEASE_DURATION}）"
}

acquire_release_lock() {
  local deadline=$(( SECONDS + LOCK_WAIT_TIMEOUT )) remaining
  log_info "获取发布锁 ${NAMESPACE}/${LOCK_NAME}（holder=${HOLDER_IDENTITY}，租期 ${LOCK_LEASE_DURATION}s，续租间隔 ${LOCK_RENEW_INTERVAL}s，续租请求超时 ${LOCK_RENEW_REQUEST_TIMEOUT}s）"

  while :; do
    if lease_create 2> "${LOCK_STATE_DIR}/lease-create.err"; then
      LOCK_HELD=1
      log_info "已获取发布锁（新建 Lease，holder=${HOLDER_IDENTITY}）"
      finalize_lock_acquisition "创建"
      return 0
    fi
    if ! grep -q 'AlreadyExists' "${LOCK_STATE_DIR}/lease-create.err" 2>/dev/null; then
      log_error "创建发布锁失败：$(tr '\n' ' ' < "${LOCK_STATE_DIR}/lease-create.err")"
      return 2
    fi

    if lease_read; then
      if lock_is_free || lease_expired; then
        if lock_is_free; then
          log_info "发布锁处于已释放状态，准备接管（resourceVersion=${LEASE_RV}）"
        else
          log_warn "发布锁已过期（holder=${LEASE_HOLDER}，renewTime=${LEASE_RENEW}）：准备接管"
        fi

        # AUD-01：接管之前必须用真实 Job 对象核验上一轮迁移。
        # 注解只是陈述，不能单独作为「迁移已结束」的依据。
        if ! verify_previous_migration; then
          log_error "拒绝接管发布锁：上一轮迁移的生命周期不允许接管（fail-closed）。"
          log_error "排查：${KUBECTL[*]} -n ${NAMESPACE} get lease ${LOCK_NAME} -o yaml"
          log_error "      ${KUBECTL[*]} -n ${NAMESPACE} get jobs -l ${MIGRATION_JOB_SELECTOR} -o wide"
          log_error "恢复流程见 docs/development/kubernetes.md §10.8；禁止为了让流水线继续而手工清空迁移记录。"
          return 3
        fi

        if lease_takeover; then
          LOCK_HELD=1
          log_info "已获取发布锁（接管，holder=${HOLDER_IDENTITY}，leaseTransitions=$(( $(to_int "${LEASE_TRANSITIONS:-0}") + 1 ))）"
          # 接管的 renewTime 同样锚定在写入完成之后（B-2）：先断言窗口、再立即续租，
          # 之后才允许写迁移记录（否则会用一把窗口已被侵蚀的锁去写记录）。
          finalize_lock_acquisition "接管"
          # 接管保留了上一轮记录；若记录停在 PENDING/RUNNING 而 Job 已终结（或已用
          # 数据库证据恢复），在这里收敛为可核验的终态，避免后续归档一条含糊的记录。
          if ! finalize_previous_migration; then
            log_error "接管后无法收敛上一轮迁移记录（fail-closed）"
            return 3
          fi
          return 0
        fi
        log_warn "接管失败（CAS 冲突）：其他 Runner 抢先"
      else
        remaining=$(( deadline - SECONDS ))
        (( remaining > 0 )) || remaining=0
        log_info "另一个发布正在持有锁（holder=${LEASE_HOLDER}）：等待其释放，剩余 ${remaining}s"
      fi
    else
      log_warn "读取发布锁失败（${LEASE_READ_ERROR:-未知错误}），重试中"
    fi

    (( SECONDS < deadline )) || return 1
    sleep 2
  done
}

# ---------------------------------------------------------------------------
# 迁移生命周期（T2-06.1.1 / AUD-01）
#
# 目标不变量：
#   只要上一轮迁移 Job 尚未进入终态，任何 Runner 都不能「接管发布锁 + 开始新一轮迁移」。
#
# 手段（三层，缺一不可）：
#   1) 持久关联：Lease 注解记录 state/job/job-uid（跨 Runner、跨脚本重启都在）；
#   2) 真实核验：接管前用 Job 对象复核（state、uid、终态、deletionTimestamp）；
#   3) fail-closed：查询失败、记录与事实不一致、Job NotFound 一律拒绝，并要求人工核实。
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Pod 生命周期门禁（T2-06.1.2 / AUD-D3）
#
# Job Failed=True 只说明「Job controller 判定失败」，**不说明执行 DDL 的 Pod 已经停止**：
# backoffLimit 用尽时，最后一个 Pod 仍可能处于 Running（卡在 DDL、kubelet 尚未终止）。
# 此时若允许接管并创建 Job B，Pod A 与 Pod B 的 DDL 可能重叠 —— 数据库 GET_LOCK 也不
# 一定挡得住（Pod A 可能已持有锁、也可能在等锁，语义不可依赖）。
#
# 因此「允许开始新一轮迁移」的判据不是 Job 条件，而是**关联 Pod 全部终态**：
#   Running / Pending / Unknown / 空 phase / 带 deletionTimestamp → 拒绝（fail-closed）
#   Succeeded / Failed                                            → 允许
#   Pod 查询失败 / 无法归属到该 Job                                → 拒绝（fail-closed）
#
# 归属判定用 ownerReferences 里的 Job uid，而不是仅凭 job-name 标签。
# ---------------------------------------------------------------------------

# 读某个 Job 的 Pod（job-name 标签 + ownerReferences）。成功输出每行
# name~phase~deletionTimestamp~ownerKind~ownerUID；读取失败返回非零并写入
# MIGRATION_PODS_ERROR（保留原始错误，消息里必须体现「查询失败 ≠ 没有 Pod」）。
#
# 结果通过全局变量 MIGRATION_PODS_ROWS 返回，**不要**写成 `rows="$(f ...)"`：
# 命令替换会开子 shell，函数里设置的 MIGRATION_PODS_ERROR 会丢失，错误信息退化成
# 「未知错误」——那样「403 还是 500」就无法体现在输出里（F-07 的可读性要求）。
read_migration_pods_for_job() {
  local job="$1" err="${TMP_DIR}/get-migration-pods.err" out
  MIGRATION_PODS_ROWS=""
  MIGRATION_PODS_ERROR=""
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "job-name=${job}" \
    -o "go-template=${MIGRATION_PODS_TEMPLATE}" 2>"${err}")"; then
    MIGRATION_PODS_ROWS="$(strip_template_noise "${out}")"
    return 0
  fi
  MIGRATION_PODS_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  return 1
}

# 全命名空间迁移 Pod 扫描：不依赖 Job 对象是否还存在（覆盖「Job 被删除、Pod 被孤立」）。
#
# 返回码（T2-06.1.3 / F-01 / F-07）：结果必须分成**三态**，调用方不得合并前两者以外的情形：
#   0 = 查询成功，没有活动 Pod（Pod 不存在，或已全部 Succeeded/Failed）
#   1 = 查询成功，**确实存在**仍在执行/终止中的 Pod（逐条打印名称与 phase）
#   2 = 查询失败，**无法确认**（≠ 存在活动 Pod，也 ≠ 已终止）
#
# 旧实现把第 2 种也返回 1，调用方于是输出「仍有迁移 Pod 未终止」—— 那是在描述一个
# 并未被观察到的事实。403/网络错误/超时只能说明「不知道」。
scan_migration_pods() {
  local err="${TMP_DIR}/list-migration-pods.err" out line name phase deleting kind owner active=0
  MIGRATION_PODS_ERROR=""
  if ! out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "${MIGRATION_POD_SELECTOR}" \
    -o "go-template=${MIGRATION_PODS_TEMPLATE}" 2>"${err}")"; then
    MIGRATION_PODS_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
    log_error "无法列出迁移 Pod（${MIGRATION_POD_SELECTOR}）：${MIGRATION_PODS_ERROR:-未知错误}"
    log_error "  这是「无法确认迁移 Pod 状态」，**不是**「Pod 确实还在运行」，也不是「Pod 已终止」。"
    log_error "  常见原因：pods 的 get/list 权限缺失（403）、API Server 5xx、请求超时。按 fail-closed 处理。"
    return 2
  fi
  out="$(strip_template_noise "${out}")"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    IFS='~' read -r name phase deleting kind owner <<< "${line}"
    if [[ -n "${deleting}" || -z "${phase}" || "${phase}" == "Running" \
          || "${phase}" == "Pending" || "${phase}" == "Unknown" ]]; then
      log_error "发现仍在执行/终止中的迁移 Pod/${name:-<未知>}（phase=${phase:-<空>}${deleting:+ deletionTimestamp=${deleting}}${owner:+ ownerJobUID=${owner}}）"
      active=1
    fi
  done <<< "${out}"
  if (( active == 0 )); then
    return 0
  fi
  return 1
}

# 报告 Pod 扫描结论（统一措辞，避免把「无法确认」说成「确实存在」）。
#   $1 = scan_migration_pods 的返回码   $2 = 上下文（拒绝接管 / 拒绝开始新一轮迁移）
report_migration_pod_scan() {
  case "${1}" in
    0)
      return 0
      ;;
    1)
      log_error "${2}：**确实存在**仍在执行/终止中的迁移 Pod（见上方 Pod 名称与 phase）"
      ;;
    *)
      log_error "${2}：**无法确认**迁移 Pod 状态（查询失败：${MIGRATION_PODS_ERROR:-未知错误}）"
      log_error "  不得据此认为 Pod 已终止，也不得据此认为 Pod 存在；两种情况都 fail-closed。"
      ;;
  esac
}

# 判断某 Job 的 Pod 是否全部终止；返回 0 = 允许开始新一轮迁移。
# 设置 POD_GATE_CODE（0 允许 / 1 未终止 / 2 查询失败 / 3 归属不一致 / 4 phase 无法识别）
# 与 POD_GATE_SUMMARY。
#
# 1 是「观察到活动 Pod」；2/3/4 是「无法确认」（查询失败、归属无法证明、phase 不可识别）。
# 调用方必须用 report_pod_gate 输出，措辞不得互换（F-07）。
migration_pod_gate() {
  local job="$1" expected_uid="$2" rows line name phase deleting kind owner
  POD_GATE_CODE=0
  POD_GATE_SUMMARY=""
  if ! read_migration_pods_for_job "${job}"; then
    POD_GATE_CODE=2
    POD_GATE_SUMMARY="Pod 查询失败，无法确认 Pod 状态（${MIGRATION_PODS_ERROR:-未知错误}）"
    return 2
  fi
  rows="${MIGRATION_PODS_ROWS}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    IFS='~' read -r name phase deleting kind owner <<< "${line}"
    if [[ -z "${kind}" || -z "${owner}" ]]; then
      POD_GATE_CODE=3
      POD_GATE_SUMMARY="无法确认 Pod 归属：Pod/${name} 没有 Job ownerReferences，不能证明它属于 Job/${job}"
      return 3
    fi
    if [[ -n "${expected_uid}" && "${owner}" != "${expected_uid}" ]]; then
      POD_GATE_CODE=3
      POD_GATE_SUMMARY="无法确认 Pod 归属：Pod/${name} 的 ownerReferences Job uid=${owner} 与 Job/${job} 的 uid=${expected_uid} 不一致"
      return 3
    fi
    if [[ -n "${deleting}" ]]; then
      POD_GATE_CODE=1
      POD_GATE_SUMMARY="Pod/${name} 正在终止（phase=${phase:-<空>}，deletionTimestamp=${deleting}）"
      return 1
    fi
    case "${phase}" in
      Succeeded|Failed) : ;;
      Running|Pending|Unknown|"")
        POD_GATE_CODE=1
        POD_GATE_SUMMARY="Pod/${name} 仍为 ${phase:-<未知>}（未终止，可能仍在执行 DDL）"
        return 1
        ;;
      *)
        POD_GATE_CODE=4
        POD_GATE_SUMMARY="Pod/${name} 的 phase=${phase} 无法识别，无法确认其是否已终止"
        return 4
        ;;
    esac
  done <<< "${rows}"
  return 0
}

# 输出 Pod 门禁结论：必须区分「观察到未终止的 Pod」（1）与「无法确认」（2/3/4）。
report_pod_gate() {
  case "${POD_GATE_CODE}" in
    0)
      return 0
      ;;
    1)
      log_error "Pod 未确认终止：${POD_GATE_SUMMARY}"
      ;;
    *)
      # 2/3/4 都不代表「Pod 确实还在运行」：它们代表「我们无法证明它已经停了」。
      # 结论相同（fail-closed），但事实描述必须不同，否则会误导值班人员去杀 Pod。
      log_error "无法确认 Pod 状态（这是「未知」，不是「Pod 确实还在运行」）：${POD_GATE_SUMMARY}"
      ;;
  esac
}

# ---------------------------------------------------------------------------
# 数据库证据恢复（T2-06.1.2 / AUD-D4）
#
# 场景：Lease 记录为 PENDING/RUNNING，UID 为空（apply 后、回写 UID 前崩溃），Job 已不存在。
# 旧实现只会把记录当成「不可核验」拒绝，而 runbook 让人「把 state 改成 COMPLETE」——
# 改了仍然因为 uid 为空被拒；直接复位 NO_MIGRATION 又会丢掉「到底执行到哪」的证据。
#
# 现在只接受一种机器可校验的证据（由人工从只读的 `my-shop migrate version` 抄录）：
#   version=<14位时间戳> dirty=<true|false> latest=<14位时间戳>
#
# 判定（不猜、不 force、不伪造 UID、不改数据库）：
#   dirty=true              → 拒绝（MySQL DDL 隐式提交，可能已部分生效，必须人工核对）
#   version > latest        → 拒绝（数据库比本次构建更新，方向不一致）
#   version == latest       → 账本已到最新且干净 → 上一轮按 COMPLETE 记录
#   version <  latest（>0） → 账本落后且干净 → 上一轮没有完成 → 归档并复位 NO_MIGRATION
#   version == 0            → 拒绝（无法区分「全新空库」与「未纳管的既有库」）
#
# 两种结论都**不会跳过迁移**：接管后仍会为目标版本创建 Job 执行 migrate up。
# 证据只决定「上一轮的记录如何收敛」，不改变实际执行的内容。
# ---------------------------------------------------------------------------
parse_recovery_evidence() {
  local evidence="${1:-}"
  RECOVERY_VERSION=""; RECOVERY_DIRTY=""; RECOVERY_LATEST=""
  [[ "${evidence}" =~ ${RECOVERY_EVIDENCE_PATTERN} ]] || return 1
  RECOVERY_VERSION="${BASH_REMATCH[1]}"
  RECOVERY_DIRTY="${BASH_REMATCH[2]}"
  RECOVERY_LATEST="${BASH_REMATCH[3]}"
  return 0
}

# 打印可执行的人工恢复步骤（每条命令都能真正完成恢复，不再给出「改了也没用」的路径）。
print_recovery_instructions() {
  local state="$1" job="$2" uid="$3"
  log_error "锁上的迁移记录无法自动核验（state=${state} job=${job:-<空>} uid=${uid:-<空>}）。"
  log_error "**不得据此假定迁移已完成，也不要把 state 直接改成 ${MIG_STATE_COMPLETE}**："
  log_error "  终态记录缺少 Job uid 时必须附数据库证据，否则脚本仍然会拒绝（这是刻意的）。"
  log_error "人工恢复步骤（docs/development/kubernetes.md §10.8）："
  log_error "  1) 只读读取数据库事实（必须用与目标版本相同的提交构建，否则 latest 不可比）："
  log_error "       make migrate-version"
  log_error "     输出形如：version: 20261001000024 (dirty=false)"
  log_error "               latest available: 20261001000024（已是最新）"
  log_error "  2) 若 dirty=true：**停止自动恢复**，按 docs/development/migrations.md §7 人工核对"
  log_error "     schema（MySQL DDL 隐式提交，失败时可能已部分生效）；脚本不会 force。"
  log_error "  3) 若 dirty=false：把证据写到锁上（只加两个注解，不改 state）："
  log_error "       ${KUBECTL[*]} -n ${NAMESPACE} annotate lease ${LOCK_NAME} --overwrite \\"
  log_error "         ${LEASE_ANN_RECOVERY_METHOD}=${RECOVERY_METHOD_DATABASE} \\"
  log_error "         '${LEASE_ANN_RECOVERY_EVIDENCE}=version=<version> dirty=false latest=<latest>'"
  log_error "  4) 确认没有迁移 Pod 仍在运行："
  log_error "       ${KUBECTL[*]} -n ${NAMESPACE} get pods -l ${MIGRATION_POD_SELECTOR} -o wide"
  log_error "  5) 重跑发布：脚本按证据收敛上一轮记录（归档到 migration-previous-*），"
  log_error "     然后为新目标版本创建 Job 执行 migrate up（不会跳过迁移）。"
  log_error "  6) 若证据显示 dirty=true / 无法取得干净证据：不要改 state，先人工核对数据库；"
  log_error "     强行清空记录等于允许下一轮在没有证据的情况下执行 DDL。"
  log_error "  不要用 migrate force 绕过：force 只改版本记录、不执行也不校验 SQL，"
  log_error "     会把从未执行过的迁移永久标记为「已应用」，后果不可自动修复。"
}

# 用证据收敛记录。返回 0 = 允许接管；1 = 拒绝（fail-closed）。
# 成功时设置 PREV_FINAL_*（接管后由 finalize_previous_migration 写入）。
recovery_resolve_record() {
  local state="$1" job="$2" uid="$3" pod_scan_rc=0
  # 先拦住仍在执行的 Pod：Job 不存在 ≠ Pod 已停止。
  # F-07：查询失败与「确实有活动 Pod」必须分开报告，但两者都拒绝接管。
  scan_migration_pods || pod_scan_rc=$?
  if (( pod_scan_rc != 0 )); then
    report_migration_pod_scan "${pod_scan_rc}" "上一轮 Job/${job:-<无>} 已不存在，拒绝接管"
    return 1
  fi

  if [[ "${LEASE_MIG_RECOVERY_METHOD:-}" != "${RECOVERY_METHOD_DATABASE}" ]]; then
    log_error "记录缺少数据库证据：${LEASE_ANN_RECOVERY_METHOD}=\"${LEASE_MIG_RECOVERY_METHOD:-<空>}\"（需要 ${RECOVERY_METHOD_DATABASE}）"
    print_recovery_instructions "${state}" "${job}" "${uid}"
    return 1
  fi

  if ! parse_recovery_evidence "${LEASE_MIG_RECOVERY_EVIDENCE:-}"; then
    log_error "数据库证据格式无法解析：\"${LEASE_MIG_RECOVERY_EVIDENCE:-<空>}\""
    log_error "  期望格式：version=<14位时间戳> dirty=<true|false> latest=<14位时间戳>"
    print_recovery_instructions "${state}" "${job}" "${uid}"
    return 1
  fi

  local evidence="version=${RECOVERY_VERSION} dirty=${RECOVERY_DIRTY} latest=${RECOVERY_LATEST}"
  log_info "读取到数据库证据：${evidence}"

  if [[ "${RECOVERY_DIRTY}" == "true" ]]; then
    log_error "证据显示 dirty=true（上一次 migration 曾失败）：**拒绝自动恢复**。"
    log_error "  MySQL DDL 隐式提交，失败时可能已部分生效；脚本不会 force，也不会推断哪些语句生效过。"
    log_error "  按 docs/development/migrations.md §7 人工核对 schema 后，再由人工决定标记版本。"
    return 1
  fi
  if (( 10#${RECOVERY_VERSION} > 10#${RECOVERY_LATEST} )); then
    log_error "证据显示数据库版本 ${RECOVERY_VERSION} 比本次构建的最新迁移 ${RECOVERY_LATEST} 更新："
    log_error "  说明数据库已被更新的构建迁移过，本次发布方向不一致（可能降级）：拒绝恢复。"
    return 1
  fi
  if (( 10#${RECOVERY_VERSION} == 0 )); then
    log_error "证据显示 version=0（schema_migrations 不存在或为空）："
    log_error "  无法区分「全新空库」与「已有表但未纳管的库」。后者直接 migrate up 会因表已存在而失败并留下 dirty。"
    log_error "  请先确认数据库是否已有业务表；确认是空库后由人工决定是否复位记录（见 §10.8）。"
    return 1
  fi

  PREV_FINAL_METHOD="${RECOVERY_METHOD_DATABASE}"
  PREV_FINAL_EVIDENCE="${evidence}"
  if (( 10#${RECOVERY_VERSION} == 10#${RECOVERY_LATEST} )); then
    if [[ "${state}" == "${MIG_STATE_COMPLETE}" || "${state}" == "${MIG_STATE_FAILED}" ]]; then
      # 已经是终态：记录保持原状态（不做新的断言），补记证据来源。
      PREV_FINAL_STATE=""
      PREV_FINAL_DETAIL=""
      log_info "证据与终态记录一致（数据库已到 ${RECOVERY_LATEST} 且干净）：允许接管"
    else
      PREV_FINAL_STATE="${MIG_STATE_COMPLETE}"
      PREV_FINAL_DETAIL="接管时按数据库证据确认：${evidence}"
      log_warn "证据显示数据库已到最新版本且 dirty=false：把上一轮记录收敛为 ${MIG_STATE_COMPLETE}（不伪造 Job uid）"
    fi
    return 0
  fi

  # version < latest：账本干净但落后 ⇒ 上一轮没有把迁移执行完。
  PREV_FINAL_STATE="${MIG_STATE_NONE}"
  PREV_FINAL_DETAIL="数据库证据表明上一轮未完成（${evidence}）"
  log_warn "证据显示数据库版本落后（${RECOVERY_VERSION} < ${RECOVERY_LATEST}）且 dirty=false："
  log_warn "  上一轮没有完成迁移；归档该记录后复位 ${MIG_STATE_NONE}，由新一轮 Job 继续执行 migrate up（不跳过任何迁移）。"
  return 0
}

# 写迁移记录：CAS 写入 Lease 注解。失败返回非零（调用方必须 fail-closed）。
#
# T2-06.1.4 / B-3：**所有**写 Lease 的路径（create / takeover / renew / record /
# release）都必须先验证「自己仍持有**且**未过期」。旧实现只有续租路径验证过期，
# 记录写入只比 holder，于是失锁（过期）后仍能写入并给过期锁续命一个租期。
#
# 边界（不得夸大）：本函数只能阻止**新的**写入。调用方在进入本函数之前可能已经发出
# 不可撤销的 kubectl 请求（apply / create job），那些请求是否生效只能事后核验。
# AUD-02 = NOT_CLOSED、AUD-05 = NOT_CLOSED —— 这里不是 fencing，也不是在途写入的解决方案。
#
#   $1 state  $2 job  $3 uid  $4 detail
#   $5 archive（1 = 把服务器上的当前记录归档到 previous-*，用于「开始新一轮迁移」）
#   $6 terminal（auto = 终态记录写 terminal-at；unconfirmed = 状态来自 Job 但 Pod 未确认终止）
#   $7 recovery_method  $8 recovery_evidence
migration_record_write() {
  local state="$1" job="$2" uid="$3" detail="$4"
  local archive="${5:-0}" terminal_flag="${6:-auto}" method="${7:-}" evidence="${8:-}"
  case "${state}" in
    "${MIG_STATE_NONE}"|"${MIG_STATE_PENDING}"|"${MIG_STATE_RUNNING}"|"${MIG_STATE_COMPLETE}"|"${MIG_STATE_FAILED}") ;;
    *) log_error "内部错误：未知迁移状态 ${state}"; return 1 ;;
  esac
  local attempt now terminal_at=""
  # 重试是必需的：后台续租随时可能用掉当前的 resourceVersion，第一次 CAS 会 409。
  # 重试时重新读取 rv 并重新确认 holder —— 只要 holder 变成别人，立刻按「失锁」处理。
  # 退避必须明显短于续租间隔，否则每次重试都会重新撞上续租写入。
  for attempt in 1 2 3 4 5; do
    if ! lease_read; then
      log_error "写迁移记录失败：无法读取发布锁（${LEASE_READ_ERROR:-未知错误}）"
      return 1
    fi
    if [[ "${LEASE_HOLDER}" != "${HOLDER_IDENTITY}" ]]; then
      : > "${LOCK_STATE_DIR}/lost"
      log_error "写迁移记录失败：发布锁已不属于本 Runner（holder=${LEASE_HOLDER:-<空>}）"
      return 3
    fi
    if lease_expired; then
      # T2-06.1.4 / B-3：这是审计点名的缺陷 —— 旧实现只比 holder，于是「已经过期、
      # 别人随时可以接管」的锁仍然能被写入迁移记录，而那次写入把 renewTime 推到 now，
      # 等于给一把过期锁续命一个租期：接管方随后会读到"刚刚续过"的假象。
      # 写记录的路径必须与续租路径用**同一条**过期判据（服务器返回的 renewTime）。
      : > "${LOCK_STATE_DIR}/lost"
      lease_window_evidence "record-write-${attempt}" >&2 || true
      log_error "拒绝写迁移记录：本 Runner 的租约已过期（renewTime=${LEASE_RENEW:-<空>}，leaseDurationSeconds=${LEASE_DURATION:-<空>}）"
      log_error "  其他 Runner 此刻已可合法接管；写入会给过期锁续命一个租期（B-3），因此 fail-closed。"
      log_error "  这不会撤销已经发生的迁移/应用请求，只会阻止本 Runner 继续制造新的写入。"
      return 3
    fi
    now="$(timestamp_utc)"
    # 先从服务器记录继承 prev-*：任何重试/冲突都不会丢上一轮归档。
    lease_record_from_lease
    REC_STATE="${state}"
    REC_JOB="${job}"
    REC_JOB_UID="${uid}"
    REC_DETAIL="${detail}"
    REC_UPDATED_AT="${now}"
    REC_RECOVERY_METHOD="${method}"
    REC_RECOVERY_EVIDENCE="${evidence}"
    case "${state}" in
      "${MIG_STATE_COMPLETE}"|"${MIG_STATE_FAILED}")
        if [[ "${terminal_flag}" == "unconfirmed" ]]; then
          # Job 条件为终态，但 Pod 未确认终止：不能声称「已终结」。
          REC_TERMINAL_AT=""
          REC_RECOVERY_METHOD=""
          REC_RECOVERY_EVIDENCE=""
        elif [[ "${LEASE_MIG_STATE}" == "${state}" && "${LEASE_MIG_JOB}" == "${job}" \
                && "${LEASE_MIG_UID}" == "${uid}" && -n "${LEASE_MIG_TERMINAL_AT}" ]]; then
          REC_TERMINAL_AT="${LEASE_MIG_TERMINAL_AT}"
        else
          REC_TERMINAL_AT="${now}"
        fi
        if [[ -z "${REC_RECOVERY_METHOD}" && "${terminal_flag}" != "unconfirmed" ]]; then
          REC_RECOVERY_METHOD="job-object"
        fi
        ;;
      *)
        REC_TERMINAL_AT=""
        ;;
    esac

    # 归档上一轮：仅当「服务器上的记录与本次要写的记录不同」且记录有意义时。
    # 这样重试（第一次其实已经写入成功、只是客户端超时）不会把自己的记录归档掉。
    if (( archive )) && [[ -n "${LEASE_MIG_JOB}${LEASE_MIG_TERMINAL_AT}" \
          || "${LEASE_MIG_STATE}" == "${MIG_STATE_COMPLETE}" \
          || "${LEASE_MIG_STATE}" == "${MIG_STATE_FAILED}" ]] \
       && ! [[ "${LEASE_MIG_STATE}" == "${state}" && "${LEASE_MIG_JOB}" == "${job}" \
              && "${LEASE_MIG_UID}" == "${uid}" ]]; then
      REC_PREV_STATE="${LEASE_MIG_STATE}"
      REC_PREV_JOB="${LEASE_MIG_JOB}"
      REC_PREV_JOB_UID="${LEASE_MIG_UID}"
      REC_PREV_DETAIL="${LEASE_MIG_DETAIL}"
      REC_PREV_TERMINAL_AT="${LEASE_MIG_TERMINAL_AT}"
      # 恢复证据必须随归档保留：当前记录会被新迁移覆盖，证据来源只能留在 previous-*。
      if [[ "${method}" == "${RECOVERY_METHOD_DATABASE}" && -n "${evidence}" ]]; then
        REC_PREV_DETAIL="${REC_PREV_DETAIL:+${REC_PREV_DETAIL}；}恢复判定(${method}): ${detail}"
      fi
      log_info "归档上一轮迁移记录：state=${REC_PREV_STATE} job=${REC_PREV_JOB:-<无>} uid=${REC_PREV_JOB_UID:-<无>} → ${LEASE_ANN_PREV_STATE}"
    fi

    MIG_STATE="${state}"
    MIG_JOB="${job}"
    MIG_JOB_UID="${uid}"
    MIG_DETAIL="${detail}"
    terminal_at="${REC_TERMINAL_AT}"

    lease_manifest "${HOLDER_IDENTITY}" "${LEASE_ACQUIRE:-${LOCK_ACQUIRED_AT}}" "${now}" \
      "${LOCK_LEASE_DURATION}" "$(to_int "${LEASE_TRANSITIONS:-0}")" "${LEASE_RV}" "${LEASE_UID}" \
      > "${LOCK_STATE_DIR}/lease-record.yaml"
    if "${LEASE_KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-record.yaml" \
        >/dev/null 2>"${LOCK_STATE_DIR}/lease-record.err"; then
      LOCK_UID="${LEASE_UID}"
      log_info "发布锁迁移记录已更新：state=${state} job=${job:-<无>} uid=${uid:-<无>}${detail:+ detail=${detail}}${terminal_at:+ terminal-at=${terminal_at}}"
      return 0
    fi

    log_warn "写迁移记录失败（第 ${attempt} 次）：$(tr '\n' ' ' < "${LOCK_STATE_DIR}/lease-record.err" 2>/dev/null || true)"
    # 409 可能是「别人接管了锁」：再读一次确认。确认失锁时按退出码 3 处理，
    # 不要把它伪装成「生命周期状态不明」。
    if lease_read && [[ "${LEASE_HOLDER}" != "${HOLDER_IDENTITY}" ]]; then
      : > "${LOCK_STATE_DIR}/lost"
      log_error "写迁移记录失败：发布锁已被 ${LEASE_HOLDER:-<空>} 接管"
      return 3
    fi
    sleep 0.2
  done
  log_error "写迁移记录连续失败：无法把迁移生命周期同步到发布锁（fail-closed）"
  return 1
}

# 记录迁移状态；失败时区分「失锁（3）」与「状态不明（7）」，都 fail-closed。
record_migration_state() {
  local state="$1" job="$2" uid="$3" detail="$4" message="$5" rc=0
  migration_record_write "${state}" "${job}" "${uid}" "${detail}" || rc=$?
  if (( rc == 3 )); then
    die 3 "写迁移记录时发现发布锁已不属于本 Runner（fail-closed）：${message}"
  fi
  (( rc == 0 )) || die 7 "${message}"
}

# 读取指定 Job 的真实状态。
# 成功返回 0 并设置 JOB_PROBE_STATE ∈ complete|failed|terminating|active|absent；
# 读取失败（网络/权限/5xx）返回非零并设置 JOB_PROBE_STATE=error。
job_probe() {
  local name="$1" err="${TMP_DIR}/job-probe.err" out
  JOB_UID=""; JOB_COMPLETE=""; JOB_FAILED=""; JOB_REASON=""; JOB_MESSAGE=""
  JOB_PROBE_STATE=""; JOB_PROBE_ERROR=""
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get job "${name}" \
    -o "go-template=${JOB_STATE_TEMPLATE}" 2>"${err}")"; then
    out="$(strip_template_noise "${out}")"
    local deleting
    IFS='~' read -r JOB_UID deleting JOB_COMPLETE JOB_FAILED JOB_REASON JOB_MESSAGE <<< "${out}"
    JOB_UID="${JOB_UID:-}"
    if [[ -n "${deleting}" ]]; then
      JOB_PROBE_STATE="terminating"
    elif [[ "${JOB_COMPLETE}" == "True" ]]; then
      JOB_PROBE_STATE="complete"
    elif [[ "${JOB_FAILED}" == "True" ]]; then
      JOB_PROBE_STATE="failed"
    else
      JOB_PROBE_STATE="active"
    fi
    return 0
  fi
  if is_not_found_for "${err}" "jobs" "${name}"; then
    JOB_PROBE_STATE="absent"
    return 0
  fi
  JOB_PROBE_STATE="error"
  JOB_PROBE_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  return 1
}

# 列出命名空间内所有迁移 Job：每行 name~uid~deletionTimestamp~Complete~Failed。
#
# 结果通过全局变量 MIGRATION_LIST_ROWS 返回，**不要**写成 `rows="$(job_list_migrations)"`：
# 命令替换会开子 shell，函数里设置的 MIGRATION_LIST_ERROR / MIGRATION_LIST_CLASS 会丢失，
# 错误信息退化成「未知错误」——那样「403 还是 500」就无法体现在输出里（审计 D-3）。
job_list_migrations() {
  local err="${TMP_DIR}/jobs-list.err" out
  MIGRATION_LIST_ROWS=""
  MIGRATION_LIST_ERROR=""
  MIGRATION_LIST_CLASS=""
  if ! out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get jobs -l "${MIGRATION_JOB_SELECTOR}" \
    -o "go-template=${JOBS_STATE_TEMPLATE}" 2>"${err}")"; then
    # 审计 D-3：查询失败必须保留 stderr 并分类，否则调用方会把「读不到」说成
    # 「存在未终结的 Job」（或反过来当成「没有 Job」）。
    classify_api_error "${err}" "jobs" ""
    MIGRATION_LIST_CLASS="${API_ERROR_CLASS}"
    MIGRATION_LIST_ERROR="${API_ERROR_TEXT:-未知错误}"
    return 1
  fi
  MIGRATION_LIST_ROWS="$(strip_template_noise "${out}")"
}

# 扫描「没有任何未终结的迁移 Job」。
#   返回 0 = 干净（查询成功且没有未终结 Job）
#   返回 1 = **确实存在**未终结的迁移 Job（观察到的对象，不是推断）
#   返回 2 = **无法确认**（查询失败：Forbidden / 5xx / 超时），fail-closed
#
# 三态必须分开（审计 D-3）：把 2 说成 1 是在描述一个没有被观测到的事实，
# 会把排查方向从「权限/网络」误导到「有一个卡住的 Job」。
#
# 这是注解记录之外的第二道防线：即使上一轮 Runner 来不及写注解（升级前的老版本、
# 手工 kubectl apply），只要集群里还有未终结的迁移 Job，就不允许开始新一轮迁移。
scan_active_migrations() {
  local rows line name uid deleting complete failed found=0
  if ! job_list_migrations; then
    log_error "无法列出命名空间 ${NAMESPACE} 的迁移 Job（分类：$(api_error_class_label "${MIGRATION_LIST_CLASS}")）：${MIGRATION_LIST_ERROR:-未知错误}"
    if [[ "${MIGRATION_LIST_CLASS}" == "forbidden" ]]; then
      log_error "  这是**权限不足**（jobs 的 list 缺失）：**不是**「存在未终结的 Job」，**也不是**「没有 Job」。"
    else
      log_error "  这是**查询失败**：不能证明没有迁移正在执行，也不能证明存在迁移。按 fail-closed 处理。"
    fi
    return 2
  fi
  rows="${MIGRATION_LIST_ROWS}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    IFS='~' read -r name uid deleting complete failed <<< "${line}"
    if [[ "${complete}" != "True" && "${failed}" != "True" ]]; then
      log_error "发现未终结的迁移 Job/${name:-<未知>}（uid=${uid:-<空>}${deleting:+ deletionTimestamp=${deleting}}）：不允许开始新一轮迁移"
      found=1
    fi
  done <<< "${rows}"
  (( found == 0 ))
}

# 终态记录（COMPLETE/FAILED）的核验。
verify_terminal_record() {
  local state="$1" job="$2" uid="$3"
  if [[ -z "${job}" || -z "${uid}" ]]; then
    log_warn "上一轮终态记录不完整（state=${state} job=${job:-<空>} uid=${uid:-<空>}）：需要数据库证据才能确认"
    recovery_resolve_record "${state}" "${job}" "${uid}" || return 1
    return 0
  fi
  if ! job_probe "${job}"; then
    log_error "无法读取上一轮 Job/${job}（${JOB_PROBE_ERROR:-未知错误}）：状态未知，拒绝接管"
    return 1
  fi
  case "${JOB_PROBE_STATE}" in
    absent)
      # 终态 + uid 是「写入记录时用真实对象确认过」的产物，TTL 清理后对象消失不算新证据。
      if ! migration_pod_gate "${job}" "${uid}"; then
        log_error "上一轮 Job/${job} 已不存在，但其 Pod 状态不允许开始新一轮迁移（拒绝接管）："
        report_pod_gate
        return 1
      fi
      log_warn "上一轮 Job/${job} 已不存在（多为 ttlSecondsAfterFinished 清理）：记录为 ${state} 且 UID 曾核验，允许接管"
      return 0
      ;;
    complete|failed)
      if [[ "${JOB_UID}" != "${uid}" ]]; then
        log_error "上一轮 Job/${job} 的 uid=${JOB_UID:-<空>} 与锁记录 ${uid} 不一致：同名 Job 被删除重建，拒绝接管"
        return 1
      fi
      # AUD-D3：Job 终态不等于 Pod 已停止执行；必须逐个 Pod 确认。
      # F-07：门禁结论要区分「确实有 Pod 未终止」与「无法确认」（查询失败/归属不明）。
      if ! migration_pod_gate "${job}" "${uid}"; then
        log_error "上一轮 Job/${job} 已 ${JOB_PROBE_STATE}，但 Pod 状态不允许开始新一轮迁移（拒绝接管）："
        report_pod_gate
        log_error "  **Job ${JOB_PROBE_STATE} 不等价于「Pod 已停止执行」**：Job 条件只说明 controller 的判定，"
        log_error "  不得据此假定 DDL 已经停止；此时创建新一轮 Job 会让两个 Pod 的 DDL 重叠。"
        log_error "  处置：等待 Pod 真正终止后重跑；不要删除 Pod/Job 来「让流程过」。"
        return 1
      fi
      log_info "上一轮 Job/${job} 已终态（${JOB_PROBE_STATE}）且 Pod 已终止：允许接管"
      if [[ "${state}" == "${MIG_STATE_FAILED}" && "${JOB_PROBE_STATE}" == "complete" ]]; then
        log_warn "锁记录为 ${MIG_STATE_FAILED}，但 Job 当前为 Complete：记录与事实不一致，需人工核实（本次不据此放行任何补偿动作）"
      fi
      return 0
      ;;
    *)
      log_error "上一轮 Job/${job} 当前为 ${JOB_PROBE_STATE}（未终态）：拒绝接管，等待其结束"
      return 1
      ;;
  esac
}

# 非终态记录（PENDING/RUNNING）的核验。
verify_inflight_record() {
  local state="$1" job="$2" uid="$3"
  if [[ -z "${job}" ]]; then
    log_warn "上一轮记录为 ${state} 但没有 Job 名：只能用数据库证据恢复"
    recovery_resolve_record "${state}" "" "${uid}" || return 1
    return 0
  fi
  if ! job_probe "${job}"; then
    log_error "无法读取上一轮 Job/${job}（${JOB_PROBE_ERROR:-未知错误}）：状态未知，拒绝接管"
    return 1
  fi
  case "${JOB_PROBE_STATE}" in
    absent)
      log_error "锁记录指向 Job/${job}（uid=${uid:-<未记录>}，state=${state}），但该 Job 不存在。"
      log_error "**不得据此假定迁移已经完成**：可能被提前删除，也可能 DDL 已在数据库侧生效。"
      recovery_resolve_record "${state}" "${job}" "${uid}" || return 1
      return 0
      ;;
    active|terminating)
      log_error "上一轮迁移 Job/${job} 仍为 ${JOB_PROBE_STATE}（uid=${JOB_UID:-<空>}，未终态）：拒绝接管"
      log_error "  迁移可能仍在执行 DDL；等待其进入 Complete/Failed 后重跑发布。"
      return 1
      ;;
  esac
  if [[ -n "${uid}" && "${JOB_UID}" != "${uid}" ]]; then
    log_error "上一轮 Job/${job} 的 uid=${JOB_UID:-<空>} 与锁记录 ${uid} 不一致：同名 Job 被删除重建，拒绝接管"
    return 1
  fi
  if ! migration_pod_gate "${job}" "${JOB_UID}"; then
    log_error "上一轮 Job/${job} 已 ${JOB_PROBE_STATE}，但 Pod 状态不允许开始新一轮迁移（拒绝接管）："
    report_pod_gate
    return 1
  fi
  # 记录停在 PENDING/RUNNING 但 Job 已经终结：接管后先把记录收敛为终态，
  # 避免「开始新一轮迁移」时把一条未确认的记录归档掉了。
  if [[ "${JOB_PROBE_STATE}" == "complete" ]]; then
    PREV_FINAL_STATE="${MIG_STATE_COMPLETE}"
  else
    PREV_FINAL_STATE="${MIG_STATE_FAILED}"
  fi
  PREV_FINAL_DETAIL="接管时核验：Job ${JOB_PROBE_STATE}，Pod 已终止"
  log_info "上一轮迁移 Job/${job} 已终态（${JOB_PROBE_STATE}）且 Pod 已终止：允许接管（脚本不会自动 force）"
  return 0
}

# 接管前的核验：返回 0 = 允许接管，1 = 拒绝（fail-closed）。
#
# 顺序很重要：先用真实 Job 对象核验，再（仅在记录无法自证时）接受数据库证据。
verify_previous_migration() {
  local state="${LEASE_MIG_STATE:-${MIG_STATE_NONE}}"
  local job="${LEASE_MIG_JOB:-}" uid="${LEASE_MIG_UID:-}" pod_scan_rc=0
  PREV_FINAL_STATE=""; PREV_FINAL_DETAIL=""; PREV_FINAL_METHOD=""; PREV_FINAL_EVIDENCE=""
  log_info "核验上一轮迁移关联：state=${state} job=${job:-<无>} uid=${uid:-<无>}"

  case "${state}" in
    "${MIG_STATE_NONE}")
      # 没有记录不等于没有迁移：注解可能丢、上一轮可能是升级前的老 Runner。
      if ! scan_active_migrations; then
        log_error "上一轮没有迁移记录，但集群里存在未终结的迁移 Job：拒绝接管"
        return 1
      fi
      # F-07：Pod 扫描的三种结果必须分开描述（干净 / 确实有活动 Pod / 无法确认）。
      scan_migration_pods || pod_scan_rc=$?
      if (( pod_scan_rc != 0 )); then
        report_migration_pod_scan "${pod_scan_rc}" "上一轮没有迁移记录，拒绝接管"
        return 1
      fi
      return 0
      ;;
    "${MIG_STATE_COMPLETE}"|"${MIG_STATE_FAILED}")
      verify_terminal_record "${state}" "${job}" "${uid}"
      ;;
    "${MIG_STATE_PENDING}"|"${MIG_STATE_RUNNING}")
      verify_inflight_record "${state}" "${job}" "${uid}"
      ;;
    *)
      log_error "锁上的迁移状态 ${state} 无法识别：拒绝接管（fail-closed）"
      return 1
      ;;
  esac
}

# 接管成功后：把上一轮记录收敛为终态（或按证据复位）。写记录需要 holder 是自己。
finalize_previous_migration() {
  case "${PREV_FINAL_STATE}" in
    "") return 0 ;;
    "${MIG_STATE_NONE}")
      log_warn "按数据库证据复位上一轮记录（归档到 migration-previous-*）：job=${LEASE_MIG_JOB:-<无>}"
      if ! migration_record_write "${MIG_STATE_NONE}" "" "" "${PREV_FINAL_DETAIL}" 1 "" \
            "${PREV_FINAL_METHOD}" "${PREV_FINAL_EVIDENCE}"; then
        log_error "复位上一轮记录失败（fail-closed）：锁与证据保留，人工核实后重跑"
        return 1
      fi
      return 0
      ;;
    *)
      log_info "收敛上一轮记录为 ${PREV_FINAL_STATE}（job=${LEASE_MIG_JOB:-<无>} uid=${LEASE_MIG_UID:-<无>}）"
      if ! migration_record_write "${PREV_FINAL_STATE}" "${LEASE_MIG_JOB}" "${LEASE_MIG_UID}" \
            "${PREV_FINAL_DETAIL}" 0 "" "${PREV_FINAL_METHOD}" "${PREV_FINAL_EVIDENCE}"; then
        log_error "收敛上一轮记录失败（fail-closed）：锁与证据保留，人工核实后重跑"
        return 1
      fi
      return 0
      ;;
  esac
}

# apply 之后等待 Job 对象真实可见（apply 的退出码不是事实来源）。
await_job_object() {
  local limit="${MIGRATION_APPLY_VISIBILITY_TIMEOUT}"
  if (( MIGRATION_TIMEOUT < limit )); then
    limit="${MIGRATION_TIMEOUT}"
  fi
  local deadline=$(( SECONDS + limit ))
  while :; do
    if job_probe "${JOB_NAME}"; then
      case "${JOB_PROBE_STATE}" in
        absent) : ;;
        *) return 0 ;;
      esac
    else
      log_warn "读取 Job/${JOB_NAME} 失败（${JOB_PROBE_ERROR:-未知错误}）：重试"
    fi
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep 1
  done
}

# 迁移开始：先记录「预期 Job 名」，再 apply，再用真实 UID 回写（§2.3 的固定顺序）。
begin_migration() {
  local pod_scan_rc=0 job_scan_rc=0
  require_lock_owned_server_side

  log_info "迁移前置检查：确认没有未终结的迁移 Job / Pod"
  scan_active_migrations || job_scan_rc=$?
  if (( job_scan_rc == 1 )); then
    die 7 "**确实存在**未终结的迁移 Job：拒绝开始新一轮迁移（fail-closed）。排查：${KUBECTL[*]} -n ${NAMESPACE} get jobs -l ${MIGRATION_JOB_SELECTOR} -o wide"
  fi
  if (( job_scan_rc != 0 )); then
    # 审计 D-3：查询失败（Forbidden/5xx/超时）**不能**说成「存在未终结的 Job」。
    die 7 "**无法确认**迁移 Job 状态（查询失败：$(api_error_class_label "${MIGRATION_LIST_CLASS}")：${MIGRATION_LIST_ERROR:-未知错误}）：拒绝开始新一轮迁移（fail-closed）。先修权限/网络，不要当作「没有 Job」。排查：${KUBECTL[*]} -n ${NAMESPACE} get jobs -l ${MIGRATION_JOB_SELECTOR} -o wide"
  fi
  # F-07：这里同样必须区分「确实有活动 Pod」与「无法确认」——两者的处置不同
  # （前者等待 Pod 结束，后者先修权限/网络），但都不得开始新一轮迁移。
  scan_migration_pods || pod_scan_rc=$?
  if (( pod_scan_rc == 1 )); then
    die 7 "**确实存在**仍在执行/终止中的迁移 Pod：拒绝开始新一轮迁移（fail-closed）。排查：${KUBECTL[*]} -n ${NAMESPACE} get pods -l ${MIGRATION_POD_SELECTOR} -o wide"
  fi
  if (( pod_scan_rc != 0 )); then
    die 7 "**无法确认**迁移 Pod 状态（查询失败：${MIGRATION_PODS_ERROR:-未知错误}）：拒绝开始新一轮迁移（fail-closed）。先修权限/网络，不要当作「没有 Pod」。排查：${KUBECTL[*]} -n ${NAMESPACE} get pods -l ${MIGRATION_POD_SELECTOR} -o wide"
  fi

  MIG_JOB="${JOB_NAME}"
  MIG_JOB_UID=""
  MIG_DETAIL=""
  # archive=1：开始新一轮迁移时，把上一轮（若仍留在记录里）归档到 migration-previous-*，
  # 再写本轮 PENDING。两件事在同一次 CAS 里完成，不会出现「清了旧的、新的没写上」。
  if ! migration_record_write "${MIG_STATE_PENDING}" "${MIG_JOB}" "" "" 1; then
    die 7 "无法把「预期迁移 Job 名」写入发布锁（fail-closed：不 apply 迁移 Job）"
  fi
  MIG_RECORD_WRITTEN=1

  log_info "应用 Migration 清单（Job/${JOB_NAME}）"
  local apply_rc=0
  MIG_APPLY_ISSUED=1
  "${KUBECTL[@]}" apply -f "${MIGRATION_MANIFEST}" || apply_rc=$?
  if (( apply_rc != 0 )); then
    log_warn "Migration apply 返回非零（${apply_rc}）：不能据此断定 Job 未创建（API Server 可能已接受请求）"
  fi

  # D-1/D-2/B-13：迁移 apply 之后必须做一次「请求返回后的归属观察」。
  # 已经发出的 apply 不可撤销；若这期间失去锁（含租约过期），Job 是否被创建、
  # DDL 是否已开始都只能标记为未知，不能靠客户端返回码推断。
  if ! observe_lock_after_request; then
    MIGRATION_OUTCOME_UNKNOWN=1
    printf 'MIGRATION_OUTCOME_UNKNOWN\n'
    log_error "MIGRATION_OUTCOME_UNKNOWN: 迁移 apply 之后本 Runner 已失去发布锁（客户端返回码 ${apply_rc}）。"
    log_error "  Job/${JOB_NAME} 是否被创建、DDL 是否已经开始执行都无法由此确认；请求不可撤销。"
    log_error "  不做任何补偿动作（不删除 Job、不复位迁移记录），保留证据供人工核实。"
    report_lock_lost
    exit 3
  fi

  if ! await_job_object; then
    if (( apply_rc == 0 )); then
      die 7 "apply 返回成功，但 Job/${JOB_NAME} 在 ${MIGRATION_APPLY_VISIBILITY_TIMEOUT}s 内不可见：状态不一致（fail-closed）。锁与迁移记录保留，人工核实后再发布"
    fi
    # 旧实现会在这里断言「应用未更新」。这是错的：Job 不存在只证明**这个 Job**
    # 没被创建；同一份清单里的 Namespace / ConfigMap 等对象可能已经生效。
    # 也不存在「已发出请求 → 一定没生效」的推论（客户端超时不等于服务端未接受）。
    MIGRATION_OUTCOME_UNKNOWN=1
    printf 'MIGRATION_OUTCOME_UNKNOWN\n'
    log_error "Migration 清单应用失败，且 Job/${JOB_NAME} 确实不存在（${JOB_PROBE_ERROR:-kubectl apply 返回非零}）"
    log_error "  **不得**据此断言「应用未更新」：这只证明该 Job 对象未被创建，清单中的其他对象可能已生效。"
    MIG_APPLY_ISSUED=0
    migration_record_write "${MIG_STATE_NONE}" "" "" "" || true
    MIG_RECORD_WRITTEN=0
    die 1 "Migration 清单应用失败（Job 未创建，其余对象是否生效未知）：${JOB_PROBE_ERROR:-kubectl apply 返回非零}"
  fi

  MIG_JOB_UID="${JOB_UID}"
  if [[ -z "${MIG_JOB_UID}" ]]; then
    die 7 "Job/${JOB_NAME} 缺少 metadata.uid：无法建立生命周期关联（fail-closed）"
  fi
  record_migration_state "${MIG_STATE_PENDING}" "${MIG_JOB}" "${MIG_JOB_UID}" "" \
    "无法把 Job UID 写回发布锁（fail-closed：后续接管将无法核验）"

  log_info "迁移 Job 已建立关联：job=${MIG_JOB} uid=${MIG_JOB_UID}（state=${MIG_STATE_PENDING}）"
}

# 等待 Job 进入终态。返回 0=complete，2=failed，1=等待上限到达（需二次判定）。
wait_for_migration() {
  local deadline=$(( SECONDS + MIGRATION_TIMEOUT )) marked_running=0
  log_info "等待 Job/${JOB_NAME} 进入终态（脚本上限 ${MIGRATION_TIMEOUT}s；关联 uid=${MIG_JOB_UID}）"
  while :; do
    require_lock_alive
    if job_probe "${JOB_NAME}"; then
      # 同名 Job 被删除重建会让「关联 uid」失效：此时任何终态结论都不可信。
      if [[ "${JOB_PROBE_STATE}" != "absent" && -n "${MIG_JOB_UID}" && -n "${JOB_UID}" \
            && "${JOB_UID}" != "${MIG_JOB_UID}" ]]; then
        die 7 "Job/${JOB_NAME} 的 uid 变化（记录=${MIG_JOB_UID}，实际=${JOB_UID}）：同名 Job 被删除重建，迁移生命周期不可信（fail-closed）"
      fi
      case "${JOB_PROBE_STATE}" in
        complete)
          # Complete 说明所有 Pod 都已成功退出；仍然显式核验一次，避免「条件与 Pod 不一致」
          # 的脏状态被当成终态（例如同名 Job 被重建后条件残留）。
          if ! migration_pod_gate "${JOB_NAME}" "${MIG_JOB_UID}"; then
            log_error "Job/${JOB_NAME} 为 Complete，但 Pod 状态不允许继续（迁移生命周期未确认，fail-closed）："
            report_pod_gate
            exit 7
          fi
          record_migration_state "${MIG_STATE_COMPLETE}" "${MIG_JOB}" "${MIG_JOB_UID}" "" \
            "迁移已完成，但无法把完成状态写回发布锁（fail-closed）"
          MIG_TERMINAL=1
          log_info "迁移成功: Job/${JOB_NAME} 已完成（uid=${MIG_JOB_UID}）"
          return 0
          ;;
        failed)
          # AUD-D3：Failed=True 不等于 Pod 已停止执行。Pod 仍在 Running/Pending/终止中时，
          # 记录失败条件但**不声称终态**，也不放锁；下一次接管同样会被门禁拦住。
          if ! migration_pod_gate "${JOB_NAME}" "${MIG_JOB_UID}"; then
            log_error "Job/${JOB_NAME} 为 Failed=True（reason=${JOB_REASON:-<无>}），但 Pod 状态不允许继续："
            report_pod_gate
            log_error "  **不得据此假定 DDL 已经停止**：Pod 可能仍在执行迁移中的语句。"
            log_error "  本次不创建新 Job、不更新 Application、不删除 Job、不执行 migrate force。"
            log_error "  关联记录：job=${MIG_JOB:-<无>} uid=${MIG_JOB_UID:-<未记录>}"
            migration_record_write "${MIG_STATE_FAILED}" "${MIG_JOB}" "${MIG_JOB_UID}" \
              "Job Failed=True 但 ${POD_GATE_SUMMARY}" 0 unconfirmed || true
            return 7
          fi
          migration_record_write "${MIG_STATE_FAILED}" "${MIG_JOB}" "${MIG_JOB_UID}" \
            "${JOB_REASON:-Failed}${JOB_MESSAGE:+: ${JOB_MESSAGE}}" || true
          MIG_TERMINAL=1
          return 2
          ;;
        absent)
          die 7 "Job/${JOB_NAME} 在等待过程中消失：迁移生命周期状态未知（fail-closed）。锁与关联记录保留，人工核实后再发布"
          ;;
        terminating)
          log_warn "Job/${JOB_NAME} 正在被删除（deletionTimestamp 非空）：继续等待其结束"
          ;;
        *)
          if (( ! marked_running )); then
            record_migration_state "${MIG_STATE_RUNNING}" "${MIG_JOB}" "${MIG_JOB_UID}" "" \
              "无法把迁移状态更新为 ${MIG_STATE_RUNNING}（fail-closed）"
            marked_running=1
          fi
          ;;
      esac
    else
      log_warn "读取 Job/${JOB_NAME} 失败（${JOB_PROBE_ERROR:-未知错误}）：继续等待"
    fi
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep "${POLL_INTERVAL}"
  done
}

# ---------------------------------------------------------------------------
# 版本降级保护（T2-06.1-02）
#
# C = 当前 Deployment 记录的版本（Pod 模板标签 app.kubernetes.io/version）
# T = 本次目标版本（镜像 tag，同时必须等于版本标记）
#
#   Deployment 不存在      → 允许（首次部署）
#   C == T                → 允许（幂等重发）
#   C 是 T 的祖先          → 允许（升级）
#   T 是 C 的祖先          → 拒绝（降级）
#   C 与 T 分叉            → 拒绝
#   C 或 T 无法在 Git 历史中解析 → 拒绝（fail-closed，绝不猜测先后）
#
# AUD-09：C 必须来自 Deployment 对象本身。原实现在标签缺失时回退到镜像 tag，
# 而镜像 tag 可能是 digest、latest 或手工标签，会把「线上版本不可识别」伪装成
# 「已知版本」，进而让降级判断建立在错误事实上。现在标签缺失直接拒绝（退出码 4）。
#
# 只要本地 Git 历史不足（浅克隆、镜像标签来自别的仓库），一律拒绝：
# 「无法判断」不能退化成「允许发布」。
# ---------------------------------------------------------------------------
git_resolve_commit() {
  command -v git >/dev/null 2>&1 || return 1
  git -C "${ROOT_DIR}" rev-parse --verify --quiet "$1^{commit}" 2>/dev/null
}

git_is_ancestor() {
  git -C "${ROOT_DIR}" merge-base --is-ancestor "$1" "$2" 2>/dev/null
}

reject_release_version() {
  local reason="$1"
  if (( ALLOW_DOWNGRADE )); then
    DOWNGRADE_AUTHORIZED=1
    log_warn "[AUDIT] 降级授权已生效（--allow-downgrade）：${reason}"
    log_warn "[AUDIT] 当前版本 ${CUR_VERSION:-未知} → 目标版本 ${TARGET_VERSION}（holder=${HOLDER_IDENTITY}）"
    log_warn "数据库兼容性风险：schema 不会被回滚，旧版本代码可能读到新版本已写入的列/数据，"
    log_warn "                   由此产生的数据不一致无法通过再次升级自动修复，需要人工核对。"
    return 0
  fi
  log_error "版本降级保护拒绝发布：${reason}"
  log_error "当前版本: ${CUR_VERSION:-<未知>}（镜像 ${CUR_IMAGE:-<未知>}）"
  log_error "目标版本: ${TARGET_VERSION}（镜像 ${IMAGE}）"
  log_error "如确认需要降级（例如回滚故障版本），由人工显式执行："
  log_error "  bash scripts/deploy-k8s.sh --allow-downgrade"
  log_error "禁止把 --allow-downgrade 写进流水线：降级可能破坏数据库兼容性。"
  exit 4
}

enforce_version_guard() {
  local current target cur_sha target_sha

  if (( ! CUR_EXISTS )); then
    log_info "版本检查：Deployment/${DEPLOYMENT_NAME} 不存在 → 首次部署，允许发布 ${TARGET_VERSION}"
    return 0
  fi

  current="${DS_VERSION}"
  target="${TARGET_VERSION}"
  if [[ -z "${current}" ]]; then
    reject_release_version "无法识别当前线上版本：Deployment/${DEPLOYMENT_NAME} 缺少 Pod 模板标签 app.kubernetes.io/version（AUD-09：不再从镜像 tag 猜测线上版本；请人工补齐标签后再发布）"
    return 0
  fi

  if [[ "${current}" == "${target}" ]]; then
    log_info "版本检查：目标版本 ${target} 与当前版本相同 → 幂等发布"
    return 0
  fi

  if ! command -v git >/dev/null 2>&1; then
    reject_release_version "未安装 git：无法验证 ${current} 与 ${target} 的祖先关系"
    return 0
  fi
  if [[ "$(git -C "${ROOT_DIR}" rev-parse --is-shallow-repository 2>/dev/null || echo unknown)" == "true" ]]; then
    log_warn "当前仓库是浅克隆：祖先关系可能无法判定（如需发布请先 git fetch --unshallow）"
  fi

  if ! cur_sha="$(git_resolve_commit "${current}")"; then
    reject_release_version "当前版本 ${current} 无法在本地 Git 历史中解析（浅克隆或跨仓库发布）"
    return 0
  fi
  if ! target_sha="$(git_resolve_commit "${target}")"; then
    reject_release_version "目标版本 ${target} 无法在本地 Git 历史中解析（浅克隆或镜像来自其他提交）"
    return 0
  fi

  if git_is_ancestor "${cur_sha}" "${target_sha}"; then
    log_info "版本检查：${current} 是 ${target} 的祖先 → 允许升级（${cur_sha:0:8} → ${target_sha:0:8}）"
    return 0
  fi
  if git_is_ancestor "${target_sha}" "${cur_sha}"; then
    reject_release_version "目标版本 ${target} 是当前版本 ${current} 的祖先（降级）"
    return 0
  fi
  reject_release_version "当前版本 ${current} 与目标版本 ${target} 分叉（无祖先关系）"
  return 0
}

# ---------------------------------------------------------------------------
# 结构化读取：Deployment / Pod / Job
# ---------------------------------------------------------------------------
parse_deployment_state() {
  IFS='~' read -r DS_GENERATION DS_OBSERVED DS_DESIRED DS_TOTAL DS_UPDATED DS_AVAILABLE \
    DS_UNAVAILABLE DS_IMAGE DS_VERSION <<< "$1"
}

# 读取 Deployment 快照：CUR_EXISTS=0 表示不存在；读取失败（网络/权限/命名空间）返回非零。
read_deployment_state() {
  local err="${TMP_DIR}/get-deployment.err" out
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get deployment "${DEPLOYMENT_NAME}" \
    -o "go-template=${DEPLOYMENT_STATE_TEMPLATE}" 2>"${err}")"; then
    out="$(strip_template_noise "${out}")"
    parse_deployment_state "${out}"
    CUR_EXISTS=1
    return 0
  fi
  # AUD-09：只有「deployments "<name>" not found」才算不存在；
  # 命名空间不存在、鉴权失败等其他 NotFound 必须按读取失败处理（fail-closed）。
  if is_not_found_for "${err}" "deployments" "${DEPLOYMENT_NAME}"; then
    CUR_EXISTS=0
    DS_GENERATION=0 DS_OBSERVED=0 DS_DESIRED=0 DS_TOTAL=0 DS_UPDATED=0
    DS_AVAILABLE=0 DS_UNAVAILABLE=0 DS_IMAGE="" DS_VERSION=""
    return 0
  fi
  log_error "读取 Deployment/${DEPLOYMENT_NAME} 失败：$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  return 1
}

deployment_converged() {
  local generation observed desired total updated available unavailable
  generation="$(to_int "${DS_GENERATION}")"
  observed="$(to_int "${DS_OBSERVED}")"
  desired="$(to_int "${DS_DESIRED}")"
  total="$(to_int "${DS_TOTAL}")"
  updated="$(to_int "${DS_UPDATED}")"
  available="$(to_int "${DS_AVAILABLE}")"
  unavailable="$(to_int "${DS_UNAVAILABLE}")"

  (( generation > 0 )) || return 1
  (( observed == generation )) || return 1
  (( desired > 0 )) || return 1
  (( total == desired )) || return 1
  (( updated == desired )) || return 1
  (( available == desired )) || return 1
  (( unavailable == 0 )) || return 1
  return 0
}

# 迁移 Job 的 Pod 快照：name ~ phase ~ deletionTimestamp ~ version ~ app 容器 ready ~ 镜像
# AUD-08：查询失败必须与「没有 Pod」区分开（失败返回非零并设置 JOB_PODS_ERROR）。
# 与 read_migration_pods_for_job 一样通过全局变量返回结果，避免子 shell 丢掉错误文本。
read_job_pods() {
  local err="${TMP_DIR}/get-job-pods.err" out
  JOB_PODS_ROWS=""
  JOB_PODS_ERROR=""
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "job-name=${JOB_NAME}" \
    -o "go-template=${PODS_STATE_TEMPLATE}" 2>"${err}")"; then
    JOB_PODS_ROWS="$(strip_template_noise "${out}")"
    return 0
  fi
  JOB_PODS_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  return 1
}

# ---------------------------------------------------------------------------
# 1. 环境预检
# ---------------------------------------------------------------------------
"${KUBECTL[@]}" version >/dev/null 2>&1 \
  || die "kubectl 无法连接集群（context=${CONTEXT:-当前}）：请检查 kubeconfig 与网络"

log_info "集群连接正常（context=${CONTEXT:-当前}）"

MIGRATION_MANIFEST="${TMP_DIR}/migration.yaml"
APPLICATION_MANIFEST="${TMP_DIR}/application.yaml"

log_info "构建 Migration 清单: ${MIGRATION_ENTRYPOINT}"
"${KUBECTL[@]}" kustomize "${MIGRATION_ENTRYPOINT}" > "${MIGRATION_MANIFEST}" \
  || die "Migration Kustomize 构建失败"

log_info "构建 Application 清单: ${APP_ENTRYPOINT}"
"${KUBECTL[@]}" kustomize "${APP_ENTRYPOINT}" > "${APPLICATION_MANIFEST}" \
  || die "Application Kustomize 构建失败"

# 占位符未替换就发布，等于把模板值直接推到集群；这里直接拦截。
if grep -q 'REPLACE_WITH_' "${MIGRATION_MANIFEST}" "${APPLICATION_MANIFEST}"; then
  log_error "清单中仍存在 REPLACE_WITH_* 占位符，拒绝发布。需先替换的文件："
  grep -rn 'REPLACE_WITH_' \
    manifest/kubernetes/overlays/production \
    manifest/kubernetes/migration/overlays/production \
    manifest/kubernetes/shared >&2 || true
  exit 1
fi

# 镜像版本：两个入口必须一致、不可变，且不能是占位值。
mapfile -t IMAGES < <(grep -hoE "${IMAGE_REPOSITORY}:[^[:space:]]+" \
  "${MIGRATION_MANIFEST}" "${APPLICATION_MANIFEST}" | sort -u)
[[ "${#IMAGES[@]}" -eq 1 ]] \
  || die "Migration 与 Application 必须使用同一个镜像版本，实际: ${IMAGES[*]:-<空>}"

IMAGE="${IMAGES[0]}"
IMAGE_TAG="${IMAGE##*:}"
case "${IMAGE_TAG}" in
  latest|unreleased|"${PLACEHOLDER_TAG}")
    die "镜像版本 ${IMAGE_TAG} 不可发布：请把 overlay 中的版本替换为真实短 SHA" ;;
esac
[[ "${IMAGE_TAG}" =~ ^[0-9a-f]{7,40}$ ]] \
  || die "镜像版本 ${IMAGE_TAG} 不是短 SHA/digest 形式的不可变标签"

TARGET_VERSION="${IMAGE_TAG}"

# 命名空间以构建产物为准，避免脚本与清单各说一套。
NAMESPACE="$(awk '/^kind: Namespace$/{found=1} found && /^  name:/{print $2; exit}' "${APPLICATION_MANIFEST}")"
[[ -n "${NAMESPACE}" ]] || die "Application 清单缺少 Namespace 资源"

DEPLOYMENT_NAME="$(awk '/^kind: Deployment$/{found=1} found && /^  name:/{print $2; exit}' "${APPLICATION_MANIFEST}")"
[[ -n "${DEPLOYMENT_NAME}" ]] || die "Application 清单缺少 Deployment 资源"

JOB_NAME="$(awk '/^kind: Job$/{found=1} found && /^  name:/{print $2; exit}' "${MIGRATION_MANIFEST}")"
[[ -n "${JOB_NAME}" ]] || die "Migration 清单缺少 Job 资源"
case "${JOB_NAME}" in
  *"${IMAGE_TAG}") ;;
  *) die "迁移 Job 名称 ${JOB_NAME} 不包含镜像版本 ${IMAGE_TAG}：两个入口版本不一致，拒绝发布" ;;
esac

# 版本标记必须与镜像版本一致：不一致意味着「线上记录的版本」和「实际运行的镜像」对不上，
# 后续发布的降级判断会基于错误事实。
MANIFEST_VERSION="$(grep -m1 -oE 'app\.kubernetes\.io/version: "[^"]+"' "${APPLICATION_MANIFEST}" \
  | sed -E 's/.*"([^"]+)"/\1/' || true)"
[[ -n "${MANIFEST_VERSION}" ]] \
  || die "Application 清单缺少 Pod 模板版本标记 app.kubernetes.io/version（发布判定依赖它）"
[[ "${MANIFEST_VERSION}" == "${IMAGE_TAG}" ]] \
  || die "版本标记 ${MANIFEST_VERSION} 与镜像版本 ${IMAGE_TAG} 不一致：请同步 overlays/production/version-patch.yaml"

# AUD-04/06：脚本等待上限与 Job 自身截止时间的关系。
# 若两者相等（旧默认：900 / 900），"脚本超时"和"Job 被 kubelet 终止"会同时发生，
# 结果取决于 API 传播速度 —— 必须由配置消除，而不是靠运气。
JOB_ACTIVE_DEADLINE="$(grep -m1 -oE '^[[:space:]]*activeDeadlineSeconds:[[:space:]]*[0-9]+' "${MIGRATION_MANIFEST}" \
  | grep -oE '[0-9]+$' || true)"
JOB_ACTIVE_DEADLINE="$(to_int "${JOB_ACTIVE_DEADLINE:-0}")"
if (( JOB_ACTIVE_DEADLINE > 0 )); then
  if (( MIGRATION_TIMEOUT <= JOB_ACTIVE_DEADLINE )); then
    die 1 "--migration-timeout (${MIGRATION_TIMEOUT}s) 必须大于 Job activeDeadlineSeconds (${JOB_ACTIVE_DEADLINE}s)：否则脚本会先于 Job 自己的截止时间超时，重新引入「超时 vs Deadline」边界竞态"
  fi
  migration_margin=$(( MIGRATION_TIMEOUT - JOB_ACTIVE_DEADLINE ))
  if (( migration_margin < MIGRATION_TIMEOUT_MARGIN )); then
    log_warn "迁移等待余量 ${migration_margin}s 小于建议的 ${MIGRATION_TIMEOUT_MARGIN}s：Job 被终止后需要时间传播 Failed 条件，建议加大 --migration-timeout"
  else
    log_info "迁移超时关系：Job activeDeadlineSeconds=${JOB_ACTIVE_DEADLINE}s + 余量 ${migration_margin}s = 脚本上限 ${MIGRATION_TIMEOUT}s"
  fi
else
  log_warn "迁移清单未声明 activeDeadlineSeconds：无法校验「脚本超时 > Job deadline」的时间关系"
fi

log_info "目标命名空间: ${NAMESPACE}；镜像版本: ${IMAGE_TAG}；迁移 Job: ${JOB_NAME}"

# ---------------------------------------------------------------------------
# 2. 权限预检 + 命名空间引导（T2-06.1.4：预检前移到**任何写请求之前**）
#
# 旧顺序（T2-06.1.3）是「get namespace → 失败就当不存在 → apply 命名空间 →
# check_lease_permissions」，两个缺陷：
#   A-1/A-2/D-9：`get namespace` 的任何失败（403/5xx/超时）都被当成「不存在」，
#     接着 apply，首次 least-privilege 部署必现 403；
#   A-5：权限预检晚于一次真实的写请求，缺权限时已经动过集群。
#
# 现在由 check_required_permissions 负责全部顺序（集群级预检 → 命名空间判定 →
# 命名空间级预检 → 只有确实不存在才创建），细节与 fail-closed 依据见该函数注释。
# 命名空间创建仍然只做幂等 apply，不改动任何工作负载；锁在预检全部通过之后才获取。
# ---------------------------------------------------------------------------
check_required_permissions

# ---------------------------------------------------------------------------
# 4. 获取发布锁（接管前核验上一轮迁移）
# ---------------------------------------------------------------------------
lock_rc=0
acquire_release_lock || lock_rc=$?
if (( lock_rc == 3 )); then
  exit 7
fi
if (( lock_rc != 0 )); then
  log_error "未能在 ${LOCK_WAIT_TIMEOUT}s 内获取发布锁 ${NAMESPACE}/${LOCK_NAME}：本次发布不执行任何变更。"
  log_error "排查：${KUBECTL[*]} -n ${NAMESPACE} get lease ${LOCK_NAME} -o yaml"
  log_error "锁卡住时的人工处理见 docs/development/kubernetes.md（禁止直接删除正在被持用的锁）。"
  exit 3
fi

start_lease_renewer
log_info "发布锁已生效：迁移、应用更新、发布验证全程持锁（后台每 ${LOCK_RENEW_INTERVAL}s 续租，单次请求超时 ${LOCK_RENEW_REQUEST_TIMEOUT}s）"

# ---------------------------------------------------------------------------
# 5. 锁内复检：Secret 与版本降级保护
#
# 这些条件都可能在「预检 → 拿到锁」的窗口内被其他流水线改动，因此必须在锁内重新判断。
# ---------------------------------------------------------------------------
require_lock_alive

mapfile -t SECRET_NAMES < <(awk '/secretRef:/{flag=1; next} flag && /name:/{print $2; flag=0}' \
  "${APPLICATION_MANIFEST}" | sort -u)
[[ "${#SECRET_NAMES[@]}" -gt 0 ]] || die "Application 清单没有任何 Secret 引用（配置边界异常）"
# 审计 A-3：旧实现 `get secret ... >/dev/null 2>&1 || die "缺少 Secret"` 把 403 / 5xx /
# 超时全部报成「缺少 Secret」，并且丢弃了 kubectl 的 stderr。现在按四类分别处置：
#   NotFound → 确实是缺少 Secret（提示按 examples/secrets 创建）
#   Forbidden → 发布的 secrets get 权限缺失（退出码 3），**不能**说成「Secret 不存在」
#   api/timeout → 无法确认（退出码 1），同样不能说成「缺少 Secret」
for secret in "${SECRET_NAMES[@]}"; do
  secret_err="${TMP_DIR}/get-secret.err"
  if ! "${KUBECTL[@]}" -n "${NAMESPACE}" get secret "${secret}" >/dev/null 2>"${secret_err}"; then
    classify_api_error "${secret_err}" "secrets" "${secret}"
    case "${API_ERROR_CLASS}" in
      notfound)
        die "缺少 Secret ${NAMESPACE}/${secret}（NotFound）：请先按 manifest/kubernetes/examples/secrets/ 创建。stderr: ${API_ERROR_TEXT:-无输出}"
        ;;
      forbidden)
        die 3 "无权读取 Secret ${NAMESPACE}/${secret}（Forbidden：发布身份缺少 secrets 的 get 权限，见 manifest/kubernetes/examples/deploy-lock/rbac.yaml）。**这不是「Secret 不存在」**。stderr: ${API_ERROR_TEXT:-无输出}"
        ;;
      *)
        die "无法确认 Secret ${NAMESPACE}/${secret} 是否存在（$(api_error_class_label "${API_ERROR_CLASS}")）：${API_ERROR_TEXT:-无输出}。**这不等于「缺少 Secret」**，按 fail-closed 处理。"
        ;;
    esac
  fi
done
log_info "Secret 就绪: ${SECRET_NAMES[*]}"

read_deployment_state || die "无法确认线上 Deployment 状态（fail-closed，拒绝在未知状态下发布）"
CUR_IMAGE="${DS_IMAGE}"
CUR_VERSION="${DS_VERSION}"
enforce_version_guard
require_lock_alive

# ---------------------------------------------------------------------------
# 6. 迁移（必须先于应用）
# ---------------------------------------------------------------------------
begin_migration

migration_status=0
wait_for_migration || migration_status=$?

if (( migration_status == 1 )); then
  # 二次判定（AUD-04/§4.1）：达到脚本等待上限后必须重新读取最新状态，
  # 不能在 Job 已经 Complete 的情况下仍报告超时。
  log_warn "达到脚本等待上限 ${MIGRATION_TIMEOUT}s：重新读取 Job 最新状态后再判定（不假设超时=失败）"
  require_lock_alive
  if job_probe "${JOB_NAME}"; then
    case "${JOB_PROBE_STATE}" in
      complete)
        if ! migration_pod_gate "${JOB_NAME}" "${MIG_JOB_UID}"; then
          log_error "二次判定：Job/${JOB_NAME} 为 Complete，但 Pod 状态不允许继续（生命周期未确认，fail-closed）："
          report_pod_gate
          exit 7
        fi
        record_migration_state "${MIG_STATE_COMPLETE}" "${MIG_JOB}" "${MIG_JOB_UID}" "" \
          "二次判定为 Complete，但无法把完成状态写回发布锁（fail-closed）"
        MIG_TERMINAL=1
        migration_status=0
        log_info "二次判定：Job/${JOB_NAME} 已 Complete → 继续发布（超时不等于失败）"
        ;;
      failed)
        # AUD-D3：与等待循环内同一套判据，避免「超时后二次判定」绕过 Pod 门禁。
        if ! migration_pod_gate "${JOB_NAME}" "${MIG_JOB_UID}"; then
          log_error "二次判定：Job/${JOB_NAME} 为 Failed=True，但 Pod 状态不允许继续："
          report_pod_gate
          log_error "  不得据此假定 DDL 已经停止；本次不创建新 Job、不更新 Application、不删除 Job。"
          migration_record_write "${MIG_STATE_FAILED}" "${MIG_JOB}" "${MIG_JOB_UID}" \
            "Job Failed=True 但 ${POD_GATE_SUMMARY}" 0 unconfirmed || true
          migration_status=7
        else
          migration_record_write "${MIG_STATE_FAILED}" "${MIG_JOB}" "${MIG_JOB_UID}" \
            "${JOB_REASON:-Failed}${JOB_MESSAGE:+: ${JOB_MESSAGE}}" || true
          MIG_TERMINAL=1
          migration_status=2
          log_error "二次判定：Job/${JOB_NAME} 已 Failed=True → 按迁移失败处理"
        fi
        ;;
      absent)
        migration_status=7
        ;;
      *)
        log_warn "二次判定：Job/${JOB_NAME} 仍为 ${JOB_PROBE_STATE}（未终态）→ 按超时处理"
        ;;
    esac
  else
    log_error "二次判定失败：无法读取 Job/${JOB_NAME}（${JOB_PROBE_ERROR:-未知错误}）：状态未知，按超时处理"
    migration_status=6
  fi
fi

if (( migration_status == 7 )); then
  # ---- AUD-D3：Job 已终结，但其 Pod 未确认停止执行 DDL ----
  log_error "迁移生命周期未确认（退出码 7）：Job/${JOB_NAME} 的条件与 Pod 状态不一致（fail-closed）"
  log_error "**Job Failed=True 不等价于「Pod 已停止执行」**：Pod 可能仍在跑迁移语句，"
  log_error "  此时创建新一轮 Job 会让两个 Pod 的 DDL 重叠。"
  log_error "本次未做任何后续动作：不创建新 Job、不更新 Application、不删除 Job、不执行 migrate force。"
  log_error "旧 Job: ${JOB_NAME}（uid=${MIG_JOB_UID:-<未记录>}，conditions: Complete=${JOB_COMPLETE:-<空>} Failed=${JOB_FAILED:-<空>} reason=${JOB_REASON:-<无>}）"
  log_error "--- Job 状态 ---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" -o wide >&2 || true
  log_error "--- 关联 Pod（name / phase / 是否终止中 / ownerJobUID）---"
  gate_rows=""
  if read_migration_pods_for_job "${JOB_NAME}"; then
    gate_rows="${MIGRATION_PODS_ROWS}"
    if [[ -n "${gate_rows}" ]]; then
      printf '%s\n' "${gate_rows}" >&2
    else
      log_error "（没有查询到该 Job 的 Pod：Job 条件为 Failed，但无法用 Pod 证明 DDL 已停止）"
    fi
  else
    log_error "**无法查询该 Job 的 Pod**（${MIGRATION_PODS_ERROR:-未知错误}）：状态未知"
  fi
  log_error "--- 发布锁与记录 ---"
  log_error "发布锁**未释放**；记录保留在 Lease 注解上：state=${MIG_STATE} job=${MIG_JOB:-<无>} uid=${MIG_JOB_UID:-<未记录>}"
  log_error "后续动作：等待 Pod 真正终止（不要删除 Pod/Job 来「让流程过」），然后重跑发布："
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} get pods -l job-name=${JOB_NAME} -o wide"
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} wait --for=condition=failed job/${JOB_NAME} --timeout=300s"
  log_error "确认 Pod 全部 Succeeded/Failed 后：bash scripts/deploy-k8s.sh"
  log_error "应用 Deployment 未更新。"
  exit 7
fi

if (( migration_status == 2 )); then
  # ---- Failed：确定性失败，打印证据，给出安全的人工恢复步骤 ----
  log_error "迁移失败：Job/${JOB_NAME} 进入 Failed=True（应用 Deployment 未更新）"
  log_error "失败原因: ${JOB_REASON:-<无 reason>} ${JOB_MESSAGE:-<无 message>}"
  log_error "关联记录已保留在发布锁上：state=${MIG_STATE_FAILED} job=${MIG_JOB} uid=${MIG_JOB_UID}"
  log_error "--- Job 状态 ---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" -o wide >&2 || true
  log_error "--- Job Pod 状态 ---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "job-name=${JOB_NAME}" -o wide >&2 || true
  log_error "--- Job 日志（最后 200 行）---"
  # 审计 A-8：日志读取失败必须被报告（缺 pods/log 权限 / Pod 已被 TTL 清理），
  # 不得再用 `|| true` 静默吞掉，否则「迁移失败」的排障信息会凭空消失。
  print_job_logs "${JOB_NAME}" 200
  log_error "--- Pod 详情（终止原因 / 退出码）---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" describe pods -l "job-name=${JOB_NAME}" >&2 || true
  log_error "恢复步骤（人工）："
  log_error "  1) 按上述日志修复根因（schema/SQL/权限/连接），不要用 migrate force 绕过；"
  log_error "  2) 确认没有 Pod 仍在运行：${KUBECTL[*]} -n ${NAMESPACE} get pods -l job-name=${JOB_NAME}"
  log_error "  3) 删除失败 Job 后重跑：${KUBECTL[*]} -n ${NAMESPACE} delete job ${JOB_NAME} && bash scripts/deploy-k8s.sh"
  log_error "  4) 若版本记录已 dirty，按 docs/development/migrations.md §7 人工核对后再继续。"
  log_error "脚本不会自动 force，也不会自动删除 Job。"
  exit 2
fi

if (( migration_status != 0 )); then
  # ---- Timeout / 状态未知 / 记录与事实不一致 ----
  log_error "迁移等待超时（${MIGRATION_TIMEOUT}s）：Job/${JOB_NAME} 既未 Complete 也未 Failed。"
  log_error "**超时不等同于失败**：迁移 Pod 可能仍在执行 DDL，删除 Job 会中断迁移并可能留下 dirty 状态。"
  require_lock_alive
  log_error "--- Job 当前状态 ---"
  log_error "job=${JOB_NAME} uid=${MIG_JOB_UID:-<未记录>} 迁移记录 state=${MIG_STATE}"
  if job_probe "${JOB_NAME}"; then
    case "${JOB_PROBE_STATE}" in
      absent)
        log_error "Job/${JOB_NAME} 当前读取结果为 NotFound：状态未知（不允许假定已结束）"
        ;;
      *)
        log_error "conditions: Complete=${JOB_COMPLETE:-<空>} Failed=${JOB_FAILED:-<空>} deletionTimestamp=${JOB_PROBE_STATE}"
        "${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" -o wide >&2 || true
        ;;
    esac
  else
    log_error "Job/${JOB_NAME} 当前状态无法读取（${JOB_PROBE_ERROR:-未知错误}）：按状态未知处理"
  fi
  log_error "--- 迁移 Pod 当前状态 ---"
  pods_still_running=0
  pods_unknown=0
  if read_job_pods; then
    job_pods="${JOB_PODS_ROWS}"
    if [[ -n "${job_pods}" ]]; then
      printf '%s\n' "${job_pods}" >&2
      while IFS= read -r line; do
        [[ -n "${line}" ]] || continue
        IFS='~' read -r pod_name pod_phase pod_deleting pod_version pod_ready pod_image <<< "${line}"
        case "${pod_phase}" in
          Running|Pending|Unknown) pods_still_running=1 ;;
        esac
        if [[ -n "${pod_deleting}" ]]; then
          pods_still_running=1
        fi
      done <<< "${job_pods}"
    else
      log_error "（没有查询到迁移 Pod：可能已被 TTL 清理，或 Job 尚未调度）"
    fi
  else
    # AUD-08：查询失败 ≠ 没有 Pod。
    pods_unknown=1
    log_error "**无法查询迁移 Pod 状态**（${JOB_PODS_ERROR:-未知错误}）"
    log_error "  这不等于「没有 Pod」，也不等于迁移已经结束：按状态未知处理，禁止删除 Job。"
  fi
  if (( pods_still_running )); then
    log_error "**禁止删除 Job**：迁移 Pod 仍在运行/终止中，删除会中断 DDL。"
  elif (( pods_unknown )); then
    log_error "**禁止删除 Job**：Pod 状态未知，无法证明 DDL 已结束。"
  else
    log_error "当前未见运行中的迁移 Pod，但 Job 未收敛：请先确认数据库侧是否有残留 DDL，再决定是否删除 Job。"
  fi
  log_error "--- 发布锁与生命周期 ---"
  log_error "发布锁**未释放**（迁移未确认终态）：关联记录保留在 Lease 注解上，"
  log_error "  ${LEASE_ANN_STATE}=${MIG_STATE}，${LEASE_ANN_JOB}=${MIG_JOB}，${LEASE_ANN_JOB_UID}=${MIG_JOB_UID:-<未记录>}"
  log_error "锁会在 ${LOCK_LEASE_DURATION}s 后过期；下一个 Runner 接管时会先核验该 Job，"
  log_error "在其进入 Complete/Failed 之前不会开始新一轮发布（退出码 7）。"
  log_error "继续等待（不要杀 Pod）："
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} wait --for=condition=complete job/${JOB_NAME} --timeout=900s"
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} logs -f job/${JOB_NAME} --tail=100"
  log_error "后续检查（在 Job 收敛后）："
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} get job,pod -l job-name=${JOB_NAME} -o wide"
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} get job ${JOB_NAME} -o jsonpath='{.status.conditions}'"
  log_error "确认 Pod 已退出且数据库无残留 DDL 之后，才允许删除 Job 重跑。"
  log_error "应用 Deployment 未更新。"
  if (( migration_status == 7 )); then
    exit 7
  fi
  exit 6
fi

# ---------------------------------------------------------------------------
# 7. 应用
#
# AUD-02 / AUD-05（**未关闭**）：Kubernetes 没有 fencing token，已经发出的请求不能被撤销。
# 这里能做的只有两件事：
#   1) 发出前做服务端 holder/uid 复核（缩小窗口，但不消除 TOCTOU）；
#   2) 返回非零时用 read-after-write 观察「是否其实已经生效」，并把结果**如实**报告。
# 客户端返回非零**不等于**资源没有被修改；read-after-write 也**不能**撤销已生效的请求。
# ---------------------------------------------------------------------------
require_lock_owned_server_side
log_info "应用 Application 清单"
APPLICATION_APPLY_ISSUED=1
APPLICATION_INFLIGHT=1
application_apply_rc=0
"${KUBECTL[@]}" apply -f "${APPLICATION_MANIFEST}" || application_apply_rc=$?
APPLICATION_INFLIGHT=0
# B-11：apply 已返回（无论退出码），但**结果尚未核实**。从此到 verify_release 通过
# 之间收到信号，结论必须是 APPLICATION_OUTCOME_UNKNOWN，不能是"成功"或"未应用"。
APPLICATION_APPLY_RETURNED=1

# 「请求在途时失锁」：无论客户端返回什么，都不能假装这次写没有发生。
# 已经发出的请求无法撤销；这里只能停止后续动作并如实报告。
if ! observe_lock_after_request; then
  printf 'APPLICATION_OUTCOME_UNKNOWN\n'
  log_error "APPLICATION_OUTCOME_UNKNOWN: Application apply 期间本 Runner 失去发布锁（客户端返回码 ${application_apply_rc}）。"
  log_error "  该请求**可能已被 API Server 接受并生效**；已经发出的请求**无法撤销**。"
  log_error "  不再发起任何新的变更；请先核对 Deployment 实际状态，再决定是否重跑。"
  report_lock_lost
  exit 3
fi

if (( application_apply_rc != 0 )); then
  log_error "Application 清单应用失败（kubectl 退出码 ${application_apply_rc}）"
  log_error "  客户端非零退出**不能**证明资源没有被修改：请求可能已被 API Server 接受并生效。"
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    printf 'APPLICATION_OUTCOME_UNKNOWN\n'
    log_error "APPLICATION_OUTCOME_UNKNOWN: 请求发出后本 Runner 已失去发布锁，且无法撤销已发出的请求。"
    log_error "  本 Runner 不会再发起任何变更；请先核对 Deployment 实际状态，再决定是否重跑。"
    report_lock_lost
    exit 3
  fi
  # read-after-write：只看事实（镜像 + 版本标记），不把「客户端成功」当事实来源。
  #
  # 注意：这里**不会**因为「Deployment 已是目标版本」就报告成功 —— 清单里还有
  # Service/ConfigMap 等对象，客户端超时无法证明它们全部被应用。可观测性提高 ≠ 结果确定。
  if read_deployment_state && (( CUR_EXISTS )) \
     && [[ "${DS_IMAGE}" == "${IMAGE}" && "${DS_VERSION}" == "${TARGET_VERSION}" ]]; then
    log_error "APPLICATION_OUTCOME_UNKNOWN: read-after-write 显示 Deployment/${DEPLOYMENT_NAME} 已是目标镜像与版本，"
    log_error "  说明**本次请求很可能已被 API Server 接受并生效**（不能因为客户端非零就认为「没有变更」）。"
    log_error "  但仍无法确认清单中的其余对象（Service / ConfigMap / 其他资源）是否全部应用。"
  else
    log_error "APPLICATION_OUTCOME_UNKNOWN: 无法通过 read-after-write 判定本次 apply 是否生效。"
    log_error "  已观察到的线上状态：镜像=${DS_IMAGE:-<未知>} 版本=${DS_VERSION:-<未知>}（目标 ${IMAGE_TAG}）"
    log_error "  这不等于「没有变更」：请求可能仍在 API Server 侧生效，或只生效了一部分对象。"
  fi
  printf 'APPLICATION_OUTCOME_UNKNOWN\n'
  log_error "  本次不报告成功，也不重复 apply（apply 幂等，但重复写入会扩大在途范围）。"
  log_error "  下一步（只读核对）："
  log_error "    ${KUBECTL[*]} -n ${NAMESPACE} get deployment ${DEPLOYMENT_NAME} -o yaml"
  log_error "    ${KUBECTL[*]} -n ${NAMESPACE} get pods -l ${POD_SELECTOR} -o wide"
  log_error "  确认实际状态后再重跑；重跑前先确认锁已释放（本 Runner 会释放自己的锁）。"
  exit 1
fi

log_info "等待 Deployment/${DEPLOYMENT_NAME} 收敛（上限 ${ROLLOUT_TIMEOUT}s）"
rollout_timeout_hit=0
deadline=$(( SECONDS + ROLLOUT_TIMEOUT ))
while :; do
  require_lock_alive
  if read_deployment_state && (( CUR_EXISTS )) && deployment_converged; then
    break
  fi
  if (( SECONDS >= deadline )); then
    rollout_timeout_hit=1
    log_error "Deployment/${DEPLOYMENT_NAME} 未在 ${ROLLOUT_TIMEOUT}s 内收敛（generation/副本/可用数未对齐）"
    break
  fi
  sleep "${POLL_INTERVAL}"
done

# ---------------------------------------------------------------------------
# 8. 发布成功判定（T2-06.1-03）
#
# rollout status 只能证明「某个 ReplicaSet 可用」，不能证明「可用的是本次发布的版本」。
# 这里逐项比对目标事实：镜像、版本标记、generation 收敛、副本数、实际 Pod。
# ---------------------------------------------------------------------------
verify_release() {
  local expected_image="$1" expected_version="$2" failures=0
  local generation observed desired total updated available unavailable

  log_info "发布验证：比对 Deployment/${DEPLOYMENT_NAME} 与目标镜像 ${expected_version}"
  if ! read_deployment_state; then
    log_error "发布验证失败：无法读取 Deployment/${DEPLOYMENT_NAME}"
    return 1
  fi
  if (( ! CUR_EXISTS )); then
    log_error "发布验证失败：Deployment/${DEPLOYMENT_NAME} 不存在（apply 未生效或已被删除）"
    return 1
  fi

  if [[ "${DS_IMAGE}" != "${expected_image}" ]]; then
    log_error "发布验证失败：Pod 模板镜像 = ${DS_IMAGE:-<空>}，期望 = ${expected_image}"
    failures=$(( failures + 1 ))
  fi

  if [[ "${DS_VERSION}" != "${expected_version}" ]]; then
    log_error "发布验证失败：app.kubernetes.io/version = ${DS_VERSION:-<空>}，期望 = ${expected_version}"
    failures=$(( failures + 1 ))
  fi

  generation="$(to_int "${DS_GENERATION}")"
  observed="$(to_int "${DS_OBSERVED}")"
  desired="$(to_int "${DS_DESIRED}")"
  total="$(to_int "${DS_TOTAL}")"
  updated="$(to_int "${DS_UPDATED}")"
  available="$(to_int "${DS_AVAILABLE}")"
  unavailable="$(to_int "${DS_UNAVAILABLE}")"

  if (( generation <= 0 || observed != generation )); then
    log_error "发布验证失败：observedGeneration=${observed} 未追上 generation=${generation}"
    failures=$(( failures + 1 ))
  fi
  if (( desired <= 0 )); then
    log_error "发布验证失败：期望副本数为 ${desired}"
    failures=$(( failures + 1 ))
  elif (( total != desired || updated != desired || available != desired || unavailable != 0 )); then
    log_error "发布验证失败：副本未对齐（期望=${desired} 总=${total} 已更新=${updated} 可用=${available} 不可用=${unavailable}）"
    failures=$(( failures + 1 ))
  fi

  local err="${TMP_DIR}/verify-pods.err" pods_out pod_count=0
  if ! pods_out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "${POD_SELECTOR}" \
      -o "go-template=${PODS_STATE_TEMPLATE}" 2>"${err}")"; then
    log_error "发布验证失败：无法列出 Pod（${POD_SELECTOR}）：$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
    failures=$(( failures + 1 ))
    pods_out=""
  fi
  pods_out="$(strip_template_noise "${pods_out}")"

  local line pod_name pod_phase pod_deleting pod_version pod_ready pod_image
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    IFS='~' read -r pod_name pod_phase pod_deleting pod_version pod_ready pod_image <<< "${line}"
    if [[ -n "${pod_deleting}" ]]; then
      # 正在终止的旧 Pod 不计入本次发布结果（Recreate 切换瞬间的正常现象）。
      continue
    fi
    pod_count=$(( pod_count + 1 ))
    if [[ -z "${pod_image}" ]]; then
      log_error "发布验证失败：Pod/${pod_name} 没有名为 ${APP_CONTAINER_NAME} 的容器（Pod 结构异常）"
      failures=$(( failures + 1 ))
    elif [[ "${pod_image}" != "${expected_image}" ]]; then
      log_error "发布验证失败：Pod/${pod_name} 镜像 = ${pod_image}，期望 = ${expected_image}"
      failures=$(( failures + 1 ))
    fi
    if [[ "${pod_version}" != "${expected_version}" ]]; then
      log_error "发布验证失败：Pod/${pod_name} 版本标记 = ${pod_version:-<空>}，期望 = ${expected_version}"
      failures=$(( failures + 1 ))
    fi
    if [[ "${pod_phase}" != "Running" ]]; then
      log_error "发布验证失败：Pod/${pod_name} phase = ${pod_phase:-<空>}，期望 Running"
      failures=$(( failures + 1 ))
    fi
    if [[ "${pod_ready}" != "true" ]]; then
      # AUD-11：只认 app 容器；sidecar/initContainer 不能替代它的就绪状态。
      log_error "发布验证失败：Pod/${pod_name} 的 ${APP_CONTAINER_NAME} 容器未 Ready（readinessProbe 未通过或容器缺失）"
      failures=$(( failures + 1 ))
    fi
  done <<< "${pods_out}"

  if (( desired > 0 && pod_count != desired )); then
    log_error "发布验证失败：匹配 ${POD_SELECTOR} 的 Pod 数量 = ${pod_count}，期望 = ${desired}"
    failures=$(( failures + 1 ))
  fi

  if (( failures > 0 )); then
    log_error "发布验证未通过（${failures} 项不匹配）：本次发布不得报告成功。"
    return 1
  fi
  return 0
}

require_lock_owned_server_side
if ! verify_release "${IMAGE}" "${TARGET_VERSION}"; then
  log_error "实际状态与目标不一致，退出码 5。"
  log_error "排查：${KUBECTL[*]} -n ${NAMESPACE} get deployment/${DEPLOYMENT_NAME} -o yaml"
  log_error "      ${KUBECTL[*]} -n ${NAMESPACE} get pods -l ${POD_SELECTOR} -o wide"
  log_error "      ${KUBECTL[*]} -n ${NAMESPACE} describe pod -l ${POD_SELECTOR}"
  (( rollout_timeout_hit == 0 )) || log_error "（等待阶段已超时：${ROLLOUT_TIMEOUT}s 内未收敛）"
  exit 5
fi

if (( DOWNGRADE_AUTHORIZED )); then
  log_warn "[AUDIT] 本次发布经由 --allow-downgrade 授权降级到 ${TARGET_VERSION}"
fi

# B-11：到这里 apply 的结果已经用**集群事实**核实过（verify_release 通过），
# 信号路径才可以打印「已核实生效」而不是「结果未知」。
APPLICATION_OUTCOME_VERIFIED=1
log_info "发布验证通过：镜像 ${IMAGE_TAG}，版本标记 ${TARGET_VERSION}，副本与 Pod 全部就绪"

# 锁必须在「验证通过」之后才释放：迁移完成不等于发布完成。
require_lock_alive
stop_lease_renewer
lease_release || true

log_info "发布完成：镜像 ${IMAGE_TAG}，迁移 Job/${JOB_NAME} 完成（uid=${MIG_JOB_UID}），Deployment/${DEPLOYMENT_NAME} 已验证"
"${KUBECTL[@]}" -n "${NAMESPACE}" get deployment,service,pod -l app.kubernetes.io/name=surgecart || true
printf 'DEPLOY_K8S_SUCCESS\n'
