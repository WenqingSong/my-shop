#!/usr/bin/env bash
# 启动测试依赖（仅 MySQL 与 Redis），并等待二者健康。
#
# 与 make init / make up 不同：本脚本不生成 .env、不构建 app/nginx/prometheus 镜像、
# 不启动完整容器栈，仅用于「测试前先就绪数据库/缓存」这一最小依赖场景（CI 与本地测试共用）。
#
# 前置：调用方需在「环境准备」阶段注入 MYSQL_ROOT_PASSWORD 与 AUTH_JWT_SECRET。
#   - MYSQL_ROOT_PASSWORD：mysql 服务 ${MYSQL_ROOT_PASSWORD:?} 必填插值（无内置默认回退）；
#   - AUTH_JWT_SECRET：仅为满足 compose 全文件 ${AUTH_JWT_SECRET:?} 插值（占位值即可，
#     本脚本只启动 mysql/redis，app 服务不启动，该值不用于任何真实服务）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "启动测试依赖容器（MySQL、Redis）..."
docker_compose up -d mysql redis
wait_for_deps

log_info "deps-start complete（MySQL 与 Redis 已就绪）"
