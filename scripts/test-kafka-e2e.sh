#!/usr/bin/env bash
# B4 真实 Kafka 秒杀端到端门禁：真实 MySQL + Redis + Kafka + HTTP。
#
# 覆盖：
#   - db_scanner 模式（不需要 Kafka）；
#   - kafka 模式完整链路（HTTP → Redis → MySQL Outbox → Kafka → Consumer → 订单 → Offset）；
#   - DB Scanner ↔ Kafka 模式切换（重启生效，停旧启新）；
#   - Publisher/Consumer 重启后 Outbox 恢复；
#   - Publisher 与 Consumer 同时运行时关闭；
#   - 可选故障注入：Kafka 停机恢复、MySQL 停机恢复（B4_E2E_FAULTS=1，默认开启）。
#
# 隔离保证：
#   - 使用专用数据库（默认 my_shop_kafka_e2e）与专用 Redis DB（默认 6），测试前重建、测试后删除；
#   - 拒绝把 my_shop / my_shop_staging 等开发库当作测试库；
#   - 每次运行创建唯一 Topic（带时间戳），结束后删除，避免跨运行残留消息干扰位移断言。
#
# 用法：
#   bash scripts/test-kafka-e2e.sh
#   B4_E2E_FAULTS=0 bash scripts/test-kafka-e2e.sh   # 跳过 stop/start 容器的故障注入
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

E2E_DB="${B4_E2E_DB:-my_shop_kafka_e2e}"
REDIS_DB="${B4_E2E_REDIS_DB:-6}"
BROKER="${FLASH_SALE_KAFKA_TEST_BROKER:-127.0.0.1:29092}"
KAFKA_CONTAINER="${B4_E2E_KAFKA_CONTAINER:-my-shop-kafka}"
MYSQL_CONTAINER="${B4_E2E_MYSQL_CONTAINER:-my-shop-mysql}"
RUN_FAULTS="${B4_E2E_FAULTS:-1}"
TOPIC="surgecart.e2e.flashsale.$(date +%Y%m%d%H%M%S).v1"
DLQ="${TOPIC}.dlq"
GROUP="surgecart-e2e-$(date +%Y%m%d%H%M%S)"

case "${E2E_DB}" in
  my_shop|my_shop_staging|mysql|sys|information_schema|performance_schema)
    log_error "拒绝使用 ${E2E_DB} 作为端到端测试库（必须是可销毁的专用库）"
    exit 1
    ;;
esac

export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"
export MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:-${DATABASE_DEFAULT_PASS}}"
export AUTH_JWT_SECRET="${AUTH_JWT_SECRET:-ci-placeholder-0123456789-0123456789}"

mysql_exec() {
  docker exec -i -e MYSQL_PWD="${DATABASE_DEFAULT_PASS}" "${MYSQL_CONTAINER}" mysql -uroot "$@"
}

kafka_topics() {
  docker exec "${KAFKA_CONTAINER}" /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 "$@"
}

run_log="$(mktemp)"
cleanup() {
  rm -f "${run_log}"
  # 故障注入可能停在容器已停止的状态：先恢复运行，便于后续清理与本地环境复用。
  docker start "${KAFKA_CONTAINER}" >/dev/null 2>&1 || true
  docker start "${MYSQL_CONTAINER}" >/dev/null 2>&1 || true
  if docker inspect "${KAFKA_CONTAINER}" >/dev/null 2>&1; then
    docker exec "${KAFKA_CONTAINER}" /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server kafka:9092 --delete --group "${GROUP}" >/dev/null 2>&1 || true
    kafka_topics --delete --if-exists --topic "${TOPIC}" >/dev/null 2>&1 || true
    kafka_topics --delete --if-exists --topic "${DLQ}" >/dev/null 2>&1 || true
  fi
  if [[ "${B4_E2E_KEEP_DB:-0}" != "1" ]]; then
    mysql_exec -e "DROP DATABASE IF EXISTS \`${E2E_DB}\`" >/dev/null 2>&1 ||
      log_warn "清理测试库 ${E2E_DB} 失败，可人工删除"
  fi
}
trap cleanup EXIT

# 1) 依赖就绪：MySQL/Redis 没有启动时自动拉起（不构建 app）。
if [[ -z "$(docker_compose_placeholder ps -q mysql 2>/dev/null | head -n 1)" ]]; then
  log_info "启动 MySQL/Redis 测试依赖..."
  docker_compose up -d mysql redis
fi
wait_for_deps

# 2) Kafka（profile kafka，默认不随普通 compose up 启动）。
log_info "启动 Kafka（profile=kafka）..."
docker_compose --profile kafka up -d kafka
for _ in $(seq 1 60); do
  if [[ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${KAFKA_CONTAINER}" 2>/dev/null)" == "healthy" ]]; then
    break
  fi
  sleep 2
done
if [[ "$(docker inspect -f '{{.State.Health.Status}}' "${KAFKA_CONTAINER}" 2>/dev/null)" != "healthy" ]]; then
  log_error "Kafka 未在 120s 内 healthy"
  exit 1
fi

# 3) 专用数据库（重建，绝不清理开发库）。
log_info "重建专用测试库 ${E2E_DB}（Redis DB=${REDIS_DB}）..."
mysql_exec -e "DROP DATABASE IF EXISTS \`${E2E_DB}\`"
mysql_exec -e "CREATE DATABASE \`${E2E_DB}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"

# 4) 唯一 Topic（显式创建，不依赖 Broker 自动建 Topic）。
log_info "创建测试 Topic ${TOPIC} / ${DLQ}..."
kafka_topics --create --if-not-exists --topic "${TOPIC}" --partitions 6 --replication-factor 1
kafka_topics --create --if-not-exists --topic "${DLQ}" --partitions 6 --replication-factor 1
kafka_topics --describe --topic "${TOPIC}" >/dev/null

export DATABASE_DEFAULT_NAME="${E2E_DB}"
export REDIS_DEFAULT_DB="${REDIS_DB}"
export FLASH_SALE_KAFKA_TEST_BROKER="${BROKER}"
export FLASH_SALE_KAFKA_TEST_TOPIC="${TOPIC}"
export FLASH_SALE_KAFKA_TEST_GROUP="${GROUP}"
export FLASH_SALE_KAFKA_E2E_DOCKER="${RUN_FAULTS}"
export FLASH_SALE_KAFKA_E2E_CONTAINER="${KAFKA_CONTAINER}"
export FLASH_SALE_KAFKA_E2E_MYSQL_CONTAINER="${MYSQL_CONTAINER}"
export FLASH_SALE_METRICS_ENABLED="true"

required=(
  "TestKafkaFlashSaleEndToEnd"
  "TestDBScannerFlashSaleEndToEndWithoutKafka"
  "TestFlashSaleModeSwitchRequiresRestart"
  "TestKafkaFlashSalePublisherRestartRecoversOutbox"
  "TestKafkaFlashSaleShutdownWithInflightTraffic"
)
if [[ "${RUN_FAULTS}" == "1" ]]; then
  required+=(
    "TestKafkaFlashSaleBrokerOutageRecovery"
    "TestKafkaFlashSaleMySQLOutageRecovery"
  )
fi

run_pattern="${B4_E2E_RUN:-TestKafkaFlashSale|TestDBScannerFlashSale|TestFlashSaleModeSwitch}"
log_info "go test -tags kafka_e2e（真实 MySQL/Redis/Kafka/HTTP）..."
go test -tags kafka_e2e -p 1 -count=1 -v -timeout 30m \
  -run "${run_pattern}" \
  ./test/e2e/ | tee "${run_log}"

# 不允许「用例被跳过/改名」伪装成通过：逐个校验实际 PASS。
# B4_E2E_RUN 用于单用例调试，此时只校验实际匹配到的用例。
if [[ -n "${B4_E2E_RUN:-}" ]]; then
  required=()
  while IFS= read -r name; do
    required+=("${name}")
  done < <(grep -oE '^--- (PASS|FAIL|SKIP): [A-Za-z0-9_]+' "${run_log}" | awk '{print $3}' | sort -u)
fi
for name in "${required[@]}"; do
  grep -qE "^--- PASS: ${name} " "${run_log}" || {
    log_error "端到端用例未实际执行或未通过：${name}"
    exit 1
  }
done

log_info "Kafka 秒杀端到端门禁通过（${#required[@]} 个用例）"
