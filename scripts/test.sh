#!/usr/bin/env bash
# 统一测试入口（make test）：完整质量门禁。
# 依次执行：静态检查（go vet）→ lib.sh 加载语义校验 → 单元测试（无 DB）→ 集成测试（需 DB）
# → 迁移门禁（T2-05：真实 MySQL + 多进程）→ Registry/容器化静态断言 → Kubernetes 迁移清单校验（T2-05）。
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

# T2-05：migration 的进程级语义（锁等待/超时、并发互斥、中途终止、严格校验顺序）。
# 需要真实 MySQL 与真实独立进程；不使用 Mock，也不把「跳过」当通过。
log_info "数据库迁移门禁（T2-05，真实 MySQL + 多进程）..."
"${SCRIPT_DIR}/test-migration.sh"

# 全局资源 Registry 一致性校验（Migration 版本冲突 + 错误码域漂移），纯静态、无需 DB。
# 检测到冲突/漂移即非零退出，使门禁阶段失败并阻止后续构建/发布。
log_info "校验全局资源 Registry 一致性（migration version / 错误码域）..."
"${SCRIPT_DIR}/check-registry.sh"

log_info "校验 Prometheus 接入配置..."
"${SCRIPT_DIR}/test-prometheus.sh"

log_info "校验容器化配置（Dockerfile / .dockerignore / compose / config 凭据分离）..."
"${SCRIPT_DIR}/test-container.sh"

log_info "校验 Kustomize 部署体系（T2-06：真实构建 + 构建产物断言 + API Schema）..."
"${SCRIPT_DIR}/test-k8s-manifest.sh"

# T2-06.1：发布脚本行为语义（发布锁互斥/版本降级保护/发布成功判定/Migration 失败与超时分离）。
# 真正执行 deploy-k8s.sh + fake kubectl；不依赖集群，也不把「静态文本包含某段话」当行为验证。
log_info "校验发布脚本行为（T2-06.1：发布锁 + 降级保护 + 成功判定 + 故障恢复）..."
"${SCRIPT_DIR}/test-deploy-k8s.sh"

# T2-07：本地 Kubernetes 基础设施（MySQL/Redis/Kafka）清单契约与集群脚本行为。
# 都不需要集群：「能构建、契约正确、脚本幂等」与「真实 Pod 能跑起来」是两种事实，
# 后者由 scripts/k8s-infra.sh verify 在真实集群内验证。
log_info "校验本地基础设施清单（T2-07：依赖构建产物 + restricted PSS + Kafka 监听器边界）..."
"${SCRIPT_DIR}/test-k8s-infra.sh"

log_info "校验集群管理脚本行为（T2-07：创建幂等 + 状态 + 销毁确认，fake kind/kubectl）..."
"${SCRIPT_DIR}/test-k8s-cluster.sh"

log_info "校验 Nginx 反向代理配置..."
"${SCRIPT_DIR}/test-nginx.sh"

log_info "测试全部通过"
