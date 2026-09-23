#!/usr/bin/env bash
# 停止应用进程并停止/移除依赖容器（不删除 MySQL/Redis 数据卷）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

stop_app

log_info "停止并移除依赖容器（保留数据卷）..."
docker_compose down

log_info "down complete（MySQL/Redis 数据卷已保留）"
