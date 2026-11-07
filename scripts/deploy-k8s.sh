#!/usr/bin/env bash
# T2-06 Kubernetes 发布脚本：**先迁移，后应用**。
#
#   bash scripts/deploy-k8s.sh [--context <name>] [--migration-timeout <sec>] [--rollout-timeout <sec>]
#
# 执行顺序（任一步失败立即非零退出，后续步骤不执行）：
#   1. 环境预检：kubectl / 集群可达 / Secret 已创建 / 构建产物无占位符 / 镜像版本不可变
#   2. 构建 Migration 与 Application 两个独立清单（kubectl kustomize）
#   3. 应用 Migration 清单 → 等待 Job 完成（失败即打印日志并终止，不更新应用）
#   4. 应用 Application 清单 → 等待 Deployment 可用 → 等待 Pod Ready
#
# 明确不做：
#   - 不执行 `migrate force`（人工风险操作，脚本永不调用）；
#   - 不修改清单里的镜像版本（版本是 Git 里的事实，脚本只校验和部署）；
#   - 不删除失败的历史 Job（避免并发流水线互相踩踏；失败处理见 docs/development/kubernetes.md §10）。
#
# 退出码：0 成功；1 参数/预检/构建/部署失败；2 迁移失败（应用未更新）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

APP_ENTRYPOINT="manifest/kubernetes/overlays/production"
MIGRATION_ENTRYPOINT="manifest/kubernetes/migration/overlays/production"
IMAGE_REPOSITORY="docker.cnb.cool/framework-station/my-shop"
# 生产 overlay 中的占位版本；必须与 manifest 中的值一致（由 test/manifest 断言）。
PLACEHOLDER_TAG="0000000"

CONTEXT=""
MIGRATION_TIMEOUT=900
ROLLOUT_TIMEOUT=600

log_info()  { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }
log_warn()  { printf '\033[33m[WARN]\033[0m %s\n' "$*"; }
log_error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; }
die()       { log_error "$*"; exit 1; }

usage() {
  cat <<'EOF'
用法：bash scripts/deploy-k8s.sh [选项]

先执行数据库迁移（独立 Job），确认成功后才会发布应用 Deployment。
任一步失败立即非零退出；迁移失败时应用保持不变（退出码 2）。

选项：
  --context <name>              kubeconfig context（默认使用当前 context）
  --migration-timeout <sec>     等待迁移 Job 完成的上限（默认 900）
  --rollout-timeout <sec>       等待 Deployment 可用的上限（默认 600）
  -h, --help                    显示帮助

前置条件：
  - 已安装 kubectl，且 kubeconfig 指向目标集群；
  - 已按 manifest/kubernetes/examples/secrets/ 创建 Secret；
  - manifest/kubernetes/overlays/production 与 migration/overlays/production
    中的镜像版本已替换为真实短 SHA，REPLACE_WITH_* 占位符已全部替换。

完整流程与故障处理见 docs/development/kubernetes.md。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context)           CONTEXT="${2:?--context 需要参数}"; shift 2 ;;
    --migration-timeout) MIGRATION_TIMEOUT="${2:?--migration-timeout 需要参数}"; shift 2 ;;
    --rollout-timeout)   ROLLOUT_TIMEOUT="${2:?--rollout-timeout 需要参数}"; shift 2 ;;
    -h|--help)           usage; exit 0 ;;
    *)                   die "未知参数: $1（使用 --help 查看用法）" ;;
  esac
done

for value in "${MIGRATION_TIMEOUT}" "${ROLLOUT_TIMEOUT}"; do
  [[ "${value}" =~ ^[0-9]+$ ]] || die "超时必须是非负整数，实际: ${value}"
done

KUBECTL=(kubectl)
[[ -n "${CONTEXT}" ]] && KUBECTL+=(--context "${CONTEXT}")

cd "${ROOT_DIR}"

# ---------------------------------------------------------------------------
# 1. 环境预检
# ---------------------------------------------------------------------------
command -v kubectl >/dev/null 2>&1 || die "未安装 kubectl：无法构建 Kustomize 清单或部署集群"
"${KUBECTL[@]}" version >/dev/null 2>&1 \
  || die "kubectl 无法连接集群（context=${CONTEXT:-当前}）：请检查 kubeconfig 与网络"

log_info "集群连接正常（context=${CONTEXT:-当前}）"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

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

# 命名空间以构建产物为准，避免脚本与清单各说一套。
NAMESPACE="$(awk '/^kind: Namespace$/{found=1} found && /^  name:/{print $2; exit}' "${APPLICATION_MANIFEST}")"
[[ -n "${NAMESPACE}" ]] || die "Application 清单缺少 Namespace 资源"
log_info "目标命名空间: ${NAMESPACE}；镜像版本: ${IMAGE_TAG}"

# 首次部署时命名空间尚不存在，无法在其中创建 Secret。
# 这里用与清单同源的 namespace.yaml 建命名空间（不复制标签、不引入第二份定义）。
if ! "${KUBECTL[@]}" get namespace "${NAMESPACE}" >/dev/null 2>&1; then
  log_info "命名空间 ${NAMESPACE} 不存在，按 manifest/kubernetes/shared/namespace.yaml 创建"
  "${KUBECTL[@]}" apply -f "${ROOT_DIR}/manifest/kubernetes/shared/namespace.yaml" \
    || die "创建命名空间 ${NAMESPACE} 失败"
fi

# Secret 只能由部署环境创建（仓库里只有模板）：缺失时在迁移之前就失败，
# 避免 Job 因认证失败反复重试并消耗 backoffLimit。
mapfile -t SECRET_NAMES < <(awk '/secretRef:/{flag=1; next} flag && /name:/{print $2; flag=0}' \
  "${APPLICATION_MANIFEST}" | sort -u)
[[ "${#SECRET_NAMES[@]}" -gt 0 ]] || die "Application 清单没有任何 Secret 引用（配置边界异常）"
for secret in "${SECRET_NAMES[@]}"; do
  "${KUBECTL[@]}" -n "${NAMESPACE}" get secret "${secret}" >/dev/null 2>&1 \
    || die "缺少 Secret ${NAMESPACE}/${secret}：请先按 manifest/kubernetes/examples/secrets/ 创建"
done
log_info "Secret 就绪: ${SECRET_NAMES[*]}"

# ---------------------------------------------------------------------------
# 2. 迁移（必须先于应用）
# ---------------------------------------------------------------------------
JOB_NAME="$(awk '/^kind: Job$/{found=1} found && /^  name:/{print $2; exit}' "${MIGRATION_MANIFEST}")"
[[ -n "${JOB_NAME}" ]] || die "Migration 清单缺少 Job 资源"
log_info "应用 Migration 清单（Job/${JOB_NAME}）"
"${KUBECTL[@]}" apply -f "${MIGRATION_MANIFEST}" \
  || die "Migration 清单应用失败（应用未更新）"

log_info "等待 Job/${JOB_NAME} 完成（上限 ${MIGRATION_TIMEOUT}s）"
deadline=$(( SECONDS + MIGRATION_TIMEOUT ))
migration_status="timeout"
while (( SECONDS < deadline )); do
  complete="$("${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null || true)"
  failed="$("${KUBECTL[@]}" -n "${NAMESPACE}" get job "${JOB_NAME}" \
    -o jsonpath='{.status.conditions[?(@.type=="Failed")].status}' 2>/dev/null || true)"
  if [[ "${complete}" == "True" ]]; then
    migration_status="complete"
    break
  fi
  if [[ "${failed}" == "True" ]]; then
    migration_status="failed"
    break
  fi
  sleep 5
done

if [[ "${migration_status}" != "complete" ]]; then
  log_error "迁移未成功（状态: ${migration_status}）。应用 Deployment 未更新。"
  log_error "Job 日志（最后 200 行）："
  "${KUBECTL[@]}" -n "${NAMESPACE}" logs "job/${JOB_NAME}" --tail=200 --all-containers=true >&2 || true
  log_error "处理方式：修复后 ${KUBECTL[*]} -n ${NAMESPACE} delete job ${JOB_NAME}，再重新执行本脚本。"
  log_error "禁止用 migrate force 绕过失败（force 只修改版本记录，不修复 schema）。"
  exit 2
fi

log_info "迁移成功: Job/${JOB_NAME} 已完成"

# ---------------------------------------------------------------------------
# 3. 应用
# ---------------------------------------------------------------------------
log_info "应用 Application 清单"
"${KUBECTL[@]}" apply -f "${APPLICATION_MANIFEST}" || die "Application 清单应用失败"

log_info "等待 Deployment 可用（上限 ${ROLLOUT_TIMEOUT}s）"
"${KUBECTL[@]}" -n "${NAMESPACE}" rollout status deployment/surgecart \
  --timeout="$(( ROLLOUT_TIMEOUT ))s" || die "Deployment 未在 ${ROLLOUT_TIMEOUT}s 内可用（Pod 未就绪，检查 /ready 与事件）"

log_info "等待 Pod Ready（Readiness Probe 通过）"
"${KUBECTL[@]}" -n "${NAMESPACE}" wait \
  --for=condition=Ready pod \
  -l app.kubernetes.io/name=surgecart,app.kubernetes.io/component=backend \
  --timeout="$(( ROLLOUT_TIMEOUT ))s" || die "Pod 未进入 Ready 状态"

log_info "发布完成：镜像 ${IMAGE_TAG}，迁移 Job/${JOB_NAME} 完成，Deployment/surgecart 可用"
"${KUBECTL[@]}" -n "${NAMESPACE}" get deployment,service,pod -l app.kubernetes.io/name=surgecart || true
