#!/usr/bin/env bash
# T2-04 真实 Docker 镜像 / 容器生命周期门禁。
#
# 与 test-container.sh（纯静态断言）互补：本脚本必须真正构建镜像、启动容器、发 HTTP 请求、
# 在在途请求存在时停止容器，并核对 SIGTERM 传递、HTTP drain、Runtime 关闭顺序、退出码与残留进程。
#
# 覆盖：
#   1. 生产镜像构建（--target runtime）与镜像元数据（非 root / HEALTHCHECK / entrypoint）；
#   2. 镜像文件系统审计：不含源码、测试、Go 工具链、.env 与凭据；
#   3. 生产二进制在只读根文件系统 + cap_drop ALL 下可执行（migrate version）；
#   4. 验证镜像（--target lifecycle-verify）启动真实 serve：非 root、只读根文件系统、
#      cap_drop ALL、no-new-privileges、tmpfs、数据卷写权限、HEALTHCHECK、/health、/ready；
#   5. PID 1 为 Go 应用（entrypoint 的 exec 生效），UID=10001；
#   6. 空闲停止：SIGTERM 直接送达，优雅关闭，退出码 0；
#   7. 在途请求停止：HTTP drain 超时后强制关闭、Runtime 关闭顺序、退出码 0、
#      耗时不超过 60s 终止预算、Docker 未提前 SIGKILL、无残留进程；
#   8. Compose stop_grace_period 实际生效值。
#
# 隔离：独立 Compose project + 独立 container_name + 独立数据卷，测试结束删除，
# 不触碰开发环境的 my-shop-* 容器与数据卷。
#
# 用法：
#   bash scripts/test-container-runtime.sh
#   T204_KEEP_ARTIFACTS=1 bash scripts/test-container-runtime.sh   # 保留日志/证书便于排查
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

PROJECT="t204-runtime-verify"
APP_CONTAINER="${PROJECT}-app"
MOCK_CONTAINER="${PROJECT}-qiniu-mock"
MYSQL_CONTAINER="${PROJECT}-mysql"
REDIS_CONTAINER="${PROJECT}-redis"
CERT_DIR="${ROOT_DIR}/test/container/.artifacts"
COMPOSE_FILE="${ROOT_DIR}/docker-compose.yml"
OVERRIDE_FILE="${ROOT_DIR}/test/container/compose.runtime-verify.yml"

command -v docker >/dev/null 2>&1 || fail "未找到 docker：本门禁要求真实容器运行验证，不能用静态检查替代"
docker compose version >/dev/null 2>&1 || fail "未找到 docker compose v2"
command -v openssl >/dev/null 2>&1 || fail "未找到 openssl：需要生成测试用 TLS 证书"
command -v go >/dev/null 2>&1 || fail "未找到 go：需要编译测试专用 qiniu-mock"
command -v python3 >/dev/null 2>&1 || fail "未找到 python3：需要选择空闲端口"

# 测试专用凭据（非生产值，仅在隔离 project 内使用）。
export MYSQL_ROOT_PASSWORD="t204-root-password"
export MYSQL_DATABASE="my_shop_container_verify"
export AUTH_JWT_SECRET="t204-container-verify-jwt-secret-0123456789"
export T204_ADMIN_PASSWORD="t204-admin-placeholder"

free_port() {
  # 固定候选区间（18000-18999）：避免使用宿主机/容器网关可能未转发的随机高端口，
  # 同时避开开发栈固定端口（8080 nginx / 9090 prometheus / 3306 mysql / 6379 redis）。
  python3 - <<'PY'
import socket
import sys

for port in range(18000, 19000):
    s = socket.socket()
    try:
        s.bind(("127.0.0.1", port))
        s.close()
        print(port)
        sys.exit(0)
    except OSError:
        s.close()
sys.exit("18000-18999 内无可用端口")
PY
}
export T204_APP_PORT="${T204_APP_PORT:-$(free_port)}"

compose() {
  docker compose -p "${PROJECT}" \
    -f "${COMPOSE_FILE}" \
    -f "${OVERRIDE_FILE}" "$@"
}

APP_LOGS="${T204_APP_LOG:-$(mktemp -t t204-app-logs-XXXXXX)}"
MOCK_LOGS="${T204_MOCK_LOG:-$(mktemp -t t204-mock-logs-XXXXXX)}"
ARTIFACT_DIR="${T204_ARTIFACT_DIR:-$(mktemp -d -t t204-runtime-XXXXXX)}"
KEEP_ARTIFACTS="${T204_KEEP_ARTIFACTS:-0}"
cleanup() {
  local exit_code=$?
  docker logs "${APP_CONTAINER}" >"${APP_LOGS}" 2>&1 || true
  docker logs "${MOCK_CONTAINER}" >"${MOCK_LOGS}" 2>&1 || true
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  docker rm -f t204-fs-audit >/dev/null 2>&1 || true
  if [[ ${exit_code} -ne 0 ]]; then
    log_error "容器运行门禁失败；app 日志: ${APP_LOGS}；mock 日志: ${MOCK_LOGS}"
    log_error "artifacts: ${ARTIFACT_DIR}"
  else
    if [[ "${KEEP_ARTIFACTS}" == "1" ]]; then
      log_info "已保留 artifacts：${ARTIFACT_DIR}（app 日志: ${APP_LOGS}）"
    else
      rm -rf "${ARTIFACT_DIR}"
      rm -f "${APP_LOGS}" "${MOCK_LOGS}"
      rm -rf "${CERT_DIR}"
    fi
  fi
  exit ${exit_code}
}
trap cleanup EXIT

PASS_COUNT=0
pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  log_info "PASS[$PASS_COUNT] $*"
}

wait_container_healthy() {
  local name="$1" timeout="${2:-300}" waited=0 health
  while (( waited < timeout )); do
    health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${name}" 2>/dev/null || true)"
    [[ "${health}" == "healthy" ]] && return 0
    if [[ "${health}" == "none" ]]; then
      # 容器没有 healthcheck 时退回 running 判断（仅用于 mock）。
      [[ "$(docker inspect -f '{{.State.Status}}' "${name}" 2>/dev/null || true)" == "running" ]] && return 0
    fi
    sleep 2
    waited=$((waited + 2))
  done
  log_error "容器 ${name} 未在 ${timeout}s 内 healthy（当前 health=${health:-missing}）"
  docker inspect -f '{{json .State}}' "${name}" 2>/dev/null || true
  return 1
}

wait_http() {
  local url="$1" timeout="${2:-120}" waited=0 code
  while (( waited < timeout )); do
    code="$(curl -s -o /dev/null -m 2 -w '%{http_code}' "${url}" || true)"
    [[ "${code}" == "200" ]] && return 0
    sleep 1
    waited=$((waited + 1))
  done
  log_error "${url} 未在 ${timeout}s 内返回 200（最后状态码 ${code:-none}）"
  return 1
}

assert_eq() {
  local expected="$1" actual="$2" message="$3"
  [[ "${actual}" == "${expected}" ]] || fail "${message}：期望 [${expected}]，实际 [${actual}]"
}

# ---------------------------------------------------------------------------
# 0) 清理可能残留的测试容器/卷（只清理本门禁固定前缀）
# ---------------------------------------------------------------------------
compose down -v --remove-orphans >/dev/null 2>&1 || true
docker rm -f "${APP_CONTAINER}" "${MOCK_CONTAINER}" "${MYSQL_CONTAINER}" "${REDIS_CONTAINER}" t204-fs-audit >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# 1) 构建生产镜像
# ---------------------------------------------------------------------------
log_info "构建生产镜像 my-shop-app:t204（--target runtime）..."
docker build --progress=plain --target runtime -t my-shop-app:t204 "${ROOT_DIR}" >"${ARTIFACT_DIR}/build-production.log" 2>&1 ||
  { tail -40 "${ARTIFACT_DIR}/build-production.log" >&2; fail "生产镜像构建失败"; }
pass "生产镜像构建"

IMAGE_USER="$(docker image inspect -f '{{.Config.User}}' my-shop-app:t204)"
assert_eq "10001:10001" "${IMAGE_USER}" "镜像默认运行用户"
IMAGE_WORKDIR="$(docker image inspect -f '{{.Config.WorkingDir}}' my-shop-app:t204)"
assert_eq "/app" "${IMAGE_WORKDIR}" "镜像工作目录"
IMAGE_ENTRYPOINT="$(docker image inspect -f '{{json .Config.Entrypoint}}' my-shop-app:t204)"
assert_eq '["/app/docker-entrypoint.sh"]' "${IMAGE_ENTRYPOINT}" "镜像 ENTRYPOINT"
IMAGE_HEALTH="$(docker image inspect -f '{{json .Config.Healthcheck}}' my-shop-app:t204)"
for token in '127.0.0.1:8000/health' '10000000000' '5000000000' '60000000000' '"Retries":5'; do
  [[ "${IMAGE_HEALTH}" == *"${token}"* ]] || fail "镜像 HEALTHCHECK 缺少预期参数 ${token}: ${IMAGE_HEALTH}"
done
pass "镜像元数据（非 root / 工作目录 / ENTRYPOINT / HEALTHCHECK）"

# 镜像中不得出现敏感环境变量。
IMAGE_ENV="$(docker image inspect -f '{{json .Config.Env}}' my-shop-app:t204)"
if grep -qiE 'PASSWORD|SECRET|TOKEN|JWT|ACCESS_KEY|SECRET_KEY' <<<"${IMAGE_ENV}"; then
  fail "镜像 Config.Env 含敏感键：${IMAGE_ENV}"
fi
pass "镜像环境变量不含凭据"

# ---------------------------------------------------------------------------
# 2) 镜像文件系统审计（导出 tar 列表，不启动容器）
# ---------------------------------------------------------------------------
docker create --name t204-fs-audit my-shop-app:t204 >/dev/null
docker export t204-fs-audit | tar -tf - >"${ARTIFACT_DIR}/image-files.txt"
docker rm t204-fs-audit >/dev/null

for required in \
  'app/my-shop' \
  'app/manifest/config/config.yaml' \
  'app/docker-entrypoint.sh' \
  'usr/share/zoneinfo/Asia/Shanghai' \
  'etc/ssl/certs/ca-certificates.crt'; do
  grep -qx "${required}" "${ARTIFACT_DIR}/image-files.txt" ||
    fail "镜像缺少必要文件 ${required}"
done
if grep -qE '(^|/)src/|(^|/)usr/local/go|(^|/)app/.*\.go$|(^|/)\.env$|(^|/)test/' "${ARTIFACT_DIR}/image-files.txt"; then
  fail "镜像包含源码 / Go 工具链 / 测试文件 / .env（运行层内容审计失败）"
fi
pass "镜像文件系统审计（仅二进制 + 配置 + 入口 + tzdata + CA）"

# ---------------------------------------------------------------------------
# 3) 依赖与验证镜像
# ---------------------------------------------------------------------------
log_info "构建七牛 Mock 与生命周期验证镜像..."
mkdir -p "${CERT_DIR}"
( cd "${ROOT_DIR}" && CGO_ENABLED=0 GOOS=linux go build -trimpath -o "${CERT_DIR}/qiniu-mock" ./test/container/qiniu_mock ) ||
  fail "编译 qiniu-mock 失败（需静态二进制以在 alpine 中运行）"

openssl req -x509 -newkey rsa:2048 -sha256 -days 2 -nodes \
  -keyout "${CERT_DIR}/server.key" -out "${CERT_DIR}/server.crt" \
  -subj "/CN=127.0.0.1" \
  -addext "subjectAltName=IP:127.0.0.1" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign" >/dev/null 2>&1 ||
  fail "生成测试 TLS 证书失败"
cp "${CERT_DIR}/server.crt" "${CERT_DIR}/ca.crt"
chmod 0644 "${CERT_DIR}"/server.crt "${CERT_DIR}"/server.key "${CERT_DIR}"/ca.crt

docker build --build-context t204-certs="${CERT_DIR}" \
  -f "${ROOT_DIR}/test/container/Dockerfile.mock" -t my-shop-qiniu-mock:t204-verify \
  "${ROOT_DIR}/test/container" >"${ARTIFACT_DIR}/build-mock.log" 2>&1 ||
  { tail -40 "${ARTIFACT_DIR}/build-mock.log" >&2; fail "qiniu-mock 镜像构建失败"; }

docker build --progress=plain --build-context t204-certs="${CERT_DIR}" \
  --target lifecycle-verify -t my-shop-app:t204-verify "${ROOT_DIR}" \
  >"${ARTIFACT_DIR}/build-verify.log" 2>&1 ||
  { tail -40 "${ARTIFACT_DIR}/build-verify.log" >&2; fail "生命周期验证镜像构建失败"; }

compose up -d --no-build mysql redis qiniu-mock >/dev/null
wait_container_healthy "${MYSQL_CONTAINER}" 180 || fail "MySQL 未就绪"
wait_container_healthy "${REDIS_CONTAINER}" 120 || fail "Redis 未就绪"
wait_container_healthy "${MOCK_CONTAINER}" 60 || fail "qiniu-mock 未运行"
pass "依赖（MySQL / Redis / qiniu-mock）就绪"

# ---------------------------------------------------------------------------
# 4) 生产二进制：只读根文件系统 + cap_drop ALL + 非 root 下可执行
# ---------------------------------------------------------------------------
COMPOSE_NETWORK="${PROJECT}_default"
docker run --rm \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m \
  --network "${COMPOSE_NETWORK}" \
  -e DATABASE_DEFAULT_HOST=mysql \
  -e DATABASE_DEFAULT_PORT=3306 \
  -e DATABASE_DEFAULT_USER=root \
  -e DATABASE_DEFAULT_PASS="${MYSQL_ROOT_PASSWORD}" \
  -e DATABASE_DEFAULT_NAME="${MYSQL_DATABASE}" \
  -e AUTH_JWT_SECRET="${AUTH_JWT_SECRET}" \
  --entrypoint /app/my-shop \
  my-shop-app:t204 migrate version >"${ARTIFACT_DIR}/production-smoke.log" 2>&1 ||
  { cat "${ARTIFACT_DIR}/production-smoke.log" >&2; fail "生产二进制在加固运行参数下执行 migrate version 失败"; }
grep -q 'version:' "${ARTIFACT_DIR}/production-smoke.log" ||
  fail "生产二进制 migrate version 输出异常：$(cat "${ARTIFACT_DIR}/production-smoke.log")"
pass "生产二进制在只读根文件系统 + cap_drop ALL + 非 root 下可执行"

PROD_UID="$(docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m --entrypoint /bin/sh my-shop-app:t204 -c 'id -u')"
assert_eq "10001" "${PROD_UID}" "生产镜像默认 UID"
PROD_CFG="$(docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m --entrypoint /bin/sh my-shop-app:t204 -c 'head -1 /app/manifest/config/config.yaml')"
[[ "${PROD_CFG}" == "# 服务配置" ]] || fail "生产镜像配置不可读：${PROD_CFG}"
pass "生产镜像配置可读、默认 UID=10001"

# ---------------------------------------------------------------------------
# 5) 启动验证容器
# ---------------------------------------------------------------------------
log_info "启动验证 app 容器（独立 project=${PROJECT}，共享 mock 网络命名空间）..."
compose up -d --no-build app >/dev/null
wait_container_healthy "${APP_CONTAINER}" 300 || fail "app 容器未 healthy"
wait_http "http://127.0.0.1:${T204_APP_PORT}/health" 60 || fail "/health 不可用"
wait_http "http://127.0.0.1:${T204_APP_PORT}/ready" 60 || fail "/ready 不可用"
pass "容器启动、HEALTHCHECK、/health、/ready"

# 容器安全上下文（直接读取 Docker 实际生效值）。
assert_eq "10001:10001" "$(docker inspect -f '{{.Config.User}}' "${APP_CONTAINER}")" "容器运行用户"
assert_eq "true" "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "${APP_CONTAINER}")" "只读根文件系统"
assert_eq "false" "$(docker inspect -f '{{.HostConfig.Privileged}}' "${APP_CONTAINER}")" "privileged"
CAP_DROP="$(docker inspect -f '{{json .HostConfig.CapDrop}}' "${APP_CONTAINER}")"
[[ "${CAP_DROP}" == *'"ALL"'* ]] || fail "cap_drop 未包含 ALL：${CAP_DROP}"
SECURITY_OPT="$(docker inspect -f '{{json .HostConfig.SecurityOpt}}' "${APP_CONTAINER}")"
[[ "${SECURITY_OPT}" == *'no-new-privileges:true'* ]] || fail "缺少 no-new-privileges：${SECURITY_OPT}"
PID_MODE="$(docker inspect -f '{{.HostConfig.PidMode}}' "${APP_CONTAINER}")"
assert_eq "" "${PID_MODE}" "host PID 命名空间必须未启用"
NETWORK_MODE="$(docker inspect -f '{{.HostConfig.NetworkMode}}' "${APP_CONTAINER}")"
[[ "${NETWORK_MODE}" != "host" ]] || fail "不得使用 host 网络"
BINDS="$(docker inspect -f '{{json .HostConfig.Binds}}' "${APP_CONTAINER}")"
[[ "${BINDS}" != *'docker.sock'* ]] || fail "不得挂载 Docker Socket"
TMPFS="$(docker inspect -f '{{json .HostConfig.Tmpfs}}' "${APP_CONTAINER}")"
[[ "${TMPFS}" == *'/tmp'* ]] || fail "缺少 /tmp tmpfs：${TMPFS}"
pass "安全上下文（只读根 / cap_drop ALL / no-new-privileges / 无 privileged/host/PID/socket）"

# 进程与权限事实。
PID1_CMD="$(docker exec "${APP_CONTAINER}" cat /proc/1/cmdline | tr '\0' ' ' | sed 's/ $//')"
[[ "${PID1_CMD}" == "/app/my-shop serve" ]] || fail "PID 1 不是 Go 应用（可能是 shell 阻断信号）：[${PID1_CMD}]"
PID1_STATUS="$(docker exec "${APP_CONTAINER}" grep -E '^(Uid|CapEff):' /proc/1/status)"
grep -qE '^Uid:[[:space:]]+10001[[:space:]]+10001' <<<"${PID1_STATUS}" || fail "PID 1 不是 UID 10001：${PID1_STATUS}"
grep -qE '^CapEff:[[:space:]]+0+$' <<<"${PID1_STATUS}" || fail "进程仍有有效 capabilities：${PID1_STATUS}"
# 排除 docker exec 自身启动的 sh，只统计容器内常驻进程。
PROC_COUNT="$(docker exec "${APP_CONTAINER}" sh -c 'n=0; for p in /proc/[0-9]*; do [ "$p" = "/proc/$$" ] && continue; n=$((n+1)); done; echo "$n"')"
assert_eq "1" "${PROC_COUNT}" "容器内常驻进程数（应只有 Go 应用，无 shell 残留）"
pass "PID 1 = /app/my-shop serve、UID=10001、CapEff=0、单进程"

# 文件系统写权限边界。
if docker exec "${APP_CONTAINER}" sh -c 'touch /app/t204-probe' >/dev/null 2>&1; then
  fail "只读根文件系统下仍可写 /app"
fi
docker exec "${APP_CONTAINER}" sh -c 'touch /app/storage/t204-probe && rm -f /app/storage/t204-probe' ||
  fail "/app/storage（持久化卷）不可写"
docker exec "${APP_CONTAINER}" sh -c 'touch /tmp/t204-probe && rm -f /tmp/t204-probe' ||
  fail "/tmp（tmpfs）不可写"
pass "只读根文件系统 + /app/storage 与 /tmp 可写"

# 终止预算与 Compose 实际生效值。
STOP_TIMEOUT="$(docker inspect -f '{{.Config.StopTimeout}}' "${APP_CONTAINER}")"
assert_eq "60" "${STOP_TIMEOUT}" "容器 StopTimeout（应来自 stop_grace_period: 60s）"
pass "stop_grace_period 实际生效为 60s"

# ---------------------------------------------------------------------------
# 6) 空闲停止：SIGTERM 直接送达 + 优雅关闭
# ---------------------------------------------------------------------------
log_info "空闲状态停止容器（验证 SIGTERM 与优雅关闭）..."
IDLE_START_MS="$(date +%s%3N)"
docker stop "${APP_CONTAINER}" >/dev/null
IDLE_ELAPSED_MS=$(( $(date +%s%3N) - IDLE_START_MS ))
IDLE_EXIT="$(docker inspect -f '{{.State.ExitCode}}' "${APP_CONTAINER}")"
assert_eq "0" "${IDLE_EXIT}" "空闲停止退出码（137 表示被 SIGKILL）"
(( IDLE_ELAPSED_MS < 15000 )) || fail "空闲停止耗时 ${IDLE_ELAPSED_MS}ms 超过预期（未优雅退出？）"
IDLE_LOGS="$(docker logs "${APP_CONTAINER}" 2>&1)"
grep -q '"event":"http_server_drain_begin"' <<<"${IDLE_LOGS}" || fail "缺少 http_server_drain_begin"
grep -q '"signal":"terminated"' <<<"${IDLE_LOGS}" || fail "未收到 SIGTERM（signal 不是 terminated）"
grep -q '"event":"http_server_stopped".*"timed_out":false' <<<"${IDLE_LOGS}" ||
  grep -q '"timed_out":false.*"event":"http_server_stopped"' <<<"${IDLE_LOGS}" ||
  fail "空闲停止未走优雅 drain 路径（timed_out=false）"
grep -q '"event":"flashsale_runtime_shutdown_complete"' <<<"${IDLE_LOGS}" || fail "Runtime 关闭未完成"
pass "空闲停止：SIGTERM → 优雅 drain → Runtime 关闭，退出码 0，耗时 ${IDLE_ELAPSED_MS}ms"

# ---------------------------------------------------------------------------
# 7) 在途请求停止：HTTP drain 超时 → 强制关闭 → Runtime 关闭
# ---------------------------------------------------------------------------
log_info "重启容器并发起在途请求（/__lifecycle/block 阻塞 30s）..."
docker start "${APP_CONTAINER}" >/dev/null
wait_container_healthy "${APP_CONTAINER}" 300 || fail "app 容器重启后未 healthy"
wait_http "http://127.0.0.1:${T204_APP_PORT}/health" 60 || fail "重启后 /health 不可用"
SECOND_RUN_SINCE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

curl -sS -m 45 -o /dev/null "http://127.0.0.1:${T204_APP_PORT}/__lifecycle/block" &
CURL_PID=$!
for _ in $(seq 1 30); do
  docker logs --since "${SECOND_RUN_SINCE}" "${APP_CONTAINER}" 2>&1 |
    grep -q '"event":"lifecycle_block_handler_entered"' && break
  sleep 1
done
docker logs --since "${SECOND_RUN_SINCE}" "${APP_CONTAINER}" 2>&1 |
  grep -q '"event":"lifecycle_block_handler_entered"' || fail "阻塞 handler 未进入，无法验证在途请求 drain"
docker exec "${APP_CONTAINER}" sh -c 'grep -q . /proc/1/cmdline' || fail "阻塞请求期间容器已异常退出"

APP_HOST_PID="$(docker inspect -f '{{.State.Pid}}' "${APP_CONTAINER}")"
[[ "${APP_HOST_PID}" =~ ^[0-9]+$ && "${APP_HOST_PID}" -gt 0 ]] || fail "无法取得 app 宿主机 PID"

log_info "有在途请求时停止容器（预算 60s，HTTP drain 10s）..."
INFLIGHT_START_MS="$(date +%s%3N)"
docker stop "${APP_CONTAINER}" >/dev/null
INFLIGHT_ELAPSED_MS=$(( $(date +%s%3N) - INFLIGHT_START_MS ))
wait "${CURL_PID}" >/dev/null 2>&1 || true

INFLIGHT_EXIT="$(docker inspect -f '{{.State.ExitCode}}' "${APP_CONTAINER}")"
assert_eq "0" "${INFLIGHT_EXIT}" "在途停止退出码（137 表示被 SIGKILL）"
(( INFLIGHT_ELAPSED_MS >= 9000 )) || fail "在途停止仅耗时 ${INFLIGHT_ELAPSED_MS}ms，未实际等待 HTTP drain 预算"
(( INFLIGHT_ELAPSED_MS < 60000 )) || fail "在途停止耗时 ${INFLIGHT_ELAPSED_MS}ms 超出 60s 终止预算"

INFLIGHT_LOGS="$(docker logs --since "${SECOND_RUN_SINCE}" "${APP_CONTAINER}" 2>&1)"
ORDER_EVENTS=(
  http_server_drain_begin
  http_server_drain_timeout
  http_server_force_closed
  http_server_stopped
  flashsale_runtime_shutdown_begin
  flashsale_runtime_shutdown_complete
)
last_line=0
for event in "${ORDER_EVENTS[@]}"; do
  line="$(grep -n "\"event\":\"${event}\"" <<<"${INFLIGHT_LOGS}" | head -n 1 | cut -d: -f1)"
  [[ -n "${line}" ]] || fail "关闭事件顺序缺少 ${event}"
  (( line > last_line )) || fail "关闭事件顺序错误：${event} 未按预期顺序出现"
  last_line="${line}"
done
grep -q '"event":"http_server_stopped".*"timed_out":true' <<<"${INFLIGHT_LOGS}" ||
  grep -q '"timed_out":true.*"event":"http_server_stopped"' <<<"${INFLIGHT_LOGS}" ||
  fail "在途请求停止未记录 drain 超时（timed_out=true）"
grep -q '"forced_connections":[1-9]' <<<"${INFLIGHT_LOGS}" || fail "未记录被强制关闭的连接数"
pass "在途停止：drain_timeout → force_closed → stopped → Runtime 关闭，退出码 0，耗时 ${INFLIGHT_ELAPSED_MS}ms"

# ---------------------------------------------------------------------------
# 8) 残留进程 / SIGKILL 证据
# ---------------------------------------------------------------------------
assert_eq "0" "$(docker inspect -f '{{.State.Pid}}' "${APP_CONTAINER}")" "停止后容器 PID"
assert_eq "false" "$(docker inspect -f '{{.State.OOMKilled}}' "${APP_CONTAINER}")" "OOMKilled"
if kill -0 "${APP_HOST_PID}" >/dev/null 2>&1; then
  fail "容器 PID ${APP_HOST_PID} 在停止后仍存活（存在残留进程）"
fi
pass "无残留进程、未被 SIGKILL/OOM"

# ---------------------------------------------------------------------------
log_info "容器运行安全门禁通过（共 ${PASS_COUNT} 项断言）"
log_info "镜像 digest: $(docker image inspect -f '{{index .RepoDigests 0}}' my-shop-app:t204 2>/dev/null || docker image inspect -f '{{.Id}}' my-shop-app:t204)"
log_info "app 退出码: idle=${IDLE_EXIT}(${IDLE_ELAPSED_MS}ms) inflight=${INFLIGHT_EXIT}(${INFLIGHT_ELAPSED_MS}ms)"
