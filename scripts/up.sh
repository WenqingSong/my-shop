#!/usr/bin/env bash
# 构建并启动完整容器栈（mysql / redis / app / prometheus），幂等可重复执行。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "构建并启动完整容器栈（mysql / redis / app / prometheus）..."
# --build 使 app / prometheus 服务按各自 Dockerfile 重新构建（配置经 COPY 内置进镜像，
# rootless/嵌套容器环境下无法用 bind mount 单文件）。mysql/redis 无 build 定义，不受影响。
docker_compose up -d --build

log_info "等待 app 健康（migration + serve 启动 + 健康检查）..."
wait_for_app

log_info "up complete"
