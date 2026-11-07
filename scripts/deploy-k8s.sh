#!/usr/bin/env bash
# T2-06.1 Kubernetes 发布脚本：**先拿发布锁，再迁移，后应用，最后验证**。
#
#   bash scripts/deploy-k8s.sh [--context <name>] [--migration-timeout <sec>] [--rollout-timeout <sec>]
#                              [--lock-wait-timeout <sec>] [--lease-duration <sec>]
#                              [--lease-renew-interval <sec>] [--allow-downgrade]
#
# 执行顺序（任一步失败立即非零退出，后续步骤不执行）：
#   1. 环境预检：kubectl / 集群可达 / 构建两个清单 / 占位符 / 镜像与版本标记一致
#   2. 命名空间引导（幂等；锁资源必须落在已存在的命名空间里）
#   3. 锁权限预检（get / create / update leases）：权限不足在迁移之前失败
#   4. 获取发布锁 coordination.k8s.io/v1 Lease/surgecart-deploy-lock（原子 create + resourceVersion CAS）
#   5. 锁内复检：Secret 就绪 + 版本降级保护（当前 Deployment 版本 vs 本次目标版本）
#   6. 应用 Migration 清单 → 轮询 Job 状态（Failed 与 Timeout 分开处理）
#   7. 应用 Application 清单 → 轮询 Deployment 收敛
#   8. 发布成功判定：镜像 / 版本标签 / generation / 副本数 / 实际 Pod 全部匹配
#   9. 释放发布锁 → 打印 DEPLOY_K8S_SUCCESS
#
# 明确不做：
#   - 不执行 `migrate force`（人工风险操作，脚本永不调用）；
#   - 不修改清单里的镜像版本（版本是 Git 里的事实，脚本只校验和部署）；
#   - 不删除 Job（迁移失败或超时都不自动删除，见 docs/development/kubernetes.md §10）；
#   - 不在迁移完成后提前释放发布锁（锁覆盖到「验证通过」为止）；
#   - 不使用本地 flock（不同 CI Runner 不共享文件系统，本地锁等于没有锁）。
#
# 退出码：
#   0  发布成功且已验证（stdout 打印 DEPLOY_K8S_SUCCESS）
#   1  参数 / 预检 / 清单构建 / 应用清单 apply 失败
#   2  迁移失败（Job Failed=True；应用 Deployment 未更新）
#   3  发布锁失败（权限不足 / 获取超时 / 续租失败被中止）
#   4  版本降级保护拒绝（降级、分叉、当前或目标版本无法在 Git 历史中验证）
#   5  发布后验证失败（实际镜像/版本/副本与目标不一致，或未在超时内收敛）
#   6  迁移等待超时（Job 未确认失败，Pod 可能仍在执行 DDL）
#   143 收到 SIGTERM（已尽可能释放锁）
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
LOCK_LEASE_DURATION=30
LOCK_RENEW_INTERVAL=10
LOCK_WAIT_TIMEOUT=60

CONTEXT=""
MIGRATION_TIMEOUT=900
ROLLOUT_TIMEOUT=600
ALLOW_DOWNGRADE=0
POLL_INTERVAL=2

# Pod 选择器必须与 base/deployment.yaml 的 Pod 模板标签一致（test/manifest 断言，
# Service selector 用同一组标签，因此这里不会选到别的负载）。
POD_SELECTOR="app.kubernetes.io/name=surgecart,app.kubernetes.io/component=backend"

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
'{{(index .spec.template.spec.containers 0).image}}~'\
'{{index .spec.template.metadata.labels "app.kubernetes.io/version"}}'

# Lease 快照：resourceVersion ~ uid ~ holderIdentity ~ acquireTime ~ renewTime ~
# leaseDurationSeconds ~ leaseTransitions
LEASE_STATE_TEMPLATE='{{index .metadata "resourceVersion"}}~{{index .metadata "uid"}}~'\
'{{index .spec "holderIdentity"}}~{{index .spec "acquireTime"}}~{{index .spec "renewTime"}}~'\
'{{index .spec "leaseDurationSeconds"}}~{{index .spec "leaseTransitions"}}'

# Job 状态：Complete.status ~ Failed.status ~ Failed.reason ~ Failed.message
JOB_STATE_TEMPLATE='{{$complete := ""}}{{$failed := ""}}{{$reason := ""}}{{$message := ""}}'\
'{{if .status}}{{range .status.conditions}}'\
'{{if eq .type "Complete"}}{{$complete = .status}}{{end}}'\
'{{if eq .type "Failed"}}{{$failed = .status}}{{$reason = .reason}}{{$message = .message}}{{end}}'\
'{{end}}{{end}}'\
'{{$complete}}~{{$failed}}~{{$reason}}~{{$message}}'

# 一行一个 Pod：name ~ phase ~ deletionTimestamp ~ app.kubernetes.io/version ~
# 容器 ready（聚合）~ 镜像
PODS_STATE_TEMPLATE='{{range .items}}'\
'{{index .metadata "name"}}~{{index .status "phase"}}~{{index .metadata "deletionTimestamp"}}~'\
'{{index .metadata.labels "app.kubernetes.io/version"}}~'\
'{{if .status}}{{range .status.containerStatuses}}{{.ready}}{{end}}{{end}}~'\
'{{(index .spec.containers 0).image}}{{"\n"}}'\
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
  --migration-timeout <sec>     等待迁移 Job 完成的上限（默认 900）
  --rollout-timeout <sec>       等待 Deployment 收敛的上限（默认 600）
  --lock-wait-timeout <sec>     等待其他发布释放锁的上限（默认 60）
  --lease-duration <sec>        发布锁租期（默认 30；崩溃后最多这么久即可被接管）
  --lease-renew-interval <sec>  发布锁续租间隔（默认 10；必须小于租期）
  --allow-downgrade             允许把 Deployment 降级到更旧的 commit（默认禁止）
  -h, --help                    显示帮助

退出码：
  0 成功（打印 DEPLOY_K8S_SUCCESS）；1 参数/预检/apply 失败；2 迁移失败；
  3 发布锁失败；4 版本降级保护拒绝；5 发布后验证失败；6 迁移等待超时。

前置条件：
  - 已安装 kubectl，且 kubeconfig 指向目标集群；
  - 执行发布的身份具备 leases 的 get/create/update 权限
    （见 manifest/kubernetes/examples/deploy-lock/rbac.yaml）；
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
    --allow-downgrade)      ALLOW_DOWNGRADE=1; shift ;;
    -h|--help)              usage; exit 0 ;;
    *)                      die "未知参数: $1（使用 --help 查看用法）" ;;
  esac
done

for value in "${MIGRATION_TIMEOUT}" "${ROLLOUT_TIMEOUT}" "${LOCK_WAIT_TIMEOUT}" \
             "${LOCK_LEASE_DURATION}" "${LOCK_RENEW_INTERVAL}"; do
  [[ "${value}" =~ ^[0-9]+$ ]] || die "超时/租期必须是非负整数，实际: ${value}"
done
(( LOCK_LEASE_DURATION > 0 )) || die "--lease-duration 必须大于 0"
(( LOCK_RENEW_INTERVAL > 0 )) || die "--lease-renew-interval 必须大于 0"
(( LOCK_RENEW_INTERVAL < LOCK_LEASE_DURATION )) \
  || die "--lease-renew-interval (${LOCK_RENEW_INTERVAL}) 必须小于 --lease-duration (${LOCK_LEASE_DURATION})：否则锁会先过期再续租"

# CI 不允许「默认携带」降级授权：流水线必须显式再加一层开关，避免回滚开关被写进流水线后长期生效。
is_ci_env() {
  [[ "${CI:-}" =~ ^(1|true|TRUE|True)$ ]]
}
if (( ALLOW_DOWNGRADE == 1 )) && is_ci_env && [[ "${DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI:-}" != "1" ]]; then
  die 4 "CI 环境禁止自动降级：--allow-downgrade 在 CI 中需要额外设置 DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1（人工应急）"
fi

KUBECTL=(kubectl --request-timeout=30s)
[[ -n "${CONTEXT}" ]] && KUBECTL+=(--context "${CONTEXT}")

cd "${ROOT_DIR}"

# ---------------------------------------------------------------------------
# 运行时状态与清理
# ---------------------------------------------------------------------------
command -v kubectl >/dev/null 2>&1 || die "未安装 kubectl：无法构建 Kustomize 清单或部署集群"

TMP_DIR="$(mktemp -d)"
LOCK_STATE_DIR="${TMP_DIR}/lock"
mkdir -p "${LOCK_STATE_DIR}"

LOCK_HELD=0
RENEWER_PID=""
DOWNGRADE_AUTHORIZED=0
HOLDER_IDENTITY="deploy-$(hostname 2>/dev/null || echo unknown)-$$-$(date -u +%Y%m%dT%H%M%SZ)"
# holderIdentity 只保留 DNS 子集字符：避免影响锁记录解析与可读性。
HOLDER_IDENTITY="$(printf '%s' "${HOLDER_IDENTITY}" | tr -c 'A-Za-z0-9._-' '-')"
LOCK_ACQUIRED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

cleanup() {
  local code=$?
  # 清理路径不允许再触发 set -e 退出：这里必须尽最大努力释放锁并删除临时文件。
  set +e
  # 续租进程只有在 start_lease_renewer 之后才存在，此时 stop_lease_renewer 已定义。
  if [[ -n "${RENEWER_PID}" ]]; then
    stop_lease_renewer || true
  fi
  if (( LOCK_HELD )); then
    lease_release || true
  fi
  rm -rf "${TMP_DIR}"
  exit "${code}"
}

on_signal() {
  local name="$1"
  if [[ -f "${LOCK_STATE_DIR}/lost" ]]; then
    log_error "收到 SIG${name} 且发布锁续租已失败：中止发布，不再变更任何资源"
    if declare -F report_lock_lost >/dev/null; then
      report_lock_lost
    fi
    exit 3
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

# 判断 kubectl 的错误输出是否表示「对象不存在」。
is_absent_error() {
  local file="$1"
  [[ -f "${file}" ]] || return 1
  grep -qi 'not[[:space:]]*found' "${file}" 2>/dev/null
}

# 从镜像引用里提取 tag（不含 digest 形式）。
tag_of_image() {
  local image="$1"
  [[ -n "${image}" ]] || return 1
  [[ "${image}" == *"@"* ]] && return 1
  [[ "${image}" == *":"* ]] || return 1
  printf '%s' "${image##*:}"
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
#     本脚本用「每次变更前检查续租状态 + 变更前 CAS 校验持有者」把窗口压到最小，
#     但无法做到「旧持有者的写一定被拒绝」。剩余风险见 docs/design/deploy-lock.md §6。
# ---------------------------------------------------------------------------

# 生成 Lease 清单：holder / acquireTime / renewTime / duration / transitions / rv / uid
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
  if ! out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get lease "${LOCK_NAME}" \
    -o "go-template=${LEASE_STATE_TEMPLATE}" 2>"${err}")"; then
    LEASE_READ_ERROR="$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
    return 1
  fi
  out="$(strip_template_noise "${out}")"
  IFS='~' read -r LEASE_RV LEASE_UID LEASE_HOLDER LEASE_ACQUIRE LEASE_RENEW \
    LEASE_DURATION LEASE_TRANSITIONS <<< "${out}"
  return 0
}

# 锁是否已过期（renewTime + leaseDurationSeconds < now）。
# 无法解析 renewTime 时返回「未过期」：宁可不接管，也不打断一个可能仍在执行的发布。
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

lease_create() {
  lease_manifest "${HOLDER_IDENTITY}" "${LOCK_ACQUIRED_AT}" "${LOCK_ACQUIRED_AT}" \
    "${LOCK_LEASE_DURATION}" 0 "" "" > "${LOCK_STATE_DIR}/lease-create.yaml"
  "${KUBECTL[@]}" -n "${NAMESPACE}" create -f "${LOCK_STATE_DIR}/lease-create.yaml" >/dev/null
}

# 接管一把已释放或已过期的锁：CAS 写入，resourceVersion 不匹配即失败（别人抢先）。
lease_takeover() {
  local now transitions
  now="$(timestamp_utc)"
  transitions=$(( $(to_int "${LEASE_TRANSITIONS:-0}") + 1 ))
  lease_manifest "${HOLDER_IDENTITY}" "${now}" "${now}" \
    "${LOCK_LEASE_DURATION}" "${transitions}" "${LEASE_RV}" "${LEASE_UID}" \
    > "${LOCK_STATE_DIR}/lease-takeover.yaml"
  "${KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-takeover.yaml" >/dev/null
}

# 续租：以 resourceVersion 做 CAS；持有者不是自己时立即失败（锁已被接管）。
lease_renew() {
  lease_read || return 1
  [[ "${LEASE_HOLDER}" == "${HOLDER_IDENTITY}" ]] || return 1
  lease_manifest "${HOLDER_IDENTITY}" "${LEASE_ACQUIRE:-${LOCK_ACQUIRED_AT}}" \
    "$(timestamp_utc)" "$(to_int "${LEASE_DURATION:-0}")" \
    "$(to_int "${LEASE_TRANSITIONS:-0}")" "${LEASE_RV}" "${LEASE_UID}" \
    > "${LOCK_STATE_DIR}/lease-renew.yaml"
  "${KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-renew.yaml" >/dev/null
}

# 释放：先确认持有者仍是自己，再用 CAS 把持有者清空（不删除对象，避免与接管者竞态）。
# 释放失败不算发布失败：锁会按租期自然过期。
# 重试是必要的：后台续租可能刚刚用旧 resourceVersion 提交过一次 renew，导致第一次 CAS 冲突。
lease_release() {
  (( LOCK_HELD )) || return 0
  local attempt now
  for attempt in 1 2 3; do
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
    now="$(timestamp_utc)"
    lease_manifest "" "${now}" "${now}" 1 "$(to_int "${LEASE_TRANSITIONS:-0}")" \
      "${LEASE_RV}" "${LEASE_UID}" > "${LOCK_STATE_DIR}/lease-release.yaml"
    if "${KUBECTL[@]}" -n "${NAMESPACE}" replace -f "${LOCK_STATE_DIR}/lease-release.yaml" >/dev/null; then
      LOCK_HELD=0
      log_info "已释放发布锁 ${NAMESPACE}/${LOCK_NAME}"
      return 0
    fi
    sleep 1
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
# 这里只写 stop 标记 + 发 SIGTERM，不做 wait：续租进程可能正阻塞在 sleep 上
# （bash 会延迟处理信号），wait 会让退出路径多等一个续租周期；它的输出已经重定向到
# renew.log，不占用调用方管道，因此让它看到 stop 标记后自行退出即可。
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

# 权限预检必须在迁移之前：没有 leases 权限就无法保证互斥，不能「先迁移再说」。
check_lease_permissions() {
  local verb
  for verb in get create update; do
    if ! "${KUBECTL[@]}" auth can-i "${verb}" leases.coordination.k8s.io -n "${NAMESPACE}" >/dev/null 2>&1; then
      log_error "当前身份在命名空间 ${NAMESPACE} 缺少 leases 的 ${verb} 权限，无法建立发布互斥。"
      log_error "授权示例：kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml"
      log_error "自检：${KUBECTL[*]} auth can-i ${verb} leases.coordination.k8s.io -n ${NAMESPACE}"
      exit 3
    fi
  done
  log_info "发布锁权限就绪（leases: get/create/update @ ${NAMESPACE}）"
}

lock_is_free() {
  [[ -z "${LEASE_HOLDER}" ]]
}

acquire_release_lock() {
  local deadline=$(( SECONDS + LOCK_WAIT_TIMEOUT )) remaining
  log_info "获取发布锁 ${NAMESPACE}/${LOCK_NAME}（holder=${HOLDER_IDENTITY}，租期 ${LOCK_LEASE_DURATION}s，续租间隔 ${LOCK_RENEW_INTERVAL}s）"

  while :; do
    if lease_create 2> "${LOCK_STATE_DIR}/lease-create.err"; then
      LOCK_HELD=1
      log_info "已获取发布锁（新建 Lease，holder=${HOLDER_IDENTITY}）"
      return 0
    fi
    if ! grep -q 'AlreadyExists' "${LOCK_STATE_DIR}/lease-create.err" 2>/dev/null; then
      log_error "创建发布锁失败：$(tr '\n' ' ' < "${LOCK_STATE_DIR}/lease-create.err")"
      return 2
    fi

    if lease_read; then
      if lock_is_free; then
        log_info "发布锁处于已释放状态，尝试接管（resourceVersion=${LEASE_RV}）"
        if lease_takeover; then
          LOCK_HELD=1
          log_info "已获取发布锁（接管已释放的 Lease，holder=${HOLDER_IDENTITY}）"
          return 0
        fi
        log_warn "接管失败（CAS 冲突）：其他 Runner 抢先"
      elif lease_expired; then
        log_warn "发布锁已过期（holder=${LEASE_HOLDER}，renewTime=${LEASE_RENEW}）：尝试接管"
        if lease_takeover; then
          LOCK_HELD=1
          log_info "已获取发布锁（接管过期 Lease，holder=${HOLDER_IDENTITY}，leaseTransitions=$(( $(to_int "${LEASE_TRANSITIONS:-0}") + 1 ))）"
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
# 版本降级保护（T2-06.1-02）
#
# C = 当前 Deployment 记录的版本（Pod 模板标签 app.kubernetes.io/version，必要时回退镜像 tag）
# T = 本次目标版本（镜像 tag，同时必须等于版本标记）
#
#   Deployment 不存在      → 允许（首次部署）
#   C == T                → 允许（幂等重发）
#   C 是 T 的祖先          → 允许（升级）
#   T 是 C 的祖先          → 拒绝（降级）
#   C 与 T 分叉            → 拒绝
#   C 或 T 无法在 Git 历史中解析 → 拒绝（fail-closed，绝不猜测先后）
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
    if current="$(tag_of_image "${DS_IMAGE}")"; then
      log_warn "Deployment 未记录 app.kubernetes.io/version，回退到当前镜像标签 ${current}"
    else
      current=""
    fi
  fi
  if [[ -z "${current}" ]]; then
    reject_release_version "无法识别当前线上版本（既无版本标签，镜像 ${DS_IMAGE:-<空>} 也无法提取版本）"
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

# 读取 Deployment 快照：CUR_EXISTS=0 表示不存在；读取失败（网络/权限）返回非零。
read_deployment_state() {
  local err="${TMP_DIR}/get-deployment.err" out
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get deployment "${DEPLOYMENT_NAME}" \
    -o "go-template=${DEPLOYMENT_STATE_TEMPLATE}" 2>"${err}")"; then
    out="$(strip_template_noise "${out}")"
    parse_deployment_state "${out}"
    CUR_EXISTS=1
    return 0
  fi
  if is_absent_error "${err}"; then
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

read_job_state() {
  local err="${TMP_DIR}/get-job.err" out
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" \
    -o "go-template=${JOB_STATE_TEMPLATE}" 2>"${err}")"; then
    out="$(strip_template_noise "${out}")"
    IFS='~' read -r JOB_COMPLETE JOB_FAILED JOB_REASON JOB_MESSAGE <<< "${out}"
    JOB_EXISTS=1
    return 0
  fi
  JOB_COMPLETE="" JOB_FAILED="" JOB_REASON="" JOB_MESSAGE=""
  if is_absent_error "${err}"; then
    JOB_EXISTS=0
    return 0
  fi
  log_error "读取 Job/${JOB_NAME} 失败：$(tr '\n' ' ' < "${err}" 2>/dev/null || true)"
  return 1
}

# 迁移 Job 的 Pod 快照：name ~ phase ~ deletionTimestamp（用于 Timeout 判定「是否仍在执行」）
read_job_pods() {
  local err="${TMP_DIR}/get-job-pods.err" out
  if out="$("${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "job-name=${JOB_NAME}" \
    -o "go-template=${PODS_STATE_TEMPLATE}" 2>"${err}")"; then
    strip_template_noise "${out}"
    return 0
  fi
  printf '%s' ""
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

log_info "目标命名空间: ${NAMESPACE}；镜像版本: ${IMAGE_TAG}；迁移 Job: ${JOB_NAME}"

# ---------------------------------------------------------------------------
# 2. 命名空间引导（锁之前，且只做幂等操作）
#
# 首次部署时命名空间还不存在，而锁资源必须落在命名空间里。这里的顺序是：
#   建命名空间（幂等）→ 锁权限预检 → 获取锁 → 之后才是迁移与应用。
# 命名空间创建本身不是「发布变更」：它幂等、不改动任何工作负载，且是锁得以存在的前提。
# 绝不因为「锁资源还不存在」就跳过锁直接部署。
# ---------------------------------------------------------------------------
if ! "${KUBECTL[@]}" get namespace "${NAMESPACE}" >/dev/null 2>&1; then
  log_info "命名空间 ${NAMESPACE} 不存在，按 ${NAMESPACE_MANIFEST} 创建"
  "${KUBECTL[@]}" apply -f "${ROOT_DIR}/${NAMESPACE_MANIFEST}" >/dev/null \
    || die "创建命名空间 ${NAMESPACE} 失败"
fi

# ---------------------------------------------------------------------------
# 3. 锁权限预检（必须在迁移之前失败）
# ---------------------------------------------------------------------------
check_lease_permissions

# ---------------------------------------------------------------------------
# 4. 获取发布锁
# ---------------------------------------------------------------------------
if ! acquire_release_lock; then
  log_error "未能在 ${LOCK_WAIT_TIMEOUT}s 内获取发布锁 ${NAMESPACE}/${LOCK_NAME}：本次发布不执行任何变更。"
  log_error "排查：${KUBECTL[*]} -n ${NAMESPACE} get lease ${LOCK_NAME} -o yaml"
  log_error "锁卡住时的人工处理见 docs/development/kubernetes.md（禁止直接删除正在被持用的锁）。"
  exit 3
fi

start_lease_renewer
log_info "发布锁已生效：迁移、应用更新、发布验证全程持锁（后台每 ${LOCK_RENEW_INTERVAL}s 续租）"

# ---------------------------------------------------------------------------
# 5. 锁内复检：Secret 与版本降级保护
#
# 这些条件都可能在「预检 → 拿到锁」的窗口内被其他流水线改动，因此必须在锁内重新判断。
# ---------------------------------------------------------------------------
require_lock_alive

mapfile -t SECRET_NAMES < <(awk '/secretRef:/{flag=1; next} flag && /name:/{print $2; flag=0}' \
  "${APPLICATION_MANIFEST}" | sort -u)
[[ "${#SECRET_NAMES[@]}" -gt 0 ]] || die "Application 清单没有任何 Secret 引用（配置边界异常）"
for secret in "${SECRET_NAMES[@]}"; do
  "${KUBECTL[@]}" -n "${NAMESPACE}" get secret "${secret}" >/dev/null 2>&1 \
    || die "缺少 Secret ${NAMESPACE}/${secret}：请先按 manifest/kubernetes/examples/secrets/ 创建"
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
log_info "应用 Migration 清单（Job/${JOB_NAME}）"
"${KUBECTL[@]}" apply -f "${MIGRATION_MANIFEST}" \
  || die "Migration 清单应用失败（应用未更新）"

log_info "等待 Job/${JOB_NAME} 完成（上限 ${MIGRATION_TIMEOUT}s）"
migration_outcome="timeout"
deadline=$(( SECONDS + MIGRATION_TIMEOUT ))
while (( SECONDS < deadline )); do
  require_lock_alive
  if read_job_state; then
    if [[ "${JOB_COMPLETE}" == "True" ]]; then
      migration_outcome="complete"
      break
    fi
    if [[ "${JOB_FAILED}" == "True" ]]; then
      migration_outcome="failed"
      break
    fi
  fi
  sleep "${POLL_INTERVAL}"
done

if [[ "${migration_outcome}" == "failed" ]]; then
  # ---- Failed：确定性失败，打印证据，给出安全的人工恢复步骤 ----
  log_error "迁移失败：Job/${JOB_NAME} 进入 Failed=True（应用 Deployment 未更新）"
  log_error "失败原因: ${JOB_REASON:-<无 reason>} ${JOB_MESSAGE:-<无 message>}"
  log_error "--- Job 状态 ---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" -o wide >&2 || true
  log_error "--- Job Pod 状态 ---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" get pods -l "job-name=${JOB_NAME}" -o wide >&2 || true
  log_error "--- Job 日志（最后 200 行）---"
  "${KUBECTL[@]}" -n "${NAMESPACE}" logs "job/${JOB_NAME}" --tail=200 --all-containers=true >&2 || true
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

if [[ "${migration_outcome}" != "complete" ]]; then
  # ---- Timeout：等待超时 ≠ 迁移失败。Pod 可能仍在执行 DDL，绝不能按失败流程处理 ----
  log_error "迁移等待超时（${MIGRATION_TIMEOUT}s）：Job/${JOB_NAME} 既未 Complete 也未 Failed。"
  log_error "**超时不等同于失败**：迁移 Pod 可能仍在执行 DDL，删除 Job 会中断迁移并可能留下 dirty 状态。"
  require_lock_alive
  log_error "--- Job 当前状态 ---"
  if read_job_state && (( JOB_EXISTS )); then
    log_error "conditions: Complete=${JOB_COMPLETE:-<空>} Failed=${JOB_FAILED:-<空>}"
    "${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" -o wide >&2 || true
  else
    log_error "Job/${JOB_NAME} 当前状态无法读取（可能尚未被 controller 创建/观察到）"
  fi
  log_error "--- 迁移 Pod 当前状态 ---"
  job_pods="$(read_job_pods || true)"
  pods_still_running=0
  if [[ -n "${job_pods}" ]]; then
    printf '%s\n' "${job_pods}" >&2
    while IFS= read -r line; do
      [[ -n "${line}" ]] || continue
      IFS='~' read -r pod_name pod_phase pod_deleting pod_version pod_ready pod_image <<< "${line}"
      case "${pod_phase}" in
        Running|Pending|Unknown) pods_still_running=1 ;;
      esac
      [[ -n "${pod_deleting}" ]] && pods_still_running=1
    done <<< "${job_pods}"
  else
    log_error "（没有查询到迁移 Pod：可能已被 TTL 清理，或 Job 尚未调度）"
  fi
  if (( pods_still_running )); then
    log_error "**禁止删除 Job**：迁移 Pod 仍在运行/终止中，删除会中断 DDL。"
  else
    log_error "当前未见运行中的迁移 Pod，但 Job 未收敛：请先确认数据库侧是否有残留 DDL，再决定是否删除 Job。"
  fi
  log_error "继续等待（不要杀 Pod）："
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} wait --for=condition=complete job/${JOB_NAME} --timeout=900s"
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} logs -f job/${JOB_NAME} --tail=100"
  log_error "后续检查（在 Job 收敛后）："
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} get job,pod -l job-name=${JOB_NAME} -o wide"
  log_error "  ${KUBECTL[*]} -n ${NAMESPACE} get job ${JOB_NAME} -o jsonpath='{.status.conditions}'"
  log_error "确认 Pod 已退出且数据库无残留 DDL 之后，才允许删除 Job 重跑。"
  log_error "应用 Deployment 未更新；发布锁将释放，等待下一次发布。"
  exit 6
fi

log_info "迁移成功: Job/${JOB_NAME} 已完成"

# ---------------------------------------------------------------------------
# 7. 应用
# ---------------------------------------------------------------------------
require_lock_alive
log_info "应用 Application 清单"
"${KUBECTL[@]}" apply -f "${APPLICATION_MANIFEST}" || die "Application 清单应用失败"

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
    if [[ "${pod_image}" != "${expected_image}" ]]; then
      log_error "发布验证失败：Pod/${pod_name} 镜像 = ${pod_image:-<空>}，期望 = ${expected_image}"
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
      log_error "发布验证失败：Pod/${pod_name} 容器未 Ready（readinessProbe 未通过）"
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

require_lock_alive
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

log_info "发布验证通过：镜像 ${IMAGE_TAG}，版本标记 ${TARGET_VERSION}，副本与 Pod 全部就绪"

# 锁必须在「验证通过」之后才释放：迁移完成不等于发布完成。
require_lock_alive
stop_lease_renewer
lease_release || true

log_info "发布完成：镜像 ${IMAGE_TAG}，迁移 Job/${JOB_NAME} 完成，Deployment/${DEPLOYMENT_NAME} 已验证"
"${KUBECTL[@]}" -n "${NAMESPACE}" get deployment,service,pod -l app.kubernetes.io/name=surgecart || true
printf 'DEPLOY_K8S_SUCCESS\n'
