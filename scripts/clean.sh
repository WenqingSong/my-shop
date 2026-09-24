#!/usr/bin/env bash
# 清理编译产物与临时文件，不误删源码与持久数据（MySQL/Redis 数据卷）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

# 若应用仍在运行，先停止它，避免删除 pid 文件后留下无法被生命周期感知/停止的孤儿进程。
if is_app_running; then
  log_warn "检测到应用正在运行，先停止应用再清理..."
  stop_app
fi

log_info "清理编译产物与临时文件..."
rm -rf "${BIN_DIR}" "${TMP_DIR}"
( cd "${ROOT_DIR}" && go clean )
log_info "clean complete（源码与持久数据不受影响）"
