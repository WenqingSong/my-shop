#!/usr/bin/env bash
# race 门禁：进程内共享内存并发逻辑 + 真实数据库并发事务 + 秒杀回归。
#
# 阶段（任一阶段失败，整个脚本非零退出）：
#   1) 纯内存共享状态并发：秒杀熔断器状态机、Kafka Producer 逐条结果快照、
#      B4 秒杀 Runtime 装配、Consumer Group 状态机与指标、HTTP drain/强制关闭；
#   2) Outbox 并发可靠性 + B3 Consumer 并发（真实 MySQL）；
#   3) B1-05-03 五类并发与事务一致性（库存/钱包/拼团/砍价/优惠券，真实 MySQL）；
#   4) B1-05-03 秒杀回归（Redis Lua 准入/售罄/重复购买/fail-closed/Kafka 消费幂等）；
#   5) 秒杀既有回归全集（flashsale 包，排除需要真实 Kafka Broker 的用例）；
#   6) 秒杀 HTTP 端到端回归（test/e2e 的 flashsale*_test.go，真实路由 + 进程内 server）。
#
# 环境隔离（硬性要求）：
#   - 每个数据库阶段使用脚本自建、脚本自毁的专用库（my_shop_outbox_race /
#     my_shop_b10503_test / my_shop_b10503_flash_test），绝不复用开发库 my_shop、
#     也不触碰历史迁移验证库 my_shop_b10304_verify；assert_owned_db 对库名做白名单式校验，
#     只有 my_shop_*_test / my_shop_*_race 才允许 DROP/CREATE。
#   - 每个阶段开始前 DROP + CREATE，保证不残留上一轮数据（例如 admins 表残留会让既有
#     秒杀用例 1062 失败），因此本脚本可重复运行。
#   - Redis 统一使用专用 DB（默认 15，REDIS_DEFAULT_DB 覆盖），不 Flush 开发用的 DB 0。
#   - MySQL root 密码取自 DATABASE_DEFAULT_PASS（默认 root，与 make deps-start 一致），
#     经 MYSQL_PWD 传入，不出现在进程参数里。
#
# 反「静默零匹配 / 假 PASS」机制（本文件的核心约束）：
#   - 每个阶段先用 `go test -list <同一正则>` 枚举真实存在的测试；
#   - 枚举结果为 0 → 立刻失败，绝不打印 PASS；
#   - 执行 `go test -race -count=1 -v` 后，逐条核对枚举出的测试名都出现了
#     `--- PASS: <name> `，并核对 PASS 行数 == 枚举数量（SKIP 同样视为未通过）；
#   - 命令退出码非零 → 立即失败；不使用 `|| true` 吞掉任何 go test 失败。
#
# 与 CI 的关系：.cnb.yml 的 pr-check / main-ci 都在 `make deps-start` 之后执行
# `make test-race`，所以本脚本依赖 compose 已启动且健康的 MySQL/Redis。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

# ---------------------------------------------------------------------------
# 测试库定义与安全校验
# ---------------------------------------------------------------------------
OUTBOX_RACE_DB="${OUTBOX_RACE_DB_NAME:-my_shop_outbox_race}"
CONCURRENCY_DB="my_shop_b10503_test"
FLASH_REGRESSION_DB="my_shop_b10503_flash_test"
# 专用 Redis DB：避免 FlushDB 打到开发/联调使用的 DB 0。
REDIS_TEST_DB="${B10503_REDIS_DB:-15}"
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"

# assert_owned_db 拒绝任何不属于本脚本的数据库：
#   - 显式拒绝共享库/历史验证库；
#   - 只允许 my_shop_*_test / my_shop_*_race 命名。
assert_owned_db() {
  local name="$1"
  case "${name}" in
    ""|my_shop|my_shop_staging|my_shop_b10304_verify|mysql|sys|information_schema|performance_schema)
      fail "拒绝操作共享或历史数据库：${name}" ;;
  esac
  [[ "${name}" == my_shop_* ]] || fail "拒绝操作非 my_shop_ 前缀数据库：${name}"
  [[ "${name}" == *_test || "${name}" == *_race ]] ||
    fail "拒绝操作非测试命名数据库（需 _test/_race 后缀）：${name}"
}

# ---------------------------------------------------------------------------
# MySQL 连接（复用 compose 已启动的容器；未启动时明确失败并给出修复命令）
# ---------------------------------------------------------------------------
mysql_id="$(docker_compose_placeholder ps -q mysql 2>/dev/null | head -n 1)"
if [[ -z "${mysql_id}" ]]; then
  log_error "race 门禁需要 compose 管理的 MySQL：请先执行 make deps-start"
  exit 1
fi

# db_exec 在 MySQL 容器内执行 SQL（密码经 MYSQL_PWD 传入，不出现在进程参数里）。
db_exec() {
  docker exec -i -e MYSQL_PWD="${DATABASE_DEFAULT_PASS}" "${mysql_id}" mysql -uroot "$@"
}

LOG_DIR="$(mktemp -d)"
created_dbs=()

cleanup() {
  local name
  if [[ ${#created_dbs[@]} -gt 0 ]]; then
    for name in "${created_dbs[@]}"; do
      db_exec -e "DROP DATABASE IF EXISTS \`${name}\`" >/dev/null 2>&1 ||
        log_warn "清理专用库 ${name} 失败，可人工删除"
    done
  fi
  rm -rf "${LOG_DIR}"
}
trap cleanup EXIT

# create_fresh_db 建库前先 DROP，保证每次执行都从干净 schema 开始（可重复运行）。
create_fresh_db() {
  local name="$1"
  assert_owned_db "${name}"
  db_exec -e "DROP DATABASE IF EXISTS \`${name}\`; CREATE DATABASE \`${name}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
  created_dbs+=("${name}")
}

# ---------------------------------------------------------------------------
# 阶段执行器：枚举 → 执行 → 逐条核对 PASS
# ---------------------------------------------------------------------------
# run_stage <标签> <包> <数据库> <测试正则>
#   - 正则同时用于 go test -list 与 go test -run，保证「枚举到的 == 执行的」；
#   - 失败即退出非零，不会打印通过文案。
run_stage() {
  local label="$1" pkg="$2" db="$3" pattern="$4"
  local list_log run_log
  list_log="${LOG_DIR}/list.$$.$RANDOM.log"
  run_log="${LOG_DIR}/run.$$.$RANDOM.log"

  # 1) 先确认目标测试真实存在（零匹配直接失败）。
  if ! DATABASE_DEFAULT_NAME="${db}" REDIS_DEFAULT_DB="${REDIS_TEST_DB}" \
    go test -list "${pattern}" "${pkg}" >"${list_log}" 2>&1; then
    sed -n '1,60p' "${list_log}" >&2
    fail "阶段「${label}」无法枚举测试（编译失败或包路径错误）：${pkg}"
  fi
  local expected
  expected="$(awk '!seen[$0]++ && /^Test/{c++} END{print c+0}' "${list_log}")"
  if [[ "${expected}" -eq 0 ]]; then
    sed -n '1,60p' "${list_log}" >&2
    fail "阶段「${label}」零匹配：${pkg} 中没有测试匹配 ${pattern}（脚本拒绝报告 PASS）"
  fi

  # 2) 真实执行（-v 才能拿到逐条 --- PASS: 证据）。
  log_info "go test -race（${label}｜包 ${pkg}｜库 ${db}｜预期 ${expected} 项）"
  if ! DATABASE_DEFAULT_NAME="${db}" REDIS_DEFAULT_DB="${REDIS_TEST_DB}" \
    go test -race -p 1 -count=1 -v -timeout 20m -run "${pattern}" "${pkg}" 2>&1 |
    tee "${run_log}"; then
    fail "阶段「${label}」测试命令返回非零退出码"
  fi

  # 3) 逐条核对枚举到的测试都真实 PASS，且 PASS 行数与枚举数一致（SKIP 不算通过）。
  local passed=0 name missing=""
  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    if grep -qE "^--- PASS: ${name} " "${run_log}"; then
      passed=$((passed + 1))
    else
      missing="${missing} ${name}"
    fi
  done < <(awk '!seen[$0]++ && /^Test/{print $1}' "${list_log}")

  local pass_lines
  pass_lines="$(awk '/^--- PASS: /{c++} END{print c+0}' "${run_log}")"
  if [[ "${passed}" -ne "${expected}" || "${pass_lines}" -ne "${expected}" ]]; then
    fail "阶段「${label}」实际 PASS ${passed}/${expected} 项（PASS 行 ${pass_lines} 行）；未通过:${missing}"
  fi
  log_info "阶段「${label}」真实执行并通过 ${passed} 项"
}

# flashsale_pattern_except_real_kafka 枚举 flashsale 包的全部测试，排除需要真实
# Kafka Broker 的用例（缺少 Broker 时它们会 t.Skip，混入门禁会把「未验证」伪装成通过；
# 这类用例由 make test-kafka-e2e 单独覆盖）。返回可直接用于 -list/-run 的锚定正则。
flashsale_pattern_except_real_kafka() {
  local pkg="$1" list_log="${LOG_DIR}/flashsale-list.$$.$RANDOM.log"
  if ! DATABASE_DEFAULT_NAME="${FLASH_REGRESSION_DB}" REDIS_DEFAULT_DB="${REDIS_TEST_DB}" \
    go test -list '.' "${pkg}" >"${list_log}" 2>&1; then
    sed -n '1,60p' "${list_log}" >&2
    fail "无法枚举 ${pkg} 的测试（编译失败？）"
  fi
  local names
  names="$(awk '/^Test/ && $1 !~ /RealKafka/ {print $1}' "${list_log}")"
  [[ -n "${names}" ]] || fail "${pkg} 未枚举到任何测试（排除 RealKafka 后为空）"
  printf '^(%s)$' "$(printf '%s\n' "${names}" | tr '\n' '|' | sed 's/|$//')"
}

# ---------------------------------------------------------------------------
# 阶段 1：纯内存共享状态并发（不连接 MySQL/Redis）
# ---------------------------------------------------------------------------
log_info "阶段 1：进程内共享内存并发逻辑（熔断器 / Producer 快照 / Runtime 装配 / 指标 / HTTP drain）"
run_stage "秒杀熔断器状态机" ./internal/logic/flashsale/ "${CONCURRENCY_DB}" 'TestCircuitBreakerStateMachine'
run_stage "Producer 逐条结果快照" ./internal/mq/kafka/ "${CONCURRENCY_DB}" 'TestProducerPublishResults'
run_stage "B4 秒杀 Runtime 装配" ./internal/worker/ "${CONCURRENCY_DB}" \
  'TestResolveFlashSaleRuntimeConfig|TestFlashSaleRuntime|TestScannersStopOnContextCancel|TestFlashSaleRuntimeOutboxBacklogSampler|TestFlashSaleRuntimeDBScannerModeSkipsBacklogSampling|TestFlashSaleRuntimeBacklogSamplingDisabledWithMetrics'
run_stage "Consumer Group 状态机" ./internal/mq/kafka/ "${CONCURRENCY_DB}" 'TestConsumerGroupJoined'
run_stage "指标并发读写" ./internal/metrics/ "${CONCURRENCY_DB}" '.'
run_stage "HTTP drain 与强制关闭" ./internal/cmd/ "${CONCURRENCY_DB}" \
  'TestRuntimeContextDetachesSignalCancellation|TestForceCloseWindow|TestTrackedListener|TestStopHTTPServer'

# ---------------------------------------------------------------------------
# 阶段 2：Outbox 并发可靠性（F-14）+ B3 Consumer 并发
# ---------------------------------------------------------------------------
# 用例与审查要求一一对应，显式列出而不用宽泛的 ^TestOutbox：
# TestOutboxRealKafka* 需要真实 Broker，缺少环境时会 t.Skip，
# 若混入本门禁会让「未验证」伪装成通过。
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

CONSUMER_RACE_TESTS=(
  "TestConsumerRaceOversellGuard"              # 并发消费者竞争同一 SKU：成功订单数恰好等于库存
  "TestConsumerRaceKafkaAndScannerSameRequest" # Kafka 重复投递 + Scanner 并发接管：只落一单
)

create_fresh_db "${OUTBOX_RACE_DB}"
log_info "阶段 2：Outbox 并发可靠性（隔离库 ${OUTBOX_RACE_DB}）"
run_stage "Outbox 并发可靠性" ./internal/logic/flashsale/ "${OUTBOX_RACE_DB}" \
  "^($(IFS='|'; printf '%s' "${OUTBOX_RACE_TESTS[*]}"))$"
run_stage "Consumer 并发落单" ./internal/logic/flashsale/ "${OUTBOX_RACE_DB}" \
  "^($(IFS='|'; printf '%s' "${CONSUMER_RACE_TESTS[*]}"))$"

# ---------------------------------------------------------------------------
# 阶段 3：B1-05-03 五类并发与事务一致性（真实 MySQL，独立库）
# ---------------------------------------------------------------------------
# 包内每个用例都会先校验「库名必须等于 ${CONCURRENCY_DB}」再清表，
# 因此这里传入错误的库名只会 Skip，不会破坏共享库；脚本侧再用 PASS 数量把它暴露为失败。
B10503_CONCURRENCY_TESTS=(
  "TestInventoryConcurrentDeductNoOversell"                # 4.1 不超卖 + 流水自洽
  "TestInventoryConcurrentDeductInsufficientNoPhantomLog"  # 4.1 失败请求零流水
  "TestInventoryConcurrentIncreaseAndDeductKeepLedgerChain" # 4.1 混合增减账实一致
  "TestWalletConcurrentDebitNoNegativeBalance"             # 4.2 不超额扣款/不负余额
  "TestWalletConcurrentSameOrderSingleDebit"               # 4.2 同订单并发只扣一次
  "TestWalletConcurrentMixedAmountsLedgerConsistent"       # 4.2 不同金额并发对账
  "TestWalletDebitRollbackLeavesNoTrace"                   # 4.2 事务失败整体回滚
  "TestGroupBuyConcurrentJoinNoOverCapacity"               # 4.3 不超员 + 一人一单
  "TestGroupBuyConcurrentDuplicateJoinSingleParticipant"   # 4.3 同用户并发重复参团
  "TestGroupBuyJoinOrderConflictRollsBackCapacity"         # 4.3 失败事务不泄漏名额
  "TestBargainConcurrentStartSingleActiveInstance"         # 4.4 active_key 唯一
  "TestBargainConcurrentHelpOncePerHelper"                 # 4.4 同助手只助力一次
  "TestBargainConcurrentHelpersPriceNeverBelowFloor"       # 4.4 不砍穿底价
  "TestBargainActiveKeyReleasedAfterAbandon"               # 4.4 生成列释放/重建
  "TestCouponConcurrentClaimNoOversell"                    # 4.5 不超发
  "TestCouponConcurrentClaimSameUserSingleCoupon"          # 4.5 单用户限领 + 计数回滚
  "TestCouponDuplicateClaimKeepsIssuedQuantity"            # 4.5 唯一键兜底
)

create_fresh_db "${CONCURRENCY_DB}"
log_info "阶段 3：B1-05-03 五类并发（隔离库 ${CONCURRENCY_DB}）"
run_stage "五类并发与事务一致性" ./internal/logic/concurrency/ "${CONCURRENCY_DB}" \
  "^($(IFS='|'; printf '%s' "${B10503_CONCURRENCY_TESTS[*]}"))$"

# ---------------------------------------------------------------------------
# 阶段 4：B1-05-03 秒杀回归（Redis Lua 准入 / 售罄 / 重复购买 / fail-closed / 消费幂等）
# ---------------------------------------------------------------------------
B10503_FLASH_REGRESSION_TESTS=(
  "TestFlashSaleRegressionLuaGateMatrix"            # 5.1 准入/幂等/一人一单/不存在/未开始/停用
  "TestFlashSaleRegressionLuaGateSoldOut"           # 5.1/5.2 库存 0 拒绝 + 售罄标记
  "TestFlashSaleRegressionLuaGateConcurrentNoOversell" # 5.2 并发不超卖
  "TestFlashSaleRegressionLuaGateDuplicateUser"     # 5.3 并发重复购买只预扣一次
  "TestFlashSaleRegressionFailClosedOnRedisFault"   # 5.4 Redis 故障 fail-closed
  "TestFlashSaleRegressionSoldOutConvergesWithDB"   # 5.2 售罄后缓存与 DB 收敛
  "TestFlashSaleRegressionDuplicatePurchaseSingleOrder" # 5.3 重复购买只落一单
  "TestFlashSaleRegressionKafkaRedeliveryConsumesOnce"  # 5.5 重复消费只落一单
)

create_fresh_db "${FLASH_REGRESSION_DB}"
log_info "阶段 4：秒杀回归（隔离库 ${FLASH_REGRESSION_DB}，Redis DB ${REDIS_TEST_DB}）"
run_stage "秒杀回归（Lua 准入/售罄/重复购买/fail-closed/消费幂等）" \
  ./internal/logic/flashsale/ "${FLASH_REGRESSION_DB}" \
  "^($(IFS='|'; printf '%s' "${B10503_FLASH_REGRESSION_TESTS[*]}"))$"

# ---------------------------------------------------------------------------
# 阶段 5：秒杀既有回归全集（排除需要真实 Kafka Broker 的用例）
# ---------------------------------------------------------------------------
log_info "阶段 5：秒杀既有回归全集（flashsale 包，排除 RealKafka 用例）"
flash_pattern="$(flashsale_pattern_except_real_kafka ./internal/logic/flashsale/)"
run_stage "秒杀既有回归全集" ./internal/logic/flashsale/ "${FLASH_REGRESSION_DB}" "${flash_pattern}"

# ---------------------------------------------------------------------------
# 阶段 6：秒杀 HTTP 端到端回归（test/e2e 中的 flashsale*_test.go）
# ---------------------------------------------------------------------------
# 真实路由 + 真实 MySQL/Redis + 进程内 HTTP server：覆盖 HTTP 层状态码
# （如 Redis 故障时 503/1005）、预热/失效、售罄快速失败、V3 异步入队与幂等、V5 限流与指标。
log_info "阶段 6：秒杀 HTTP 端到端回归（test/e2e，隔离库 ${FLASH_REGRESSION_DB}）"
run_stage "秒杀 HTTP 端到端回归" ./test/e2e/ "${FLASH_REGRESSION_DB}" 'TestFlashSale'

log_info "race 门禁全部通过：Outbox ${#OUTBOX_RACE_TESTS[@]} 项、Consumer ${#CONSUMER_RACE_TESTS[@]} 项、B1-05-03 五类并发 ${#B10503_CONCURRENCY_TESTS[@]} 项、B1-05-03 秒杀回归 ${#B10503_FLASH_REGRESSION_TESTS[@]} 项、秒杀既有回归全集与 HTTP 端到端回归"
