#!/usr/bin/env bash
# 启动前统一预检：qiniu check + admin check（复用 my-shop 子命令的同一事实源与错误语义）。
#
# - qiniu check：七牛 required 配置结构 + 存在性 + bucket 最小权限只读可用性（复用 serve fail-fast）。
# - admin check：超级管理员创建条件（复用 boot.CheckSuperAdminCondition，需 MySQL 可连）。
# - 两者都执行，任一失败即非零退出，便于一次暴露全部问题。
#
# 前置：依赖容器已就绪（make init）、数据库已迁移（make migrate-up）、.env 已填七牛与超管配置。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

BUILD_DIR="${BUILD_DIR:-bin}"
BINARY="${BINARY:-my-shop}"
BIN="${ROOT_DIR}/${BUILD_DIR}/${BINARY}"

# admin check 需连接 MySQL：未显式设置 DATABASE_DEFAULT_PASS 时回退到 .env 的 MYSQL_ROOT_PASSWORD。
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-${MYSQL_ROOT_PASSWORD:-}}"

if [[ -z "${DATABASE_DEFAULT_PASS:-}" ]]; then
  log_error "数据库密码未配置：请先执行 make init 并填写 .env 的 MYSQL_ROOT_PASSWORD（admin check 需要连接 MySQL）"
  exit 1
fi

if [[ ! -x "${BIN}" ]]; then
  log_info "未找到 ${BIN}，先执行统一构建..."
  bash "${SCRIPT_DIR}/build.sh"
fi

cd "${ROOT_DIR}"

fail=0
log_info "执行 qiniu check ..."
if ! "${BIN}" qiniu check; then
  fail=1
fi

log_info "执行 admin check ..."
if ! "${BIN}" admin check; then
  fail=1
fi

if [[ "${fail}" -ne 0 ]]; then
  log_error "config-check 未通过（qiniu check 或 admin check 失败，详见上方输出）"
  exit 1
fi
log_info "config-check 通过（qiniu check + admin check）"
