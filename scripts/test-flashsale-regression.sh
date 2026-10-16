#!/usr/bin/env bash
# 秒杀正确性回归入口（make test-flashsale-regression）：
# Kafka 改造前后正确性/一致性对比的唯一入口，需 MySQL/Redis 就绪。
#
# 按序执行：
#   1. 白盒正确性/一致性（含并发一人一单/并发幂等/并发领取唯一性/修复审计等）：go test -p 1 ./internal/logic/flashsale/...
#   2. 秒杀 e2e（下单/消费/对账/补偿/限流熔断等）：go test -p 1 -run 'TestFlashSale' ./test/e2e/...
#   3. 秒杀并发 + race（真实 MySQL/Redis 下验证业务并发正确性 + Go 数据竞争检测）：
#      go test -race -run '<秒杀并发测试>' ./internal/logic/flashsale/... ./test/e2e/...
#
# 与 test-race.sh 的区别：test-race 只跑纯内存熔断器状态机（不连 DB）；本脚本跑真实 DB/Redis 的
# 秒杀业务并发正确性（库存不超卖、一人一单、幂等、消费领取唯一性），二者互补、都要跑。
# 本脚本不改变既有 make test / make test-race 语义，按需运行。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

# 与 test-integration.sh 一致：显式注入隔离测试环境的测试凭据（默认 root，与 deps/up 启动 MySQL 的
# MYSQL_ROOT_PASSWORD 一致；如需自定义请先导出 DATABASE_DEFAULT_PASS）。
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"

# 秒杀并发测试匹配：V1 并发不超卖、V2 Redis 预扣不超卖、并发一人一单/幂等/消费领取唯一性。
RACE_RUN='FlashSaleConcurrent|RedisPreDeductNoOversell|ConsumeConcurrent|InsertOrderConcurrent'

log_info "1/3 白盒正确性/一致性（go test -p 1 ./internal/logic/flashsale/...）"
( cd "${ROOT_DIR}" && go test -p 1 -count=1 ./internal/logic/flashsale/... )

log_info "2/3 秒杀 e2e（go test -p 1 -run 'TestFlashSale' ./test/e2e/...）"
( cd "${ROOT_DIR}" && go test -p 1 -count=1 -run 'TestFlashSale' ./test/e2e/... )

log_info "3/3 秒杀并发 + race（go test -race -run '${RACE_RUN}' ...）"
( cd "${ROOT_DIR}" && go test -race -count=1 -run "${RACE_RUN}" ./internal/logic/flashsale/... ./test/e2e/... )

log_info "秒杀正确性回归全部通过"
