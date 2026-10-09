#!/usr/bin/env bash
# 统一测试入口：先静态检查（go vet），再执行单元测试（go test）。
#
# 环境隔离（AC-010）：本脚本刻意不 source lib.sh，因为 lib.sh 会加载 .env 并把真实凭据
# （七牛 AK/SK、JWT secret、超管密码等）导出到环境变量，污染 go test 结果。测试应在
# 干净环境变量下运行，配置一律走 manifest/config/config.yaml 的开发默认值，
# 结果与本地 .env 无关（依赖容器仍需由 make init / make up 提前就绪）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

log_info "go vet ./..."
( cd "${ROOT_DIR}" && go vet ./... )

# 各测试包共享同一个 MySQL/Redis 实例（docker-compose），并会清理/写入 admins、roles、
# permissions 等同一批表；跨包并行执行会产生数据竞争导致 flaky。因此用 -p 1 串行执行。
log_info "go test -p 1 ./..."
( cd "${ROOT_DIR}" && go test -p 1 ./... )

log_info "校验 Prometheus 接入配置..."
"${SCRIPT_DIR}/test-prometheus.sh"

log_info "测试全部通过"
