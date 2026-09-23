#!/usr/bin/env bash
# 清理编译产物与临时文件，不误删源码与持久数据（MySQL/Redis 数据卷）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "清理编译产物与临时文件..."
rm -rf "${BIN_DIR}" "${TMP_DIR}"
( cd "${ROOT_DIR}" && go clean )
log_info "clean complete（源码与持久数据不受影响）"
