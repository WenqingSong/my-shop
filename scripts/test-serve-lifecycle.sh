#!/usr/bin/env bash
# B4.1 进程级 serve 生命周期门禁：真实可执行文件 + 真实 HTTP + 真实 Kafka + 操作系统 SIGTERM。
#
# 覆盖：
#   - 真实 serve 命令启动（Bootstrap → 配置校验 → Kafka Runtime 装配 → HTTP 监听 → 健康检查）；
#   - 七牛启动校验：把 SDK 的 UC endpoint 指向本地 Mock（真实签名/请求/解析代码不变，
#     生产二进制不含该能力，也没有任何 skip 校验开关）；
#   - 在途秒杀流量下向应用 PID 发送 SIGTERM，验证关闭顺序、退出码与关闭预算；
#   - 同配置重启后请求收敛，MySQL 订单/库存/请求状态与 Kafka 消息一致性核对。
#
# 隔离保证：
#   - 专用 MySQL 库（默认 my_shop_serve_lifecycle）与专用 Redis DB（默认 7），测试前重建、测试后删除；
#   - 拒绝 my_shop / my_shop_staging 等开发库；
#   - 每次运行创建唯一 Topic / DLQ / Consumer Group，结束后删除；
#   - 子进程监听独立端口，日志写入独立产物目录；
#   - 失败不静默：超时、进程未退出、用例未实际执行都以非零退出。
#
# 用法：
#   bash scripts/test-serve-lifecycle.sh
#   SERVE_LIFECYCLE_KEEP_DB=1 bash scripts/test-serve-lifecycle.sh   # 保留数据库与产物便于排查
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

LIFECYCLE_DB="${SERVE_LIFECYCLE_DB:-my_shop_serve_lifecycle}"
REDIS_DB="${SERVE_LIFECYCLE_REDIS_DB:-7}"
BROKER="${FLASH_SALE_KAFKA_TEST_BROKER:-127.0.0.1:29092}"
KAFKA_CONTAINER="${B4_E2E_KAFKA_CONTAINER:-my-shop-kafka}"
MYSQL_CONTAINER="${B4_E2E_MYSQL_CONTAINER:-my-shop-mysql}"
RUN_ID="$(date +%Y%m%d%H%M%S)-$$"
TOPIC="surgecart.lifecycle.flashsale.${RUN_ID}.v1"
DLQ="${TOPIC}.dlq"
GROUP="surgecart-lifecycle-${RUN_ID}"
artifact_created=0
if [[ -n "${SERVE_LIFECYCLE_ARTIFACT_DIR:-}" ]]; then
  ARTIFACT_DIR="${SERVE_LIFECYCLE_ARTIFACT_DIR}"
  mkdir -p "${ARTIFACT_DIR}"
else
  ARTIFACT_DIR="$(mktemp -d -t my-shop-serve-lifecycle-XXXXXX)"
  artifact_created=1
fi
BIN_DIR="$(mktemp -d -t my-shop-serveproc-XXXXXX)"
SERVE_BIN="${BIN_DIR}/my-shop-serveproc"
TEST_LOG="$(mktemp)"

case "${LIFECYCLE_DB}" in
  my_shop|my_shop_staging|mysql|sys|information_schema|performance_schema)
    log_error "拒绝使用 ${LIFECYCLE_DB} 作为生命周期测试库（必须是可销毁的专用库）"
    exit 1
    ;;
esac
if [[ "${REDIS_DB}" == "0" ]]; then
  log_error "SERVE_LIFECYCLE_REDIS_DB 不得为 0（会清空开发 Redis DB）"
  exit 1
fi

export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"
export MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:-${DATABASE_DEFAULT_PASS}}"
export AUTH_JWT_SECRET="${AUTH_JWT_SECRET:-ci-placeholder-0123456789-0123456789}"
export SERVE_LIFECYCLE_ARTIFACT_DIR="${ARTIFACT_DIR}"

mysql_exec() {
  docker exec -i -e MYSQL_PWD="${DATABASE_DEFAULT_PASS}" "${MYSQL_CONTAINER}" mysql -uroot "$@"
}

kafka_topics() {
  docker exec "${KAFKA_CONTAINER}" /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 "$@"
}

db_created=0
cleanup() {
  local exit_code=$?
  # 只清理本脚本创建的进程与资源，绝不触碰其他 Topic / 库。
  pkill -f "${SERVE_BIN} serve" >/dev/null 2>&1 || true
  if docker inspect "${KAFKA_CONTAINER}" >/dev/null 2>&1; then
    docker exec "${KAFKA_CONTAINER}" /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server kafka:9092 --delete --group "${GROUP}" >/dev/null 2>&1 || true
    kafka_topics --delete --if-exists --topic "${TOPIC}" >/dev/null 2>&1 || true
    kafka_topics --delete --if-exists --topic "${DLQ}" >/dev/null 2>&1 || true
  fi
  if [[ "${db_created}" == "1" && "${SERVE_LIFECYCLE_KEEP_DB:-0}" != "1" ]]; then
    mysql_exec -e "DROP DATABASE IF EXISTS \`${LIFECYCLE_DB}\`" >/dev/null 2>&1 ||
      log_warn "清理测试库 ${LIFECYCLE_DB} 失败，可人工删除"
  fi
  rm -rf "${BIN_DIR}"
  rm -f "${TEST_LOG}"
  if [[ ${exit_code} -ne 0 ]]; then
    log_error "生命周期门禁失败；子进程日志与产物保留在 ${ARTIFACT_DIR}"
  elif [[ "${SERVE_LIFECYCLE_KEEP_DB:-0}" == "1" ]]; then
    log_info "已保留测试库 ${LIFECYCLE_DB} 与产物目录 ${ARTIFACT_DIR}"
  elif [[ "${artifact_created}" == "1" ]]; then
    rm -rf "${ARTIFACT_DIR}"
  fi
  exit ${exit_code}
}
trap cleanup EXIT

# 1) 依赖：MySQL/Redis 未启动时自动拉起；Kafka 使用 profile。
if [[ -z "$(docker_compose_placeholder ps -q mysql 2>/dev/null | head -n 1)" ]]; then
  log_info "启动 MySQL/Redis 测试依赖..."
  docker_compose up -d mysql redis
fi
wait_for_deps

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

# 2) 专用数据库 + 唯一 Topic（显式创建，不依赖 Broker 自动建 Topic）。
log_info "重建专用测试库 ${LIFECYCLE_DB}（Redis DB=${REDIS_DB}）..."
mysql_exec -e "DROP DATABASE IF EXISTS \`${LIFECYCLE_DB}\`"
mysql_exec -e "CREATE DATABASE \`${LIFECYCLE_DB}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
db_created=1

log_info "创建测试 Topic ${TOPIC} / ${DLQ}..."
kafka_topics --create --if-not-exists --topic "${TOPIC}" --partitions 6 --replication-factor 1 >/dev/null
kafka_topics --create --if-not-exists --topic "${DLQ}" --partitions 6 --replication-factor 1 >/dev/null
kafka_topics --describe --topic "${TOPIC}" >/dev/null

# 3) 构建测试专用 serve 入口（与生产 main.go 同一命令装配，仅七牛 UC endpoint 可注入）。
log_info "构建 serve_lifecycle 二进制..."
( cd "${ROOT_DIR}" && go build -tags serve_lifecycle -o "${SERVE_BIN}" ./test/serveproc )

# 4) 进程级用例：必须实际执行并通过（不接受 skip / 改名静默匹配）。
export SERVE_LIFECYCLE_DB="${LIFECYCLE_DB}"
export SERVE_LIFECYCLE_REDIS_DB="${REDIS_DB}"
export FLASH_SALE_KAFKA_TEST_BROKER="${BROKER}"
export FLASH_SALE_KAFKA_TEST_TOPIC="${TOPIC}"
export FLASH_SALE_KAFKA_TEST_GROUP="${GROUP}"
export SERVE_LIFECYCLE_BIN="${SERVE_BIN}"

log_info "go test -tags serve_lifecycle（真实进程/HTTP/Kafka/SIGTERM）..."
set +e
go test -tags serve_lifecycle -p 1 -count=1 -v -timeout 20m \
  -run '^TestServeProcessLifecycle$' ./test/e2e/ 2>&1 | tee "${TEST_LOG}"
go_test_code=${PIPESTATUS[0]}
set -e
if [[ ${go_test_code} -ne 0 ]]; then
  log_error "go test 退出码 ${go_test_code}；日志见 ${ARTIFACT_DIR}"
  exit ${go_test_code}
fi
if ! grep -qE '^--- PASS: TestServeProcessLifecycle ' "${TEST_LOG}"; then
  log_error "进程级用例未实际执行或未通过（可能存在 t.Skip / 用例改名）"
  exit 1
fi

# 5) 子进程必须真正退出：残留进程视为失败（不允许依赖 kill -9 收尾）。
if pgrep -f "${SERVE_BIN} serve" >/dev/null 2>&1; then
  log_error "检测到残留 serve 子进程："
  pgrep -af "${SERVE_BIN} serve" >&2 || true
  exit 1
fi

log_info "进程级 serve 生命周期门禁通过（DB=${LIFECYCLE_DB} RedisDB=${REDIS_DB} Topic=${TOPIC} Group=${GROUP}）"
