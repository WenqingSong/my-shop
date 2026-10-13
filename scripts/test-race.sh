#!/usr/bin/env bash
# race 门禁：对进程内共享内存状态的并发逻辑（熔断器状态机等）运行 go test -race。
#
# 与 DB 集成测试隔离：race 目标为纯内存并发逻辑（不连接、不修改 MySQL/Redis），
# 因此不注入 DATABASE_DEFAULT_PASS 等 DB 配置，也不与 `go test -p 1 ./...` 共享库表，
# 二者可先后独立执行，互不污染、不因共享数据库竞争产生 flaky 失败。
#
# 仅用 -run 精确匹配纯内存测试（TestCircuitBreakerStateMachine*），DB 集成测试
# （TestCircuitBreakerOpensAndRecovers 等）只编译不执行，故无需 MySQL/Redis 就绪。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

log_info "go test -race（进程内共享内存并发逻辑：flashsale 熔断器状态机）"
( cd "${ROOT_DIR}" && go test -race -count=1 -run 'TestCircuitBreakerStateMachine' ./internal/logic/flashsale/... )

log_info "race 门禁通过"
