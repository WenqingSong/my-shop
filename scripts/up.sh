#!/usr/bin/env bash
# 启动依赖容器并启动应用（幂等，可重复执行）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "确保依赖容器已启动..."
docker_compose up -d mysql redis prometheus

log_info "等待依赖就绪..."
wait_for_deps

build_app

# 启动前集中预检：七牛配置/bucket 可用性 + 超级管理员创建条件。
# 两项都执行，以一次性报告所有可预先发现的缺失/非法项（键名/依赖名级别），
# 任一失败非零退出、不启动后端、错误不含凭据。
log_info "七牛云配置与 bucket 可用性预检..."
qiniu_ok=false
if "${APP_BIN}" qiniu check; then
  qiniu_ok=true
fi

# 超级管理员创建条件预检：复用 Go 侧数据库访问与超管判断逻辑（不写 Shell SQL）。
# 首次创建场景缺 ADMIN_SUPER_PASSWORD 时在此集中失败、不启动后端；已存在超管则幂等通过。
log_info "超级管理员创建条件预检..."
admin_ok=false
if "${APP_BIN}" admin check; then
  admin_ok=true
fi

if [[ "${qiniu_ok}" != "true" || "${admin_ok}" != "true" ]]; then
  log_error "启动前预检未通过，已停止启动后端（请按上述提示修复后重试）"
  exit 1
fi

migrate_app
start_app

log_info "up complete"
