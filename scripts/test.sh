#!/usr/bin/env bash
# 统一测试入口：先静态检查（go vet），再执行单元测试（go test），最后跑容器化静态断言。
#
# 环境隔离：本脚本刻意不 source lib.sh，因为 lib.sh 会加载 .env 并把真实凭据
# （七牛 AK/SK、JWT secret、超管密码等）导出到环境变量，污染 go test 结果。
# 测试在干净环境变量下运行，结果与本地 .env 无关。
#
# 凭据分离：config.yaml 已清空 database.default.pass，且应用/迁移代码已无内置默认密码回退。
# 故在此显式注入隔离测试环境的测试凭据 DATABASE_DEFAULT_PASS（默认 root，与 make init/up
# 启动 MySQL 的 MYSQL_ROOT_PASSWORD 保持一致；如需自定义密码请在运行前显式导出
# DATABASE_DEFAULT_PASS），使各集成测试可连接由 make init / make up 提前就绪的 MySQL。
# 该注入不依赖应用/迁移代码的内置默认值。AUTH_JWT_SECRET / ADMIN_SUPER_PASSWORD
# 已由各测试自身 t.Setenv 注入，无需在此导出。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

# 开发测试 MySQL 凭据（与 make init/up 启动 MySQL 的 MYSQL_ROOT_PASSWORD 保持一致，仅供本地/CI；
# 隔离测试环境显式注入，不依赖应用/迁移代码的内置默认值）。
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"

log_info "go vet ./..."
( cd "${ROOT_DIR}" && go vet ./... )

# 各测试包共享同一个 MySQL/Redis 实例（docker-compose），并会清理/写入 admins、roles、
# permissions 等同一批表；跨包并行执行会产生数据竞争导致 flaky。因此用 -p 1 串行执行。
log_info "go test -p 1 ./..."
( cd "${ROOT_DIR}" && go test -p 1 ./... )

log_info "校验 Prometheus 接入配置..."
"${SCRIPT_DIR}/test-prometheus.sh"

log_info "校验容器化配置（Dockerfile / .dockerignore / compose / config 凭据分离）..."
"${SCRIPT_DIR}/test-container.sh"

log_info "校验 Nginx 反向代理配置..."
"${SCRIPT_DIR}/test-nginx.sh"

log_info "测试全部通过"
