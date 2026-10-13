#!/usr/bin/env bash
# 集成测试入口（make test-integration）：运行依赖 MySQL/Redis 的真实测试（含启动完整 app 的
# controller/cmd/boot 集成测试与各 logic 包的白盒集成测试）。
#
# 环境假设：MySQL / Redis 已由 make init / make up 提前就绪；本脚本不启动依赖容器，
# 与历史 test.sh 行为一致（测试连接由 make init/up 就绪的实例）。
#
# 凭据分离：config.yaml 已清空 database.default.pass，且应用/迁移代码已无内置默认密码回退，
# 故在此显式注入隔离测试环境的测试凭据 DATABASE_DEFAULT_PASS（默认 root，与 make init/up
# 启动 MySQL 的 MYSQL_ROOT_PASSWORD 保持一致；如需自定义密码请在运行前显式导出
# DATABASE_DEFAULT_PASS），不依赖应用/迁移代码的内置默认值。AUTH_JWT_SECRET / ADMIN_SUPER_PASSWORD
# 已由各测试自身 t.Setenv 注入，无需在此导出。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"

# 各测试包共享同一个 MySQL/Redis 实例（docker-compose），并会清理/写入 admins、roles、
# permissions 等同一批表；跨包并行执行会产生数据竞争导致 flaky。因此用 -p 1 串行执行。
log_info "go test -p 1 ./...（需 MySQL/Redis 就绪）"
( cd "${ROOT_DIR}" && go test -p 1 ./... )

log_info "集成测试全部通过"
