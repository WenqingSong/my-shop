#!/usr/bin/env bash
# T2-07 本地 Kubernetes 基础设施（MySQL / Redis / Kafka）部署与验证。
#
# 用法：
#   bash scripts/k8s-infra.sh secrets            # 从 .env / 环境变量创建数据库 Secret（不落盘、不回显）
#   bash scripts/k8s-infra.sh create             # 部署基础设施并等待就绪（幂等）
#   bash scripts/k8s-infra.sh status             # 依赖状态（Deployment 可用性 / Pod Ready / Service / PVC）
#   bash scripts/k8s-infra.sh verify [--keep]    # 集群内真实连通性验证（三类 Job）
#   bash scripts/k8s-infra.sh delete [--yes]     # 删除基础设施（PVC 数据会丢失）
#
# 与 scripts/deploy-k8s.sh 的分工：
#   - deploy-k8s.sh 负责**应用**发布（迁移 Job → Deployment → 验证，含发布锁）；
#   - 本脚本只负责**依赖**（数据库/缓存/消息），不发布应用、不执行数据库迁移。
#   两者都使用 `kubectl --context <ctx>`，通过 K8S_CONTEXT 指向同一个集群。
#
# 幂等性：
#   - secrets：已存在则原地替换（replace），不存在则创建；重复执行结果相同；
#   - create：kubectl apply -k 是声明式收敛；重复执行不会重建已就绪的 Pod，
#             也不会重复创建 Topic（Job 清单未变化则不会重跑）；
#   - delete：资源不存在时成功返回。
#
# 凭据边界：
#   - 本脚本只把 .env 中的值写进 Secret，**不**打印值、不写临时文件到仓库、不提交任何凭据；
#   - 缺值时直接失败并说明缺哪个键，不生成默认口令（默认口令等于没有认证）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

CLUSTER_NAME="${K8S_CLUSTER_NAME:-surgecart-local}"
K8S_CONTEXT="${K8S_CONTEXT:-kind-${CLUSTER_NAME}}"
NAMESPACE="${K8S_NAMESPACE:-surgecart}"
INFRA_DIR="manifest/kubernetes/infrastructure/base"
VERIFY_DIR="manifest/kubernetes/infrastructure/verify"

DB_SECRET="surgecart-db-credentials"
KAFKA_CONFIGMAP="surgecart-kafka-config"

MYSQL_TIMEOUT="${MYSQL_TIMEOUT:-420s}"
REDIS_TIMEOUT="${REDIS_TIMEOUT:-180s}"
KAFKA_TIMEOUT="${KAFKA_TIMEOUT:-420s}"
TOPICS_TIMEOUT="${TOPICS_TIMEOUT:-600s}"
VERIFY_TIMEOUT="${VERIFY_TIMEOUT:-300s}"

usage() {
  cat <<'EOF'
用法：bash scripts/k8s-infra.sh <secrets|create|status|verify|delete> [选项]

命令：
  secrets           创建/更新 Secret/surgecart-db-credentials（MYSQL_ROOT_PASSWORD、DATABASE_DEFAULT_PASS）。
                    值来自 .env 或已导出的环境变量；不打印、不写入仓库。
  create            部署 MySQL/Redis/Kafka 与业务 Topic 初始化 Job，等待全部就绪。
  status            Deployment/Pod/Service/PVC 状态；任一项未就绪时非零退出。
  verify            在集群内创建临时 Job，真实验证 MySQL SELECT 1、Redis PING、Kafka 生产/消费。
                    --keep 保留验证 Job 以便查看日志（默认验证成功后删除）。
  delete            删除基础设施（Deployment/Service/PVC/Job/ConfigMap）。
                    **PVC 数据会丢失**，需要 --yes 或交互确认。

环境变量：
  K8S_CLUSTER_NAME  集群名（默认 surgecart-local）
  K8S_CONTEXT       kubectl context（默认 kind-<K8S_CLUSTER_NAME>）
  K8S_NAMESPACE     命名空间（默认 surgecart）

前置：
  bash scripts/k8s-cluster.sh create     # 先有集群
  bash scripts/k8s-infra.sh secrets      # 先有数据库凭据
EOF
}

kc() { kubectl --context "${K8S_CONTEXT}" "$@"; }

require_kubectl() {
  command -v kubectl >/dev/null 2>&1 || fail "未找到 kubectl"
}

require_cluster() {
  require_kubectl
  kc cluster-info >/dev/null 2>&1 || fail "无法连接集群（context ${K8S_CONTEXT}）：先执行 bash scripts/k8s-cluster.sh create"
}

require_namespace() {
  kc get namespace "${NAMESPACE}" >/dev/null 2>&1 \
    || fail "命名空间 ${NAMESPACE} 不存在：先执行 bash scripts/k8s-infra.sh secrets"
}

require_db_secret() {
  kc -n "${NAMESPACE}" get secret "${DB_SECRET}" >/dev/null 2>&1 \
    || fail "Secret ${DB_SECRET} 不存在：先执行 bash scripts/k8s-infra.sh secrets"
  local key
  for key in MYSQL_ROOT_PASSWORD DATABASE_DEFAULT_PASS; do
    if ! kc -n "${NAMESPACE}" get secret "${DB_SECRET}" -o jsonpath="{.data.${key}}" 2>/dev/null | grep -q .; then
      fail "Secret ${DB_SECRET} 缺少键 ${key}：执行 bash scripts/k8s-infra.sh secrets 重新生成"
    fi
  done
}

# ---------------------------------------------------------------------------
# secrets
# ---------------------------------------------------------------------------
cmd_secrets() {
  require_cluster

  local root_pass="${MYSQL_ROOT_PASSWORD:-}"
  local app_pass="${DATABASE_DEFAULT_PASS:-}"
  local missing=()
  [[ -n "${root_pass}" ]] || missing+=("MYSQL_ROOT_PASSWORD")
  [[ -n "${app_pass}" ]] || missing+=("DATABASE_DEFAULT_PASS")
  if [[ ${#missing[@]} -gt 0 ]]; then
    log_error "缺少凭据：${missing[*]}"
    cat <<'EOF'
  处理方式：在仓库根目录的 .env 中补齐（.env 已被 .gitignore 忽略，不会进 Git）：
    MYSQL_ROOT_PASSWORD=<管理员口令>
    DATABASE_DEFAULT_PASS=<应用账号 surgecart 的口令>
  新值与旧值不同时，只在**首次初始化 PVC** 时生效；已有数据卷不会被改口令。
EOF
    exit 1
  fi
  if [[ "${root_pass}" == "${app_pass}" ]]; then
    log_warn "DATABASE_DEFAULT_PASS 与 MYSQL_ROOT_PASSWORD 相同：应用将使用管理员口令，建议分开"
  fi

  # 命名空间先于 Secret 存在：Secret 是命名空间级资源。
  if ! kc get namespace "${NAMESPACE}" >/dev/null 2>&1; then
    log_info "创建命名空间 ${NAMESPACE}"
    kc create namespace "${NAMESPACE}" >/dev/null
  fi

  # 凭据只经过管道与 0700 目录，不落在仓库文件里，也不出现在命令行参数上。
  local tmpdir
  tmpdir="$(mktemp -d)"
  chmod 700 "${tmpdir}"
  # shellcheck disable=SC2064  # 需要展开 tmpdir 后再注册 trap
  trap "rm -rf '${tmpdir}'" EXIT

  printf '%s' "${root_pass}" > "${tmpdir}/MYSQL_ROOT_PASSWORD"
  printf '%s' "${app_pass}" > "${tmpdir}/DATABASE_DEFAULT_PASS"

  local manifest="${tmpdir}/secret.yaml"
  kc -n "${NAMESPACE}" create secret generic "${DB_SECRET}" \
    --from-file="${tmpdir}/MYSQL_ROOT_PASSWORD" \
    --from-file="${tmpdir}/DATABASE_DEFAULT_PASS" \
    --dry-run=client -o yaml > "${manifest}"

  if kc -n "${NAMESPACE}" get secret "${DB_SECRET}" >/dev/null 2>&1; then
    # replace 而不是 apply：apply 会把 Secret 内容写进 last-applied-configuration 注解，
    # 让同一份口令在对象里出现两次（一次 data、一次注解），扩大泄露面。
    kc -n "${NAMESPACE}" replace -f "${manifest}" >/dev/null
    log_info "Secret ${DB_SECRET} 已更新（键：MYSQL_ROOT_PASSWORD、DATABASE_DEFAULT_PASS）"
  else
    kc -n "${NAMESPACE}" create -f "${manifest}" >/dev/null
    log_info "Secret ${DB_SECRET} 已创建（键：MYSQL_ROOT_PASSWORD、DATABASE_DEFAULT_PASS）"
  fi
}

# ---------------------------------------------------------------------------
# create
# ---------------------------------------------------------------------------
wait_rollout() {
  local name="$1" timeout="$2"
  log_info "等待 Deployment/${name} 就绪（上限 ${timeout}）..."
  if ! kc -n "${NAMESPACE}" rollout status "deployment/${name}" --timeout="${timeout}"; then
    echo
    log_error "Deployment/${name} 未在 ${timeout} 内就绪，最近日志："
    kc -n "${NAMESPACE}" logs "deployment/${name}" --tail=30 2>/dev/null || true
    return 1
  fi
}

wait_topics_job() {
  local attempt
  for attempt in 1 2; do
    log_info "等待 Job/kafka-topics-init 完成（第 ${attempt} 次，上限 ${TOPICS_TIMEOUT}）..."
    if kc -n "${NAMESPACE}" wait --for=condition=complete job/kafka-topics-init --timeout="${TOPICS_TIMEOUT}" 2>/dev/null; then
      kc -n "${NAMESPACE}" logs job/kafka-topics-init --tail=20 2>/dev/null | sed 's/^/  /' || true
      return 0
    fi
    log_warn "Job/kafka-topics-init 未完成，查看日志："
    kc -n "${NAMESPACE}" logs job/kafka-topics-init --tail=30 2>/dev/null || true
    # 只在 Job 明确失败时重建：Broker 恰好比等待窗口慢时，重建一次即可收敛。
    if [[ "${attempt}" -eq 1 ]]; then
      log_warn "删除失败的 Job 后重试一次（Topic 创建是幂等的）"
      kc -n "${NAMESPACE}" delete job/kafka-topics-init --ignore-not-found >/dev/null
      kc apply -k "${INFRA_DIR}" >/dev/null
    fi
  done
  return 1
}

print_summary() {
  echo
  log_info "基础设施状态："
  kc -n "${NAMESPACE}" get deploy,svc,pvc,job --no-headers 2>/dev/null | awk '{print "  "$0}' || true
  echo
  log_info "下一步："
  echo "  bash scripts/k8s-infra.sh verify      # 集群内真实验证（SELECT 1 / PING / 生产消费）"
  echo "  bash scripts/k8s-cluster.sh info      # 宿主机 port-forward 连接信息"
}

cmd_create() {
  require_cluster
  require_namespace
  require_db_secret

  log_info "应用基础设施清单（${INFRA_DIR}）..."
  kc apply -k "${INFRA_DIR}"

  # 启动顺序：存储/缓存先就绪，Kafka 自己的 Topic Job 会在容器内等待 Broker。
  wait_rollout mysql "${MYSQL_TIMEOUT}" || fail "MySQL 未就绪"
  wait_rollout redis "${REDIS_TIMEOUT}" || fail "Redis 未就绪"
  wait_rollout kafka "${KAFKA_TIMEOUT}" || fail "Kafka 未就绪"

  wait_topics_job || fail "Kafka Topic 初始化未完成（见上方日志）"

  print_summary
  log_info "基础设施部署完成"
}

# ---------------------------------------------------------------------------
# status
# ---------------------------------------------------------------------------
cmd_status() {
  require_cluster
  if ! kc get namespace "${NAMESPACE}" >/dev/null 2>&1; then
    fail "命名空间 ${NAMESPACE} 不存在（尚未部署基础设施）"
  fi

  local failed=0 name
  for name in mysql redis kafka; do
    if kc -n "${NAMESPACE}" rollout status "deployment/${name}" --timeout=1s >/dev/null 2>&1; then
      echo "  deployment/${name}: 就绪"
    else
      echo "  deployment/${name}: 未就绪"
      failed=1
    fi
  done

  local job_state
  job_state="$(kc -n "${NAMESPACE}" get job kafka-topics-init -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null || true)"
  if [[ "${job_state}" == "True" ]]; then
    echo "  job/kafka-topics-init: 已完成"
  else
    echo "  job/kafka-topics-init: 未完成"
    failed=1
  fi

  echo
  kc -n "${NAMESPACE}" get pods,svc,pvc --no-headers 2>/dev/null | awk '{print "  "$0}' || true

  if [[ "${failed}" -ne 0 ]]; then
    log_error "基础设施未全部就绪（Pod Running 不等于服务可用，请执行 verify 做协议级验证）"
    return 1
  fi
  log_info "基础设施全部就绪"
}

# ---------------------------------------------------------------------------
# verify
# ---------------------------------------------------------------------------
cmd_verify() {
  local keep=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --keep) keep=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) fail "未知参数：$1（可用 --keep）" ;;
    esac
  done

  require_cluster
  require_namespace
  require_db_secret

  # Job 的 Pod 模板不可变：重复 verify 必须先删旧 Job，否则 apply 会因字段不可变失败。
  log_info "清理上一轮验证 Job..."
  kc -n "${NAMESPACE}" delete job -l 'app.kubernetes.io/component=verification' --ignore-not-found --wait=true >/dev/null

  log_info "创建验证 Job（${VERIFY_DIR}）..."
  kc apply -k "${VERIFY_DIR}"

  local job failed=0
  for job in verify-mysql verify-redis verify-kafka; do
    log_info "等待 Job/${job} 完成（上限 ${VERIFY_TIMEOUT}）..."
    if ! kc -n "${NAMESPACE}" wait --for=condition=complete "job/${job}" --timeout="${VERIFY_TIMEOUT}" 2>/dev/null; then
      log_error "Job/${job} 未完成"
      failed=1
    fi
    echo "----- ${job} 日志 -----"
    kc -n "${NAMESPACE}" logs "job/${job}" --tail=40 2>/dev/null || true
    echo
  done

  if [[ "${failed}" -ne 0 ]]; then
    log_error "基础设施验证失败（Job 保留以便排查：kubectl -n ${NAMESPACE} describe job）"
    return 1
  fi

  if [[ "${keep}" -eq 1 ]]; then
    log_info "验证通过（--keep：Job 保留在命名空间 ${NAMESPACE}）"
  else
    kc -n "${NAMESPACE}" delete job -l 'app.kubernetes.io/component=verification' --ignore-not-found >/dev/null
    log_info "验证通过（验证 Job 已清理）"
  fi
}

# ---------------------------------------------------------------------------
# delete
# ---------------------------------------------------------------------------
cmd_delete() {
  local assume_yes=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --yes|-y) assume_yes=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) fail "未知参数：$1（可用 --yes）" ;;
    esac
  done

  cat <<EOF

⚠️  即将删除命名空间 ${NAMESPACE} 内的基础设施：mysql / redis / kafka

数据影响（务必确认）：
  - PVC mysql-data、redis-data、kafka-data 会被删除，其数据**不可恢复**；
  - Kafka 的业务 Topic 与消息随之消失，下次 create 会重新初始化空 Topic；
  - 不删除：Namespace 本身、ConfigMap/surgecart-database-config（应用与迁移入口共用）、
    任何应用资源（Deployment/surgecart、Secret/surgecart-app-credentials）。

EOF

  if [[ "${assume_yes}" -ne 1 ]]; then
    if [[ -t 0 ]]; then
      local answer
      read -r -p "确认删除？输入 yes 继续：" answer
      [[ "${answer}" == "yes" ]] || { log_warn "已取消"; return 1; }
    else
      log_error "非交互环境拒绝删除：确认数据影响后显式加 --yes"
      return 2
    fi
  fi

  require_cluster
  if ! kc get namespace "${NAMESPACE}" >/dev/null 2>&1; then
    log_info "命名空间 ${NAMESPACE} 不存在，无需删除（幂等）"
    return 0
  fi

  # 逐个点名删除，而不是 `kubectl delete -k`：
  # -k 会连 Namespace 与共享 ConfigMap 一起删除，等于顺手清掉应用资源。
  kc -n "${NAMESPACE}" delete deployment/mysql deployment/redis deployment/kafka --ignore-not-found
  kc -n "${NAMESPACE}" delete service/mysql service/redis service/kafka --ignore-not-found
  kc -n "${NAMESPACE}" delete job/kafka-topics-init --ignore-not-found
  kc -n "${NAMESPACE}" delete job -l 'app.kubernetes.io/component=verification' --ignore-not-found
  kc -n "${NAMESPACE}" delete configmap/"${KAFKA_CONFIGMAP}" --ignore-not-found
  # PVC 最后删除：先停工作负载再删卷，避免 Pod 持有已删除的卷。
  kc -n "${NAMESPACE}" delete pvc/mysql-data pvc/redis-data pvc/kafka-data --ignore-not-found

  log_info "基础设施已删除（PVC 数据已丢弃）"
}

# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------
main() {
  local command="${1:-}"
  [[ $# -gt 0 ]] && shift
  case "${command}" in
    secrets) cmd_secrets ;;
    create) cmd_create ;;
    status) cmd_status ;;
    verify) cmd_verify "$@" ;;
    delete) cmd_delete "$@" ;;
    ""|-h|--help|help) usage ;;
    *) usage; fail "未知命令：${command}" ;;
  esac
}

main "$@"
