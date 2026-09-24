#!/usr/bin/env bash
# 启动依赖容器并启动应用（幂等，可重复执行）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "确保依赖容器已启动..."
docker_compose up -d mysql redis

log_info "等待依赖就绪..."
wait_for_deps

build_app
start_app

log_info "up complete"
