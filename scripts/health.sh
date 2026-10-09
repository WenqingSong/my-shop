#!/usr/bin/env bash
# 校验 App / MySQL / Redis / Prometheus 四者健康状态；任一异常时以非零退出码反馈。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

overall=true

if app_healthy; then
  printf '%-9s healthy\n' "App"
else
  printf '%-9s unhealthy\n' "App"
  overall=false
fi

check_service() {
  local svc="$1"
  if container_healthy "${svc}"; then
    printf '%-9s healthy\n' "${svc}"
  else
    printf '%-9s unhealthy\n' "${svc}"
    overall=false
  fi
}
check_service mysql
check_service redis
check_service prometheus

if [[ "${overall}" == "true" ]]; then
  log_info "App / MySQL / Redis / Prometheus 全部健康"
else
  log_error "存在不健康组件"
  exit 1
fi
