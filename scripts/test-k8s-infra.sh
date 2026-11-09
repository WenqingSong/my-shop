#!/usr/bin/env bash
# T2-07 基础设施清单门禁（不需要集群）。
#
# 分层验证，任何一层失败即非零退出：
#   1. 真实构建两个基础设施入口（kubectl kustomize，其次独立 kustomize CLI）；
#   2. 结构化断言：test/manifest/infrastructure_test.go 解析构建产物
#      （依赖结构、Service/端口、restricted PSS、Kafka 监听器边界、迁移职责）；
#   3. 集群可达时追加 server-side dry-run（真实 API Server 校验，不创建资源）；
#   4. 脚本行为：k8s-infra.sh 的凭据校验与「删除会丢数据」的确认语义
#      （fake kubectl，不需要集群）。
#
# 明确边界：
#   - 本脚本**不**创建集群、**不**证明 Pod 能跑起来；接口协议级验证是
#     `bash scripts/k8s-infra.sh verify`（需要真实集群）。
#   - 「YAML 能解析」不等于「Kustomize 构建成功」，本脚本以构建退出码为准。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

INFRA_ENTRYPOINT="manifest/kubernetes/infrastructure/base"
VERIFY_ENTRYPOINT="manifest/kubernetes/infrastructure/verify"
INFRA_SCRIPT="scripts/k8s-infra.sh"

# ---------------------------------------------------------------------------
# 0. 构建工具
# ---------------------------------------------------------------------------
if command -v kubectl >/dev/null 2>&1; then
  KUSTOMIZE=(kubectl kustomize)
  log_info "使用 kubectl kustomize 构建（推荐路径）"
elif command -v kustomize >/dev/null 2>&1; then
  KUSTOMIZE=(kustomize build)
  log_warn "未找到 kubectl，使用独立 kustomize CLI"
else
  fail "未找到 kubectl 或 kustomize：无法校验基础设施 Kustomize 构建入口"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

# ---------------------------------------------------------------------------
# 1. 构建（构建失败即门禁失败，不做文本替代）
# ---------------------------------------------------------------------------
log_info "构建基础设施入口: ${INFRA_ENTRYPOINT}"
"${KUSTOMIZE[@]}" "${INFRA_ENTRYPOINT}" > "${TMP_DIR}/infra.yaml" || fail "基础设施 Kustomize 构建失败"
[[ -s "${TMP_DIR}/infra.yaml" ]] || fail "基础设施构建产物为空"

log_info "构建验证入口: ${VERIFY_ENTRYPOINT}"
"${KUSTOMIZE[@]}" "${VERIFY_ENTRYPOINT}" > "${TMP_DIR}/verify.yaml" || fail "验证 Kustomize 构建失败"
[[ -s "${TMP_DIR}/verify.yaml" ]] || fail "验证构建产物为空"

log_info "基础设施资源: $(grep -E '^kind: ' "${TMP_DIR}/infra.yaml" | awk '{print $2}' | sort | uniq -c | tr '\n' ' ')"
log_info "验证资源:     $(grep -E '^kind: ' "${TMP_DIR}/verify.yaml" | awk '{print $2}' | sort | uniq -c | tr '\n' ' ')"

# ---------------------------------------------------------------------------
# 2. 结构化断言
# ---------------------------------------------------------------------------
log_info "go test：基础设施构建产物结构化断言..."
K8S_MANIFEST_REQUIRED=1 go test -count=1 -v ./test/manifest/ \
  -run 'TestInfrastructure|TestKafkaListener|TestApplicationWorkloads|TestVerificationJobs'

# 构建产物里不得出现明文凭据占位符（占位符被 apply 上集群等于用假凭据启动）。
if grep -q 'REPLACE_WITH' "${TMP_DIR}/infra.yaml" "${TMP_DIR}/verify.yaml"; then
  fail "基础设施构建产物出现 REPLACE_WITH 占位符：清单不应携带任何未解析的凭据占位"
fi

# ---------------------------------------------------------------------------
# 3. 集群可达时的 server-side dry-run（不创建资源）
# ---------------------------------------------------------------------------
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
  log_info "集群可达：执行 server-side dry-run（不创建资源）"
  kubectl apply --dry-run=server -f "${TMP_DIR}/infra.yaml" >/dev/null || fail "基础设施清单 server-side dry-run 失败"
  kubectl apply --dry-run=server -f "${TMP_DIR}/verify.yaml" >/dev/null || fail "验证清单 server-side dry-run 失败"
else
  log_warn "无可用集群：跳过 server-side dry-run（NOT_VERIFIED）"
fi

# ---------------------------------------------------------------------------
# 4. 脚本行为（fake kubectl / docker，不需要集群）
# ---------------------------------------------------------------------------
log_info "bash -n ${INFRA_SCRIPT}"
bash -n "${INFRA_SCRIPT}" || fail "${INFRA_SCRIPT} 存在语法错误"

FAKE_BIN="${TMP_DIR}/bin"
mkdir -p "${FAKE_BIN}"

# 只用于让 require_cluster 通过；不模拟任何资源语义。
cat > "${FAKE_BIN}/kubectl" <<'FAKE'
#!/usr/bin/env bash
case "$*" in
  *cluster-info*) echo "Kubernetes control plane is running at https://127.0.0.1:1"; exit 0 ;;
  *) exit 0 ;;
esac
FAKE
chmod 0755 "${FAKE_BIN}/kubectl"

log_info "断言：缺少凭据时 secrets 必须失败并指出缺失的键名"
set +e
missing_output="$(PATH="${FAKE_BIN}:${PATH}" LIB_SKIP_ENV=1 \
  env -u MYSQL_ROOT_PASSWORD -u DATABASE_DEFAULT_PASS \
  bash "${INFRA_SCRIPT}" secrets 2>&1)"
missing_status=$?
set -e
[[ "${missing_status}" -ne 0 ]] || fail "缺少凭据时 secrets 仍然成功退出：会用空口令创建 Secret"
grep -q 'MYSQL_ROOT_PASSWORD' <<<"${missing_output}" || fail "失败信息未指出缺少 MYSQL_ROOT_PASSWORD"
grep -q 'DATABASE_DEFAULT_PASS' <<<"${missing_output}" || fail "失败信息未指出缺少 DATABASE_DEFAULT_PASS"

log_info "断言：delete 在非交互环境必须拒绝执行并说明数据丢失"
set +e
delete_output="$(PATH="${FAKE_BIN}:${PATH}" LIB_SKIP_ENV=1 bash "${INFRA_SCRIPT}" delete 2>&1 </dev/null)"
delete_status=$?
set -e
[[ "${delete_status}" -ne 0 ]] || fail "delete 未经确认就执行了：会无提示删除 PVC 数据"
grep -q 'PVC' <<<"${delete_output}" || fail "delete 的告警未说明 PVC 数据会丢失"
grep -q 'mysql-data' <<<"${delete_output}" || fail "delete 的告警未点名具体 PVC"

log_info "断言：未知命令必须非零退出（不能静默什么都不做）"
set +e
PATH="${FAKE_BIN}:${PATH}" LIB_SKIP_ENV=1 bash "${INFRA_SCRIPT}" no-such-command >/dev/null 2>&1
unknown_status=$?
set -e
[[ "${unknown_status}" -ne 0 ]] || fail "未知命令返回 0：调用方无法发现拼写错误"

log_info "基础设施清单门禁通过（真实集群运行行为未在本脚本内验证）"
