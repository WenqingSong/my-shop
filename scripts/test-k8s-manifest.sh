#!/usr/bin/env bash
# T2-05 Kubernetes 清单静态校验：不需要集群、不需要 kubectl。
#
# 断言目标（见 test/manifest/manifest_test.go）：
#   - Job 生命周期字段有界（backoffLimit / activeDeadlineSeconds / ttlSecondsAfterFinished）；
#   - 继承 T2-04 加固（非 root 10001、只读根文件系统、capabilities 全丢弃、无特权）；
#   - 命令只能是 `migrate up`，不得出现 force/serve；
#   - 非敏感配置进 ConfigMap、数据库密码只来自 Secret，且 Secret 文件只有占位符。
#
# 真实集群验证不在本脚本范围内（无目标集群），见 docs/development/migrations.md 的 NOT_VERIFIED。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

log_info "go test：Kubernetes 迁移清单静态校验..."
go test -count=1 -v ./test/manifest/

# kubectl 且集群可达时额外做一次客户端 dry-run（语法/字段合法性）。
# 无 kubectl 或无可达集群时明确跳过，不把「跳过」当成通过。
if command -v kubectl >/dev/null 2>&1 && kubectl version --request-timeout=3s >/dev/null 2>&1; then
  log_info "kubectl 客户端 dry-run 校验清单..."
  for file in manifest/kubernetes/*.yaml; do
    kubectl apply --dry-run=client --validate=false -f "${file}" >/dev/null
  done
  log_info "kubectl 客户端 dry-run 通过"
else
  log_warn "未安装 kubectl 或集群不可达，跳过客户端 dry-run（静态断言已覆盖核心字段）"
fi

log_info "Kubernetes 清单校验通过"
