#!/usr/bin/env bash
# 公共工具库：被其它生命周期脚本 source 使用。
#
# 安全约定：
#   - 不在此处硬编码任何密码 / 凭证。
#   - MySQL / Redis 健康通过 docker 容器的 healthcheck 判断；
#     应用健康通过 HTTP GET /health 判断，二者均无需密码。
#   - 配置默认值与 manifest/config/config.yaml、docker-compose.yml 保持一致，
#     支持环境变量覆盖（环境变量 > .env > 内置默认值）。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ROOT_DIR

BIN_DIR="${ROOT_DIR}/bin"
TMP_DIR="${ROOT_DIR}/tmp"
APP_BIN="${BIN_DIR}/my-shop"
APP_PID_FILE="${TMP_DIR}/my-shop.pid"
APP_LOG_FILE="${TMP_DIR}/my-shop.log"

# ---------------------------------------------------------------------------
# 日志
# ---------------------------------------------------------------------------
log_info()  { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }
log_warn()  { printf '\033[33m[WARN]\033[0m %s\n' "$*"; }
log_error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# 加载 .env（若存在）；已导出的环境变量优先级更高。
# ---------------------------------------------------------------------------
_load_env_file() {
  local file="${ROOT_DIR}/.env"
  [[ -f "${file}" ]] || return 0
  local line key val
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%%#*}"                            # 去掉行内注释
    line="${line#"${line%%[![:space:]]*}"}"       # 去掉前导空白
    line="${line%"${line##*[![:space:]]}"}"       # 去掉尾部空白
    [[ -z "${line}" ]] && continue
    [[ "${line}" == *=* ]] || continue
    key="${line%%=*}"
    val="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"          # key 去掉尾部空白
    # 去掉可选的首尾引号
    if [[ "${#val}" -ge 2 && "${val:0:1}" == "${val: -1}" ]] \
      && { [[ "${val:0:1}" == '"' ]] || [[ "${val:0:1}" == "'" ]]; }; then
      val="${val:1:${#val}-2}"
    fi
    # 环境变量优先
    if [[ -z "${!key:-}" ]]; then
      export "${key}=${val}"
    fi
  done < "${file}"
}
_load_env_file
unset -f _load_env_file

# ---------------------------------------------------------------------------
# 配置（环境变量可覆盖，默认值与 config.yaml / docker-compose.yml 一致）
# ---------------------------------------------------------------------------
SERVER_ADDRESS="${SERVER_ADDRESS:-:8000}"
STARTUP_DEPENDENCY_TIMEOUT="${STARTUP_DEPENDENCY_TIMEOUT:-30}"

# 应用健康检查 URL（仅探测用，无需密码）
app_health_url() {
  local addr="${SERVER_ADDRESS}"
  if [[ "${addr}" == :* ]]; then
    printf 'http://127.0.0.1%s/health' "${addr}"
  else
    local host="${addr%:*}" port="${addr##*:}"
    case "${host}" in
      0.0.0.0|::|\[::\]) host="127.0.0.1" ;;
    esac
    printf 'http://%s:%s/health' "${host}" "${port}"
  fi
}

# ---------------------------------------------------------------------------
# docker compose 封装
# ---------------------------------------------------------------------------
docker_compose() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    docker compose "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
  else
    log_error "未找到 docker compose，请安装 Docker Compose v2"
    exit 1
  fi
}

# 判断某服务的容器是否处于 running 且 healthcheck 为 healthy。
container_healthy() {
  local svc="$1" id state health
  id="$(docker_compose ps -q "${svc}" 2>/dev/null || true)"
  [[ -n "${id}" ]] || return 1
  id="${id%%$'\n'*}"
  state="$(docker inspect --format '{{.State.Status}}' "${id}" 2>/dev/null || printf 'missing')"
  [[ "${state}" == "running" ]] || return 1
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${id}" 2>/dev/null || printf 'missing')"
  [[ "${health}" == "healthy" ]]
}

# 轮询等待 MySQL / Redis 健康（基于 healthcheck 探测，不使用固定 sleep）。
wait_for_deps() {
  local timeout="${STARTUP_DEPENDENCY_TIMEOUT}"
  local deadline=$(( $(date +%s) + timeout ))
  local svc ok
  while true; do
    ok=true
    for svc in mysql redis; do
      if ! container_healthy "${svc}"; then
        ok=false
        break
      fi
    done
    if [[ "${ok}" == "true" ]]; then
      log_info "MySQL 与 Redis 已就绪（healthy）"
      return 0
    fi
    if (( $(date +%s) >= deadline )); then
      log_error "依赖在 ${timeout}s 内未就绪"
      return 1
    fi
    sleep 1
  done
}

# ---------------------------------------------------------------------------
# 应用进程管理（应用以本地进程方式运行）
# ---------------------------------------------------------------------------
is_app_running() {
  [[ -f "${APP_PID_FILE}" ]] || return 1
  local pid
  pid="$(cat "${APP_PID_FILE}" 2>/dev/null || true)"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

app_healthy() {
  curl -fsS "$(app_health_url)" >/dev/null 2>&1
}

build_app() {
  mkdir -p "${BIN_DIR}"
  local tmp_bin="${APP_BIN}.tmp"
  log_info "构建应用二进制..."
  ( cd "${ROOT_DIR}" && go build -o "${tmp_bin}" . )
  mv -f "${tmp_bin}" "${APP_BIN}"
  log_info "已构建 ${APP_BIN}"
}

start_app() {
  if is_app_running; then
    log_info "应用已在运行（pid $(cat "${APP_PID_FILE}")）"
    return 0
  fi
  mkdir -p "${TMP_DIR}"
  log_info "启动应用..."
  nohup "${APP_BIN}" >>"${APP_LOG_FILE}" 2>&1 &
  echo "$!" > "${APP_PID_FILE}"
  wait_for_app
}

wait_for_app() {
  local timeout="${STARTUP_DEPENDENCY_TIMEOUT}"
  local deadline=$(( $(date +%s) + timeout ))
  while true; do
    if app_healthy; then
      log_info "应用已健康（$(app_health_url)）"
      return 0
    fi
    if ! is_app_running; then
      log_error "应用启动后退出，详见 ${APP_LOG_FILE}"
      return 1
    fi
    if (( $(date +%s) >= deadline )); then
      log_error "应用在 ${timeout}s 内未通过健康检查"
      return 1
    fi
    sleep 1
  done
}

stop_app() {
  if [[ -f "${APP_PID_FILE}" ]]; then
    local pid i
    pid="$(cat "${APP_PID_FILE}" 2>/dev/null || true)"
    if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
      log_info "停止应用（pid ${pid}）..."
      kill "${pid}" 2>/dev/null || true
      for i in $(seq 1 10); do
        kill -0 "${pid}" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "${pid}" 2>/dev/null; then
        kill -9 "${pid}" 2>/dev/null || true
      fi
    fi
    rm -f "${APP_PID_FILE}"
  fi
}
