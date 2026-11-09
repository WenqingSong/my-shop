#!/usr/bin/env bash
# T2-07 集群管理脚本行为门禁（不需要真实集群、不需要 Docker）。
#
# 与 test-k8s-infra.sh 的分工：
#   - test-k8s-infra.sh：证明「基础设施清单正确」；
#   - 本脚本：证明「集群脚本的语义正确」—— 真正执行 scripts/k8s-cluster.sh，
#     用 fake docker/kind/kubectl 观察调用次数、退出码与输出。
#
# 覆盖范围：
#   - doctor：工具齐全时通过；缺 kind 时失败（不能把「没装」说成「可用」）；
#   - create：首次创建调用一次 kind create；**重复执行不重建**（幂等）；
#   - status：集群不存在时非零退出（不能把「不存在」说成「就绪」）；
#   - info：打印 context 与三个 port-forward 命令；
#   - destroy：非交互且无 --yes 时拒绝执行并说明 PVC 数据丢失；
#             --yes 后删除且重复执行不报错（幂等）。
#
# 明确边界：fake kind 只能证明脚本行为，不能证明真实集群能被创建。
# 真实创建/删除见 scripts/k8s-cluster.sh create/destroy 与 T2-07 报告的环境限制说明。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

CLUSTER_SCRIPT="scripts/k8s-cluster.sh"

log_info "bash -n ${CLUSTER_SCRIPT}"
bash -n "${CLUSTER_SCRIPT}" || fail "${CLUSTER_SCRIPT} 存在语法错误"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
mkdir -p "${FAKE_BIN}"
export FAKE_STATE="${TMP_DIR}/clusters"
export FAKE_LOG="${TMP_DIR}/calls.log"
: > "${FAKE_STATE}"
: > "${FAKE_LOG}"

# fake docker：只回答 doctor 需要的三个问题（版本、守护进程、特权容器探测）。
cat > "${FAKE_BIN}/docker" <<'FAKE'
#!/usr/bin/env bash
echo "docker $*" >> "${FAKE_LOG}"
case "$1" in
  info) echo "Server Version: fake"; exit 0 ;;
  version) echo "fake-29.0"; exit 0 ;;
  run) exit 0 ;;
  *) exit 0 ;;
esac
FAKE

# fake kind：集群状态用文件模拟；create/delete 各自只记录一次真实调用。
cat > "${FAKE_BIN}/kind" <<'FAKE'
#!/usr/bin/env bash
echo "kind $*" >> "${FAKE_LOG}"
case "$1 $2" in
  "get clusters")
    [ -s "${FAKE_STATE}" ] && cat "${FAKE_STATE}"
    exit 0
    ;;
  "create cluster")
    name=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--name" ]; then name="$2"; fi
      shift
    done
    echo "${name}" > "${FAKE_STATE}"
    echo "Creating cluster \"${name}\" ..."
    exit 0
    ;;
  "delete cluster")
    name=""
    while [ $# -gt 0 ]; do
      if [ "$1" = "--name" ]; then name="$2"; fi
      shift
    done
    : > "${FAKE_STATE}"
    echo "Deleting cluster \"${name}\" ..."
    exit 0
    ;;
  "version")
    echo "kind v0.30.0 fake"
    exit 0
    ;;
esac
exit 0
FAKE

# fake kubectl：让 status/info 有稳定的可读输出；不模拟任何资源语义。
cat > "${FAKE_BIN}/kubectl" <<'FAKE'
#!/usr/bin/env bash
echo "kubectl $*" >> "${FAKE_LOG}"
case "$*" in
  *"get nodes -o jsonpath"*) printf 'True\n'; exit 0 ;;
  *"get nodes"*) echo "NAME                                  STATUS   ROLES"; echo "surgecart-local-control-plane         Ready    control-plane"; exit 0 ;;
  *"get storageclass"*) echo "standard (default)   rancher.io/local-path   Delete   WaitForFirstConsumer"; exit 0 ;;
  *cluster-info*) echo "Kubernetes control plane is running at https://127.0.0.1:42001"; exit 0 ;;
  *"get namespace surgecart"*) exit 1 ;;
  *) exit 0 ;;
esac
FAKE
chmod 0755 "${FAKE_BIN}"/docker "${FAKE_BIN}"/kind "${FAKE_BIN}"/kubectl

run_cluster_script() {
  PATH="${FAKE_BIN}:${PATH}" bash "${CLUSTER_SCRIPT}" "$@"
}

count_calls() { grep -c "$1" "${FAKE_LOG}" 2>/dev/null || true; }

# ---------------------------------------------------------------------------
# 1. doctor
# ---------------------------------------------------------------------------
log_info "doctor：工具齐全时应通过"
PATH="${FAKE_BIN}:${PATH}" bash "${CLUSTER_SCRIPT}" doctor >/dev/null || fail "doctor 在工具齐全时失败"
grep -q 'privileged' "${TMP_DIR}/calls.log" || true

# 特权探测必须向容器传原样的 /bin/true：Git Bash（MSYS2）会把以 / 开头的参数改写成
# Windows 路径，docker 会报 "exec: D:/.../usr/bin/true: no such file or directory"，
# 把「Docker 允许特权容器」误判成「特权不可用」。
log_info "doctor：特权探测不得被 MSYS 路径转换改写（Windows/Git Bash）"
grep -q 'MSYS2_ARG_CONV_EXCL' "${CLUSTER_SCRIPT}" \
  || fail "特权探测未隔离 MSYS 参数转换：Git Bash 下会误判「特权不可用」"

log_info "doctor：Docker 不允许特权容器时必须失败（kind 的硬前提）"
mkdir -p "${TMP_DIR}/bin-nopriv"
cat > "${TMP_DIR}/bin-nopriv/docker" <<'FAKE'
#!/usr/bin/env bash
case "$1" in
  info) echo "Server Version: fake"; exit 0 ;;
  version) echo "fake-29.0"; exit 0 ;;
  run) echo "docker: Error response from daemon: operation not permitted" >&2; exit 1 ;;
  *) exit 0 ;;
esac
FAKE
chmod 0755 "${TMP_DIR}/bin-nopriv/docker"
set +e
PATH="${TMP_DIR}/bin-nopriv:${FAKE_BIN}:${PATH}" bash "${CLUSTER_SCRIPT}" doctor >/dev/null 2>&1
doctor_nopriv=$?
set -e
[[ "${doctor_nopriv}" -ne 0 ]] || fail "Docker 不允许特权容器时 doctor 仍然通过：会把「kind 无法运行」说成「环境就绪」"

log_info "doctor：缺少 kind 时必须失败"
mv "${FAKE_BIN}/kind" "${FAKE_BIN}/kind.disabled"
set +e
PATH="${FAKE_BIN}:${PATH}" bash "${CLUSTER_SCRIPT}" doctor >/dev/null 2>&1
doctor_missing=$?
set -e
mv "${FAKE_BIN}/kind.disabled" "${FAKE_BIN}/kind"
[[ "${doctor_missing}" -ne 0 ]] || fail "缺少 kind 时 doctor 仍然通过：会把「无法创建集群」说成「环境就绪」"

# ---------------------------------------------------------------------------
# 2. status（无集群）
# ---------------------------------------------------------------------------
log_info "status：集群不存在时必须非零退出"
set +e
run_cluster_script status >/dev/null 2>&1
status_missing=$?
set -e
[[ "${status_missing}" -ne 0 ]] || fail "集群不存在时 status 返回 0：无法区分「就绪」与「没创建」"

# ---------------------------------------------------------------------------
# 3. create（幂等）
# ---------------------------------------------------------------------------
log_info "create：首次创建应调用一次 kind create cluster"
run_cluster_script create >/dev/null || fail "create 失败"
[[ "$(count_calls 'kind create cluster')" -eq 1 ]] || fail "create 未调用 kind create cluster 一次（实际 $(count_calls 'kind create cluster') 次）"
[[ -s "${FAKE_STATE}" ]] || fail "create 后 fake 集群状态为空"

log_info "create：重复执行不得重建已有集群"
run_cluster_script create >/dev/null || fail "create 重复执行失败（幂等性破坏）"
[[ "$(count_calls 'kind create cluster')" -eq 1 ]] || fail "重复 create 又创建了一次集群（实际 $(count_calls 'kind create cluster') 次）"

# ---------------------------------------------------------------------------
# 4. info
# ---------------------------------------------------------------------------
log_info "info：必须打印 context 与三个 port-forward 命令"
info_output="$(run_cluster_script info)"
grep -q 'kind-surgecart-local' <<<"${info_output}" || fail "info 未打印 kubectl context"
grep -q 'port-forward svc/mysql 3306:3306' <<<"${info_output}" || fail "info 未打印 mysql port-forward"
grep -q 'port-forward svc/redis 6379:6379' <<<"${info_output}" || fail "info 未打印 redis port-forward"
grep -q 'port-forward svc/kafka 29092:29092' <<<"${info_output}" || fail "info 未打印 kafka port-forward"
grep -q 'mysql.surgecart.svc.cluster.local' <<<"${info_output}" || fail "info 未打印集群内 Service DNS"

# ---------------------------------------------------------------------------
# 5. destroy（数据损失告警 + 确认）
# ---------------------------------------------------------------------------
log_info "destroy：非交互且无 --yes 时必须拒绝并说明 PVC 数据丢失"
set +e
destroy_output="$(run_cluster_script destroy 2>&1 </dev/null)"
destroy_status=$?
set -e
[[ "${destroy_status}" -ne 0 ]] || fail "destroy 未经确认就执行：可能无提示删除数据"
grep -q 'PVC' <<<"${destroy_output}" || fail "destroy 告警未说明 PVC 数据会丢失"
grep -q '不可恢复' <<<"${destroy_output}" || fail "destroy 告警未说明数据不可恢复"
[[ -s "${FAKE_STATE}" ]] || fail "被拒绝的 destroy 仍然删除了集群"
[[ "$(count_calls 'kind delete cluster')" -eq 0 ]] || fail "被拒绝的 destroy 仍然调用了 kind delete cluster"

log_info "destroy --yes：应删除集群"
run_cluster_script destroy --yes >/dev/null || fail "destroy --yes 失败"
[[ ! -s "${FAKE_STATE}" ]] || fail "destroy --yes 后集群仍存在"
[[ "$(count_calls 'kind delete cluster')" -eq 1 ]] || fail "destroy --yes 未调用 kind delete cluster 一次"

log_info "destroy --yes：集群已不存在时应成功返回（幂等）"
run_cluster_script destroy --yes >/dev/null || fail "集群不存在时 destroy 失败（破坏幂等性）"
[[ "$(count_calls 'kind delete cluster')" -eq 1 ]] || fail "重复 destroy 又调用了一次 kind delete cluster"

log_info "集群管理脚本行为门禁通过（未验证真实 kind 集群）"
