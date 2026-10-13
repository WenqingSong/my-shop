#!/usr/bin/env bash
# 一键初始化开发环境：bootstrap = init + up。
# 先执行 init（准备依赖容器与 .env、启动 MySQL/Redis），再执行 up（构建并启动完整容器栈）。
# 适用于全新 / 每日重置的环境，无需手动分两步执行。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "bootstrap：一键初始化开发环境（init + up）..."
"${SCRIPT_DIR}/init.sh"
"${SCRIPT_DIR}/up.sh"
log_info "bootstrap complete：依赖就绪、应用已启动"
