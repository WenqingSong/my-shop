#!/usr/bin/env bash
# 公共工具库：被其它生命周期脚本 source 使用。
#
# 应用与依赖统一经 Docker Compose 编排运行，脚本不再管理宿主机进程
# （已移除 nohup / pid 文件，从根上杜绝宿主机进程与 Compose app 双启动）。
#
# 安全约定：
#   - 不在此处硬编码任何密码 / 凭证。
#   - MySQL / Redis / App / Prometheus 健康通过 docker 容器的 healthcheck 判断，无需密码。
#   - 配置默认值与 manifest/config/config.yaml、docker-compose.yml 保持一致，
#     支持环境变量覆盖（环境变量 > .env > 内置默认值）。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ROOT_DIR

# ---------------------------------------------------------------------------
# 日志
# ---------------------------------------------------------------------------
log_info()  { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }
log_warn()  { printf '\033[33m[WARN]\033[0m %s\n' "$*"; }
log_error() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# 加载 .env（若存在）；已导出的环境变量优先级更高。
# 参数：可选的文件路径，缺省为 ${ROOT_DIR}/.env（便于 shell 测试注入临时文件）。
# ---------------------------------------------------------------------------
_load_env_file() {
  local file="${1:-${ROOT_DIR}/.env}"
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
    # 空值不导出：`KEY=` 若被导出为空环境变量，GoFrame GetEffective 会把「空但已设置」
    # 视为覆盖 config.yaml 非空默认值，导致启动失败。
    # 跳过空值，使加载优先级保持「已导出环境变量 > .env 非空值 > config.yaml 默认值」。
    [[ -n "${val}" ]] || continue
    # 已导出的环境变量优先：仅当该键当前未设置（或为空）时用 .env 值填充。
    if [[ -z "${!key:-}" ]]; then
      export "${key}=${val}"
    fi
  done < "${file}"
}
_load_env_file

# ---------------------------------------------------------------------------
# 配置（环境变量可覆盖，默认值与 config.yaml / docker-compose.yml 一致）
# ---------------------------------------------------------------------------
# 依赖就绪等待超时（mysql/redis 健康检查轮询），秒。
STARTUP_DEPENDENCY_TIMEOUT="${STARTUP_DEPENDENCY_TIMEOUT:-30}"
# app 容器健康等待超时（含首次构建、mysql/redis 健康、app 迁移与启动），秒。
APP_START_TIMEOUT="${APP_START_TIMEOUT:-300}"

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

# 轮询等待 app 容器健康；检测到容器已退出（迁移/启动失败）时提前失败。
wait_for_app() {
  local timeout="${APP_START_TIMEOUT}"
  local deadline=$(( $(date +%s) + timeout ))
  local id state
  while true; do
    if container_healthy app; then
      log_info "app 已健康"
      return 0
    fi
    id="$(docker_compose ps -q app 2>/dev/null || true)"
    if [[ -n "${id}" ]]; then
      id="${id%%$'\n'*}"
      state="$(docker inspect --format '{{.State.Status}}' "${id}" 2>/dev/null || printf 'missing')"
      if [[ "${state}" == "exited" || "${state}" == "dead" ]]; then
        log_error "app 容器已退出（state=${state}），请执行 make logs 查看原因"
        return 1
      fi
    fi
    if (( $(date +%s) >= deadline )); then
      log_error "app 在 ${timeout}s 内未通过健康检查，请执行 make logs 查看原因"
      return 1
    fi
    sleep 1
  done
}
