#!/usr/bin/env bash
# 统一测试入口：先静态检查（go vet），再执行单元测试（go test）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "go vet ./..."
( cd "${ROOT_DIR}" && go vet ./... )

log_info "go test ./..."
( cd "${ROOT_DIR}" && go test ./... )

log_info "测试全部通过"
