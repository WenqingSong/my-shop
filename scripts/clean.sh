#!/usr/bin/env bash
# 清理编译产物与临时文件，不误删源码与持久数据（MySQL/Redis/本地存储数据卷）。
# 容器与镜像经 make down（停止并移除容器，保留卷）管理，不在此处删除。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "清理编译产物与临时文件..."
rm -rf "${ROOT_DIR}/bin" "${ROOT_DIR}/tmp"
( cd "${ROOT_DIR}" && go clean )
log_info "clean complete（源码与数据卷不受影响；容器/镜像经 make down 管理）"
