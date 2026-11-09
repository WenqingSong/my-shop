#!/usr/bin/env bash
# race 门禁：两段互相独立的检查。
#   1) 进程内共享内存并发逻辑（熔断器状态机）：纯内存，不连接 MySQL/Redis；
#   2) Outbox 并发可靠性（领取/租约/token/人工修复竞态）：需要真实 MySQL/Redis。
#
# 环境隔离（F-14）：
#   - 第二段使用独立、可销毁的专用数据库（默认 my_shop_outbox_race），
#     不复用开发库 my_shop / my_shop_staging，更不连接生产库；测试结束后删除该库。
#   - 专用库通过 compose 里的 mysql 客户端创建，宿主机无需安装 mysql CLI；
#     MySQL root 密码取自 DATABASE_DEFAULT_PASS（默认 root，与 make deps-start 一致）。
#
# 与 CI 的关系：.cnb.yml 的 pr-check / main-ci 都在 `make deps-start` 之后执行
# `make test-race`，所以本脚本依赖 compose 已启动且健康的 MySQL/Redis。
#
# 不允许静默跳过：数据库不可用、专用库创建失败、用例未实际执行或未通过，都以非零退出。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

log_info "go test -race（进程内共享内存并发逻辑：flashsale 熔断器状态机）"
go test -race -count=1 -run 'TestCircuitBreakerStateMachine' ./internal/logic/flashsale/...

# F-03：Producer 等待 context 结束时复制逐条结果，晚到回调不得改写已返回快照。
# B4：模式装配、Consumer 监督循环、关闭预算与 goroutine 回收。
log_info "go test -race（F-03 逐条结果快照 + B4 秒杀装配/生命周期）"
go test -race -count=1 -run 'TestProducerPublishResults' ./internal/mq/kafka/...
go test -race -count=1 -run 'TestResolveFlashSaleRuntimeConfig|TestFlashSaleRuntime|TestScannersStopOnContextCancel' ./internal/worker/...

# ---------------------------------------------------------------------------
# Outbox 并发可靠性 Race 门禁（F-14）
# ---------------------------------------------------------------------------
# 用例与审查要求一一对应，显式列出而不用宽泛的 ^TestOutbox：
# TestOutboxRealKafka* 需要真实 Broker，缺少环境时会 t.Skip，
# 若混入本门禁会让「未验证」伪装成通过。每个用例跑完后还会校验其确实执行并通过，
# 避免测试改名后 -run 静默匹配不到任何用例。
OUTBOX_RACE_TESTS=(
  "TestOutboxConcurrentClaimNoOverlap"          # 并发领取：两个 Publisher 的领取集合不重叠
  "TestOutboxConcurrentRoundsNoDeadlock"        # 多 Worker 并发发布：不死锁、全部发布
  "TestOutboxRestartRecovery"                   # 租约过期恢复：新进程接管崩溃租约
  "TestOutboxMarkSkippedWhenLeaseTaken"         # 旧 Token 失效：迟到标记被丢弃
  "TestOutboxRepairDeadToQueuedRepublishes"     # 人工修复与旧 Publisher 竞争
  "TestOutboxSuccessThenMarkFailureRecoverable" # ACK 成功但标记失败的窗口可恢复
  "TestOutboxTerminalStatusNotReverted"         # 发布标记不回写业务终态
  "TestOutboxPublishUnknownResult"              # 发布结果未知后的重发
  "TestOutboxPartialAckTimeoutDoesNotAmplifyDuplicates" # F-03：部分 ACK + 超时不得整批重发
  "TestOutboxMarkDoesNotWaitBusinessRowLock"    # F-04：业务行锁不得突破标记预算/租约
  "TestOutboxMarkTimeoutDoesNotWritePublishedAt" # F-04：标记总预算耗尽不得写 published_at
)

RACE_DB_NAME="${OUTBOX_RACE_DB_NAME:-my_shop_outbox_race}"
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"
export DATABASE_DEFAULT_NAME="${RACE_DB_NAME}"

# 复用 compose 已启动的 MySQL；未启动时明确失败并给出修复命令，绝不跳过。
mysql_id="$(docker_compose_placeholder ps -q mysql 2>/dev/null | head -n 1)"
if [[ -z "${mysql_id}" ]]; then
  log_error "Outbox Race 门禁需要 compose 管理的 MySQL：请先执行 make deps-start"
  exit 1
fi

# race_db_exec 在 MySQL 容器内执行 SQL（密码经 MYSQL_PWD 传入，不出现在进程参数里）。
race_db_exec() {
  docker exec -i -e MYSQL_PWD="${DATABASE_DEFAULT_PASS}" "${mysql_id}" mysql -uroot "$@"
}

# race_db_created 标记专用库是否已由本脚本创建：未创建成功时不执行 DROP，避免误报清理失败。
race_db_created=0
# cleanup_race_db 删除专用库：测试失败也执行，保证可重复运行、不残留数据。
cleanup_race_db() {
  [[ "${race_db_created}" == "1" ]] || return 0
  race_db_exec -e "DROP DATABASE IF EXISTS \`${RACE_DB_NAME}\`" >/dev/null 2>&1 ||
    log_warn "清理 Outbox Race 专用库 ${RACE_DB_NAME} 失败，可人工删除"
}

race_log="$(mktemp)"
trap 'rm -f "${race_log}"; cleanup_race_db' EXIT

log_info "创建 Outbox Race 专用数据库 ${RACE_DB_NAME}（可销毁，不影响开发库）..."
race_db_exec -e "CREATE DATABASE IF NOT EXISTS \`${RACE_DB_NAME}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
race_db_created=1

pattern="^($(IFS='|'; printf '%s' "${OUTBOX_RACE_TESTS[*]}"))$"
log_info "go test -race（Outbox 并发/租约/token/人工修复竞态，隔离库 ${RACE_DB_NAME}）"
go test -race -p 1 -count=1 -v -timeout 15m -run "${pattern}" ./internal/logic/flashsale/ |
  tee "${race_log}"

for name in "${OUTBOX_RACE_TESTS[@]}"; do
  grep -qE "^--- PASS: ${name} " "${race_log}" || fail "Outbox Race 门禁未实际执行或未通过：${name}"
done

# ---------------------------------------------------------------------------
# B3 Kafka Consumer 并发可靠性 Race 门禁
# ---------------------------------------------------------------------------
# 覆盖两类并发风险（同样复用上面的隔离库，测试内部自行清表，不触碰开发库）：
#   - 同一活动×SKU 的并发消费：条件扣减 + 行锁保证不超卖；
#   - Kafka 重复投递与 MySQL Scanner 同时接管同一 Request（B4 切换期风险）：
#     只允许一次业务效果。用例内部用 -race 覆盖业务事务与状态迁移的共享内存访问。
CONSUMER_RACE_TESTS=(
  "TestConsumerRaceOversellGuard"              # 并发消费者竞争同一 SKU：成功订单数恰好等于库存
  "TestConsumerRaceKafkaAndScannerSameRequest" # Kafka 重复投递 + Scanner 并发接管：只落一单
)

consumer_log="$(mktemp)"
trap 'rm -f "${race_log}" "${consumer_log}"; cleanup_race_db' EXIT

consumer_pattern="^($(IFS='|'; printf '%s' "${CONSUMER_RACE_TESTS[*]}"))$"
log_info "go test -race（B3 Consumer 并发落单/超卖/双入口竞态，隔离库 ${RACE_DB_NAME}）"
go test -race -p 1 -count=1 -v -timeout 15m -run "${consumer_pattern}" ./internal/logic/flashsale/ |
  tee "${consumer_log}"

for name in "${CONSUMER_RACE_TESTS[@]}"; do
  grep -qE "^--- PASS: ${name} " "${consumer_log}" || fail "Consumer Race 门禁未实际执行或未通过：${name}"
done

log_info "race 门禁通过（含 Outbox 并发可靠性用例 ${#OUTBOX_RACE_TESTS[@]} 项、B3 Consumer 并发用例 ${#CONSUMER_RACE_TESTS[@]} 项）"
