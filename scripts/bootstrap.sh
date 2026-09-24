#!/usr/bin/env bash
# 一键初始化开发环境：依赖容器就绪 + 构建 + 启动应用。
# 适用于全新 / 每日重置的环境，与 up 共用同一套幂等启动逻辑。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "bootstrap：初始化开发环境..."
"${SCRIPT_DIR}/up.sh"
log_info "bootstrap complete：依赖就绪、应用已启动"
