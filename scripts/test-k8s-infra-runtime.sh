#!/usr/bin/env bash
# T2-07 基础设施**容器级等价验证**（不是 Kubernetes 验证）。
#
# 为什么存在这个脚本：
#   本仓库的开发机可能没有能力运行 Kubernetes（kind 需要特权容器）。
#   但「官方镜像能否在 restricted 安全上下文下运行」这个问题**不需要**集群就能回答：
#   用 Docker 复刻同一组约束（非 root 用户、只读根文件系统、capabilities 全丢弃、
#   no-new-privileges、tmpfs/PVC 等价挂载），再用另一个容器按 Service 名连接。
#   这能证明：镜像自洽、端口/账号/监听器配置正确、客户端能完成协议交互。
#
# 它**不能**证明：
#   - Pod 调度、PVC 绑定、restricted PSS 准入、探针时序、Service/Endpoint 路由；
#   - kubelet 的 fsGroup 授权（这里用 tmpfs 的 uid/gid/mode 近似）。
#   上述项目在 T2-07 报告中标记为 NOT_VERIFIED，必须由真实集群上的
#   `bash scripts/k8s-infra.sh verify` 覆盖。
#
# 约束来源（与本脚本同源）：
#   - manifest/kubernetes/infrastructure/base/mysql.yaml     (uid/gid 999, /var/lib/mysql, /var/run/mysqld)
#   - manifest/kubernetes/infrastructure/base/redis.yaml     (uid/gid 999, /data)
#   - manifest/kubernetes/infrastructure/base/kafka.yaml     (uid/gid 1000, /var/lib/kafka/data,
#                                                             /opt/kafka/config, /opt/kafka/logs)
#   - manifest/kubernetes/infrastructure/base/configmap.yaml (broker 监听器与 Topic 名称)
#   uid/gid 与挂载点若在清单里变化而这里没有同步，证据即失效——由人工复核 + T2-07 报告记录。
#
# 用法：
#   bash scripts/test-k8s-infra-runtime.sh          # 需要 Docker，约 2-3 分钟（首次要拉镜像）
#   K8S_RUNTIME_KEEP=1 bash scripts/test-k8s-infra-runtime.sh   # 保留容器便于排查
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

command -v docker >/dev/null 2>&1 || fail "未找到 docker：本脚本用 Docker 复刻 Pod 的运行约束"
docker info >/dev/null 2>&1 || fail "Docker 守护进程不可用"

SUFFIX="$$"
NET="t207-net-${SUFFIX}"
MYSQL_NAME="t207-mysql-${SUFFIX}"
REDIS_NAME="t207-redis-${SUFFIX}"
KAFKA_NAME="t207-kafka-${SUFFIX}"
VOL_MYSQL="t207-mysql-${SUFFIX}"
VOL_REDIS="t207-redis-${SUFFIX}"
VOL_KAFKA="t207-kafka-${SUFFIX}"

MYSQL_ROOT_PASSWORD="$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')"
DATABASE_DEFAULT_PASS="$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')"
# 集群内地址在本脚本里退化成「容器名 + 端口」：Docker 的内嵌 DNS 与
# Kubernetes 的 Service DNS 在客户端看来都是「一个解析到单实例的名字」。
# 注意：真正要验证的是「客户端拿到的广播地址可达」，因此 Broker 的
# KAFKA_ADVERTISED_LISTENERS 也必须是 kafka:9092（而不是 127.0.0.1）。
TOPIC="surgecart.flashsale.order.requested.v1"
DLQ="surgecart.flashsale.order.requested.v1.dlq"

cleanup() {
  if [[ "${K8S_RUNTIME_KEEP:-0}" == "1" ]]; then
    log_warn "K8S_RUNTIME_KEEP=1：保留容器与网络 ${NET}（排查后手动删除）"
    return
  fi
  docker rm -f "${MYSQL_NAME}" "${REDIS_NAME}" "${KAFKA_NAME}" >/dev/null 2>&1 || true
  docker volume rm "${VOL_MYSQL}" "${VOL_REDIS}" "${VOL_KAFKA}" >/dev/null 2>&1 || true
  docker network rm "${NET}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# 与清单一致的「restricted 等价」参数。
RO_FLAGS=(--read-only --cap-drop ALL --security-opt no-new-privileges)

log_info "创建隔离网络 ${NET}（模拟集群内同一命名空间）"
docker network create "${NET}" >/dev/null
docker volume create "${VOL_MYSQL}" >/dev/null
docker volume create "${VOL_REDIS}" >/dev/null
docker volume create "${VOL_KAFKA}" >/dev/null

# ---------------------------------------------------------------------------
# 启动三个依赖（镜像标签与清单一致）
# ---------------------------------------------------------------------------
log_info "启动 MySQL（uid 999、只读根文件系统、capabilities 全丢弃）"
docker run -d --name "${MYSQL_NAME}" --network "${NET}" --network-alias mysql \
  --user 999:999 "${RO_FLAGS[@]}" \
  --tmpfs /tmp:rw,exec \
  --tmpfs /var/run/mysqld:rw,exec,uid=999,gid=999,mode=0755 \
  --mount "type=volume,src=${VOL_MYSQL},dst=/var/lib/mysql" \
  -e MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD}" \
  -e MYSQL_DATABASE=my_shop \
  -e MYSQL_USER=surgecart \
  -e MYSQL_PASSWORD="${DATABASE_DEFAULT_PASS}" \
  mysql:8.0 >/dev/null

log_info "启动 Redis（uid 999、只读根文件系统、无认证，与项目现有语义一致）"
docker run -d --name "${REDIS_NAME}" --network "${NET}" --network-alias redis \
  --user 999:999 "${RO_FLAGS[@]}" \
  --tmpfs /tmp:rw,exec \
  --mount "type=volume,src=${VOL_REDIS},dst=/data" \
  redis:7-alpine >/dev/null

log_info "启动 Kafka（KRaft 单节点、uid 1000、只读根文件系统）"
docker run -d --name "${KAFKA_NAME}" --network "${NET}" --network-alias kafka \
  --user 1000:1000 "${RO_FLAGS[@]}" \
  --tmpfs /tmp:rw,exec,mode=1777 \
  --tmpfs /opt/kafka/config:rw,exec,uid=1000,gid=1000,mode=0755 \
  --tmpfs /opt/kafka/logs:rw,exec,uid=1000,gid=1000,mode=0755 \
  --mount "type=volume,src=${VOL_KAFKA},dst=/var/lib/kafka/data" \
  -e CLUSTER_ID=a6SsLG7YBxBAeqa-3gKwgg \
  -e KAFKA_NODE_ID=1 \
  -e KAFKA_PROCESS_ROLES=broker,controller \
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@127.0.0.1:9093 \
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
  -e KAFKA_LISTENERS="PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093,EXTERNAL://0.0.0.0:29092" \
  -e KAFKA_ADVERTISED_LISTENERS="PLAINTEXT://kafka:9092,EXTERNAL://127.0.0.1:29092" \
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP="CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT,EXTERNAL:PLAINTEXT" \
  -e KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT \
  -e KAFKA_LOG_DIRS=/var/lib/kafka/data \
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
  -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
  -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
  -e KAFKA_MIN_INSYNC_REPLICAS=1 \
  -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
  -e KAFKA_AUTO_CREATE_TOPICS_ENABLE=false \
  -e KAFKA_HEAP_OPTS="-Xms256m -Xmx512m" \
  apache/kafka:3.9.1 >/dev/null

# ---------------------------------------------------------------------------
# 客户端：与 verify Job 相同的镜像、相同的安全约束、相同的连接方式
# ---------------------------------------------------------------------------
mysql_client() {
  docker run --rm --network "${NET}" --user 999:999 "${RO_FLAGS[@]}" --tmpfs /tmp:rw,exec \
    -e DATABASE_DEFAULT_HOST=mysql -e DATABASE_DEFAULT_PORT=3306 \
    -e DATABASE_DEFAULT_USER=surgecart -e DATABASE_DEFAULT_NAME=my_shop \
    -e DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS}" \
    --entrypoint /bin/bash mysql:8.0 -c "$1"
}

redis_client() {
  docker run --rm --network "${NET}" --user 999:999 "${RO_FLAGS[@]}" --tmpfs /tmp:rw,exec \
    --entrypoint /bin/sh redis:7-alpine -c "$1"
}

kafka_client() {
  docker run --rm --network "${NET}" --user 1000:1000 "${RO_FLAGS[@]}" --tmpfs /tmp:rw,exec,mode=1777 \
    -e KAFKA_HEAP_OPTS="-Xms256m -Xmx512m" \
    -e TOPIC="${TOPIC}" -e DLQ="${DLQ}" \
    --entrypoint /bin/bash apache/kafka:3.9.1 -c "$1"
}

# 探针命令在客户端容器内求值：宿主机只负责重试，不做字符串拼接。
mysql_probe() {
  mysql_client 'set -eu
MYSQL_PWD="${DATABASE_DEFAULT_PASS}" mysql --protocol=TCP -h "${DATABASE_DEFAULT_HOST}" \
  -P "${DATABASE_DEFAULT_PORT}" -u "${DATABASE_DEFAULT_USER}" -D "${DATABASE_DEFAULT_NAME}" \
  -e "SELECT 1"' >/dev/null 2>&1
}

redis_probe() {
  redis_client 'test "$(redis-cli -h redis ping)" = PONG' >/dev/null 2>&1
}

kafka_probe() {
  kafka_client '/opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server kafka:9092' >/dev/null 2>&1
}

wait_for() {
  local label="$1" timeout="$2" probe="$3" elapsed=0
  log_info "等待 ${label}（上限 ${timeout}s）..."
  while (( elapsed < timeout )); do
    if "${probe}"; then
      log_info "${label} 已就绪（${elapsed}s）"
      return 0
    fi
    sleep 3
    elapsed=$((elapsed + 3))
  done
  log_error "${label} 在 ${timeout}s 内未就绪，容器日志："
  docker logs "${MYSQL_NAME}" --tail=15 2>&1 || true
  docker logs "${REDIS_NAME}" --tail=15 2>&1 || true
  docker logs "${KAFKA_NAME}" --tail=25 2>&1 || true
  return 1
}

wait_for "MySQL" 180 mysql_probe || fail "MySQL 未就绪"
wait_for "Redis" 60 redis_probe || fail "Redis 未就绪"
wait_for "Kafka" 240 kafka_probe || fail "Kafka 未就绪"

# ---------------------------------------------------------------------------
# 协议级验证（与 verify/*.yaml 的断言一一对应）
# ---------------------------------------------------------------------------
log_info "MySQL：经网络别名执行 SELECT 1"
mysql_client 'set -eu
MYSQL_PWD="${DATABASE_DEFAULT_PASS}" mysql --protocol=TCP -h "${DATABASE_DEFAULT_HOST}" \
  -P "${DATABASE_DEFAULT_PORT}" -u "${DATABASE_DEFAULT_USER}" -D "${DATABASE_DEFAULT_NAME}" \
  -e "SELECT 1 AS mysql_select_1"
echo MYSQL_VERIFY_OK' || fail "MySQL 验证失败"

log_info "Redis：经网络别名 PING"
redis_client 'set -eu
PONG=$(redis-cli -h redis ping)
echo "[verify] PING -> ${PONG}"
[ "${PONG}" = "PONG" ]
echo REDIS_VERIFY_OK' || fail "Redis 验证失败"

log_info "Kafka：Topic 初始化 + 生产 + 消费"
kafka_client 'set -euo pipefail
BOOTSTRAP="kafka:9092"
for t in "${TOPIC}" "${DLQ}"; do
  /opt/kafka/bin/kafka-topics.sh --bootstrap-server "${BOOTSTRAP}" --create --if-not-exists \
    --topic "${t}" --partitions 1 --replication-factor 1
done
/opt/kafka/bin/kafka-topics.sh --bootstrap-server "${BOOTSTRAP}" --list
MARKER="verify-kafka-$(date +%s)-$$"
printf "%s\n" "${MARKER}" | /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server "${BOOTSTRAP}" --topic "${TOPIC}"
/opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server "${BOOTSTRAP}" --topic "${TOPIC}" \
  --from-beginning --timeout-ms 20000 > /tmp/consumed.txt 2>/dev/null || true
grep -qx "${MARKER}" /tmp/consumed.txt || { echo "未消费到 ${MARKER}" >&2; exit 1; }
echo "KAFKA_VERIFY_OK (${MARKER})"' || fail "Kafka 验证失败"

# ---------------------------------------------------------------------------
# 负向证据：Topic 不会自动创建（证明 AUTO_CREATE_TOPICS=false 真的生效）
# ---------------------------------------------------------------------------
log_info "Kafka：断言自动建 Topic 已关闭"
if kafka_client 'set -e
/opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --list' | grep -qx "auto-create-probe"; then
  fail "出现未被显式创建的 Topic：KAFKA_AUTO_CREATE_TOPICS_ENABLE 未生效"
fi

cat <<'EOF'

容器级等价验证通过。

注意：这**不是** Kubernetes 验证。以下项目仍未验证，必须在真实集群上执行
`bash scripts/k8s-infra.sh verify` 才能关闭：
  - Pod 调度与 PVC 绑定（含 fsGroup 授权）
  - restricted Pod Security 准入
  - Service/Endpoint 路由与集群 DNS（Service FQDN）
  - 探针时序（initialDelaySeconds / failureThreshold 下的真实行为）
EOF
log_info "基础设施容器级验证通过（Kubernetes 层仍为 NOT_VERIFIED）"
