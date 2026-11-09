#!/usr/bin/env bash
# T2-05 数据库迁移门禁：真实 MySQL + 真实独立进程（不 Mock）。
#
# 覆盖：
#   - migrate up 不依赖 Redis / Kafka / JWT / 七牛（无关服务全部不可用仍成功）；
#   - 空库首次迁移、重复执行幂等；
#   - 两个独立进程并发迁移：数据库级 GET_LOCK 互斥，最终 Schema/版本唯一正确；
#   - 锁被占用时等待（1.5s 内不结束、不建表），释放后完成；
#   - 锁等待超时（driver 内置 10s）→ 明确失败、零 DDL，释放后重试成功；
#   - 持锁会话断开（进程被杀）→ 锁自动释放，不永久阻塞；
#   - 进程在 DDL 执行中被 SIGKILL → 状态可解释（dirty/明确报错），清理后可恢复；
#   - 严格模式 + 回环地址 → 在连接数据库之前失败，错误不含密码；
#   - 错误密码 / 数据库不可达 → 非零退出且不泄漏密码。
#
# 环境假设：MySQL 已由 make init / make up 就绪（与 test-integration.sh 一致）。
# 注意：本脚本会清空并重建目标库中的全部业务表（与 internal/migrations 的既有测试一致）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

# 测试凭据与 test-integration.sh 同源：显式注入，不依赖应用/迁移代码的内置默认值。
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-${MYSQL_ROOT_PASSWORD:-root}}"

log_info "go test：migration 真实 MySQL + 多进程语义（约 60s）..."
go test -count=1 -v -timeout 600s \
  -run 'TestUpFailsWhenLockHeldByAnotherSession|TestUpSucceedsAfterLockHolderDisconnects|TestUpReleasesDatabaseConnections|TestUpCreatesSchemaAndIsIdempotent|TestUpFailsFastAndMarksDirty|TestConcurrentUp|TestMigrateProcess' \
  ./internal/migrations/

log_info "migration 门禁全部通过"
