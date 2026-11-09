#!/usr/bin/env bash
# T2-07 本地 Kubernetes 集群生命周期（kind 单节点）。
#
# 用法：
#   bash scripts/k8s-cluster.sh doctor            # 检查依赖工具与本机是否具备运行 kind 的能力
#   bash scripts/k8s-cluster.sh create            # 创建集群（已存在则跳过，幂等）
#   bash scripts/k8s-cluster.sh status            # 集群状态（节点 Ready、系统 Pod、StorageClass）
#   bash scripts/k8s-cluster.sh info              # 连接信息（context / API / port-forward 命令）
#   bash scripts/k8s-cluster.sh destroy [--yes]   # 删除集群（会丢失全部 PVC 数据，见下方告警）
#
# 为什么不引入第二套集群工具：
#   kind 在本仓库的用途只有「本地能跑起 Pod」，Docker 已经是既有依赖（镜像构建、compose 联调）。
#   同时装 minikube/k3d 只会让「本地集群」这件事出现多个事实来源。
#
# 幂等性：
#   - create 先判断集群是否已存在；存在则只做状态检查，**不会**重建或改动已有集群；
#   - destroy 在集群不存在时成功返回（已经是目标状态）；
#   - 两条命令都可以重复执行，不会留下半成品状态。
#
# 数据影响（destroy）：
#   集群内所有 PVC 的数据都会丢失。kind 的 PV 由节点容器内的目录提供，
#   删除集群 = 删除节点容器 = 删除数据。MySQL/Redis/Kafka 的消息与数据不可恢复。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 集群生命周期不需要 .env（不需要任何业务凭据）。
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

CLUSTER_NAME="${K8S_CLUSTER_NAME:-surgecart-local}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
KIND_CONFIG="${SCRIPT_DIR}/kind-config.yaml"
# 用于探测「Docker 是否允许特权容器」的镜像：kind 的控制面容器必须是特权容器。
# 可用 K8S_PROBE_IMAGE 覆盖（例如内网镜像仓库）。
PROBE_IMAGE="${K8S_PROBE_IMAGE:-alpine:3.20}"
# 创建/删除的等待上限。
KIND_CREATE_TIMEOUT="${KIND_CREATE_TIMEOUT:-300s}"

usage() {
  cat <<'EOF'
用法：bash scripts/k8s-cluster.sh <doctor|create|status|info|destroy> [选项]

命令：
  doctor           检查 docker / kubectl / kind 是否存在、Docker 守护进程是否可用、
                   Docker 是否允许特权容器（kind 的硬前提）。任一不满足即非零退出。
  create           创建 kind 集群（幂等：已存在则跳过创建，仅做状态检查）。
  status           输出节点、系统 Pod 与 StorageClass 状态；节点不 Ready 时非零退出。
  info             输出集群连接信息与三个依赖的 port-forward 命令。
  destroy          删除集群。**会丢失全部 PVC 数据**，需要 --yes 或交互确认。

环境变量：
  K8S_CLUSTER_NAME      集群名（默认 surgecart-local），决定 kubectl context（kind-<name>）
  K8S_PROBE_IMAGE       doctor 探测特权容器使用的镜像（默认 alpine:3.20）
  KIND_CREATE_TIMEOUT   kind 创建集群的等待上限（默认 300s）

示例：
  bash scripts/k8s-cluster.sh doctor
  bash scripts/k8s-cluster.sh create
  bash scripts/k8s-cluster.sh info
  bash scripts/k8s-cluster.sh destroy --yes
EOF
}

# ---------------------------------------------------------------------------
# 依赖工具
# ---------------------------------------------------------------------------
has() { command -v "$1" >/dev/null 2>&1; }

docker_ok() { has docker && docker info >/dev/null 2>&1; }

kind_ok() { has kind; }

kubectl_ok() { has kubectl; }

# 检查 kubectl 能否独立构建清单（kubectl kustomize 内建 kustomize）。
kustomize_ok() { has kubectl && kubectl kustomize "${ROOT_DIR}/manifest/kubernetes/base" >/dev/null 2>&1; }

# Docker 是否允许创建特权容器：kind 的控制面容器需要特权（sysfs/cgroup 挂载、网络命名空间）。
# 这不是「资源不足」问题：受限的嵌套容器环境里会直接 operation not permitted。
docker_allows_privileged() {
  local output
  if output="$(docker run --rm --privileged --network none --entrypoint /bin/true "${PROBE_IMAGE}" 2>&1)"; then
    return 0
  fi
  PROBE_ERROR="${output}"
  return 1
}

cluster_exists() {
  kind_ok || return 1
  kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"
}

require_cluster_tools() {
  docker_ok || fail "Docker 守护进程不可用：kind 依赖 Docker 创建控制面容器（检查 docker info）"
  kubectl_ok || fail "未找到 kubectl：请安装后重试（https://kubernetes.io/docs/tasks/tools/）"
  kind_ok || fail "未找到 kind：本仓库的本地集群方案固定为 kind（https://kind.sigs.k8s.io/docs/user/quick-start/#installation）"
  [[ -f "${KIND_CONFIG}" ]] || fail "缺少 kind 集群定义：${KIND_CONFIG}"
}

# ---------------------------------------------------------------------------
# doctor
# ---------------------------------------------------------------------------
cmd_doctor() {
  local failed=0

  log_info "依赖工具"
  if docker_ok; then
    echo "  docker    : $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown)"
  else
    if has docker; then
      log_error "  docker    : 已安装但守护进程不可用（docker info 失败）"
    else
      log_error "  docker    : 未安装"
    fi
    failed=1
  fi

  if kubectl_ok; then
    echo "  kubectl   : $(kubectl version --client=true 2>/dev/null | head -1 | awk '{print $3}' || true)"
  else
    log_error "  kubectl   : 未安装（清单构建/集群操作都需要）"
    failed=1
  fi

  if kind_ok; then
    echo "  kind      : $(kind version 2>/dev/null || echo unknown)"
  else
    log_error "  kind      : 未安装（本地集群方案依赖 kind）"
    failed=1
  fi

  if kustomize_ok; then
    echo "  kustomize : kubectl 内建（kubectl kustomize 可用）"
  elif has kustomize; then
    echo "  kustomize : 独立 CLI"
  else
    log_error "  kustomize : 不可用（kubectl kustomize 失败）"
    failed=1
  fi

  log_info "集群运行时前提"
  if [[ "${failed}" -ne 0 ]]; then
    log_warn "  跳过特权容器探测：缺少必要工具"
    return 1
  fi
  if docker_allows_privileged; then
    echo "  privileged: 可用（kind 控制面容器可以创建）"
  else
    log_error "  privileged: 不可用 —— kind 无法在此 Docker 上创建集群"
    if [[ -n "${PROBE_ERROR:-}" ]]; then
      echo "  探测输出：${PROBE_ERROR}" | head -3
    fi
    cat <<'EOF'
  原因与处理：
    - kind 的控制面容器必须能以 --privileged 运行（挂载 sysfs/cgroup、创建网络命名空间）；
    - 受限的嵌套容器（无 CAP_SYS_ADMIN、禁止 mount/unshare）里无法运行 kind、minikube-docker、k3d；
    - 请在具备该能力的宿主机执行（Linux + 完整权限的 Docker，或 Docker Desktop）。
      只想做静态校验时用：bash scripts/test-k8s-infra.sh（不创建集群）。
EOF
    failed=1
  fi

  if [[ "${failed}" -ne 0 ]]; then
    return 1
  fi
  log_info "环境可用于创建本地集群（下一步：bash scripts/k8s-cluster.sh create）"
}

# ---------------------------------------------------------------------------
# create
# ---------------------------------------------------------------------------
cmd_create() {
  require_cluster_tools

  if cluster_exists; then
    log_info "集群 ${CLUSTER_NAME} 已存在，跳过创建（幂等）"
    cmd_status
    return 0
  fi

  log_info "创建 kind 集群 ${CLUSTER_NAME}（等待 ${KIND_CREATE_TIMEOUT}）..."
  kind create cluster \
    --name "${CLUSTER_NAME}" \
    --config "${KIND_CONFIG}" \
    --wait "${KIND_CREATE_TIMEOUT}" \
    || fail "kind 创建集群失败（若报 mount/privileged 相关错误，先执行 bash scripts/k8s-cluster.sh doctor）"

  cmd_status
  log_info "集群已就绪：下一步 bash scripts/k8s-infra.sh secrets && bash scripts/k8s-infra.sh create"
}

# ---------------------------------------------------------------------------
# status
# ---------------------------------------------------------------------------
cmd_status() {
  kubectl_ok || fail "未找到 kubectl"
  cluster_exists || fail "集群 ${CLUSTER_NAME} 不存在：先执行 bash scripts/k8s-cluster.sh create"

  echo "集群：${CLUSTER_NAME}（context ${KUBE_CONTEXT}）"
  kubectl --context "${KUBE_CONTEXT}" get nodes -o wide || fail "无法读取节点信息"

  local ready
  ready="$(kubectl --context "${KUBE_CONTEXT}" get nodes \
    -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null \
    | grep -c '^True$' || true)"
  if [[ "${ready:-0}" -lt 1 ]]; then
    log_error "没有 Ready 的节点：集群存在但不可用"
    return 1
  fi
  log_info "Ready 节点数：${ready}"

  echo
  echo "系统组件（kube-system）："
  kubectl --context "${KUBE_CONTEXT}" -n kube-system get pods --no-headers 2>/dev/null \
    | awk '{print "  "$0}' || true

  echo
  echo "StorageClass（PVC 绑定依赖默认 StorageClass）："
  kubectl --context "${KUBE_CONTEXT}" get storageclass --no-headers 2>/dev/null \
    | awk '{print "  "$0}' || echo "  （无）"

  if kubectl --context "${KUBE_CONTEXT}" get namespace surgecart >/dev/null 2>&1; then
    echo
    echo "命名空间 surgecart："
    kubectl --context "${KUBE_CONTEXT}" -n surgecart get deploy,svc,pvc,pod --no-headers 2>/dev/null \
      | awk '{print "  "$0}' || true
  fi
}

# ---------------------------------------------------------------------------
# info
# ---------------------------------------------------------------------------
cmd_info() {
  kubectl_ok || fail "未找到 kubectl"
  cluster_exists || fail "集群 ${CLUSTER_NAME} 不存在：先执行 bash scripts/k8s-cluster.sh create"

  echo "集群名        : ${CLUSTER_NAME}"
  echo "kubectl context: ${KUBE_CONTEXT}"
  echo "kubeconfig    : ${KUBECONFIG:-$HOME/.kube/config}"
  echo
  echo "API Server："
  kubectl --context "${KUBE_CONTEXT}" cluster-info 2>/dev/null | sed 's/^/  /' || true
  echo
  echo "节点："
  kubectl --context "${KUBE_CONTEXT}" get nodes -o wide 2>/dev/null | sed 's/^/  /' || true
  echo
  echo "集群内 Service DNS（Pod 内使用）："
  echo "  mysql : mysql.surgecart.svc.cluster.local:3306"
  echo "  redis : redis.surgecart.svc.cluster.local:6379"
  echo "  kafka : kafka.surgecart.svc.cluster.local:9092"
  echo
  echo "宿主机访问（三个 port-forward，需另开终端；端口与 docker-compose 一致）："
  echo "  kubectl --context ${KUBE_CONTEXT} -n surgecart port-forward svc/mysql 3306:3306"
  echo "  kubectl --context ${KUBE_CONTEXT} -n surgecart port-forward svc/redis 6379:6379"
  echo "  kubectl --context ${KUBE_CONTEXT} -n surgecart port-forward svc/kafka 29092:29092"
  echo
  echo "  说明：kafka 的 EXTERNAL 监听器广播地址是 127.0.0.1:29092，"
  echo "        因此宿主机客户端必须连接 127.0.0.1:29092（与 config.yaml 默认值一致）。"
  echo
  echo "删除集群：bash scripts/k8s-cluster.sh destroy（会丢失全部 PVC 数据）"
}

# ---------------------------------------------------------------------------
# destroy
# ---------------------------------------------------------------------------
cmd_destroy() {
  local assume_yes=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --yes|-y) assume_yes=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) fail "未知参数：$1（可用 --yes）" ;;
    esac
  done

  cat <<EOF

⚠️  即将删除本地 Kubernetes 集群：${CLUSTER_NAME}

数据影响（务必确认）：
  - 集群内所有 PVC/PV 会随集群一起删除：mysql-data、redis-data、kafka-data、surgecart-storage；
  - kind 的 PV 实际由节点容器内的目录承载，删除集群 = 删除节点容器 = 删除这些目录；
  - MySQL 表数据、Redis 快照、Kafka 消息（含业务 Topic）**不可恢复**；
  - 不会删除的内容：本地 Docker 镜像、仓库文件、.env、宿主机上的 port-forward 进程。

如果这些数据还需要，请先自行导出（mysqldump / redis SAVE / kafka-console-consumer 落盘）。

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

  kubectl_ok || fail "未找到 kubectl"
  if ! kind_ok; then
    log_warn "未找到 kind，无法删除集群（若集群已不存在，则已经是目标状态）"
    return 0
  fi

  if ! cluster_exists; then
    log_info "集群 ${CLUSTER_NAME} 不存在，无需删除（幂等）"
    return 0
  fi

  log_info "删除 kind 集群 ${CLUSTER_NAME}..."
  kind delete cluster --name "${CLUSTER_NAME}" || fail "kind 删除集群失败"
  log_info "集群已删除"
}

# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------
main() {
  local command="${1:-}"
  [[ $# -gt 0 ]] && shift
  case "${command}" in
    doctor) cmd_doctor ;;
    create) cmd_create ;;
    status) cmd_status ;;
    info) cmd_info ;;
    destroy) cmd_destroy "$@" ;;
    ""|-h|--help|help) usage ;;
    *) usage; fail "未知命令：${command}" ;;
  esac
}

main "$@"
