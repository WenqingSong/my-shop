#!/usr/bin/env bash
# 统一测试入口：先静态检查（go vet），再执行单元测试（go test）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "go vet ./..."
( cd "${ROOT_DIR}" && go vet ./... )

# 各测试包共享同一个 MySQL/Redis 实例（docker-compose），并会清理/写入 admins、roles、
# permissions 等同一批表；跨包并行执行会产生数据竞争导致 flaky（例如不同包用不同密码
# seed 超级管理员、或一方 DELETE 另一方正在使用的权限行）。因此用 -p 1 串行执行以保证隔离。
log_info "go test -p 1 ./..."
( cd "${ROOT_DIR}" && go test -p 1 ./... )

log_info "测试全部通过"
