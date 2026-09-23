#!/usr/bin/env bash
# 先停止再启动，最终使服务恢复可用状态。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "restart：先停止..."
"${SCRIPT_DIR}/down.sh"
log_info "restart：再启动..."
"${SCRIPT_DIR}/up.sh"
log_info "restart complete"
