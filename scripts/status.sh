#!/usr/bin/env bash
# 展示 App / MySQL / Redis 当前运行状态。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

print_service_status() {
  local svc="$1" id state health
  id="$(docker_compose ps -q -a "${svc}" 2>/dev/null || true)"
  if [[ -z "${id}" ]]; then
    printf '%-9s not created\n' "${svc}"
    return 0
  fi
  id="${id%%$'\n'*}"
  state="$(docker inspect --format '{{.State.Status}}' "${id}" 2>/dev/null || printf 'missing')"
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${id}" 2>/dev/null || printf 'missing')"
  printf '%-9s %s (health: %s)\n' "${svc}" "${state}" "${health}"
}

if is_app_running; then
  printf '%-9s running (pid %s)\n' "App" "$(cat "${APP_PID_FILE}")"
else
  printf '%-9s stopped\n' "App"
fi
print_service_status mysql
print_service_status redis
