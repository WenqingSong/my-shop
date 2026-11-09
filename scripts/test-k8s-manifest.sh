#!/usr/bin/env bash
# T2-06 Kustomize 部署体系校验（不需要集群）。
#
# 分层验证，任何一层失败即非零退出：
#   1. 真实构建两个部署入口（kubectl kustomize，其次独立 kustomize CLI）；
#   2. 结构化断言：test/manifest 直接解析构建产物（不是源 YAML 文本匹配）；
#   3. API Schema 校验：kubeconform（若安装）逐条验证字段合法性；
#   4. 集群可用时追加 server-side dry-run（真实 API Server 校验）。
#
# 明确边界：
#   - 「YAML 能被解析」不等于「Kustomize 构建成功」，本脚本以构建退出码为准；
#   - 本脚本不创建任何集群资源；真实集群部署验证见 docs/development/kubernetes.md §12。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

APP_ENTRYPOINT="manifest/kubernetes/overlays/production"
MIGRATION_ENTRYPOINT="manifest/kubernetes/migration/overlays/production"

# ---------------------------------------------------------------------------
# 0. 构建工具：没有 kustomize 就失败，不用「源文件看起来对」代替构建
# ---------------------------------------------------------------------------
if command -v kubectl >/dev/null 2>&1; then
  KUSTOMIZE=(kubectl kustomize)
  log_info "使用 kubectl kustomize 构建（推荐路径）"
elif command -v kustomize >/dev/null 2>&1; then
  KUSTOMIZE=(kustomize build)
  log_warn "未找到 kubectl，使用独立 kustomize CLI"
else
  fail "未找到 kubectl 或 kustomize：无法校验 Kustomize 构建入口。请安装 kubectl（https://kubernetes.io/docs/tasks/tools/）后重试"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

APP_MANIFEST="${TMP_DIR}/application.yaml"
MIGRATION_MANIFEST="${TMP_DIR}/migration.yaml"

log_info "构建 Application 入口: ${APP_ENTRYPOINT}"
"${KUSTOMIZE[@]}" "${APP_ENTRYPOINT}" > "${APP_MANIFEST}" || fail "Application Kustomize 构建失败"
[[ -s "${APP_MANIFEST}" ]] || fail "Application 构建产物为空"

log_info "构建 Migration 入口: ${MIGRATION_ENTRYPOINT}"
"${KUSTOMIZE[@]}" "${MIGRATION_ENTRYPOINT}" > "${MIGRATION_MANIFEST}" || fail "Migration Kustomize 构建失败"
[[ -s "${MIGRATION_MANIFEST}" ]] || fail "Migration 构建产物为空"

log_info "Application 资源: $(grep -E '^kind: ' "${APP_MANIFEST}" | awk '{print $2}' | sort | uniq -c | tr '\n' ' ')"
log_info "Migration 资源:   $(grep -E '^kind: ' "${MIGRATION_MANIFEST}" | awk '{print $2}' | sort | uniq -c | tr '\n' ' ')"

# ---------------------------------------------------------------------------
# 1. 结构化断言（解析构建产物）
# ---------------------------------------------------------------------------
log_info "go test：Kustomize 构建产物结构化断言..."
# K8S_MANIFEST_REQUIRED=1：缺少构建工具时必须失败，不能退化为跳过。
K8S_MANIFEST_REQUIRED=1 go test -count=1 -v ./test/manifest/

# ---------------------------------------------------------------------------
# 2. API Schema 校验（离线）
# ---------------------------------------------------------------------------
if command -v kubeconform >/dev/null 2>&1; then
  log_info "kubeconform：Kubernetes API Schema 校验..."
  kubeconform -strict -summary -kubernetes-version "${KUBECONFORM_K8S_VERSION:-1.31.0}" \
    "${APP_MANIFEST}" "${MIGRATION_MANIFEST}" || fail "kubeconform 校验失败"
else
  log_warn "未安装 kubeconform，跳过 API Schema 校验（NOT_VERIFIED；安装后本项即生效）"
fi

# ---------------------------------------------------------------------------
# 3. 集群可用时：server-side dry-run（真实 API Server 校验，不创建资源）
# ---------------------------------------------------------------------------
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
  log_info "集群可达：执行 server-side dry-run（不创建资源）"
  kubectl apply --dry-run=server -f "${MIGRATION_MANIFEST}" >/dev/null || fail "迁移清单 server-side dry-run 失败"
  kubectl apply --dry-run=server -f "${APP_MANIFEST}" >/dev/null || fail "应用清单 server-side dry-run 失败"
else
  log_warn "无可用集群：跳过 server-side dry-run 与真实部署验证（NOT_VERIFIED）"
fi

log_info "Kustomize 部署体系校验通过（真实集群行为未在本脚本内验证）"
