#!/usr/bin/env bash
# 统一测试入口（make test）：完整质量门禁。
# 依次执行：静态检查（go vet）→ lib.sh 加载语义校验 → 单元测试（无 DB）→ 集成测试（需 DB）→ 容器化静态断言。
#
# 环境隔离：LIB_SKIP_ENV=1 跳过 lib.sh 的 .env 加载，避免 .env 中真实凭据（七牛 AK/SK、
# JWT secret、超管密码等）被导出到环境变量并污染 go test 结果；测试在干净环境变量下运行，
# 结果与本地 .env 无关。单元/集成测试的凭据注入分别由 test-unit.sh / test-integration.sh 负责。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

log_info "go vet ./..."
( cd "${ROOT_DIR}" && go vet ./... )

log_info "校验 lib.sh 加载语义（空值不覆盖默认值等，AC-003 回归）..."
"${SCRIPT_DIR}/test-lib.sh"

log_info "单元测试（无 MySQL/Redis）..."
"${SCRIPT_DIR}/test-unit.sh"

log_info "集成测试（需 MySQL/Redis）..."
"${SCRIPT_DIR}/test-integration.sh"

# 全局资源 Registry 一致性校验（Migration 版本冲突 + 错误码域漂移），纯静态、无需 DB。
# 检测到冲突/漂移即非零退出，使门禁阶段失败并阻止后续构建/发布。
log_info "校验全局资源 Registry 一致性（migration version / 错误码域）..."
"${SCRIPT_DIR}/check-registry.sh"

log_info "校验 Prometheus 接入配置..."
"${SCRIPT_DIR}/test-prometheus.sh"

log_info "校验容器化配置（Dockerfile / .dockerignore / compose / config 凭据分离）..."
"${SCRIPT_DIR}/test-container.sh"

log_info "校验 Nginx 反向代理配置..."
"${SCRIPT_DIR}/test-nginx.sh"

log_info "测试全部通过"
