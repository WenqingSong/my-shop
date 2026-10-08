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

# 七牛云预检：复用 serve 启动同款 ValidateConfig（结构 + 存在性 + GetBucketInfo）。
# 失败非零退出、不启动后端、错误不含凭据；serve 自身仍会再次 fail-fast（双保险，最多两次 GetBucketInfo）。
log_info "七牛云配置与 bucket 可用性预检..."
"${APP_BIN}" qiniu check

migrate_app
start_app

log_info "up complete"
