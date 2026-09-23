#!/usr/bin/env bash
# 查看应用日志与依赖容器日志。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

printf '===== 应用日志（%s）=====\n' "${APP_LOG_FILE}"
if [[ -f "${APP_LOG_FILE}" ]]; then
  tail -n 100 "${APP_LOG_FILE}"
else
  log_warn "暂无应用日志"
fi

printf '\n===== 依赖容器日志（mysql / redis）=====\n'
docker_compose logs --tail=100 mysql redis || true
